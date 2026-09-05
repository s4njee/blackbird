package history

import (
	"testing"
	"time"
)

func testLog() (*Log, time.Time) {
	now := time.Unix(2000, 0)
	return New(Options{MaxEntriesPerTorrent: 10, Retention: time.Hour, MaxEvents: 100, Now: func() time.Time { return now }}), now
}

func TestGlobalRingMirrorsPerTorrent(t *testing.T) {
	l, _ := testLog()
	l.Add("h1", Entry{Kind: KindAction, Actor: "admin", Action: "start", Result: "ok", Name: "Ubuntu"})
	l.Add("h2", Entry{Kind: KindAdd, Actor: "watch", Action: "add", Result: "ok", Name: "Debian"})

	res := l.Query(Filter{}, 50, 0)
	if len(res.Events) != 2 {
		t.Fatalf("events = %d", len(res.Events))
	}
	// Newest first with stable sequence cursors.
	if res.Events[0].Hash != "h2" || res.Events[1].Hash != "h1" {
		t.Fatalf("order = %+v", res.Events)
	}
	if res.Events[0].Seq <= res.Events[1].Seq {
		t.Fatalf("seqs not increasing: %+v", res.Events)
	}
	if res.HasMore || res.NextBeforeSeq != 0 {
		t.Fatalf("single page should not page: %+v", res)
	}
	// Per-torrent rings still serve the Logger tab, now with names.
	got := l.ForHash("h1")
	if len(got) != 1 || got[0].Name != "Ubuntu" {
		t.Fatalf("per-torrent = %+v", got)
	}
}

func TestGlobalRingBound(t *testing.T) {
	l, now := testLog()
	l.SetBounds(10, time.Hour, 5)
	for i := 0; i < 8; i++ {
		l.Add("h", Entry{Kind: KindAction, At: now.Add(time.Duration(i) * time.Second)})
	}
	res := l.Query(Filter{}, 50, 0)
	if len(res.Events) != 5 {
		t.Fatalf("events = %d, want 5", len(res.Events))
	}
	// Oldest three evicted; newest first.
	if res.Events[0].At.Unix() != 2007 || res.Events[4].At.Unix() != 2003 {
		t.Fatalf("window = %+v", res.Events)
	}
}

func TestGlobalRingAgePrune(t *testing.T) {
	l, now := testLog()
	l.Add("h", Entry{Kind: KindAction, At: now.Add(-2 * time.Hour)})
	l.Add("h", Entry{Kind: KindAction, At: now})
	res := l.Query(Filter{}, 50, 0)
	if len(res.Events) != 1 {
		t.Fatalf("events = %d, want 1 after age prune", len(res.Events))
	}
}

func TestEmptyHashGlobalOnly(t *testing.T) {
	l, _ := testLog()
	l.Add("", Entry{Kind: KindAction, Actor: "scheduler", Action: "schedule_profile", Result: "ok"})
	if n := l.Len(); n != 0 {
		t.Fatalf("empty hash must not create a per-torrent ring, len = %d", n)
	}
	res := l.Query(Filter{}, 50, 0)
	if len(res.Events) != 1 || res.Events[0].Actor != "scheduler" {
		t.Fatalf("global = %+v", res.Events)
	}
}

func TestQueryFilters(t *testing.T) {
	l, _ := testLog()
	l.Add("aaa", Entry{Kind: KindAction, Actor: "admin", Action: "start", Result: "ok", Name: "Ubuntu"})
	l.Add("bbb", Entry{Kind: KindAdd, Actor: "watch", Action: "add", Result: "ok", Name: "Debian"})
	l.Add("aaa", Entry{Kind: KindMessage, Actor: "daemon", Message: "tracker error"})

	if n := len(l.Query(Filter{Kinds: []Kind{KindAdd}}, 50, 0).Events); n != 1 {
		t.Fatalf("kind filter = %d", n)
	}
	if n := len(l.Query(Filter{Kinds: []Kind{KindAction, KindMessage}}, 50, 0).Events); n != 2 {
		t.Fatalf("multi-kind filter = %d", n)
	}
	if n := len(l.Query(Filter{Actor: "ADMIN"}, 50, 0).Events); n != 1 {
		t.Fatalf("actor filter (case-insensitive) = %d", n)
	}
	if n := len(l.Query(Filter{Hash: "bbb"}, 50, 0).Events); n != 1 {
		t.Fatalf("hash filter = %d", n)
	}
	for q, want := range map[string]int{"ubuntu": 1, "AAA": 2, "tracker": 1, "start": 1, "missing": 0} {
		if n := len(l.Query(Filter{Search: q}, 50, 0).Events); n != want {
			t.Errorf("search %q = %d, want %d", q, n, want)
		}
	}
}

