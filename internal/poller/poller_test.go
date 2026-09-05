package poller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
	"blackbird/internal/seeding"
)

func mkTorrent(hash string, completed int64, rate int64) rtorrent.Torrent {
	return rtorrent.Torrent{
		Hash:           hash,
		Name:           "torrent-" + hash,
		SizeBytes:      1000,
		CompletedBytes: completed,
		State:          rtorrent.StateDownloading,
		DownRate:       rate,
	}
}

// fakeClient returns scripted results per call; failures can be injected.
type fakeClient struct {
	mu        sync.Mutex
	torrents  []rtorrent.Torrent
	global    rtorrent.GlobalStats
	detail    rtorrent.Detail
	listErr   error
	listCalls int
	// detailCalls counts FetchDetail invocations (change-driven refresh tests).
	detailCalls int
}

func (f *fakeClient) ListAndGlobals(ctx context.Context) ([]rtorrent.Torrent, rtorrent.GlobalStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, rtorrent.GlobalStats{}, f.listErr
	}
	return f.torrents, f.global, nil
}

func (f *fakeClient) FetchDetail(ctx context.Context, hash string) (rtorrent.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detailCalls++
	return f.detail, nil
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// ptrs copies a value map into a pointer map for computeDelta tests.
func ptrs(m map[string]rtorrent.Torrent) map[string]*rtorrent.Torrent {
	out := make(map[string]*rtorrent.Torrent, len(m))
	for h, t := range m {
		t := t
		out[h] = &t
	}
	return out
}

func TestComputeDelta(t *testing.T) {
	prev := map[string]rtorrent.Torrent{
		"a": mkTorrent("a", 100, 10),
		"b": mkTorrent("b", 200, 20),
		"d": mkTorrent("d", 400, 40),
	}
	next := map[string]rtorrent.Torrent{
		"a": mkTorrent("a", 150, 15), // changed
		"d": mkTorrent("d", 400, 40), // unchanged
		"c": mkTorrent("c", 300, 30), // added
	}
	d := computeDelta(ptrs(prev), ptrs(next), time.Unix(1000, 0), nil, nil, nil)
	if len(d.Added) != 1 || d.Added[0].Hash != "c" {
		t.Fatalf("added = %+v", d.Added)
	}
	if len(d.Changed) != 1 || d.Changed[0].Hash != "a" {
		t.Fatalf("changed = %+v", d.Changed)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "b" {
		t.Fatalf("removed = %+v", d.Removed)
	}
	if !d.At.Equal(time.Unix(1000, 0)) {
		t.Fatalf("at = %v", d.At)
	}
}

func TestComputeDeltaNoChanges(t *testing.T) {
	ts := map[string]rtorrent.Torrent{"a": mkTorrent("a", 1, 1)}
	d := computeDelta(ptrs(ts), ptrs(ts), time.Now(), nil, nil, nil)
	if len(d.Added) != 0 || len(d.Changed) != 0 || len(d.Removed) != 0 {
		t.Fatalf("unexpected delta: %+v", d)
	}
}

func TestComputeDeltaIncludesExtendedTorrentFields(t *testing.T) {
	base := mkTorrent("a", 100, 10)
	base.AddedAt = time.Unix(100, 0)
	base.IsPrivate = false
	next := base
	next.AddedAt = time.Unix(101, 0)
	next.IsPrivate = true
	basePtr, nextPtr := base, next
	d := computeDelta(map[string]*rtorrent.Torrent{"a": &basePtr}, map[string]*rtorrent.Torrent{"a": &nextPtr}, time.Unix(200, 0), nil, nil, nil)
	if len(d.Changed) != 1 || d.Changed[0].AddedAt.Unix() != 101 || !d.Changed[0].IsPrivate {
		t.Fatalf("extended fields were not diffed: %+v", d)
	}
}

func TestComputeAggregates(t *testing.T) {
	ts := []rtorrent.Torrent{
		{State: rtorrent.StateDownloading, Label: "iso", TrackerHost: "a.com"},
		{State: rtorrent.StateDownloading, Label: "iso", TrackerHost: "a.com"},
		{State: rtorrent.StateSeeding, Label: "", TrackerHost: "b.com"},
		{State: rtorrent.StateStopped, Label: "", TrackerHost: ""},
	}
	agg := computeAggregates(ts)
	if agg.Status["all"] != 4 {
		t.Fatalf("all count = %d, want 4", agg.Status["all"])
	}
	if agg.Status[rtorrent.StateDownloading] != 2 || agg.Status[rtorrent.StateSeeding] != 1 || agg.Status[rtorrent.StateStopped] != 1 {
		t.Fatalf("status counts = %+v", agg.Status)
	}
	if agg.Labels["iso"] != 2 || agg.Labels[""] != 2 {
		t.Fatalf("label counts = %+v", agg.Labels)
	}
	if agg.Trackers["a.com"] != 2 || agg.Trackers["b.com"] != 1 || len(agg.Trackers) != 2 {
		t.Fatalf("tracker counts = %+v", agg.Trackers)
	}
}

func TestComputeAggregatesThrottleCounts(t *testing.T) {
	ts := []rtorrent.Torrent{
		{State: rtorrent.StateDownloading, Throttle: "slow"},
		{State: rtorrent.StateSeeding, Throttle: "slow"},
		{State: rtorrent.StateSeeding, Throttle: "seed"},
		{State: rtorrent.StateStopped, Throttle: ""},
	}
	agg := computeAggregates(ts)
	if agg.Throttles["slow"] != 2 || agg.Throttles["seed"] != 1 || len(agg.Throttles) != 2 {
		t.Fatalf("throttle counts = %+v", agg.Throttles)
	}
}

func TestResolveRatioGroups(t *testing.T) {
	rows := []rtorrent.Torrent{
		{Hash: "a", Custom2: "g2", Custom3: "g3"},
		{Hash: "b", Custom2: "", Custom4: "g4"},
	}
	resolved := resolveRatioGroups(rows, "custom3")
	if resolved[0].RatioGroup != "g3" || resolved[1].RatioGroup != "" {
		t.Fatalf("resolved = %+v", resolved)
	}
	// The input rows are untouched (snapshot ownership).
	if rows[0].RatioGroup != "" {
		t.Fatal("input rows were mutated")
	}
	resolved = resolveRatioGroups(rows, "custom4")
	if resolved[1].RatioGroup != "g4" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestComputeAggregatesCategoryMembership(t *testing.T) {
	ts := []rtorrent.Torrent{
		// Downloading at a non-zero rate is Active but not Completed or Inactive.
		{State: rtorrent.StateDownloading, DownRate: 1, IsOpen: true},
		// A completed seeder with no traffic is both Completed and Inactive.
		{State: rtorrent.StateSeeding, Complete: true, IsOpen: true},
		// Queued downloads are open but idle, so they are Inactive.
		{State: rtorrent.StateQueued, IsOpen: true},
		// A stopped torrent is not open and therefore not Inactive.
		{State: rtorrent.StateStopped},
		// Upload-only seeding is still Active.
		{State: rtorrent.StateSeeding, Complete: true, UpRate: 1, IsOpen: true},
	}

	agg := computeAggregates(ts)
	if got := agg.Status["completed"]; got != 2 {
		t.Fatalf("completed = %d, want 2", got)
	}
	if got := agg.Status["active"]; got != 2 {
		t.Fatalf("active = %d, want 2", got)
	}
	if got := agg.Status["inactive"]; got != 2 {
		t.Fatalf("inactive = %d, want 2", got)
	}
}

func TestHistoryRing(t *testing.T) {
	base := time.Unix(0, 0)
	h := newHistoryRing()
	// 61 minutes of samples at 1/s: the ring stays capped, pruning by
	// pointer bump instead of re-slicing.
	for i := 0; i < 3660; i++ {
		h.push(Sample{At: base.Add(time.Duration(i) * time.Second), DownRate: int64(i)}, base.Add(time.Duration(i)*time.Second))
	}
	if h.len() > historyCap {
		t.Fatalf("history grew to %d", h.len())
	}
	// Samples older than 60min are pruned.
	window := h.samples()
	if len(window) == 0 || len(window) > 3600 {
		t.Fatalf("window size = %d", len(window))
	}
	if window[len(window)-1].DownRate != 3659 {
		t.Fatalf("newest sample = %d", window[len(window)-1].DownRate)
	}
	if window[0].At.Before(base.Add(60 * time.Second)) {
		t.Fatalf("oldest sample = %v, want pruning at the 60min window", window[0].At)
	}
	// Order is oldest→newest across the wrap point.
	for i := 1; i < len(window); i++ {
		if !window[i].At.After(window[i-1].At) {
			t.Fatalf("ring out of order at %d", i)
		}
	}
}

func TestVolumeStatfs(t *testing.T) {
	vols := statVolumes([]string{"/", "/nonexistent-volume-xyz"})
	if len(vols) != 1 {
		t.Fatalf("volumes = %+v", vols)
	}
	if vols[0].TotalBytes == 0 {
		t.Fatal("total bytes = 0 for /")
	}
	if vols[0].UsedPercent() < 0 || vols[0].UsedPercent() > 100 {
		t.Fatalf("used percent out of range: %v", vols[0].UsedPercent())
	}
}

func TestPollerLifecycle(t *testing.T) {
	fc := &fakeClient{
		torrents: []rtorrent.Torrent{mkTorrent("a", 100, 500)},
		global:   rtorrent.GlobalStats{DownRate: 500, Version: "0.15.4"},
		detail:   rtorrent.Detail{Hash: "a"},
	}
	p := New(fc, Options{Interval: 5 * time.Millisecond, DetailInterval: 5 * time.Millisecond})

	var mu sync.Mutex
	var deltas []Delta
	unsub := p.Subscribe(func(d Delta) {
		mu.Lock()
		deltas = append(deltas, d)
		mu.Unlock()
	})
	defer unsub()

	p.Focus("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// First successful poll → connected with data.
	waitFor(t, 2*time.Second, func() bool {
		return p.Snapshot().Status == StatusConnected && len(p.Snapshot().Torrents) == 1
	}, "initial connect")

	// Detail was fetched for the focused hash.
	waitFor(t, 2*time.Second, func() bool {
		_, ok := p.Detail("a")
		return ok
	}, "detail fetch")

	// Simulate rtorrent going away.
	fc.mu.Lock()
	fc.listErr = errors.New("connection refused")
	fc.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool {
		s := p.Snapshot()
		return s.Status == StatusDisconnected && s.Stale && s.LastError != ""
	}, "disconnect + stale cache")

	// Data is retained but flagged stale.
	if s := p.Snapshot(); len(s.Torrents) != 1 {
		t.Fatal("stale snapshot lost data")
	}

	// Recovery: automatic reconnect refreshes state.
	fc.mu.Lock()
	fc.listErr = nil
	fc.torrents = append(fc.torrents, mkTorrent("b", 900, 100))
	fc.global.DownRate = 600
	fc.mu.Unlock()
	waitFor(t, 5*time.Second, func() bool {
		s := p.Snapshot()
		return s.Status == StatusConnected && !s.Stale && len(s.Torrents) == 2 && s.Global.DownRate == 600
	}, "reconnect")

	// History recorded samples from successful polls.
	waitFor(t, 2*time.Second, func() bool {
		return len(p.History()) >= 2
	}, "history samples")

	// Both connection transitions were pushed as events.
	mu.Lock()
	sawUp, sawDown, sawAggregates := false, false, false
	for _, d := range deltas {
		if d.Status == StatusConnected {
			sawUp = true
		}
		if d.Status == StatusDisconnected {
			sawDown = true
		}
		if d.Aggregates != nil && d.Aggregates.Status["all"] > 0 {
			sawAggregates = true
		}
	}
	mu.Unlock()
	if !sawUp || !sawDown || !sawAggregates {
		t.Fatalf("delta state missing: sawUp=%v sawDown=%v sawAggregates=%v", sawUp, sawDown, sawAggregates)
	}
}

func TestPollerBackoffCapped(t *testing.T) {
	fc := &fakeClient{listErr: errors.New("down")}
	p := New(fc, Options{
		Interval:    time.Hour, // never a successful poll
		BackoffBase: 5 * time.Millisecond,
		BackoffCap:  20 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	// With capped backoff the loop retries many times quickly.
	waitFor(t, 2*time.Second, func() bool {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return fc.listCalls > 20
	}, "backoff-capped retries")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestVolumeIntervalRefresh(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}}
	p := New(fc, Options{
		Interval:       5 * time.Millisecond,
		VolumeInterval: 5 * time.Millisecond,
		Volumes:        []string{"/"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		return len(p.Snapshot().Volumes) == 1
	}, "volume stats")
}

// TestSnapshotSharedWithinCycle proves the PERF-6.1 copy-on-publish
// contract: calls within one cycle share the same published pointer (no
// per-caller deep copy), and a new cycle publishes a new pointer while the
// old one keeps showing its cycle's data.
func TestSnapshotSharedWithinCycle(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}}
	p := New(fc, Options{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "first poll")

	s1 := p.Snapshot()
	if s1 != p.Snapshot() {
		t.Fatal("Snapshot did not return the published pointer")
	}

	// A new cycle publishes a new pointer; the old one is stable.
	fc.mu.Lock()
	fc.torrents = []rtorrent.Torrent{mkTorrent("a", 1, 1), mkTorrent("b", 2, 2)}
	fc.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool { return len(p.Snapshot().Torrents) == 2 }, "second poll")
	s2 := p.Snapshot()
	if s1 == s2 {
		t.Fatal("new cycle did not publish a new snapshot")
	}
	if len(s1.Torrents) != 1 {
		t.Fatalf("old snapshot changed underfoot: %+v", s1.Torrents)
	}
}

func TestUnfocusDropsDetail(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}, detail: rtorrent.Detail{Hash: "a"}}
	p := New(fc, Options{Interval: 5 * time.Millisecond, DetailInterval: 5 * time.Millisecond})
	p.Focus("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		_, ok := p.Detail("a")
		return ok
	}, "detail present")
	p.Unfocus("a")
	if _, ok := p.Detail("a"); ok {
		t.Fatal("detail should be dropped after unfocus")
	}
}

