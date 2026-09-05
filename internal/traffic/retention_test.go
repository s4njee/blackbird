package traffic

import (
	"testing"
	"time"
)

// TestSetRetentionDaysPrunesLive proves Settings saves and SIGHUP reloads
// take effect without a restart: narrowing the window drops old buckets and
// rewrites the file, and the new window reports from the accessor.
func TestSetRetentionDaysPrunesLive(t *testing.T) {
	tr, path := testTracker(t, 90)
	old := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	tr.Feed(old, 0, 0)
	tr.Feed(old.Add(time.Minute), 100, 100)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	tr.Feed(now, 200, 200)
	tr.Feed(now.Add(time.Minute), 300, 300)

	tr.SetRetentionDays(7)
	if got := tr.RetentionDays(); got != 7 {
		t.Fatalf("retention = %d", got)
	}
	for _, d := range tr.Days(old, now) {
		if d.Day < "2026-09-03" && (d.Down != 0 || d.Up != 0) {
			t.Fatalf("expired bucket survived: %+v", d)
		}
	}
	// The prune rewrote the file: a reload agrees.
	loaded, err := New(Options{Path: path, RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range loaded.Days(old, now) {
		if d.Day < "2026-09-03" && (d.Down != 0 || d.Up != 0) {
			t.Fatalf("expired bucket persisted: %+v", d)
		}
	}

	// Negative input clamps to disabled (memory-only), never panics.
	tr.SetRetentionDays(-5)
	if got := tr.RetentionDays(); got != 0 {
		t.Fatalf("retention = %d", got)
	}
}
