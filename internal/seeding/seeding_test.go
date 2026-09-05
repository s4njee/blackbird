package seeding

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
)

func seedingTorrent(hash string) rtorrent.Torrent {
	return rtorrent.Torrent{
		Hash: hash, Name: "torrent-" + hash, State: rtorrent.StateSeeding,
		Ratio: 1.5, UploadedBytes: 2_000_000_000,
		FinishedAt: time.Now().Add(-48 * time.Hour),
	}
}

func TestEvaluateConditions(t *testing.T) {
	now := time.Now()
	base := config.SeedingGroup{Name: "g", Action: config.SeedingStop}
	cases := []struct {
		what    string
		mutate  func(*config.SeedingGroup, *rtorrent.Torrent)
		want    bool
		wantCon string
	}{
		{"nothing set never fires", func(g *config.SeedingGroup, tr *rtorrent.Torrent) {}, false, ""},
		{"min_ratio met", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MinRatio = 1.0 }, true, ConditionMinRatio},
		{"min_ratio unmet", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MinRatio = 2.0 }, false, ""},
		{"min_ratio boundary fires", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MinRatio = 1.5 }, true, ConditionMinRatio},
		{"max_ratio met", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MaxRatio = 1.0 }, true, ConditionMaxRatio},
		{"max_ratio unmet", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MaxRatio = 2.0 }, false, ""},
		{"min_upload met", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MinUploadBytes = 1_000_000_000 }, true, ConditionMinUpload},
		{"min_upload unmet", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MinUploadBytes = 3_000_000_000 }, false, ""},
		{"max_time met", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MaxSeedingTime = 24 * time.Hour }, true, ConditionMaxTime},
		{"max_time unmet", func(g *config.SeedingGroup, tr *rtorrent.Torrent) { g.MaxSeedingTime = 72 * time.Hour }, false, ""},
		{"max_time without finished skips", func(g *config.SeedingGroup, tr *rtorrent.Torrent) {
			g.MaxSeedingTime = time.Hour
			tr.FinishedAt = time.Time{}
		}, false, ""},
	}
	for _, tc := range cases {
		group := base
		torrent := seedingTorrent("h")
		tc.mutate(&group, &torrent)
		trigger, ok := Evaluate(group, torrent, now)
		if ok != tc.want {
			t.Errorf("%s: got %v, want %v", tc.what, ok, tc.want)
			continue
		}
		if ok && trigger.Condition != tc.wantCon {
			t.Errorf("%s: condition = %q, want %q", tc.what, trigger.Condition, tc.wantCon)
		}
	}
}

func TestEvaluateFirstMetWins(t *testing.T) {
	group := config.SeedingGroup{Name: "g", Action: config.SeedingStop, MinRatio: 1.0, MaxRatio: 1.0}
	trigger, ok := Evaluate(group, seedingTorrent("h"), time.Now())
	if !ok || trigger.Condition != ConditionMinRatio {
		t.Fatalf("trigger = %+v, %v", trigger, ok)
	}
}

func TestFindGroup(t *testing.T) {
	groups := []config.SeedingGroup{{Name: "a"}, {Name: "b"}}
	if FindGroup(groups, "b") == nil || FindGroup(groups, "B") != nil || FindGroup(groups, "c") != nil {
		t.Fatal("group lookup must be an exact match")
	}
}

// fakeDaemon records enforcement actions.
type fakeDaemon struct {
	mu       sync.Mutex
	stops    []string
	labels   map[string]string
	erased   []string
	erasedWD []string
	fail     map[string]error // hash → forced error
}

func (f *fakeDaemon) Stop(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[hash]; err != nil {
		return err
	}
	f.stops = append(f.stops, hash)
	return nil
}

func (f *fakeDaemon) SetLabel(_ context.Context, hash, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.labels == nil {
		f.labels = map[string]string{}
	}
	f.labels[hash] = label
	return nil
}

func (f *fakeDaemon) Erase(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.erased = append(f.erased, hash)
	return nil
}

func (f *fakeDaemon) RemoveWithData(_ context.Context, hash string, _ []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.erasedWD = append(f.erasedWD, hash)
	return "/tmp/" + hash, nil
}

func (f *fakeDaemon) counts() (stops int, labels int, erased int, erasedWD int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stops), len(f.labels), len(f.erased), len(f.erasedWD)
}

