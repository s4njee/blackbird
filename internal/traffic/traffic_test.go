package traffic

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testTracker(t *testing.T, retention int) (*Tracker, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traffic.jsonl")
	tr, err := New(Options{Path: path, RetentionDays: retention})
	if err != nil {
		t.Fatal(err)
	}
	return tr, path
}

func TestFeedAccumulatesDeltas(t *testing.T) {
	tr, _ := testTracker(t, 90)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	tr.Feed(base, 1000, 2000) // baseline: no delta
	tr.Feed(base.Add(time.Minute), 3000, 2500)
	tr.Feed(base.Add(2*time.Minute), 3500, 4000)

	days := tr.Days(base, base)
	if len(days) != 1 || days[0].Down != 2500 || days[0].Up != 2000 {
		t.Fatalf("days = %+v", days)
	}
	hours := tr.Hours(base)
	if len(hours) != 24 {
		t.Fatalf("hours = %d", len(hours))
	}
	if hours[10].Down != 2500 || hours[10].Up != 2000 {
		t.Fatalf("hour 10 = %+v", hours[10])
	}
}

func TestCounterResetCountsCurrent(t *testing.T) {
	tr, _ := testTracker(t, 90)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	tr.Feed(base, 5000, 6000)
	// Daemon restart zeroes the counters: the step back counts current.
	tr.Feed(base.Add(time.Minute), 700, 800)
	days := tr.Days(base, base)
	if days[0].Down != 700 || days[0].Up != 800 {
		t.Fatalf("days = %+v", days)
	}
}

func TestMidnightCrossoverAttributesToSampleDay(t *testing.T) {
	tr, _ := testTracker(t, 90)
	before := time.Date(2026, 9, 2, 23, 59, 30, 0, time.UTC)
	after := time.Date(2026, 9, 3, 0, 0, 30, 0, time.UTC)
	tr.Feed(before, 1000, 1000)
	tr.Feed(after, 1600, 1200)
	days := tr.Days(before, after)
	if len(days) != 2 {
		t.Fatalf("days = %+v", days)
	}
	if days[0].Down != 0 || days[1].Down != 600 || days[1].Up != 200 {
		t.Fatalf("days = %+v", days)
	}
	hours := tr.Hours(after)
	if hours[0].Down != 600 {
		t.Fatalf("midnight hour = %+v", hours[0])
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	tr, path := testTracker(t, 90)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	tr.Feed(base, 0, 0)
	tr.Feed(base.Add(time.Minute), 5000, 1000)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	// A second flush with no new data appends nothing.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatal("empty flush appended lines")
	}

	loaded, err := New(Options{Path: path, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	days := loaded.Days(base, base)
	if len(days) != 1 || days[0].Down != 5000 || days[0].Up != 1000 {
		t.Fatalf("reloaded = %+v", days)
	}
	// Loaded sums are fully flushed: the next flush appends only new deltas.
	loaded.Feed(base.Add(2*time.Minute), 6000, 1500)
	if err := loaded.Flush(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(Options{Path: path, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	days = reloaded.Days(base, base)
	if days[0].Down != 6000 || days[0].Up != 1500 {
		t.Fatalf("double-counted after reload: %+v", days)
	}
}

func TestRestartBridgesShutdownGap(t *testing.T) {
	tr, path := testTracker(t, 90)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	tr.Feed(base, 1000, 1000)
	tr.Feed(base.Add(time.Minute), 1500, 1200)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}

	// Restart with the daemon 800/700 bytes further along: the gap counts.
	restarted, err := New(Options{Path: path, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	restarted.Feed(base.Add(10*time.Minute), 2300, 1900)
	days := restarted.Days(base, base)
	if days[0].Down != 1300 || days[0].Up != 900 {
		t.Fatalf("gap not bridged: %+v", days)
	}

	// Daemon restart during downtime (totals below persisted prev) counts
	// current, never negative. (The first restart above never flushed, so
	// the file still holds 500/200.)
	restarted2, err := New(Options{Path: path, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	restarted2.Feed(base.Add(20*time.Minute), 100, 100)
	days = restarted2.Days(base, base)
	if days[0].Down != 600 || days[0].Up != 300 {
		t.Fatalf("reset mishandled: %+v", days)
	}
}

func TestCorruptLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traffic.jsonl")
	content := "{\"day\":\"2026-09-02\",\"down\":100,\"up\":50}\nnot json\n{\"broken\":\n{\"hour\":\"2026-09-02T10\",\"down\":30,\"up\":10}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := New(Options{Path: path, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	days := tr.Days(base, base)
	if days[0].Down != 100 || days[0].Up != 50 {
		t.Fatalf("days = %+v", days)
	}
	if tr.Hours(base)[10].Down != 30 {
		t.Fatalf("hours = %+v", tr.Hours(base)[10])
	}
}

func TestRetentionPrunes(t *testing.T) {
	tr, path := testTracker(t, 7)
	old := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	tr.Feed(old, 0, 0)
	tr.Feed(old.Add(time.Minute), 100, 100)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	tr.Feed(now, 200, 200)
	tr.Feed(now.Add(time.Minute), 300, 300)
	days := tr.Days(old, now.AddDate(0, 0, 1))
	for _, d := range days {
		if d.Day < "2026-09-03" && (d.Down != 0 || d.Up != 0) {
			t.Fatalf("expired bucket survived: %+v", d)
		}
	}
	// Pruning rewrote the file: the old lines are gone on reload.
	loaded, err := New(Options{Path: path, RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	days = loaded.Days(old, now.AddDate(0, 0, 1))
	for _, d := range days {
		if d.Day < "2026-09-03" && (d.Down != 0 || d.Up != 0) {
			t.Fatalf("expired bucket persisted: %+v", d)
		}
	}
}

func TestDisabledPersistence(t *testing.T) {
	tr, path := testTracker(t, 0)
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	tr.Feed(base, 0, 0)
	tr.Feed(base.Add(time.Minute), 100, 100)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled tracker wrote a file")
	}
	// Live counting still works.
	if days := tr.Days(base, base); days[0].Down != 100 {
		t.Fatalf("days = %+v", days)
	}
}

func TestDaysZeroFillsRange(t *testing.T) {
	tr, _ := testTracker(t, 90)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	days := tr.Days(from, to)
	if len(days) != 3 || days[0].Day != "2026-09-01" || days[2].Day != "2026-09-03" {
		t.Fatalf("days = %+v", days)
	}
}