func TestSpeedRingSamplesFocusedAndRetainsAfterUnfocus(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 100)}}
	p := New(fc, Options{Interval: 5 * time.Millisecond})
	p.Focus("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Samples accumulate while focused.
	waitFor(t, 2*time.Second, func() bool {
		return len(p.SpeedHistory("a")) >= 3
	}, "focused speed samples")

	// Rates change; newer samples reflect the current torrent row.
	fc.mu.Lock()
	fc.torrents = []rtorrent.Torrent{mkTorrent("a", 1, 250)}
	fc.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool {
		h := p.SpeedHistory("a")
		return len(h) > 0 && h[len(h)-1].DownRate == 250
	}, "speed ring picks up new rate")

	// Unfocus: detail is dropped but the ring survives for retention.
	p.Unfocus("a")
	if _, ok := p.Detail("a"); ok {
		t.Fatal("detail should drop after unfocus")
	}
	time.Sleep(20 * time.Millisecond)
	if len(p.SpeedHistory("a")) == 0 {
		t.Fatal("speed ring should survive after unfocus within the retention window")
	}
}

func TestSpeedRingPrunesAfterRetention(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 100)}}
	p := New(fc, Options{Interval: 5 * time.Millisecond})
	p.Focus("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		return len(p.SpeedHistory("a")) >= 2
	}, "samples")
	p.Unfocus("a")

	// Force the retention deadline to pass so the next poll prunes it.
	p.mu.Lock()
	if st := p.speed.byHash["a"]; st != nil {
		st.unfocused = time.Now().Add(-speedRetainAfterUnfocus - time.Second)
	}
	p.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool {
		return len(p.SpeedHistory("a")) == 0
	}, "ring pruned after retention")
}

