package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
)

type fakeDaemon struct {
	mu       sync.Mutex
	globals  map[string]int64
	channels map[string][2]int64
	calls    int
}

func (f *fakeDaemon) SetGlobalRateKB(_ context.Context, setter string, n int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.globals == nil {
		f.globals = map[string]int64{}
	}
	f.globals[setter] = n
	f.calls++
	return nil
}

func (f *fakeDaemon) SetThrottleUp(_ context.Context, name string, kb int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.channels == nil {
		f.channels = map[string][2]int64{}
	}
	pair := f.channels[name]
	pair[0] = kb
	f.channels[name] = pair
	f.calls++
	return nil
}

func (f *fakeDaemon) SetThrottleDown(_ context.Context, name string, kb int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.channels == nil {
		f.channels = map[string][2]int64{}
	}
	pair := f.channels[name]
	pair[1] = kb
	f.channels[name] = pair
	f.calls++
	return nil
}

func (f *fakeDaemon) snapshot() (map[string]int64, map[string][2]int64, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	globals := map[string]int64{}
	for k, v := range f.globals {
		globals[k] = v
	}
	channels := map[string][2]int64{}
	for k, v := range f.channels {
		channels[k] = v
	}
	return globals, channels, f.calls
}

// testSchedule builds a two-profile Mon-only grid (UTC).
func testSchedule() config.Schedule {
	night := make([]string, 24)
	for h := range night {
		night[h] = "night"
	}
	// Monday 9-16 is "day", everything Monday else "night".
	for h := 9; h < 17; h++ {
		night[h] = "day"
	}
	return config.Schedule{
		Timezone: "UTC",
		Bandwidth: config.BandwidthSchedule{
			Profiles: []config.BandwidthProfile{
				{Name: "day", Color: "#f59e0b", DownKB: 1000, UpKB: 500, Throttles: []config.ThrottleChannel{{Name: "slow", UpKB: 100, DownKB: 200}}},
				{Name: "night", Color: "#35418f", DownKB: 0, UpKB: 0},
			},
			Grid: map[string][]string{"mon": night},
		},
	}
}

func newTestScheduler(cfg config.Schedule, daemon *fakeDaemon) *Scheduler {
	return New(Options{
		Log:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Daemon: daemon,
		Config: func() config.Schedule { return cfg },
	})
}

func TestCellForMondayFirst(t *testing.T) {
	utc := time.UTC
	// 2026-09-07 is a Monday.
	monday, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-07 00:30", utc)
	day, hour := cellFor(monday, time.UTC)
	if day != "mon" || hour != 0 {
		t.Fatalf("got %s/%d", day, hour)
	}
	sunday, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-13 23:59", utc)
	day, hour = cellFor(sunday, time.UTC)
	if day != "sun" || hour != 23 {
		t.Fatalf("got %s/%d", day, hour)
	}
}

func TestProfileAt(t *testing.T) {
	sched := testSchedule()
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	if got := profileAt(sched, at("2026-09-07 10:00")); got != "day" {
		t.Fatalf("mon 10:00 = %q", got)
	}
	if got := profileAt(sched, at("2026-09-07 08:59")); got != "night" {
		t.Fatalf("mon 08:59 = %q", got)
	}
	if got := profileAt(sched, at("2026-09-08 10:00")); got != "" {
		t.Fatalf("tue (no row) = %q, want skip", got)
	}
	// Unknown profile names in cells are skipped, not fatal.
	sched.Bandwidth.Grid["tue"] = []string{"nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope", "nope"}
	if got := profileAt(sched, at("2026-09-08 10:00")); got != "" {
		t.Fatalf("unknown profile = %q, want skip", got)
	}
}

func TestProfileAtTimezone(t *testing.T) {
	plus5 := time.FixedZone("Plus5", 5*3600)
	// Monday 20:00 UTC = Tuesday 01:00 at +5.
	mondayEvening, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-07 20:00", time.UTC)
	day, hour := cellFor(mondayEvening, plus5)
	if day != "tue" || hour != 1 {
		t.Fatalf("got %s/%d, want tue/1", day, hour)
	}
}

func newYork(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable; DST transition test needs zoneinfo")
	}
	return loc
}