func startEngine(t *testing.T, daemon *fakeDaemon, hist *history.Log) *Engine {
	t.Helper()
	if hist == nil {
		hist = history.New(history.Options{})
	}
	e := New(Options{
		Log:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Daemon:  daemon,
		History: hist,
		Roots:   func() []string { return []string{"/tmp"} },
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.Run(ctx)
	return e
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func historyFor(t *testing.T, hist *history.Log, hash, action string) history.Entry {
	t.Helper()
	for _, e := range hist.ForHash(hash) {
		if e.Actor == "seeding" && e.Action == action {
			return e
		}
	}
	t.Fatalf("no seeding/%s entry in %+v", action, hist.ForHash(hash))
	return history.Entry{}
}

func TestEngineStop(t *testing.T) {
	daemon := &fakeDaemon{fail: map[string]error{}}
	hist := history.New(history.Options{})
	e := startEngine(t, daemon, hist)

	e.Enqueue(Job{Hash: "h1", Name: "T", Group: "g", Condition: ConditionMinRatio, Action: config.SeedingStop})
	waitFor(t, "stop", func() bool {
		stops, _, _, _ := daemon.counts()
		return stops == 1
	})
	entry := historyFor(t, hist, "h1", config.SeedingStop)
	if entry.Result != "ok" || !containsStr(entry.Message, `"g"`) {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestEngineStopAndSetLabel(t *testing.T) {
	daemon := &fakeDaemon{fail: map[string]error{}}
	hist := history.New(history.Options{})
	e := startEngine(t, daemon, hist)

	e.Enqueue(Job{Hash: "h2", Name: "T", Group: "g", Condition: ConditionMaxRatio, Action: config.SeedingStopAndSetLabel, Label: "done"})
	waitFor(t, "label", func() bool {
		_, labels, _, _ := daemon.counts()
		return labels == 1
	})
	if daemon.labels["h2"] != "done" {
		t.Fatalf("labels = %+v", daemon.labels)
	}
	entry := historyFor(t, hist, "h2", config.SeedingStopAndSetLabel)
	if entry.Result != "ok" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestEngineErase(t *testing.T) {
	daemon := &fakeDaemon{fail: map[string]error{}}
	hist := history.New(history.Options{})
	e := startEngine(t, daemon, hist)

	e.Enqueue(Job{Hash: "h3", Name: "T", Group: "g", Condition: ConditionMinUpload, Action: config.SeedingErase})
	waitFor(t, "erase", func() bool {
		_, _, erased, _ := daemon.counts()
		return erased == 1
	})
	entry := historyFor(t, hist, "h3", config.SeedingErase)
	if entry.Result != "ok" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestEngineEraseWithData(t *testing.T) {
	daemon := &fakeDaemon{fail: map[string]error{}}
	hist := history.New(history.Options{})
	e := startEngine(t, daemon, hist)

	e.Enqueue(Job{Hash: "h4", Name: "T", Group: "g", Condition: ConditionMaxTime, Action: config.SeedingEraseWithData})
	waitFor(t, "erase with data", func() bool {
		_, _, _, erasedWD := daemon.counts()
		return erasedWD == 1
	})
	entry := historyFor(t, hist, "h4", config.SeedingEraseWithData)
	if entry.Result != "ok" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestEngineFailureLogged(t *testing.T) {
	daemon := &fakeDaemon{fail: map[string]error{"h5": errors.New("daemon refused")}}
	hist := history.New(history.Options{})
	e := startEngine(t, daemon, hist)

	e.Enqueue(Job{Hash: "h5", Name: "T", Group: "g", Condition: ConditionMinRatio, Action: config.SeedingStop})
	waitFor(t, "failure", func() bool {
		for _, e := range hist.ForHash("h5") {
			if e.Actor == "seeding" && e.Result == "failed" {
				return true
			}
		}
		return false
	})
}

func TestMarkerFireOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seeding-state.json")
	m, err := NewMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Fire("h", "g", time.Now()) {
		t.Fatal("first fire must report true")
	}
	if m.Fire("h", "g", time.Now()) {
		t.Fatal("second fire must report false")
	}
	// A different group is an independent pair.
	if !m.Fire("h", "other", time.Now()) {
		t.Fatal("other group must fire independently")
	}
	if m.Len() != 2 {
		t.Fatalf("len = %d", m.Len())
	}

	// Persists across restarts.
	m2, err := NewMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Fire("h", "g", time.Now()) {
		t.Fatal("marker did not persist")
	}
}

func TestMarkerCorruptStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seeding-state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewMarker(path)
	if err == nil {
		t.Fatal("expected an error for a corrupt marker")
	}
	if !m.Fire("h", "g", time.Now()) {
		t.Fatal("corrupt marker should start empty")
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestMarkerUnfireAllowsRetry covers the rollback the poller performs when
// the worker queue rejects a job. The marker is set before the enqueue, so
// without a rollback a dropped job stays recorded as done — on disk, across
// restarts — and its action never runs at all.
func TestMarkerUnfireAllowsRetry(t *testing.T) {
	m, err := NewMarker(filepath.Join(t.TempDir(), "seeding-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !m.Fire("h1", "g", now) {
		t.Fatal("first Fire was refused")
	}
	if m.Fire("h1", "g", now) {
		t.Fatal("second Fire succeeded; exactly-once is broken")
	}
	m.Unfire("h1", "g")
	if !m.Fire("h1", "g", now) {
		t.Fatal("Fire after Unfire was refused: a dropped trigger is lost forever")
	}
}

// TestEnqueueReportsRejection proves the signal the poller's rollback needs:
// a full queue must be reported, not silently swallowed.
func TestEnqueueReportsRejection(t *testing.T) {
	e := New(Options{
		Log:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Daemon: &fakeDaemon{},
	})
	// Never started, so nothing drains the queue: fill it, then overflow.
	accepted := 0
	for i := 0; i < 1000; i++ {
		if e.Enqueue(Job{Hash: "h", Group: "g", Action: config.SeedingStop}) {
			accepted++
			continue
		}
		if accepted == 0 {
			t.Fatal("first Enqueue rejected on an empty queue")
		}
		return // got a rejection, which is what the poller keys off
	}
	t.Fatal("queue never reported a rejection")
}

func TestPreservationGuardBlocksCleanupButNotStops(t *testing.T) {
	d := &fakeDaemon{}
	e := New(Options{Daemon: d, History: history.New(history.Options{}), CleanupGuard: func(string, func() error) error { return errors.New("preservation pin") }})
	for _, action := range []string{config.SeedingErase, config.SeedingEraseWithData, config.SeedingStop, config.SeedingStopAndSetLabel} {
		e.execute(context.Background(), Job{Hash: "pinned", Action: action})
	}
	stops, labels, erased, data := d.counts()
	if stops != 2 || labels != 1 || erased != 0 || data != 0 {
		t.Fatalf("pin changed stop policy or allowed removal: %d %d %d %d", stops, labels, erased, data)
	}
}
