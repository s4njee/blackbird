package automation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
)

var trueVal = true

func mkTorrent(hash, name, label string) rtorrent.Torrent {
	return rtorrent.Torrent{Hash: hash, Name: name, Label: label, SizeBytes: 1000, Complete: true, TrackerHost: "tracker.example.org"}
}

func TestMatchConditions(t *testing.T) {
	torrent := mkTorrent("h1", "Ubuntu 24.04 ISO", "linux")

	cases := []struct {
		what string
		rule config.CompletionRule
		want bool
	}{
		{"empty rule matches everything", config.CompletionRule{}, true},
		{"label exact match", config.CompletionRule{Label: "linux"}, true},
		{"label is case-insensitive", config.CompletionRule{Label: "LINUX"}, true},
		{"label mismatch", config.CompletionRule{Label: "movies"}, false},
		{"tracker substring", config.CompletionRule{Tracker: "tracker.example"}, true},
		{"tracker case-insensitive", config.CompletionRule{Tracker: "TRACKER.EXAMPLE.ORG"}, true},
		{"tracker mismatch", config.CompletionRule{Tracker: "other.example"}, false},
		{"name regex match", config.CompletionRule{NameRegex: `Ubuntu \d\d`}, true},
		{"name regex mismatch", config.CompletionRule{NameRegex: `^Debian`}, false},
		{"min size passes", config.CompletionRule{MinSize: 999}, true},
		{"min size fails", config.CompletionRule{MinSize: 1001}, false},
		{"max size passes", config.CompletionRule{MaxSize: 1000}, true},
		{"max size fails", config.CompletionRule{MaxSize: 999}, false},
		{"private true matches private", config.CompletionRule{Private: &trueVal}, false},
		{"conditions combine with AND", config.CompletionRule{Label: "linux", Tracker: "tracker.example"}, true},
		{"one failing condition blocks", config.CompletionRule{Label: "linux", Tracker: "other.example"}, false},
	}
	for _, tc := range cases {
		got, err := Match(tc.rule, torrent)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.what, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.what, got, tc.want)
		}
	}
	if _, err := Match(config.CompletionRule{NameRegex: "("}, torrent); err == nil {
		t.Error("invalid regex should return an error")
	}
}

func TestMatchFirstHonorsOrder(t *testing.T) {
	rules := []config.CompletionRule{
		{Name: "second-label", Label: "linux"},
		{Name: "catch-all"},
	}
	torrent := mkTorrent("h1", "anything", "linux")
	idx, rule, ok := MatchFirst(rules, torrent)
	if !ok || idx != 0 || rule.Name != "second-label" {
		t.Fatalf("got idx=%d rule=%q ok=%v, want first rule", idx, rule.Name, ok)
	}

	// An invalid rule in the list is skipped, not fatal.
	broken := []config.CompletionRule{{Name: "broken", NameRegex: "("}, {Name: "fallback"}}
	_, rule, ok = MatchFirst(broken, torrent)
	if !ok || rule.Name != "fallback" {
		t.Fatalf("invalid rule not skipped: ok=%v rule=%q", ok, rule.Name)
	}
}

// fakeDaemon records SetLabel/AddTracker calls.
type fakeDaemon struct {
	mu     sync.Mutex
	labels map[string]string
	tracks []string
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

func (f *fakeDaemon) AddTracker(_ context.Context, hash, url string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tracks = append(f.tracks, hash+"|"+url)
	return nil
}

type fakeMover struct {
	mu    sync.Mutex
	moves []string
	err   map[string]error // hash → forced error
}

func (f *fakeMover) MoveForAutomation(_ context.Context, hash, destination string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = map[string]error{}
	}
	if err := f.err[hash]; err != nil {
		return err
	}
	f.moves = append(f.moves, hash+"|"+destination)
	return nil
}

func (f *fakeMover) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.moves...)
}

type recorder struct {
	mu      sync.Mutex
	notices []Notice
}

func (r *recorder) onNotice(n Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, n)
}

func (r *recorder) all() []Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notice(nil), r.notices...)
}

func newTestEngine(t *testing.T, rules func() []config.CompletionRule, daemon Daemon, mover Mover) (*Engine, *recorder) {
	t.Helper()
	return newTestEngineWithHistory(t, rules, daemon, mover, history.New(history.Options{}))
}

func newTestHistory(t *testing.T) *history.Log {
	t.Helper()
	return history.New(history.Options{})
}

