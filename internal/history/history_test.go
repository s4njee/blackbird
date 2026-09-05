package history

import (
	"testing"
	"time"
)

func TestAddAndRead(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(Options{MaxEntriesPerTorrent: 5, Retention: time.Hour, Now: func() time.Time { return now }})
	l.AddAction("hash1", "admin", "start", "ok", "")
	l.AddAction("hash1", "admin", "pause", "ok", "")
	l.Add("hash1", Entry{Kind: KindMessage, Message: "Tracker error"}) // At defaults to now

	entries := l.ForHash("hash1")
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	// The three kinds are present.
	got := map[Kind]bool{}
	for _, e := range entries {
		got[e.Kind] = true
	}
	if !got[KindMessage] || !got[KindAction] {
		t.Fatalf("kinds = %+v", got)
	}
	if entries[0].Kind != KindMessage {
		t.Fatalf("newest-first ordering broken: %+v", entries[0])
	}
	// Other hashes untouched.
	if got := l.ForHash("other"); len(got) != 0 {
		t.Fatalf("other hash entries = %+v", got)
	}
}

func TestPerTorrentCountBound(t *testing.T) {
	// Use a fixed Now near the sample times so age pruning never kicks in.
	now := time.Unix(1000, 0)
	l := New(Options{MaxEntriesPerTorrent: 3, Retention: time.Hour, Now: func() time.Time { return now }})
	for i := 0; i < 10; i++ {
		l.Add("h", Entry{At: now.Add(time.Duration(i) * time.Second)})
	}
	entries := l.ForHash("h")
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	// Newest three survive (newest first).
	if entries[0].At.Unix() != 1009 {
		t.Fatalf("newest = %d, want 1009", entries[0].At.Unix())
	}
}

func TestAgePrune(t *testing.T) {
	now := time.Unix(5000, 0)
	l := New(Options{MaxEntriesPerTorrent: 100, Retention: 10 * time.Second, Now: func() time.Time { return now }})
	l.Add("h", Entry{At: now.Add(-20 * time.Second)}) // older than retention
	l.Add("h", Entry{At: now.Add(-5 * time.Second)})
	entries := l.ForHash("h")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 after age prune", len(entries))
	}
	if entries[0].At.Unix() != 4995 {
		t.Fatalf("survivor = %d, want 4995", entries[0].At.Unix())
	}
}

func TestPruneTorrentReclaimsInactiveHashes(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(Options{MaxEntriesPerTorrent: 10, Retention: time.Hour, Now: func() time.Time { return now }})
	l.Add("gone", Entry{At: now.Add(-2 * time.Hour)}) // aged out already
	l.Add("stale", Entry{At: now.Add(-10 * time.Second)})
	l.Add("active", Entry{At: now})

	l.PruneTorrent(map[string]bool{"active": true, "stale": true}, now)
	if l.Len() != 2 {
		t.Fatalf("len = %d, want 2 (gone reclaimed)", l.Len())
	}
	// After retention passes, "stale" (no longer active) is reclaimed.
	l.PruneTorrent(map[string]bool{"active": true}, now.Add(2*time.Hour))
	if l.Len() != 1 {
		t.Fatalf("len = %d, want 1", l.Len())
	}
	if got := l.ForHash("stale"); len(got) != 0 {
		t.Fatalf("stale not reclaimed: %+v", got)
	}
}

func TestMaxTorrentsCap(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(Options{MaxEntriesPerTorrent: 10, Retention: time.Hour, MaxTorrents: 3, Now: func() time.Time { return now }})
	for _, h := range []string{"a", "b", "c", "d", "e"} {
		l.Add(h, Entry{At: now})
	}
	l.PruneTorrent(map[string]bool{}, now)
	if l.Len() > 3 {
		t.Fatalf("len = %d, want <= 3", l.Len())
	}
}