func TestSpeedRingNotSampledForUnfocusedHashes(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 100)}}
	p := New(fc, Options{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Never focused: no ring exists even though polls are running.
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "connect")
	if got := p.SpeedHistory("a"); len(got) != 0 {
		t.Fatalf("unfocused hash has speed history: %+v", got)
	}
}

func TestOnTorrentMessageFiresOnTransition(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 100)}}
	var mu sync.Mutex
	var seen []string
	p := New(fc, Options{
		Interval: 5 * time.Millisecond,
		OnTorrentMessage: func(hash, message string) {
			mu.Lock()
			seen = append(seen, hash+":"+message)
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "connect")

	// Introduce a message transition.
	fc.mu.Lock()
	bad := mkTorrent("a", 1, 100)
	bad.Message = "Tracker: [Tried all trackers.]"
	fc.torrents = []rtorrent.Torrent{bad}
	fc.mu.Unlock()

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 1 && seen[0] == "a:Tracker: [Tried all trackers.]"
	}, "message transition callback")

	// Same message again: no duplicate callback.
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	n := len(seen)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("callback fired %d times for unchanged message, want 1", n)
	}
}

// TestOnTorrentCompleteFiresOnTransition asserts the PAR-3.2 hook: it fires
// once when d.complete flips false→true, never for already-complete rows or
// torrents first seen complete.
func TestOnTorrentCompleteFiresOnTransition(t *testing.T) {
	first := mkTorrent("a", 1, 100)
	fc := &fakeClient{torrents: []rtorrent.Torrent{first}}
	var mu sync.Mutex
	var seen []rtorrent.Torrent
	p := New(fc, Options{
		Interval: 5 * time.Millisecond,
		OnTorrentComplete: func(hash string, torrent rtorrent.Torrent) {
			mu.Lock()
			seen = append(seen, torrent)
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "connect")

	// Flip the torrent to complete: one callback with the new row.
	fc.mu.Lock()
	done := mkTorrent("a", 1000, 0)
	done.Complete = true
	fc.torrents = []rtorrent.Torrent{done}
	// A torrent that appears complete for the first time must NOT fire.
	backlog := mkTorrent("b", 1000, 0)
	backlog.Complete = true
	fc.torrents = append(fc.torrents, backlog)
	fc.mu.Unlock()

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 1
	}, "complete transition callback")
	mu.Lock()
	if seen[0].Hash != "a" || !seen[0].Complete {
		t.Fatalf("callback torrent = %+v", seen[0])
	}
	mu.Unlock()

	// Still complete on later polls: no duplicate callback.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := len(seen)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("callback fired %d times, want 1", n)
	}
}

