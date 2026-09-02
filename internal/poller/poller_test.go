package poller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"blackbird/internal/rtorrent"
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
}

func (f *fakeClient) ListTorrents(ctx context.Context) ([]rtorrent.Torrent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.torrents, nil
}

func (f *fakeClient) GlobalStats(ctx context.Context) (rtorrent.GlobalStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.global, nil
}

func (f *fakeClient) FetchDetail(ctx context.Context, hash string) (rtorrent.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	d := computeDelta(prev, next, time.Unix(1000, 0))
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
	d := computeDelta(ts, ts, time.Now())
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
	d := computeDelta(map[string]rtorrent.Torrent{"a": base}, map[string]rtorrent.Torrent{"a": next}, time.Unix(200, 0))
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

func TestHistoryRing(t *testing.T) {
	base := time.Unix(0, 0)
	var h []Sample
	// 61 minutes of samples at 1/s: buffer must not grow unbounded.
	for i := 0; i < 3660; i++ {
		h = appendSample(h, Sample{At: base.Add(time.Duration(i) * time.Second), DownRate: int64(i)}, base.Add(time.Duration(i)*time.Second))
	}
	if len(h) > historyCap {
		t.Fatalf("history grew to %d", len(h))
	}
	// Samples older than 60min are pruned.
	window := samplesSince(h, base.Add(3660*time.Second))
	if len(window) == 0 || len(window) > 3600 {
		t.Fatalf("window size = %d", len(window))
	}
	if window[len(window)-1].DownRate != 3659 {
		t.Fatalf("newest sample = %d", window[len(window)-1].DownRate)
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
	sawUp, sawDown := false, false
	for _, d := range deltas {
		if d.Status == StatusConnected {
			sawUp = true
		}
		if d.Status == StatusDisconnected {
			sawDown = true
		}
	}
	mu.Unlock()
	if !sawUp || !sawDown {
		t.Fatalf("status transitions missing: sawUp=%v sawDown=%v", sawUp, sawDown)
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

func TestSnapshotIsACopy(t *testing.T) {
	fc := &fakeClient{torrents: []rtorrent.Torrent{mkTorrent("a", 1, 1)}}
	p := New(fc, Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Status == StatusConnected }, "first poll")

	s1 := p.Snapshot()
	s1.Torrents[0].Name = "mutated"
	if p.Snapshot().Torrents[0].Name == "mutated" {
		t.Fatal("snapshot not copied")
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
