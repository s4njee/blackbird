package poller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"blackbird/internal/fakertorrent"
	"blackbird/internal/rtorrent"
)

// rows5k builds a deterministic 5,000-torrent session (the Epic 6 target
// shape) with stable timestamps so allocation measurements are repeatable.
// It stands in for the PERF-6.6 fixture until that harness exists.
func rows5k() []rtorrent.Torrent {
	base := time.Unix(1756700000, 0)
	rows := make([]rtorrent.Torrent, 5000)
	for i := range rows {
		rows[i] = rtorrent.Torrent{
			Hash:            fmt.Sprintf("hash-%06d", i),
			Name:            fmt.Sprintf("torrent-number-%06d-with-a-plausibly-long-name", i),
			SizeBytes:       6474842112,
			CompletedBytes:  3974842112,
			State:           rtorrent.StateDownloading,
			DownRate:        412000,
			UpRate:          128000,
			Label:           "iso",
			TrackerHost:     "torrent.ubuntu.com",
			BasePath:        "/mnt/data/iso",
			Directory:       "/mnt/data/iso",
			AddedAt:         base,
			Priority:        2,
			Seeds:           38,
			Peers:           112,
			IsOpen:          true,
			Ratio:           2.41,
			Percent:         61.4,
			LeftBytes:       2500000000,
			DownloadedBytes: 3974842112,
			UploadedBytes:   9582132740,
		}
	}
	return rows
}

// tickSessions prebuilds two 5,000-row sessions the benchmark alternates
// between: the odd session bumps ~200 rows' live counters the way a busy 2s
// poll observes. Alternating whole slices mirrors a real client (fresh rows
// per call, never mutated after return) with zero fake-side allocations, so
// the measurement isolates poller overhead.
func tickSessions() (even, odd []rtorrent.Torrent) {
	even = rows5k()
	odd = rows5k()
	for k := 0; k < 200; k++ {
		odd[k].DownRate += int64(k)
		odd[k].UpRate += int64(k)
		odd[k].CompletedBytes += 412000
		odd[k].DownloadedBytes += 412000
		odd[k].UploadedBytes += 128000
		odd[k].LeftBytes -= 412000
		odd[k].Percent += 0.01
		odd[k].Ratio += 0.001
	}
	return even, odd
}