// TestSeedingTriggerFiresOncePerGroup asserts PAR-4.2 enforcement: a seeding
// torrent in a group whose min_ratio is met triggers exactly once across
// polls; moving it to another group re-arms evaluation for that group.
func TestSeedingTriggerFiresOncePerGroup(t *testing.T) {
	seed := rtorrent.Torrent{
		Hash: "s1", Name: "seed", State: rtorrent.StateSeeding,
		Ratio: 2.0, Custom2: "archive", RatioGroup: "archive",
		Complete: true, IsOpen: true,
		FinishedAt: time.Now().Add(-time.Hour),
	}
	// A complete, open torrent with a tracker warning (UI state error) is
	// still seeding and must also be evaluated.
	warned := rtorrent.Torrent{
		Hash: "s2", Name: "warned", State: rtorrent.StateError,
		Ratio: 3.0, Custom2: "archive", RatioGroup: "archive",
		Complete: true, IsOpen: true, Message: "Tracker: [Tried all trackers.]",
		FinishedAt: time.Now().Add(-time.Hour),
	}
	fc := &fakeClient{torrents: []rtorrent.Torrent{seed, warned}}
	marker, err := seeding.NewMarker(filepath.Join(t.TempDir(), "seeding-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var jobs []seeding.Job
	var groupsMu sync.Mutex
	groups := []config.SeedingGroup{{Name: "archive", MinRatio: 1.0, Action: config.SeedingStop}}
	p := New(fc, Options{
		Interval: 5 * time.Millisecond,
		SeedingGroups: func() []config.SeedingGroup {
			groupsMu.Lock()
			defer groupsMu.Unlock()
			return append([]config.SeedingGroup(nil), groups...)
		},
		SeedingSlot:   func() string { return "custom2" },
		SeedingMarker: marker,
		OnSeedingTrigger: func(job seeding.Job) bool {
			mu.Lock()
			jobs = append(jobs, job)
			mu.Unlock()
			return true
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(jobs) == 2
	}, "seeding trigger")
	mu.Lock()
	byHash := map[string]seeding.Job{}
	for _, job := range jobs {
		byHash[job.Hash] = job
	}
	if byHash["s1"].Group != "archive" || byHash["s1"].Action != config.SeedingStop {
		t.Fatalf("jobs = %+v", byHash)
	}
	if byHash["s2"].Group != "archive" {
		t.Fatalf("error-state seeder not evaluated: %+v", byHash)
	}
	mu.Unlock()

	// Later polls must not re-fire for the same pair.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := len(jobs)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("trigger fired %d times, want 2", n)
	}

	// Moving the torrent to another group re-arms evaluation there.
	fc.mu.Lock()
	moved := seed
	moved.Custom2 = "prune"
	moved.RatioGroup = "prune"
	fc.torrents = []rtorrent.Torrent{moved}
	fc.mu.Unlock()
	// Point the config at the new group too (assignment alone is not enough;
	// the group must exist).
	groupsMu.Lock()
	groups = append(groups, config.SeedingGroup{Name: "prune", MinRatio: 1.0, Action: config.SeedingErase})
	groupsMu.Unlock()
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(jobs) == 3
	}, "trigger after group change")
	mu.Lock()
	if jobs[2].Hash != "s1" || jobs[2].Group != "prune" || jobs[2].Action != config.SeedingErase {
		t.Fatalf("third job = %+v", jobs)
	}
	mu.Unlock()
}