func newTestEngineWithHistory(t *testing.T, rules func() []config.CompletionRule, daemon Daemon, mover Mover, hist *history.Log) (*Engine, *recorder) {
	t.Helper()
	marker, err := NewMarker(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := New(Options{
		Log:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Daemon:  daemon,
		History: hist,
		Marker:  marker,
		Rules:   rules,
	})
	if mover != nil {
		e.SetMover(mover)
	}
	rec := &recorder{}
	e.Subscribe(rec.onNotice)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.Run(ctx)
	return e, rec
}

func waitForNotice(t *testing.T, rec *recorder, kind string) Notice {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range rec.all() {
			if n.Kind == kind {
				return n
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s notice arrived; got %+v", kind, rec.all())
	return Notice{}
}

func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEngineRunsFirstMatchingRuleOnce(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	rules := func() []config.CompletionRule {
		return []config.CompletionRule{
			{Name: "linux", Label: "linux", SetLabel: "done"},
			{Name: "catch-all", SetLabel: "other", MoveTo: "/data/done"},
		}
	}
	e, rec := newTestEngine(t, rules, daemon, mover)

	e.Enqueue(mkTorrent("h1", "Ubuntu ISO", "linux"))
	notice := waitForNotice(t, rec, "completed")
	if notice.Rule != "linux" {
		t.Fatalf("rule = %q, want first match", notice.Rule)
	}
	if daemon.labels["h1"] != "done" {
		t.Fatalf("label = %q", daemon.labels["h1"])
	}
	if len(mover.calls()) != 0 {
		t.Fatalf("second rule should not have run: %v", mover.calls())
	}

	// Same hash again: suppressed by the marker.
	e.Enqueue(mkTorrent("h1", "Ubuntu ISO", "linux"))
	time.Sleep(100 * time.Millisecond)
	if len(daemon.labels) != 1 {
		t.Fatalf("rule ran %d times, want 1", len(daemon.labels))
	}
}

func TestEngineMovesAndAddsTracker(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	rules := func() []config.CompletionRule {
		return []config.CompletionRule{{Name: "move-em", MoveTo: "/data/movies", AddTracker: "udp://tracker.example:1337/announce"}}
	}
	e, rec := newTestEngine(t, rules, daemon, mover)

	torrent := mkTorrent("h2", "Big Movie", "movies")
	torrent.IsPrivate = false
	e.Enqueue(torrent)
	waitForNotice(t, rec, "completed")
	waitForCond(t, "move", func() bool { return len(mover.calls()) == 1 })
	if got := mover.calls()[0]; got != "h2|/data/movies" {
		t.Fatalf("move = %q", got)
	}
	if len(daemon.tracks) != 1 || daemon.tracks[0] != "h2|udp://tracker.example:1337/announce" {
		t.Fatalf("tracker adds = %v", daemon.tracks)
	}
}

func TestEngineSkipsTrackerAddForPrivateTorrents(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	rules := func() []config.CompletionRule {
		return []config.CompletionRule{{Name: "private", AddTracker: "udp://x/announce", SetLabel: "priv"}}
	}
	e, rec := newTestEngine(t, rules, daemon, mover)

	torrent := mkTorrent("h3", "Private Torrent", "")
	torrent.IsPrivate = true
	e.Enqueue(torrent)
	waitForNotice(t, rec, "completed")
	if len(daemon.tracks) != 0 {
		t.Fatalf("tracker added to private torrent: %v", daemon.tracks)
	}
	if daemon.labels["h3"] != "priv" {
		t.Fatal("set_label skipped")
	}
}

func TestEngineFailuresToastAndLog(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{err: map[string]error{"h4": errors.New("destination outside download roots")}}
	rules := func() []config.CompletionRule {
		return []config.CompletionRule{{Name: "mover", MoveTo: "/data/elsewhere"}}
	}
	e, rec := newTestEngine(t, rules, daemon, mover)

	e.Enqueue(mkTorrent("h4", "Broken Move", ""))
	notice := waitForNotice(t, rec, "failed")
	if notice.Rule != "mover" || notice.Hash != "h4" {
		t.Fatalf("notice = %+v", notice)
	}
	if notice.Message == "" {
		t.Fatal("failure notice missing message")
	}
}

func TestEngineNoRulesDoesNotMark(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	rules := func() []config.CompletionRule { return nil }
	e, _ := newTestEngine(t, rules, daemon, mover)

	e.Enqueue(mkTorrent("h5", "Anything", ""))
	time.Sleep(100 * time.Millisecond)
	if len(daemon.labels) != 0 {
		t.Fatal("action ran without rules")
	}
	// With no rules configured, nothing is marked, so a rule added later
	// still applies to torrents completed before it existed.
}

// TestEngineUnpackHandoff asserts the PAR-3.4 handoff: the completed hash
// reaches the unpack service after rule actions, even when no on-complete
// rule matches, and exactly-once marking covers unpack-only configs.
func TestEngineUnpackHandoff(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	var mu sync.Mutex
	var unpacked []string
	rules := func() []config.CompletionRule {
		return []config.CompletionRule{{Name: "label-tv", Label: "tv", SetLabel: "done"}}
	}
	e, _ := newTestEngine(t, rules, daemon, mover)
	e.SetUnpack(func(hash string) {
		mu.Lock()
		unpacked = append(unpacked, hash)
		mu.Unlock()
	}, func() bool { return true })

	e.Enqueue(mkTorrent("h7", "Show", "movies"))
	waitForCond(t, "unpack handoff", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(unpacked) == 1 && unpacked[0] == "h7"
	})
	// No on-complete rule matched, so no label was set — but the hash was
	// marked, so a repeat completion does not hand off again.
	e.Enqueue(mkTorrent("h7", "Show", "movies"))
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(unpacked) != 1 {
		t.Fatalf("unpack ran %d times, want 1", len(unpacked))
	}
}

// TestEngineUnpackOnlyConfigStillMarks asserts an unpack-only setup (no
// on_complete rules) still marks the hash exactly once.
func TestEngineUnpackOnlyConfigStillMarks(t *testing.T) {
	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	var mu sync.Mutex
	count := 0
	e, _ := newTestEngine(t, func() []config.CompletionRule { return nil }, daemon, mover)
	e.SetUnpack(func(hash string) {
		mu.Lock()
		count++
		mu.Unlock()
	}, func() bool { return true })

	e.Enqueue(mkTorrent("h8", "Show", ""))
	waitForCond(t, "unpack handoff", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return count == 1
	})
	e.Enqueue(mkTorrent("h8", "Show", ""))
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Fatalf("unpack ran %d times, want 1", count)
	}
}

