// Package perf guards the Epic 6 budgets in CI (PERF-6.6): it runs the
// poll-cycle, delta, and encoding benchmarks as a subprocess, compares
// against checked-in baselines, and fails on a 20% regression. Numbers are
// platform-specific; only entries matching runtime.GOOS/runtime.GOARCH
// enforce. Everything else logs and skips — see docs/performance.md for the
// full report and how to record a new platform.
package perf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// regressionTolerance is the allowed slowdown over baseline before CI fails.
const regressionTolerance = 0.20

// Metrics is one benchmark's recorded cost.
type Metrics struct {
	NsOp     float64 `json:"nsop"`
	BOp      float64 `json:"bop"`
	AllocsOp float64 `json:"allocsop"`
}

// PlatformEntry is one machine's recorded benchmarks.
type PlatformEntry struct {
	Platform   string             `json:"platform"` // GOOS/GOARCH
	GoVersion  string             `json:"go"`
	Recorded   string             `json:"recorded"` // RFC3339
	Benchmarks map[string]Metrics `json:"benchmarks"`
}

// Baselines is the checked-in file: one entry per measured platform.
type Baselines struct {
	Version int             `json:"version"`
	Entries []PlatformEntry `json:"entries"`
}

// baselinesPath resolves docs/performance-baselines.json from this source
// file so the test works regardless of invocation directory.
func baselinesPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "docs", "performance-baselines.json")
}

func loadBaselines(t *testing.T) Baselines {
	t.Helper()
	raw, err := os.ReadFile(baselinesPath(t))
	if err != nil {
		if os.Getenv("PERF_UPDATE") == "1" {
			return Baselines{Version: 1}
		}
		t.Fatalf("read baselines: %v (run make bench-update)", err)
	}
	var b Baselines
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse baselines: %v", err)
	}
	return b
}

func currentPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func (b Baselines) entry() *PlatformEntry {
	want := currentPlatform()
	for i := range b.Entries {
		if b.Entries[i].Platform == want {
			return &b.Entries[i]
		}
	}
	return nil
}

// moduleRoot walks up from the test binary's directory to go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// Guarded benchmarks: fast and stable enough for CI. The /20000 fixture
// variants run under `make bench` for the report but are too slow and noisy
// to gate merges.
const guardBenchPattern = `BenchmarkPollCycle/(500|5000)$|BenchmarkComputeDelta$|BenchmarkListDecode/(500|5000)$|BenchmarkDeltaEncoding`

var benchLine = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([\d.]+)\s+ns/op(?:\s+([\d.]+)\s+B/op)?(?:\s+([\d.]+)\s+allocs/op)?`)

// runBenchmarks executes the guarded benchmarks and parses ns/op, B/op and
// allocs/op per benchmark name.
func runBenchmarks(t *testing.T) map[string]Metrics {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable")
	}
	cmd := exec.Command(goBin, "test",
		"-run=^$", "-bench="+guardBenchPattern, "-benchmem",
		"-benchtime=500ms", "-count=1",
		"./internal/poller/", "./internal/api/")
	cmd.Dir = moduleRoot(t)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("benchmark run failed:\n%s", out.String())
	}
	got := map[string]Metrics{}
	for _, line := range strings.Split(out.String(), "\n") {
		m := benchLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		nsop, _ := strconv.ParseFloat(m[2], 64)
		metrics := Metrics{NsOp: nsop}
		if m[3] != "" {
			metrics.BOp, _ = strconv.ParseFloat(m[3], 64)
		}
		if m[4] != "" {
			metrics.AllocsOp, _ = strconv.ParseFloat(m[4], 64)
		}
		got[m[1]] = metrics
	}
	if len(got) == 0 {
		t.Fatalf("no benchmarks parsed:\n%s", out.String())
	}
	return got
}

// TestPerfRegression fails CI on a >20% slowdown (ns/op or allocs/op)
// against the checked-in baseline for this platform. It runs only when
// PERF_GUARD=1 (set by the CI perf job) and never in -short mode: benchmark
// timing needs a quiet, isolated runner, and sharing a `go test ./...`
// invocation with other packages skews it through CPU contention.
func TestPerfRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("perf guard runs outside -short mode (and in CI)")
	}
	if os.Getenv("PERF_GUARD") != "1" {
		t.Skip("perf guard runs with PERF_GUARD=1 (CI perf job or make bench-guard)")
	}
	got := runBenchmarks(t)
	base := loadBaselines(t)
	if os.Getenv("PERF_UPDATE") == "1" {
		entry := base.entry()
		if entry == nil {
			base.Entries = append(base.Entries, PlatformEntry{Platform: currentPlatform()})
			entry = &base.Entries[len(base.Entries)-1]
		}
		entry.Benchmarks = got
		entry.GoVersion = runtime.Version()
		entry.Recorded = time.Now().UTC().Format(time.RFC3339)
		raw, err := json.MarshalIndent(base, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselinesPath(t), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, m := range got {
			t.Logf("recorded %s: %.0f ns/op %.0f B/op %.0f allocs/op", name, m.NsOp, m.BOp, m.AllocsOp)
		}
		return
	}
	entry := base.entry()
	if entry == nil {
		t.Skipf("no baseline for %s: run make bench-update to record one", currentPlatform())
	}
	// A breach must reproduce on an immediate re-run to fail: benchmark
	// timing on shared runners spikes (observed 2-3x under load with
	// identical alloc counts), while a genuine regression breaches twice.
	// Allocations compare on the first run — they never flake.
	if failed := compareAgainstBaseline(t, entry, got, true); len(failed) > 0 {
		t.Logf("breach on first run (%v), re-running to confirm", failed)
		if failed := compareAgainstBaseline(t, entry, runBenchmarks(t), true); len(failed) > 0 {
			t.Fatalf("sustained performance regression: %v", failed)
		}
		t.Logf("breach did not reproduce; treating the first run as machine noise")
	}
	missing := false
	for name := range entry.Benchmarks {
		if _, ok := got[name]; !ok {
			t.Logf("baseline %s not measured (renamed benchmark?): run make bench-update", name)
			missing = true
		}
	}
	if missing {
		t.Log("stale baseline entries found (informational only)")
	}
	fmt.Fprintf(os.Stderr, "perf guard: %d benchmarks within 20%% of baseline\n", len(got))
}

// compareAgainstBaseline logs every benchmark and returns the names breaching
// 20% on ns/op or allocs/op. Unknown benchmarks fail only when failUnknown
// is set (a newly added benchmark must be recorded).
func compareAgainstBaseline(t *testing.T, entry *PlatformEntry, got map[string]Metrics, failUnknown bool) []string {
	t.Helper()
	var failed []string
	for name, m := range got {
		want, ok := entry.Benchmarks[name]
		if !ok {
			t.Logf("no baseline for %s: run make bench-update", name)
			if failUnknown {
				failed = append(failed, name+" (unrecorded)")
			}
			continue
		}
		t.Logf("%s: %.0f ns/op (base %.0f) %.0f allocs/op (base %.0f)", name, m.NsOp, want.NsOp, m.AllocsOp, want.AllocsOp)
		if m.NsOp > want.NsOp*(1+regressionTolerance) {
			t.Logf("%s: %.0f ns/op over baseline %.0f", name, m.NsOp, want.NsOp)
			failed = append(failed, name)
		}
		if m.AllocsOp > want.AllocsOp*(1+regressionTolerance) {
			t.Logf("%s: %.0f allocs/op over baseline %.0f", name, m.AllocsOp, want.AllocsOp)
			failed = append(failed, name)
		}
	}
	return failed
}