func TestOnConnectFiresPerReconnect(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}}
	var mu sync.Mutex
	connects := 0
	p := New(fc, Options{
		Interval: 5 * time.Millisecond,
		OnConnect: func(ctx context.Context) {
			mu.Lock()
			connects++
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "connect")
	time.Sleep(20 * time.Millisecond) // several more poll cycles
	mu.Lock()
	n := connects
	mu.Unlock()
	if n != 1 {
		t.Fatalf("OnConnect fired %d times on steady connection, want 1", n)
	}

	// Drop and recover: OnConnect fires again.
	fc.mu.Lock()
	fc.listErr = errors.New("down")
	fc.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusDisconnected }, "disconnect")
	fc.mu.Lock()
	fc.listErr = nil
	fc.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "reconnect")
	mu.Lock()
	n = connects
	mu.Unlock()
	if n != 2 {
		t.Fatalf("OnConnect fired %d times after reconnect, want 2", n)
	}
}

// stallClient blocks FetchDetail until released, proving detail I/O never
// holds the poller lock (PERF-6.1 regression test).
type stallClient struct {
	fakeClient
	started chan struct{}
	release chan struct{}
}

func (f *stallClient) FetchDetail(ctx context.Context, hash string) (rtorrent.Detail, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	select {
	case <-f.release:
		return f.detail, nil
	case <-ctx.Done():
		return rtorrent.Detail{}, ctx.Err()
	}
}

// TestSnapshotUnblockedByStalledDetail asserts Snapshot returns within 1ms
// while a detail fetch is artificially stalled: readers take only a short
// RLock, never waiting on SCGI I/O.
func TestSnapshotUnblockedByStalledDetail(t *testing.T) {
	fc := &stallClient{
		fakeClient: fakeClient{
			torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)},
			detail:   rtorrent.Detail{Hash: "a"},
		},
		started: make(chan struct{}, 16),
		release: make(chan struct{}),
	}
	p := New(fc, Options{Interval: 5 * time.Millisecond, DetailInterval: time.Millisecond})
	p.Focus("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "first poll")
	select {
	case <-fc.started:
	case <-time.After(2 * time.Second):
		t.Fatal("detail fetch never started")
	}

	// The fetch is now stalled mid-SCGI-call. Snapshot must still be instant.
	for i := 0; i < 5; i++ {
		start := time.Now()
		s := p.Snapshot()
		if elapsed := time.Since(start); elapsed > time.Millisecond {
			t.Fatalf("Snapshot took %v with stalled detail fetch, want <1ms", elapsed)
		}
		if s.Status != StatusConnected || len(s.Torrents) != 1 {
			t.Fatalf("snapshot = %+v", s)
		}
		time.Sleep(time.Millisecond)
	}

	close(fc.release)
	waitFor(t, 2*time.Second, func() bool {
		_, ok := p.Detail("a")
		return ok
	}, "detail lands after release")
}