func TestQueryPaginationStableUnderAppend(t *testing.T) {
	l, now := testLog()
	for i := 0; i < 5; i++ {
		l.Add("h", Entry{Kind: KindAction, At: now.Add(time.Duration(i) * time.Second)})
	}
	page1 := l.Query(Filter{}, 2, 0)
	if len(page1.Events) != 2 || !page1.HasMore || page1.NextBeforeSeq == 0 {
		t.Fatalf("page1 = %+v", page1)
	}
	// Appends between pages must not shift or duplicate the older page.
	l.Add("h", Entry{Kind: KindAction, At: now.Add(99 * time.Second)})
	page2 := l.Query(Filter{}, 2, page1.NextBeforeSeq)
	if len(page2.Events) != 2 || !page2.HasMore {
		t.Fatalf("page2 = %+v", page2)
	}
	if page2.Events[0].Seq >= page1.Events[1].Seq {
		t.Fatalf("cursor moved: %+v after %+v", page2.Events[0], page1.Events[1])
	}
	page3 := l.Query(Filter{}, 2, page2.NextBeforeSeq)
	if len(page3.Events) != 1 || page3.HasMore || page3.NextBeforeSeq != 0 {
		t.Fatalf("page3 = %+v", page3)
	}
	// Same-nanosecond events page without loss: the cursor is a sequence,
	// not a timestamp.
	l2, now2 := testLog()
	for i := 0; i < 3; i++ {
		l2.Add("h", Entry{Kind: KindAction, At: now2})
	}
	p1 := l2.Query(Filter{}, 2, 0)
	p2 := l2.Query(Filter{}, 2, p1.NextBeforeSeq)
	if len(p1.Events)+len(p2.Events) != 3 || p2.HasMore {
		t.Fatalf("same-At pages = %d + %d, hasMore = %v", len(p1.Events), len(p2.Events), p2.HasMore)
	}
}

func TestQueryLimitClamp(t *testing.T) {
	l, now := testLog()
	for i := 0; i < 5; i++ {
		l.Add("h", Entry{Kind: KindAction, At: now.Add(time.Duration(i) * time.Second)})
	}
	if n := len(l.Query(Filter{}, 0, 0).Events); n != 5 {
		t.Fatalf("default limit = %d", n)
	}
	if n := len(l.Query(Filter{}, 1000, 0).Events); n != 5 {
		t.Fatalf("clamped limit = %d", n)
	}
}

func TestSetBoundsPrunesLive(t *testing.T) {
	l, _ := testLog()
	l.Add("h1", Entry{Kind: KindAction})
	l.Add("h2", Entry{Kind: KindAction})
	l.Add("h3", Entry{Kind: KindAction})
	l.SetBounds(10, time.Hour, 2)
	if n := len(l.Query(Filter{}, 50, 0).Events); n != 2 {
		t.Fatalf("global after SetBounds = %d", n)
	}
	// Non-positive inputs keep the current bounds.
	l.SetBounds(0, 0, 0)
	if n := len(l.Query(Filter{}, 50, 0).Events); n != 2 {
		t.Fatalf("global after empty SetBounds = %d", n)
	}
	l.Add("h4", Entry{Kind: KindAction})
	if n := len(l.Query(Filter{}, 50, 0).Events); n != 2 {
		t.Fatalf("global cap not enforced after add = %d", n)
	}
}
