package preservation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

const testHash = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func fixture(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "preservation.json"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err = s.Change(Change{Hash: testHash, Action: "watch"}, "test"); err != nil {
		t.Fatal(err)
	}
	return s, &now
}
func sample(s *Store, at time.Time, seeds int, state rtorrent.State) {
	s.Sample(Input{Snapshot: &poller.Snapshot{GeneratedAt: at, Status: poller.StatusConnected, Torrents: []rtorrent.Torrent{{Hash: testHash, Name: "archive", IsOpen: true, State: state, Complete: true, Seeds: seeds, UpRate: 1}}}})
}
func TestSustainedLowOutranksTransientAndUnknown(t *testing.T) {
	s, now := fixture(t)
	for i := 0; i < 12; i++ {
		sample(s, *now, 0, rtorrent.StateSeeding)
		if i < 11 {
			*now = now.Add(Interval)
		}
	}
	v, _ := s.Snapshot("")
	if v[0].Band != "few_seeds" || v[0].Known != 12 || v[0].Latest.Complete == nil || !*v[0].Latest.Complete || v[0].LastActivity == nil {
		t.Fatalf("sustained evidence: %+v", v[0])
	}
	*now = now.Add(Interval)
	sample(s, *now, 4, rtorrent.StateSeeding)
	v, _ = s.Snapshot("")
	if v[0].Band == "few_seeds" {
		t.Fatal("latest higher count should change band")
	}
	*now = now.Add(Interval)
	sample(s, *now, 0, rtorrent.StateStopped)
	v, _ = s.Snapshot("")
	if v[0].Band != "unknown" || v[0].Latest.Seeds != nil {
		t.Fatal("stopped treated as low")
	}
	*now = now.Add(Interval)
	s.Sample(Input{Snapshot: &poller.Snapshot{Status: poller.StatusConnected, GeneratedAt: now.Add(-time.Hour)}})
	v, _ = s.Snapshot("")
	if v[0].Latest.Seeds != nil || v[0].Latest.Complete != nil {
		t.Fatal("stale became zero/false")
	}
	*now = now.Add(Window)
	sample(s, *now, 0, rtorrent.StateSeeding)
	v, _ = s.Snapshot("")
	if v[0].Band != "recent_low" || v[0].Coverage >= .75 {
		t.Fatal("process gap counted as covered")
	}
}
func TestBoundedCacheSamplingAndTrackerProvenance(t *testing.T) {
	s, now := fixture(t)
	private := rtorrent.Torrent{Hash: testHash, IsPrivate: true, IsOpen: true, State: rtorrent.StateSeeding, Seeds: 1}
	sourceCalls := 0
	source := func(string) ([]rtorrent.Tracker, time.Time) {
		sourceCalls++
		return []rtorrent.Tracker{{URL: "https://user:secret@tracker.example/announce?passkey=SECRET", Seeds: 0, SuccessCount: 0}, {URL: "https://tracker.example/other", Seeds: 2, SuccessCount: 1, IsEnabled: true}}, *now
	}
	input := Input{Snapshot: &poller.Snapshot{Status: poller.StatusConnected, GeneratedAt: *now, Torrents: []rtorrent.Torrent{private}}, Trackers: source}
	s.Sample(input)
	s.Sample(input)
	v, _ := s.Snapshot(testHash)
	if sourceCalls != 1 || len(v[0].Samples) != 1 {
		t.Fatal("repeated cache count resampled")
	}
	if v[0].Trackers[0].Seeds != nil || v[0].Trackers[1].Seeds == nil || v[0].Trackers[1].ReportedAt != nil {
		t.Fatal("invented tracker availability/time")
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "user:") {
		t.Fatal("tracker credentials exposed")
	}
	for i := 0; i < MaxSamples+5; i++ {
		*now = now.Add(Interval)
		sample(s, *now, 1, rtorrent.StateSeeding)
	}
	v, _ = s.Snapshot(testHash)
	if len(v[0].Samples) != MaxSamples {
		t.Fatalf("retention: %d", len(v[0].Samples))
	}
	*now = now.Add(2*Interval + time.Second)
	v, _ = s.Snapshot("")
	if v[0].Band != "unknown" {
		t.Fatal("stale ranking")
	}
}
func TestPinsSurviveRestartAndRejectConflictAndUnwatch(t *testing.T) {
	s, now := fixture(t)
	if err := s.Change(Change{Hash: testHash, Action: "update", Revision: 1, Pinned: true, Reason: "rare source", ReviewDate: "2026-09-02"}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Change(Change{Hash: testHash, Action: "update", Revision: 1}, "test"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.Change(Change{Hash: testHash, Action: "unwatch", Revision: 2}, "test"); !errors.Is(err, ErrPinned) {
		t.Fatal(err)
	}
	sample(s, *now, 1, rtorrent.StateSeeding)
	s.Close()
	s.lock = nil
	orphan := filepath.Join(filepath.Dir(s.opts.Path), "."+filepath.Base(s.opts.Path)+".tmp-crash")
	if err := os.WriteFile(orphan, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("crashed write not cleaned up")
	}
	invoked := false
	if err = restored.Guard(strings.ToLower(testHash), func() error { invoked = true; return nil }); !errors.Is(err, ErrPinned) || invoked {
		t.Fatal("pin did not guard removal")
	}
	v, _ := restored.Snapshot(testHash)
	if !v[0].ReviewDue || v[0].Reason != "rare source" || len(v[0].Samples) != 1 {
		t.Fatal("state not restored")
	}
	if err = restored.Change(Change{Hash: testHash, Action: "update", Revision: 2, Pinned: false}, "test"); err != nil {
		t.Fatal(err)
	}
	if err = restored.Guard(testHash, func() error { invoked = true; return nil }); err != nil || !invoked {
		t.Fatal("unpin did not release cleanup")
	}
}
func TestFailedWritesAndInvalidStateFailClosed(t *testing.T) {
	s, _ := fixture(t)
	if err := s.Change(Change{Hash: testHash, Action: "update", Revision: 1, Pinned: true}, "test"); err != nil {
		t.Fatal(err)
	}
	s.opts.Write = func(string, []byte) error { return errors.New("disk full") }
	if err := s.Change(Change{Hash: testHash, Action: "update", Revision: 2, Pinned: false}, "test"); err == nil {
		t.Fatal("failed write acknowledged")
	}
	if err := s.Guard(testHash, func() error { t.Fatal("pin lost"); return nil }); !errors.Is(err, ErrPinned) {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "broken.json")
	os.WriteFile(path, []byte("broken"), 0600)
	broken, err := Open(Options{Path: path})
	if err == nil {
		t.Fatal("bad state accepted")
	}
	defer broken.Close()
	if err = broken.Guard("anything", func() error { t.Fatal("cleanup on unreadable state"); return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	for _, c := range []Change{{Hash: "bad", Action: "watch"}, {Hash: testHash, Action: "update", ReviewDate: "tomorrow"}, {Hash: testHash, Reason: strings.Repeat("x", 501)}} {
		if err = s.Change(c, "test"); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}
func TestPinWaitsForInflightCleanup(t *testing.T) {
	s, _ := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.Guard(testHash, func() error { close(started); <-release; return nil }) }()
	<-started
	pinned := make(chan error, 1)
	go func() {
		pinned <- s.Change(Change{Hash: testHash, Action: "update", Revision: 1, Pinned: true}, "test")
	}()
	select {
	case <-pinned:
		t.Fatal("pin acknowledged during cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-pinned; err != nil {
		t.Fatal(err)
	}
	if err := s.Guard(testHash, func() error { t.Fatal("later cleanup bypassed pin"); return nil }); !errors.Is(err, ErrPinned) {
		t.Fatal(err)
	}
}
func BenchmarkSample5000(b *testing.B) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s, err := Open(Options{Path: filepath.Join(b.TempDir(), "watch.json"), Now: func() time.Time { return now }, Write: func(string, []byte) error { return nil }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	rows := make([]rtorrent.Torrent, 5000)
	for i := range rows {
		rows[i] = rtorrent.Torrent{Hash: fmt.Sprintf("%040X", i), IsOpen: true, State: rtorrent.StateSeeding, Seeds: 1}
	}
	for i := 0; i < MaxWatches; i++ {
		s.state.Watches = append(s.state.Watches, Watch{Hash: rows[i].Hash, Since: now, Revision: 1, Name: strings.Repeat("n", 512)})
	}
	for i := 0; i < MaxSamples; i++ {
		s.Sample(Input{Snapshot: &poller.Snapshot{Status: poller.StatusConnected, GeneratedAt: now, Torrents: rows}})
		now = now.Add(Interval)
	}
	raw, _ := json.Marshal(s.state)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Sample(Input{Snapshot: &poller.Snapshot{Status: poller.StatusConnected, GeneratedAt: now, Torrents: rows}})
		now = now.Add(Interval)
	}

	b.ReportMetric(float64(len(raw)), "retained-bytes")
}

func TestTrackerHistoryIsBoundedAndNeverDrivesConnectedSeedBand(t *testing.T) {
	s, now := fixture(t)
	for i := 0; i < 40; i++ {
		s.Sample(Input{Snapshot: &poller.Snapshot{Status: poller.StatusConnected, GeneratedAt: *now, Torrents: []rtorrent.Torrent{{Hash: testHash, IsOpen: true, State: rtorrent.StateSeeding, Seeds: 8}}}, Trackers: func(string) ([]rtorrent.Tracker, time.Time) {
			return []rtorrent.Tracker{{URL: "https://tracker.example/secret", Seeds: 0, SuccessCount: 1}}, *now
		}})
		if i < 39 {
			*now = now.Add(Interval)
		}
	}
	v, _ := s.Snapshot(testHash)
	if len(v[0].TrackerHistory) != 32 || v[0].Band != "more_seeds" || *v[0].TrackerHistory[0].Seeds != 0 {
		t.Fatal("tracker history or ranking incorrect")
	}
	v, _ = s.Snapshot("")
	if v[0].TrackerHistory != nil || v[0].Samples != nil {
		t.Fatal("summary returned histories")
	}
}
func TestSixHourCoverageDoesNotOvercountBoundary(t *testing.T) {
	s, now := fixture(t)
	s.opts.Write = func(string, []byte) error { return nil }
	for i := 0; i <= 72; i++ {
		sample(s, *now, 0, rtorrent.StateSeeding)
		if i < 72 {
			*now = now.Add(Interval)
		}
	}
	v, _ := s.Snapshot("")
	if v[0].Known != 72 || v[0].Expected != 72 {
		t.Fatalf("slot boundary: %+v", v[0])
	}
}