// TestDiffTorrentFieldsCoversCatalogue guards the v2 patch contract: every
// Torrent field except the hash key must appear in the patch when (and only
// when) it differs, so the catalogue cannot grow a silently-unpatched field.
func TestDiffTorrentFieldsCoversCatalogue(t *testing.T) {
	base := mkTorrent("a", 100, 10)
	base.AddedAt = time.Unix(100, 0)
	rt := reflect.TypeOf(base)
	covered := 0
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "hash" {
			continue
		}
		next := base
		rv := reflect.ValueOf(&next).Elem().Field(i)
		switch rv.Kind() {
		case reflect.String:
			rv.SetString(fmt.Sprintf("changed-%d", i))
		case reflect.Int, reflect.Int64:
			rv.SetInt(rv.Int() + 1 + int64(i))
		case reflect.Bool:
			rv.SetBool(!rv.Bool())
		case reflect.Float64:
			rv.SetFloat(rv.Float() + 0.5)
		case reflect.Struct: // time.Time
			rv.Set(reflect.ValueOf(time.Unix(200+int64(i), 0)))
		default:
			t.Fatalf("field %s has unhandled kind %v", field.Name, rv.Kind())
		}
		if !torrentChanged(base, next) {
			t.Fatalf("field %s change not detected by ==", field.Name)
		}
		patch := diffTorrentFields(base, next)
		if len(patch) != 1 {
			t.Fatalf("field %s produced patch keys %v, want exactly [%s]", field.Name, keysOf(patch), tag)
		}
		if _, ok := patch[tag]; !ok {
			t.Fatalf("field %s produced patch keys %v, want [%s]", field.Name, keysOf(patch), tag)
		}
		covered++
	}
	if covered < 40 {
		t.Fatalf("only %d fields covered, catalogue shrank?", covered)
	}
	if len(diffTorrentFields(base, base)) != 0 {
		t.Fatal("identical torrents produced a non-empty patch")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestDiffAggregates(t *testing.T) {
	mk := func() *Aggregates {
		return &Aggregates{
			Status:    map[rtorrent.State]int{rtorrent.StateDownloading: 2, rtorrent.StateSeeding: 1},
			Labels:    map[string]int{"iso": 2},
			Trackers:  map[string]int{"a.com": 2},
			Throttles: map[string]int{},
		}
	}
	if p := DiffAggregates(mk(), mk()); p != nil {
		t.Fatalf("equal aggregates produced %+v", p)
	}
	if p := DiffAggregates(mk(), nil); p != nil {
		t.Fatalf("nil new produced %+v", p)
	}
	// Status change ships the whole status map.
	next := mk()
	next.Status[rtorrent.StateDownloading] = 3
	p := DiffAggregates(mk(), next)
	if p == nil || p.Status[rtorrent.StateDownloading] != 3 || p.Labels != nil {
		t.Fatalf("status patch = %+v", p)
	}
	// Label add/change/remove fold into updated/removed.
	next = mk()
	next.Labels["iso"] = 3
	next.Labels["new"] = 1
	next.Trackers = map[string]int{}
	p = DiffAggregates(mk(), next)
	if p == nil || p.Status != nil {
		t.Fatalf("label patch status = %+v", p)
	}
	if p.Labels == nil || p.Labels.Updated["iso"] != 3 || p.Labels.Updated["new"] != 1 {
		t.Fatalf("label patch = %+v", p.Labels)
	}
	if p.Trackers == nil || len(p.Trackers.Removed) != 1 || p.Trackers.Removed[0] != "a.com" {
		t.Fatalf("tracker patch = %+v", p.Trackers)
	}
	// Nil baseline sends full maps.
	p = DiffAggregates(nil, mk())
	if p == nil || len(p.Status) != 2 || p.Labels.Updated["iso"] != 2 {
		t.Fatalf("nil-baseline patch = %+v", p)
	}
}

// manualClock is a test Now func advanced by hand so poll timing is
// deterministic without sleeping.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// detailCalls counts FetchDetail invocations on the shared fake.
func (f *fakeClient) fetchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detailCalls
}

// TestDetailBackoffStretchesWhenCalm proves change-driven detail refresh
// (PERF-6.3): an unchanged payload stretches the per-hash refresh 1x, 2x,
// 4x, then caps at 8x the base interval. Driven by direct pollOnce calls on
// a manual clock — no timing flakes.
func TestDetailBackoffStretchesWhenCalm(t *testing.T) {
	clk := &manualClock{t: time.Unix(1000, 0)}
	fc := &fakeClient{
		torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)},
		detail:   rtorrent.Detail{Hash: "a"},
	}
	p := New(fc, Options{Interval: time.Second, DetailInterval: 5 * time.Millisecond, Now: clk.now})
	p.Focus("a")
	ctx := context.Background()

	p.pollOnce(ctx) // t=0: first fetch, calm 0
	if n := fc.fetchCount(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
	clk.advance(5 * time.Millisecond)
	p.pollOnce(ctx) // due at 1x: fetch, payload equal → calm 1
	if n := fc.fetchCount(); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}
	clk.advance(5 * time.Millisecond)
	p.pollOnce(ctx) // t=10: next due at +10ms → skip
	if n := fc.fetchCount(); n != 2 {
		t.Fatalf("fetches = %d, want still 2 (2x stretch)", n)
	}
	clk.advance(5 * time.Millisecond)
	p.pollOnce(ctx) // t=15: fetch, calm 2 (due every 20ms)
	if n := fc.fetchCount(); n != 3 {
		t.Fatalf("fetches = %d, want 3", n)
	}
	clk.advance(19 * time.Millisecond)
	p.pollOnce(ctx) // t=34: due at +20 → skip
	if n := fc.fetchCount(); n != 3 {
		t.Fatalf("fetches = %d, want still 3 (4x stretch)", n)
	}
	clk.advance(time.Millisecond)
	p.pollOnce(ctx) // t=35: fetch, calm 3 (capped: due every 40ms)
	if n := fc.fetchCount(); n != 4 {
		t.Fatalf("fetches = %d, want 4", n)
	}
	clk.advance(39 * time.Millisecond)
	p.pollOnce(ctx) // t=74: skip
	if n := fc.fetchCount(); n != 4 {
		t.Fatalf("fetches = %d, want still 4 (8x cap)", n)
	}
	clk.advance(time.Millisecond)
	p.pollOnce(ctx) // t=75: fetch, calm stays capped
	if n := fc.fetchCount(); n != 5 {
		t.Fatalf("fetches = %d, want 5", n)
	}
	if st := p.detailState["a"]; st == nil || st.calm != maxDetailCalmShift {
		t.Fatalf("calm = %+v, want capped at %d", st, maxDetailCalmShift)
	}
}

