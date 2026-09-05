package trackers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeDaemon records every batch it is handed so tests can assert on the
// shape of the ramp rather than only its end state.
type fakeDaemon struct {
	mu       sync.Mutex
	targets  []rtorrent.TrackerTarget
	batches  [][]rtorrent.TrackerTarget
	disabled int
	listErr  error
	setErr   error
	failPer  int // per-tracker faults reported for each batch
}

func (d *fakeDaemon) TrackerTargets(context.Context) ([]rtorrent.TrackerTarget, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listErr != nil {
		return nil, d.listErr
	}
	return d.targets, nil
}

func (d *fakeDaemon) SetTrackersEnabled(_ context.Context, t []rtorrent.TrackerTarget, _ bool) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.setErr != nil {
		return 0, d.setErr
	}
	d.batches = append(d.batches, append([]rtorrent.TrackerTarget(nil), t...))
	return d.failPer, nil
}

func (d *fakeDaemon) DisableAllTrackers(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disabled++
	return nil
}

func (d *fakeDaemon) snapshot() (batches [][]rtorrent.TrackerTarget, disabled int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][]rtorrent.TrackerTarget(nil), d.batches...), d.disabled
}

func targets(n int) []rtorrent.TrackerTarget {
	out := make([]rtorrent.TrackerTarget, n)
	for i := range out {
		out[i] = rtorrent.TrackerTarget{Hash: "H", Index: i}
	}
	return out
}

func svc(d *fakeDaemon, c config.Trackers) *Service {
	return New(Options{Log: discard(), Daemon: d, Config: func() config.Trackers { return c }})
}

// waitFor polls until cond holds, so tests never depend on a fixed sleep.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

func TestRampEnablesInBatches(t *testing.T) {
	d := &fakeDaemon{targets: targets(10)}
	s := svc(d, config.Trackers{RampBatch: 3, RampInterval: time.Millisecond})
	s.OnConnect(context.Background())

	waitFor(t, func() bool { return !s.Status().Running && s.Status().LastRamp != nil })

	batches, disabled := d.snapshot()
	if disabled != 0 {
		t.Fatalf("ramp must not disable trackers, got %d calls", disabled)
	}
	// 10 trackers at 3 per step is 3+3+3+1: the last batch is short, and no
	// target is enabled twice or skipped.
	want := []int{3, 3, 3, 1}
	if len(batches) != len(want) {
		t.Fatalf("got %d batches, want %d", len(batches), len(want))
	}
	var seen []int
	for i, b := range batches {
		if len(b) != want[i] {
			t.Errorf("batch %d has %d targets, want %d", i, len(b), want[i])
		}
		for _, tr := range b {
			seen = append(seen, tr.Index)
		}
	}
	if len(seen) != 10 {
		t.Fatalf("enabled %d trackers, want 10", len(seen))
	}
	for i, idx := range seen {
		if idx != i {
			t.Fatalf("tracker %d enabled out of order (got index %d)", i, idx)
		}
	}
	if st := s.Status(); st.Enabled != 10 || st.Total != 10 || st.Failed != 0 {
		t.Errorf("status = %+v, want 10/10 enabled and 0 failed", st)
	}
}

func TestPerTrackerFaultsCountedNotFatal(t *testing.T) {
	// A torrent erased between enumeration and the enable faults only its
	// own entries; the ramp must keep going and report them.
	d := &fakeDaemon{targets: targets(6), failPer: 1}
	s := svc(d, config.Trackers{RampBatch: 2, RampInterval: time.Millisecond})
	s.OnConnect(context.Background())

	waitFor(t, func() bool { return !s.Status().Running && s.Status().LastRamp != nil })

	st := s.Status()
	if st.Failed != 3 || st.Enabled != 3 {
		t.Fatalf("status = %+v, want 3 enabled and 3 failed across 3 batches", st)
	}
	if batches, _ := d.snapshot(); len(batches) != 3 {
		t.Fatalf("ramp stopped after %d batches, want all 3", len(batches))
	}
}

func TestRoundTripErrorStopsRampAndRecords(t *testing.T) {
	d := &fakeDaemon{targets: targets(6), setErr: errors.New("connection reset")}
	s := svc(d, config.Trackers{RampBatch: 2, RampInterval: time.Millisecond})
	s.OnConnect(context.Background())

	waitFor(t, func() bool { return s.Status().LastError != "" })
	if st := s.Status(); st.Running {
		t.Error("ramp should not still be running after a transport error")
	}
	if batches, _ := d.snapshot(); len(batches) != 0 {
		t.Fatalf("no batch should be recorded, got %d", len(batches))
	}
}

func TestEnumerationErrorRecorded(t *testing.T) {
	d := &fakeDaemon{listErr: errors.New("scgi down")}
	s := svc(d, config.Trackers{RampBatch: 2, RampInterval: time.Millisecond})
	s.OnConnect(context.Background())

	waitFor(t, func() bool { return s.Status().LastError == "scgi down" })
}

func TestDisabledPolicyDisablesInstead(t *testing.T) {
	off := false
	d := &fakeDaemon{targets: targets(4)}
	s := svc(d, config.Trackers{EnableOnConnect: &off})
	s.OnConnect(context.Background())

	batches, disabled := d.snapshot()
	if disabled != 1 {
		t.Fatalf("want one disable call, got %d", disabled)
	}
	if len(batches) != 0 {
		t.Fatalf("opt-out must not enable anything, got %d batches", len(batches))
	}
	if s.Status().Running {
		t.Error("no ramp should be running")
	}
}

func TestReconnectSupersedesInFlightRamp(t *testing.T) {
	// A reconnect invalidates the previous enumeration, so the older ramp
	// must be abandoned and exactly one ramp left running.
	d := &fakeDaemon{targets: targets(100)}
	s := svc(d, config.Trackers{RampBatch: 1, RampInterval: 20 * time.Millisecond})

	s.OnConnect(context.Background())
	waitFor(t, func() bool { b, _ := d.snapshot(); return len(b) > 0 })

	d.mu.Lock()
	d.targets = targets(2)
	d.mu.Unlock()
	s.OnConnect(context.Background())

	waitFor(t, func() bool { st := s.Status(); return !st.Running && st.Total == 2 })
	if st := s.Status(); st.Enabled != 2 {
		t.Fatalf("status = %+v, want the second ramp's 2 trackers", st)
	}
}

func TestCancelledContextStopsRamp(t *testing.T) {
	d := &fakeDaemon{targets: targets(1000)}
	s := svc(d, config.Trackers{RampBatch: 1, RampInterval: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	s.OnConnect(ctx)
	waitFor(t, func() bool { b, _ := d.snapshot(); return len(b) > 0 })
	cancel()

	// Batches must stop arriving shortly after cancellation.
	time.Sleep(50 * time.Millisecond)
	before, _ := d.snapshot()
	time.Sleep(50 * time.Millisecond)
	after, _ := d.snapshot()
	if len(after) != len(before) {
		t.Fatalf("ramp kept running after cancel: %d then %d batches", len(before), len(after))
	}
}

func TestDefaultsUsedWhenUnset(t *testing.T) {
	var c config.Trackers
	if !c.RampEnabled() {
		t.Error("ramp must default to enabled")
	}
	if got := c.EffectiveRampBatch(); got != config.DefaultTrackerRampBatch {
		t.Errorf("batch = %d, want %d", got, config.DefaultTrackerRampBatch)
	}
	if got := c.EffectiveRampInterval(); got != config.DefaultTrackerRampInterval {
		t.Errorf("interval = %v, want %v", got, config.DefaultTrackerRampInterval)
	}
}