// BenchmarkPollCycle measures a full poll cycle per fixture size through
// the real stack (fakertorrent → SCGI → typed client → poller), so transport
// and decode costs are included. Run with -benchmem; the numbers feed the
// PERF-6.6 report via `make bench`.
func BenchmarkPollCycle(b *testing.B) {
	for _, n := range []int{500, 5000, 20000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			dir, err := os.MkdirTemp("", "bench-*")
			if err != nil {
				b.Fatal(err)
			}
			defer os.RemoveAll(dir)
			sock := filepath.Join(dir, "rt.sock")
			daemon, err := fakertorrent.StartOpts(sock, fakertorrent.Options{
				SessionSize: n, ActiveFraction: 0.04, Seed: 1,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer daemon.Stop()
			c, err := rtorrent.New("unix://"+sock, 30*time.Second)
			if err != nil {
				b.Fatal(err)
			}
			p := New(c, Options{Interval: time.Second, DetailInterval: time.Second})
			ctx := context.Background()
			if err := p.pollOnce(ctx); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := p.pollOnce(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkComputeDelta measures the pure 5,000-row diff with 200 changed
// rows, isolating diff cost from transport.
func BenchmarkComputeDelta(b *testing.B) {
	rows := rows5k()
	prev := map[string]*rtorrent.Torrent{}
	fillIndex(prev, rows)
	modified := rows5k()
	for k := 0; k < 200; k++ {
		modified[k].DownRate++
	}
	next := map[string]*rtorrent.Torrent{}
	fillIndex(next, modified)
	var added, changed []rtorrent.Torrent
	var removed []string
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := computeDelta(prev, next, time.Now(), added, changed, removed)
		added, changed, removed = d.Added, d.Changed, d.Removed
	}
}

// Poll-cycle allocation budgets (PERF-6.4 regression guard): warm up the
// reuse buffers, then count. Calibrated 2026-09 on darwin/arm64; both
// ceilings carry ~2x headroom so Go-version wobble does not flake them,
// while reintroduced per-cycle maps still trip them by an order of
// magnitude.
//
// Two states are guarded: idle (nothing changes — the always-on background
// cost behind GC jitter) and busy (200 live rows per tick, where per-row
// patch maps and value boxes scale with genuine new data).
const (
	pollCycleAllocBudgetIdle = 100
	pollCycleAllocBudgetBusy = 4000
)

func TestPollCycleAllocBudget(t *testing.T) {
	even, odd := tickSessions()
	fc := &fakeClient{torrents: even, global: rtorrent.GlobalStats{DownRate: 41200000}}
	p := New(fc, Options{Interval: time.Second, DetailInterval: time.Second})
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		fc.torrents = odd
		if err := p.pollOnce(ctx); err != nil {
			t.Fatal(err)
		}
		fc.torrents = even
		if err := p.pollOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	idle := testing.AllocsPerRun(50, func() {
		fc.torrents = even
		_ = p.pollOnce(ctx)
	})
	t.Logf("idle poll cycle: %.0f allocs/op (budget %d)", idle, pollCycleAllocBudgetIdle)
	if idle > pollCycleAllocBudgetIdle {
		t.Fatalf("idle allocs/op = %.0f, budget %d", idle, pollCycleAllocBudgetIdle)
	}
	n := 0
	busy := testing.AllocsPerRun(50, func() {
		n++
		if n%2 == 0 {
			fc.torrents = odd
		} else {
			fc.torrents = even
		}
		_ = p.pollOnce(ctx)
	})
	t.Logf("busy poll cycle: %.0f allocs/op (budget %d)", busy, pollCycleAllocBudgetBusy)
	if busy > pollCycleAllocBudgetBusy {
		t.Fatalf("busy allocs/op = %.0f, budget %d", busy, pollCycleAllocBudgetBusy)
	}
}

// BenchmarkListDecode measures transport + decode + mapping alone (one
// ListAndGlobals round trip) so the report can split it from poller logic:
// BenchmarkPollCycle minus this is roughly the poller's own overhead (plus
// the fake daemon's XML encoding, which rides along in both).
func BenchmarkListDecode(b *testing.B) {
	for _, n := range []int{500, 5000, 20000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			dir, err := os.MkdirTemp("", "bench-*")
			if err != nil {
				b.Fatal(err)
			}
			defer os.RemoveAll(dir)
			sock := filepath.Join(dir, "rt.sock")
			daemon, err := fakertorrent.StartOpts(sock, fakertorrent.Options{
				SessionSize: n, ActiveFraction: 0.04, Seed: 1,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer daemon.Stop()
			c, err := rtorrent.New("unix://"+sock, 60*time.Second)
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := c.ListAndGlobals(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestPerfFootprint records process memory and goroutines around a 20,000-
// torrent session for the performance report (PERF-6.6). It is report-only
// beyond two coarse tripwires: numbers go to the test log for pasting into
// docs/performance.md. Skipped in -short mode; run explicitly when writing
// the report: go test -run TestPerfFootprint ./internal/poller/ -v.
func TestPerfFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("footprint measurement runs explicitly for the report")
	}
	dir, err := os.MkdirTemp("", "perf-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "rt.sock")
	daemon, err := fakertorrent.StartOpts(sock, fakertorrent.Options{
		SessionSize: 20000, ActiveFraction: 0.04, Seed: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Stop()
	c, err := rtorrent.New("unix://"+sock, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p := New(c, Options{Interval: time.Second, DetailInterval: time.Second})
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := p.pollOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	snap := p.Snapshot()
	if len(snap.Torrents) != 20000 {
		t.Fatalf("torrents = %d", len(snap.Torrents))
	}
	runtime.GC()
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	goroutines := runtime.NumGoroutine()
	t.Logf("footprint/20000: heap=%.1fMB live-objects=%d sys=%.1fMB goroutines=%d torrents=%d",
		float64(mem.HeapAlloc)/1048576, mem.HeapObjects, float64(mem.Sys)/1048576,
		goroutines, len(snap.Torrents))
	if mem.HeapAlloc > 1<<30 {
		t.Fatalf("heap %.1fMB exceeds 1GB tripwire", float64(mem.HeapAlloc)/1048576)
	}
	if goroutines > 64 {
		t.Fatalf("goroutines = %d exceeds 64 tripwire", goroutines)
	}
}