// TestDetailBackoffResetsOnChange proves any payload change snaps the
// refresh back to the base interval.
func TestDetailBackoffResetsOnChange(t *testing.T) {
	clk := &manualClock{t: time.Unix(2000, 0)}
	fc := &fakeClient{
		torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)},
		detail:   rtorrent.Detail{Hash: "a"},
	}
	p := New(fc, Options{Interval: time.Second, DetailInterval: 5 * time.Millisecond, Now: clk.now})
	p.Focus("a")
	ctx := context.Background()

	p.pollOnce(ctx)
	clk.advance(5 * time.Millisecond)
	p.pollOnce(ctx) // calm 1 after two identical fetches
	if n := fc.fetchCount(); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}

	// Peers arrive: the next due poll fetches and resets calm to 0.
	fc.mu.Lock()
	fc.detail.Transfer.DownloadedBytes = 999
	fc.mu.Unlock()
	clk.advance(10 * time.Millisecond)
	p.pollOnce(ctx)
	if n := fc.fetchCount(); n != 3 {
		t.Fatalf("fetches = %d, want 3", n)
	}
	if st := p.detailState["a"]; st == nil || st.calm != 0 {
		t.Fatalf("calm = %+v, want reset to 0", st)
	}
	// Back at the base interval: the very next tick fetches again and the
	// (now stable) payload starts a fresh calm count.
	clk.advance(5 * time.Millisecond)
	p.pollOnce(ctx)
	if n := fc.fetchCount(); n != 4 {
		t.Fatalf("fetches = %d, want 4 (base interval after reset)", n)
	}
	if st := p.detailState["a"]; st == nil || st.calm != 1 {
		t.Fatalf("calm = %+v, want 1", st)
	}
}

// TestDetailHashAccessor proves DetailHash exposes the fetch-time
// fingerprint clients use to skip unchanged sends.
func TestDetailHashAccessor(t *testing.T) {
	fc := &fakeClient{
		torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)},
		detail:   rtorrent.Detail{Hash: "a"},
	}
	p := New(fc, Options{Interval: 5 * time.Millisecond, DetailInterval: time.Millisecond})
	p.Focus("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		_, _, ok := p.DetailHash("a")
		return ok
	}, "detail hash present")
	d, h1, ok := p.DetailHash("a")
	if !ok || d.Hash != "a" || h1 == 0 {
		t.Fatalf("detail hash = %v %d %v", d, h1, ok)
	}
	_, h2, _ := p.DetailHash("a")
	if h1 != h2 {
		t.Fatal("hash moved without a fetch")
	}
	if _, _, ok := p.DetailHash("missing"); ok {
		t.Fatal("unknown hash reported ok")
	}
}

// TestNextWaitAdaptivity proves the wait schedule: errors back off with the
// cap, active clients poll at Interval, idle cycles double toward the live
// max cap, and any activity snaps back.
func TestNextWaitAdaptivity(t *testing.T) {
	fc := &fakeClient{}
	active := true
	p := New(fc, Options{
		Interval: 10 * time.Millisecond, MaxInterval: 40 * time.Millisecond,
		BackoffBase: 5 * time.Millisecond, BackoffCap: 20 * time.Millisecond,
		Active: func() bool { return active },
	})

	if w := p.nextWait(nil); w != 10*time.Millisecond {
		t.Fatalf("active wait = %v", w)
	}
	// Failure backoff with cap, resetting the idle stretch.
	for i, want := range []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond} {
		if w := p.nextWait(errors.New("down")); w != want {
			t.Fatalf("backoff[%d] = %v, want %v", i, w, want)
		}
	}
	if w := p.nextWait(nil); w != 10*time.Millisecond {
		t.Fatalf("recovered wait = %v", w)
	}
	// Idle stretch toward the cap.
	active = false
	for i, want := range []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond} {
		if w := p.nextWait(nil); w != want {
			t.Fatalf("idle[%d] = %v, want %v", i, w, want)
		}
	}
	// Snap back on activity, and a live cap change applies.
	active = true
	if w := p.nextWait(nil); w != 10*time.Millisecond {
		t.Fatalf("snap-back wait = %v", w)
	}
	p.SetMaxInterval(25 * time.Millisecond)
	active = false
	if w := p.nextWait(nil); w != 20*time.Millisecond {
		t.Fatalf("idle wait = %v", w)
	}
	if w := p.nextWait(nil); w != 25*time.Millisecond {
		t.Fatalf("capped wait = %v, want live 25ms cap", w)
	}
}

