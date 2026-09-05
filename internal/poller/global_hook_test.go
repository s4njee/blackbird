package poller

import (
	"context"
	"sync"
	"testing"
	"time"

	"blackbird/internal/rtorrent"
)

// TestOnGlobalStatsFiresPerPoll proves the PAR-5.2 traffic hook runs once
// per successful poll with the fresh global counters, outside the poller
// lock, and never on failed polls.
func TestOnGlobalStatsFiresPerPoll(t *testing.T) {
	fc := &fakeClient{
		torrents: []rtorrent.Torrent{mkTorrent("a", 100, 10)},
		global:   rtorrent.GlobalStats{DownRate: 7, SessionDownTotal: 1000, SessionUpTotal: 2000},
	}
	var mu sync.Mutex
	var calls []rtorrent.GlobalStats
	p := New(fc, Options{
		Interval: 5 * time.Millisecond,
		OnGlobalStats: func(g rtorrent.GlobalStats, at time.Time) {
			if at.IsZero() {
				t.Error("hook timestamp is zero")
			}
			mu.Lock()
			calls = append(calls, g)
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) >= 2
	}, "two OnGlobalStats calls")
	mu.Lock()
	defer mu.Unlock()
	for _, g := range calls {
		if g.SessionDownTotal != 1000 || g.SessionUpTotal != 2000 || g.DownRate != 7 {
			t.Fatalf("hook globals = %+v", g)
		}
	}
}

// TestOnGlobalStatsSkippedOnFailure proves a failed poll (list error) does
// not invoke the hook: traffic must only advance on real daemon totals.
func TestOnGlobalStatsSkippedOnFailure(t *testing.T) {
	fc := &fakeClient{listErr: context.DeadlineExceeded}
	var mu sync.Mutex
	fired := false
	p := New(fc, Options{
		Interval: 5 * time.Millisecond,
		OnGlobalStats: func(g rtorrent.GlobalStats, at time.Time) {
			mu.Lock()
			fired = true
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if fired {
		t.Fatal("hook fired on a failed poll")
	}
}