// TestDSTSpringForward pins the 2026-03-08 transition (02:00 EST → 03:00
// EDT): wall hours on either side map to their own cells.
func TestDSTSpringForward(t *testing.T) {
	loc := newYork(t)
	cells := make([]string, 24)
	for h := range cells {
		cells[h] = fmt.Sprintf("h%d", h)
	}
	sched := config.Schedule{
		Timezone: "America/New_York",
		Bandwidth: config.BandwidthSchedule{
			Profiles: func() []config.BandwidthProfile {
				var out []config.BandwidthProfile
				for h := range cells {
					out = append(out, config.BandwidthProfile{Name: fmt.Sprintf("h%d", h)})
				}
				return out
			}(),
			Grid: map[string][]string{"sun": cells},
		},
	}
	before := time.Date(2026, 3, 8, 1, 30, 0, 0, loc) // EST
	after := time.Date(2026, 3, 8, 3, 30, 0, 0, loc)  // EDT
	if got := profileAt(sched, before); got != "h1" {
		t.Fatalf("01:30 EST = %q", got)
	}
	if got := profileAt(sched, after); got != "h3" {
		t.Fatalf("03:30 EDT = %q", got)
	}
}

// TestDSTFallBack pins the 2026-11-01 transition (01:00 happens twice):
// both occurrences map to the same cell, so the profile re-applies
// idempotently instead of flapping.
func TestDSTFallBack(t *testing.T) {
	loc := newYork(t)
	sched := config.Schedule{
		Timezone: "America/New_York",
		Bandwidth: config.BandwidthSchedule{
			Profiles: []config.BandwidthProfile{{Name: "one"}},
			Grid:     map[string][]string{"sun": repeatProfile("one")},
		},
	}
	first := time.Date(2026, 11, 1, 1, 30, 0, 0, loc) // EDT occurrence
	// The second 01:30 (EST) is one hour later in absolute time.
	second := first.Add(time.Hour)
	if got := profileAt(sched, first); got != "one" {
		t.Fatalf("first 01:30 = %q", got)
	}
	if got := profileAt(sched, second); got != "one" {
		t.Fatalf("second 01:30 = %q", got)
	}
	_, offset1 := first.Zone()
	_, offset2 := second.Zone()
	if offset1 == offset2 {
		t.Fatal("test setup broken: expected two different UTC offsets")
	}
}

func repeatProfile(name string) []string {
	out := make([]string, 24)
	for i := range out {
		out[i] = name
	}
	return out
}

func TestTickAppliesOnChangeOnly(t *testing.T) {
	sched := testSchedule()
	daemon := &fakeDaemon{}
	s := newTestScheduler(sched, daemon)
	at := func(h int) time.Time {
		return time.Date(2026, 9, 7, h, 0, 30, 0, time.UTC) // a Monday
	}

	s.Tick(at(8)) // night
	globals, _, calls := daemon.snapshot()
	if globals["throttle.global_down.max_rate.set_kb"] != 0 || calls != 2 {
		t.Fatalf("night apply = %+v, calls = %d", globals, calls)
	}
	s.Tick(at(8)) // same cell: no re-apply
	if _, _, calls := daemon.snapshot(); calls != 2 {
		t.Fatalf("re-applied unchanged profile: %d calls", calls)
	}
	s.Tick(at(10)) // day: globals + one channel (2 + 2 calls)
	globals, channels, calls := daemon.snapshot()
	if globals["throttle.global_down.max_rate.set_kb"] != 1000 || globals["throttle.global_up.max_rate.set_kb"] != 500 {
		t.Fatalf("day globals = %+v", globals)
	}
	if channels["slow"] != [2]int64{100, 200} {
		t.Fatalf("day channels = %+v", channels)
	}
	if calls != 6 {
		t.Fatalf("calls = %d, want 6", calls)
	}
	s.Tick(at(11)) // empty Tuesday row next day boundary is far; same day cell: skip
	s.Tick(at(12))
	if _, _, calls := daemon.snapshot(); calls != 6 {
		t.Fatalf("re-applied within profile: %d calls", calls)
	}
}

func TestTickSkipsEmptyCells(t *testing.T) {
	sched := testSchedule()
	daemon := &fakeDaemon{}
	s := newTestScheduler(sched, daemon)
	// Tuesday has no row: nothing applied, nothing recorded.
	s.Tick(time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC))
	if _, _, calls := daemon.snapshot(); calls != 0 {
		t.Fatalf("empty cell applied: %d calls", calls)
	}
}