// TestAdaptivePollingIntegration watches the real loop stretch while idle
// and snap back on activity. Bounds are wide on purpose: it asserts the
// direction, while TestNextWaitAdaptivity pins the exact schedule.
func TestAdaptivePollingIntegration(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}}
	var mu sync.Mutex
	active := true
	p := New(fc, Options{
		Interval: 10 * time.Millisecond, MaxInterval: 80 * time.Millisecond,
		Active: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return active
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "connect")

	time.Sleep(150 * time.Millisecond)
	fc.mu.Lock()
	activeCalls := fc.listCalls
	fc.mu.Unlock()

	mu.Lock()
	active = false
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	fc.mu.Lock()
	idleCalls := fc.listCalls - activeCalls
	fc.mu.Unlock()

	mu.Lock()
	active = true
	mu.Unlock()
	if idleCalls > 6 {
		t.Fatalf("idle polls = %d in 200ms, want a stretched trickle", idleCalls)
	}
	if activeCalls < 8 {
		t.Fatalf("active polls = %d in 150ms, loop is not polling at interval", activeCalls)
	}
}

// TestPerTorrentRingEvictsOldest proves the per-torrent speed history is a
// proper fixed-capacity ring (PERF-6.4): pushes past capacity overwrite the
// oldest in place, and window() returns the live range oldest→newest.
func TestPerTorrentRingEvictsOldest(t *testing.T) {
	base := time.Unix(5000, 0)
	r := newPerTorrentRing(4)
	for i := 0; i < 6; i++ {
		r.push(rateSample{At: base.Add(time.Duration(i) * time.Second), DownRate: int64(i)})
	}
	if r.size != 4 {
		t.Fatalf("size = %d, want the 4 newest", r.size)
	}
	got := r.window(base) // everything newer than base
	if len(got) != 4 || got[0].DownRate != 2 || got[3].DownRate != 5 {
		t.Fatalf("window = %+v, want rates 2..5", got)
	}
	// A cutoff drops the stale prefix without touching storage.
	got = r.window(base.Add(5 * time.Second))
	if len(got) != 1 || got[0].DownRate != 5 {
		t.Fatalf("cutoff window = %+v", got)
	}
}

// TestConnectedSinceStableAcrossPolls covers the status bar's uptime
// readout. The timestamp must survive later cycles — the arguments to
// orFirst were reversed, so it was recomputed every poll and uptime never
// exceeded one interval — and must restart after a reconnect.
func TestConnectedSinceStableAcrossPolls(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}}
	p := New(fc, Options{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "first poll")
	first := p.Snapshot().ConnectedSince
	if first.IsZero() {
		t.Fatal("ConnectedSince unset after connecting")
	}

	gen := p.Snapshot().GeneratedAt
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().GeneratedAt.After(gen) }, "a later cycle")
	if got := p.Snapshot().ConnectedSince; !got.Equal(first) {
		t.Fatalf("ConnectedSince moved across polls: %v -> %v", first, got)
	}

	// A reconnect starts a new connection, so the clock restarts.
	p.onDisconnect(errors.New("down"))
	if !p.Snapshot().ConnectedSince.IsZero() {
		t.Fatal("disconnect left the previous connection's clock running")
	}
	waitFor(t, 2*time.Second, func() bool {
		s := p.Snapshot()
		return s.Status == StatusConnected && !s.ConnectedSince.IsZero()
	}, "reconnect")
	if got := p.Snapshot().ConnectedSince; got.Equal(first) {
		t.Fatalf("reconnect did not restart the clock (still %v)", got)
	}
}

type preservationCacheClient struct {
	fakeClient
	detailErr error
}

func (c *preservationCacheClient) FetchDetail(ctx context.Context, hash string) (rtorrent.Detail, error) {
	if c.detailErr != nil {
		return rtorrent.Detail{}, c.detailErr
	}
	return c.fakeClient.FetchDetail(ctx, hash)
}
func TestPreservationTrackerCacheKeepsSuccessfulReadTime(t *testing.T) {
	clk := &manualClock{t: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	client := &preservationCacheClient{fakeClient: fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}, detail: rtorrent.Detail{Trackers: []rtorrent.Tracker{{Seeds: 3}}}}}
	p := New(client, Options{Now: clk.now})
	p.Focus("a")
	if err := p.pollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	trackers, at := p.CachedTrackers("a")
	if len(trackers) != 1 || at.IsZero() {
		t.Fatal("missing cache provenance")
	}
	calls := client.fetchCount()
	p.CachedTrackers("a")
	p.CachedTrackers("missing")
	if client.fetchCount() != calls {
		t.Fatal("cache inspection fetched detail")
	}
	clk.advance(time.Minute)
	client.detailErr = errors.New("offline")
	p.fetchDetails(context.Background(), []string{"a"})
	_, after := p.CachedTrackers("a")
	if !after.Equal(at) {
		t.Fatal("failed read refreshed timestamp")
	}
}
