// Package trackers ramps tracker announces back up after the daemon
// connects.
//
// rTorrent's appliance config disables every tracker while the session
// loads, so torrents that are still hash-checking do not announce. Turning
// them all back on with the global trackers.enable command makes the whole
// session announce at once, and that is not survivable on a large session:
// every in-flight announce holds a file descriptor, and when rTorrent runs
// out it raises torrent::resource_error from inside its poll thread
// ("Listener port accept() failed: Too many open files"), which is fatal
// rather than degrading — the daemon aborts and Compose restarts it. A
// 1,091-torrent session with 2,648 trackers reproduced this in 42 seconds
// against an nofile limit of 2048.
//
// So the ramp enables trackers a batch at a time with a pause between
// batches, keeping concurrent announces bounded no matter how large the
// session is. The raised nofile limit in Compose lifts the ceiling; this
// keeps the session from walking into it.
package trackers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
)

// Daemon is the subset of the rTorrent client the ramp drives.
type Daemon interface {
	TrackerTargets(ctx context.Context) ([]rtorrent.TrackerTarget, error)
	SetTrackersEnabled(ctx context.Context, targets []rtorrent.TrackerTarget, enabled bool) (int, error)
	DisableAllTrackers(ctx context.Context) error
}

// Options configures the Service.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon receives the tracker enumeration and the batched enables.
	Daemon Daemon
	// Config returns the live ramp configuration (read under the caller's
	// config lock) so Settings saves and SIGHUP reloads apply without a
	// restart. The next connect picks up the new values.
	Config func() config.Trackers
}

// Status is the observable ramp state. A daemon abort used to be invisible
// in Blackbird's log; the ramp reports its own progress so a restart during
// one is legible after the fact.
type Status struct {
	Running   bool       `json:"running"`
	Enabled   int        `json:"enabled"`
	Total     int        `json:"total"`
	Failed    int        `json:"failed"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	LastRamp  *time.Time `json:"lastRamp,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

// Service runs at most one ramp at a time.
type Service struct {
	opts Options
	log  *slog.Logger

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc // cancels the in-flight ramp, if any
	done   chan struct{}      // closed when the in-flight ramp returns
}

// New builds a Service. Daemon and Config are required.
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{opts: opts, log: log}
}

// OnConnect applies the appliance-wide tracker policy after a successful
// daemon connection. base must outlive the connection (the poller's
// per-cycle context is cancelled long before a ramp finishes); the ramp
// itself runs in the background so it never blocks the poll loop.
//
// Tracker enablement lives in rTorrent's session per torrent, so this runs
// after every reconnect: the daemon may have restarted underneath us and
// come back with every tracker disabled again.
func (s *Service) OnConnect(base context.Context) {
	cfg := s.opts.Config()
	if !cfg.RampEnabled() {
		// Opt-out: hold the daemon in the state rtorrent.rc booted it into.
		// One cheap global call, so no need to leave the caller's goroutine.
		if err := s.opts.Daemon.DisableAllTrackers(base); err != nil {
			s.log.Warn("trackers: connect-time disable failed", "err", err)
		}
		return
	}
	s.start(base, cfg.EffectiveRampBatch(), cfg.EffectiveRampInterval())
}

// start cancels any ramp still in flight and launches a fresh one. A
// reconnect invalidates the previous enumeration — the daemon may have
// restarted and be holding entirely different torrents — so the old ramp is
// abandoned rather than allowed to finish against stale targets.
func (s *Service) start(base context.Context, batch int, interval time.Duration) {
	s.mu.Lock()
	prevCancel, prevDone := s.cancel, s.done
	ctx, cancel := context.WithCancel(base)
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.mu.Unlock()

	if prevCancel != nil {
		prevCancel()
	}

	go func() {
		defer close(done)
		defer cancel()
		// Wait for the superseded ramp to unwind so two ramps never write
		// status or drive the daemon concurrently.
		if prevDone != nil {
			select {
			case <-prevDone:
			case <-ctx.Done():
				return
			}
		}
		s.run(ctx, batch, interval)
	}()
}

func (s *Service) run(ctx context.Context, batch int, interval time.Duration) {
	started := time.Now()
	targets, err := s.opts.Daemon.TrackerTargets(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.fail(err)
		s.log.Warn("trackers: ramp could not enumerate trackers", "err", err)
		return
	}
	total := len(targets)

	s.mu.Lock()
	s.status = Status{Running: true, Total: total, StartedAt: &started}
	s.mu.Unlock()

	if total == 0 {
		s.finish(0, 0, nil)
		return
	}
	s.log.Info("trackers: ramping announces up",
		"trackers", total, "batch", batch, "interval", interval,
		"estimate", (time.Duration((total+batch-1)/batch) * interval).Round(time.Second))

	var enabled, failed int
	for start := 0; start < total; start += batch {
		end := min(start+batch, total)
		n, err := s.opts.Daemon.SetTrackersEnabled(ctx, targets[start:end], true)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// A failed round trip mid-ramp usually means the daemon went
			// away. Stop here; the next connect starts a fresh ramp with a
			// fresh enumeration.
			s.fail(err)
			s.log.Warn("trackers: ramp stopped", "err", err, "enabled", enabled, "of", total)
			return
		}
		enabled += (end - start) - n
		failed += n

		if end >= total {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
	s.finish(enabled, failed, &started)
	s.log.Info("trackers: ramp complete",
		"enabled", enabled, "failed", failed, "of", total,
		"took", time.Since(started).Round(time.Second))
}

func (s *Service) finish(enabled, failed int, started *time.Time) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = false
	s.status.Enabled = enabled
	s.status.Failed = failed
	s.status.StartedAt = started
	s.status.LastRamp = &now
	s.status.LastError = ""
}

func (s *Service) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = false
	s.status.LastError = err.Error()
}

// Status returns a snapshot of the ramp state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}