func TestEngineWebhookDeliversPayload(t *testing.T) {
	var mu sync.Mutex
	var bodies []webhookPayload
	var codes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		bodies = append(bodies, p)
		if len(codes) == 0 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		codes = append(codes, 0)
		mu.Unlock()
	}))
	defer srv.Close()

	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	rules := func() []config.CompletionRule {
		return []config.CompletionRule{{Name: "hook", Webhook: srv.URL}}
	}
	e, rec := newTestEngine(t, rules, daemon, mover)

	e.Enqueue(mkTorrent("h6", "Webhook Torrent", "linux"))
	waitForNotice(t, rec, "completed")
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || bodies[0].Rule != "hook" || bodies[0].Hash != "h6" || bodies[0].Name != "Webhook Torrent" {
		t.Fatalf("payloads = %+v", bodies)
	}
}

func TestMarkerPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	m1, err := NewMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if m1.Seen("abc") {
		t.Fatal("fresh marker reports seen")
	}
	if err := m1.Mark("abc", time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := m1.Mark("def", time.Now()); err != nil {
		t.Fatal(err)
	}

	m2, err := NewMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m2.Seen("abc") || !m2.Seen("def") {
		t.Fatal("marker did not persist")
	}
	if m2.Len() != 2 {
		t.Fatalf("len = %d", m2.Len())
	}
}

func TestMarkerPrunesOldest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	m, err := NewMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cap = 5
	m.mu.Unlock()

	// Six entries at increasing timestamps; the first must be pruned.
	hashes := []string{"h0", "h1", "h2", "h3", "h4", "h5"}
	base := time.Unix(1700000000, 0)
	for i, hash := range hashes {
		if err := m.Mark(hash, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if m.Len() != 5 {
		t.Fatalf("len = %d, want 5", m.Len())
	}
	if m.Seen("h0") {
		t.Fatal("oldest entry survived pruning")
	}
	for _, hash := range hashes[1:] {
		if !m.Seen(hash) {
			t.Fatalf("%s was pruned unexpectedly", hash)
		}
	}
	// The pruned set persisted to disk too.
	m2, err := NewMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Len() != 5 || m2.Seen("h0") {
		t.Fatalf("reloaded marker = %d entries, h0 seen = %v", m2.Len(), m2.Seen("h0"))
	}
}

func TestMarkerCorruptFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewMarker(path)
	if err == nil {
		t.Fatal("expected an error for a corrupt marker")
	}
	if m.Seen("abc") {
		t.Fatal("corrupt marker should start empty")
	}
	// The engine must still be able to mark and persist over it.
	if err := m.Mark("abc", time.Now()); err != nil {
		t.Fatal(err)
	}
}