func TestOverridePausesAndExpires(t *testing.T) {
	sched := testSchedule()
	daemon := &fakeDaemon{}
	s := newTestScheduler(sched, daemon)
	base := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC) // day profile
	s.Tick(base)
	if _, _, calls := daemon.snapshot(); calls != 4 {
		t.Fatalf("setup calls = %d", calls)
	}

	ctx := context.Background()
	until := base.Add(30 * time.Minute)
	if err := s.SetOverride(ctx, 100, 50, until); err != nil {
		t.Fatal(err)
	}
	globals, _, _ := daemon.snapshot()
	if globals["throttle.global_down.max_rate.set_kb"] != 100 {
		t.Fatalf("override not applied: %+v", globals)
	}
	// Ticks during the override leave the daemon alone.
	s.Tick(base.Add(10 * time.Minute))
	s.Tick(base.Add(20 * time.Minute))
	if _, _, calls := daemon.snapshot(); calls != 6 {
		t.Fatalf("schedule ran during override: %d calls", calls)
	}
	st := s.Status(base.Add(20 * time.Minute))
	if !st.Overridden || st.ActiveProfile != "day" {
		t.Fatalf("status = %+v", st)
	}

	// After expiry the scheduled profile re-applies even though its name
	// never changed.
	s.Tick(base.Add(31 * time.Minute))
	globals, _, calls := daemon.snapshot()
	if globals["throttle.global_down.max_rate.set_kb"] != 1000 {
		t.Fatalf("scheduled profile not restored: %+v", globals)
	}
	if calls != 10 {
		t.Fatalf("calls = %d, want 10 (day 4 + override 2 + restore 4)", calls)
	}
	st = s.Status(base.Add(31 * time.Minute))
	if st.Overridden {
		t.Fatalf("status still overridden: %+v", st)
	}
}

func TestClearOverride(t *testing.T) {
	sched := testSchedule()
	daemon := &fakeDaemon{}
	s := newTestScheduler(sched, daemon)
	base := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := s.SetOverride(ctx, 0, 0, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.ClearOverride()
	s.Tick(base.Add(time.Minute))
	globals, _, _ := daemon.snapshot()
	if globals["throttle.global_down.max_rate.set_kb"] != 1000 {
		t.Fatalf("profile not restored after clear: %+v", globals)
	}
}

func TestApplyNowForces(t *testing.T) {
	sched := testSchedule()
	daemon := &fakeDaemon{}
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	s := New(Options{
		Daemon: daemon,
		Config: func() config.Schedule { return sched },
		Now:    func() time.Time { return now },
	})
	s.Tick(now)
	if _, _, calls := daemon.snapshot(); calls != 4 {
		t.Fatalf("calls = %d", calls)
	}
	// Same instant via ApplyNow (reconnect path) re-applies unconditionally.
	s.ApplyNow(context.Background())
	if _, _, calls := daemon.snapshot(); calls != 8 {
		t.Fatalf("ApplyNow did not force: %d calls", calls)
	}
}

func TestNextChange(t *testing.T) {
	sched := testSchedule()
	// Monday 08:30 (night) → next change is 09:00 day.
	next, at := nextChange(sched, time.Date(2026, 9, 7, 8, 30, 0, 0, time.UTC), "night")
	if next != "day" || at.Hour() != 9 || at.Minute() != 0 {
		t.Fatalf("next = %q at %v", next, at)
	}
	// Monday 10:00 (day) → next change is 17:00 night.
	next, at = nextChange(sched, time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC), "day")
	if next != "night" || at.Hour() != 17 {
		t.Fatalf("next = %q at %v", next, at)
	}
	// Empty schedule never changes.
	empty := config.Schedule{}
	if next, at := nextChange(empty, time.Now(), ""); next != "" || !at.IsZero() {
		t.Fatalf("empty = %q at %v", next, at)
	}
}

func TestNextMinute(t *testing.T) {
	base := time.Date(2026, 9, 7, 10, 0, 30, 0, time.UTC)
	if got := nextMinute(base); !got.Equal(time.Date(2026, 9, 7, 10, 1, 0, 0, time.UTC)) {
		t.Fatalf("got %v", got)
	}
}

func TestStatusShape(t *testing.T) {
	sched := testSchedule()
	daemon := &fakeDaemon{}
	s := newTestScheduler(sched, daemon)
	now := time.Date(2026, 9, 7, 10, 0, 30, 0, time.UTC)
	s.Tick(now)
	st := s.Status(now)
	if st.ActiveProfile != "day" || st.Timezone != "UTC" {
		t.Fatalf("status = %+v", st)
	}
	if st.NextProfile != "night" || st.NextChange.Hour() != 17 {
		t.Fatalf("next = %+v", st)
	}
}
