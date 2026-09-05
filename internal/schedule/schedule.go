// Package schedule implements PAR-4.3 bandwidth scheduling: named limit
// profiles painted onto a 7×24 grid are applied on the minute boundary and
// after reconnect, in an explicit time zone. A manual override pauses the
// schedule until it expires.
package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
)

// Daemon applies scheduled limits.
type Daemon interface {
	SetGlobalRateKB(ctx context.Context, setter string, kb int64) error
	SetThrottleUp(ctx context.Context, name string, kb int64) error
	SetThrottleDown(ctx context.Context, name string, kb int64) error
}

// Options configures the Scheduler.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon applies profile limits.
	Daemon Daemon
	// Config returns the live schedule (read under the caller's config
	// lock; Settings saves and SIGHUP reloads apply without a restart).
	Config func() config.Schedule
	// History records applied profiles and override expiries (PAR-5.3) with
	// the "scheduler" actor; nil disables the logging.
	History *history.Log
	// Now is the clock, overridable for tests.
	Now func() time.Time
}

// Override is a temporary manual limit set that pauses the schedule.
type Override struct {
	Until        time.Time
	DownKB, UpKB int64
}

// Status is the scheduler overview for the status bar and settings.
type Status struct {
	ActiveProfile  string
	Overridden     bool
	OverrideUntil  time.Time
	OverrideDownKB int64
	OverrideUpKB   int64
	Timezone       string
	NextProfile    string
	NextChange     time.Time
}

// Scheduler applies bandwidth profiles per the weekly grid.
type Scheduler struct {
	opts Options

	mu          sync.Mutex
	lastProfile string // "" = nothing applied yet
	override    *Override
}

// New builds a Scheduler. Run starts the minute loop; Tick and ApplyNow are
// safe before that.
func New(opts Options) *Scheduler {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Scheduler{opts: opts}
}

// location resolves the schedule zone: empty or Local means server local.
func location(name string) *time.Location {
	if name == "" || name == "Local" || name == "local" {
		return time.Local
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.Local
}

// cellFor maps a time to its grid cell (weekday key Monday-first, hour).
func cellFor(t time.Time, loc *time.Location) (string, int) {
	local := t.In(loc)
	day := (int(local.Weekday()) + 6) % 7 // Monday=0 … Sunday=6
	return config.ScheduleWeekdays[day], local.Hour()
}

// findProfile returns the named profile, if defined.
func findProfile(profiles []config.BandwidthProfile, name string) *config.BandwidthProfile {
	for i := range profiles {
		if profiles[i].Name == name {
			return &profiles[i]
		}
	}
	return nil
}

// profileAt returns the active profile name for t ("" = no profile: skip).
func profileAt(sched config.Schedule, t time.Time) string {
	day, hour := cellFor(t, location(sched.Timezone))
	cells, ok := sched.Bandwidth.Grid[day]
	if !ok || hour < 0 || hour >= len(cells) {
		return ""
	}
	name := cells[hour]
	if name == "" || findProfile(sched.Bandwidth.Profiles, name) == nil {
		return ""
	}
	return name
}

// Tick evaluates the schedule for now: expiring overrides, skipping empty
// cells and unchanged profiles, applying the active one. Safe to call from
// the minute loop, tests, and reconnect hooks.
func (s *Scheduler) Tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickLocked(context.Background(), now)
}

func (s *Scheduler) tickLocked(ctx context.Context, now time.Time) {
	cfg := config.Schedule{}
	if s.opts.Config != nil {
		cfg = s.opts.Config()
	}
	if ov := s.override; ov != nil && !now.Before(ov.Until) {
		s.override = nil
		s.lastProfile = "" // force re-apply: the daemon still has override limits
		s.opts.Log.Info("schedule: manual override expired")
		s.logEvent("schedule_override_expired", "ok", fmt.Sprintf("override down %d KB/s, up %d KB/s expired", ov.DownKB, ov.UpKB))
	}
	if s.override != nil {
		return
	}
	profile := profileAt(cfg, now)
	if profile == "" || profile == s.lastProfile {
		return
	}
	cause := s.opts.History.Begin("", history.Entry{Kind: history.KindAction, Actor: "scheduler", Action: "schedule_profile", Before: map[string]string{"profile": s.lastProfile}, After: map[string]string{"profile": profile}, Message: "Request profile application; individual writes may partially succeed."})
	if err := s.applyProfile(ctx, cfg, profile); err != nil {
		if s.opts.History != nil {
			s.opts.History.Add("", history.Entry{Kind: history.KindAction, Phase: "rpc_result", Actor: "scheduler", Action: "schedule_profile", CauseID: cause, Result: "failed", Message: err.Error()})
		}
		s.opts.Log.Warn("schedule: apply failed, will retry next tick", "profile", profile, "err", err)
		return
	}
	s.lastProfile = profile
	s.opts.Log.Info("schedule: profile applied", "profile", profile)
	if p := findProfile(cfg.Bandwidth.Profiles, profile); p != nil {
		if s.opts.History != nil {
			s.opts.History.Add("", history.Entry{Kind: history.KindAction, Phase: "rpc_result", Actor: "scheduler", Action: "schedule_profile", CauseID: cause, Result: "ok", After: map[string]string{"profile": profile, "downKB": fmt.Sprint(p.DownKB), "upKB": fmt.Sprint(p.UpKB)}, Message: fmt.Sprintf("profile %q applied: down %d KB/s, up %d KB/s", profile, p.DownKB, p.UpKB)})
		}
	}
}

// logEvent records a daemon-wide scheduler outcome (empty hash: global
// ring only) with the "scheduler" actor. History may be nil; caller holds mu.
func (s *Scheduler) logEvent(action, result, message string) {
	if s.opts.History == nil {
		return
	}
	s.opts.History.Add("", history.Entry{
		Kind: history.KindAction, Actor: "scheduler", Action: action, Result: result, Message: message,
	})
}

// applyProfile sets a profile's global and channel limits. Caller holds mu.
func (s *Scheduler) applyProfile(ctx context.Context, cfg config.Schedule, name string) error {
	profile := findProfile(cfg.Bandwidth.Profiles, name)
	if profile == nil {
		return fmt.Errorf("unknown profile %q", name)
	}
	if err := s.opts.Daemon.SetGlobalRateKB(ctx, "throttle.global_down.max_rate.set_kb", profile.DownKB); err != nil {
		return fmt.Errorf("global down: %w", err)
	}
	if err := s.opts.Daemon.SetGlobalRateKB(ctx, "throttle.global_up.max_rate.set_kb", profile.UpKB); err != nil {
		return fmt.Errorf("global up: %w", err)
	}
	for _, ch := range profile.Throttles {
		if err := s.opts.Daemon.SetThrottleUp(ctx, ch.Name, ch.UpKB); err != nil {
			return fmt.Errorf("throttle.up %s: %w", ch.Name, err)
		}
		if err := s.opts.Daemon.SetThrottleDown(ctx, ch.Name, ch.DownKB); err != nil {
			return fmt.Errorf("throttle.down %s: %w", ch.Name, err)
		}
	}
	return nil
}

// ApplyNow forces the current profile to apply (reconnect path).
func (s *Scheduler) ApplyNow(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastProfile = ""
	s.tickLocked(ctx, s.opts.Now())
}

// SetOverride installs a manual global-limit override until the given time,
// applying it immediately. The schedule resumes (re-applying the current
// profile) when it expires or is cleared.
func (s *Scheduler) SetOverride(ctx context.Context, downKB, upKB int64, until time.Time) error {
	if err := s.opts.Daemon.SetGlobalRateKB(ctx, "throttle.global_down.max_rate.set_kb", downKB); err != nil {
		return fmt.Errorf("global down: %w", err)
	}
	if err := s.opts.Daemon.SetGlobalRateKB(ctx, "throttle.global_up.max_rate.set_kb", upKB); err != nil {
		return fmt.Errorf("global up: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.override = &Override{Until: until, DownKB: downKB, UpKB: upKB}
	s.opts.Log.Info("schedule: manual override set", "until", until.Format(time.RFC3339), "downKB", downKB, "upKB", upKB)
	return nil
}

// ClearOverride cancels the manual override; the next tick re-applies the
// scheduled profile.
func (s *Scheduler) ClearOverride() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.override != nil {
		s.override = nil
		s.lastProfile = ""
		s.opts.Log.Info("schedule: manual override cleared")
	}
}

// OverrideActive reports whether a manual override is currently in effect.
func (s *Scheduler) OverrideActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.override != nil && s.opts.Now().Before(s.override.Until)
}

// SetOverrideValues replaces an active override's limits (keeping its
// expiry), applying them immediately. It reports false when no override is
// active, in which case the caller should set the daemon directly — this
// keeps the status display truthful while an override owns the limits.
func (s *Scheduler) SetOverrideValues(ctx context.Context, downKB, upKB int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.override == nil || !s.opts.Now().Before(s.override.Until) {
		return false
	}
	if err := s.opts.Daemon.SetGlobalRateKB(ctx, "throttle.global_down.max_rate.set_kb", downKB); err != nil {
		return false
	}
	if err := s.opts.Daemon.SetGlobalRateKB(ctx, "throttle.global_up.max_rate.set_kb", upKB); err != nil {
		return false
	}
	s.override.DownKB = downKB
	s.override.UpKB = upKB
	return true
}

// Status snapshots the scheduler for the status bar and settings.
func (s *Scheduler) Status(now time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := config.Schedule{}
	if s.opts.Config != nil {
		cfg = s.opts.Config()
	}
	tz := cfg.Timezone
	if tz == "" {
		tz = "Local"
	}
	st := Status{Timezone: tz, ActiveProfile: s.lastProfile}
	if s.override != nil {
		st.Overridden = true
		st.OverrideUntil = s.override.Until
		st.OverrideDownKB = s.override.DownKB
		st.OverrideUpKB = s.override.UpKB
		st.NextProfile = profileAt(cfg, s.override.Until)
		st.NextChange = s.override.Until
		return st
	}
	current := profileAt(cfg, now)
	if s.lastProfile != "" {
		current = s.lastProfile
	}
	st.ActiveProfile = current
	st.NextProfile, st.NextChange = nextChange(cfg, now, current)
	return st
}

// nextChange scans forward for the next minute whose profile differs from
// current (skipping empty cells). It looks ahead just over a week, so a
// weekly grid always terminates the scan.
func nextChange(cfg config.Schedule, now time.Time, current string) (string, time.Time) {
	loc := location(cfg.Timezone)
	t := now.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 8*24*60; i++ {
		if p := profileAt(cfg, t); p != "" && p != current {
			return p, t.In(loc)
		}
		t = t.Add(time.Minute)
	}
	return "", time.Time{}
}

// nextMinute returns the next minute boundary after t.
func nextMinute(t time.Time) time.Time {
	return t.Truncate(time.Minute).Add(time.Minute)
}

// Run applies the current profile at startup, then ticks on every minute
// boundary until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.Tick(s.opts.Now())
	for {
		timer := time.NewTimer(time.Until(nextMinute(s.opts.Now())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.Tick(s.opts.Now())
		}
	}
}
