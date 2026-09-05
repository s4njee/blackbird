// Package seeding implements PAR-4.2 ratio groups and seeding limits:
// torrents assigned to a group (via a configurable custom slot) are
// evaluated per poll cycle against the group's conditions, and the first met
// condition fires the group's action exactly once per (torrent, group).
//
// Design note (required by the acceptance criteria): enforcement lives in
// Blackbird's poller rather than rTorrent's group.seeding.* schedules
// because (1) every trigger and outcome lands in the per-torrent history log
// and server logs, so policy is visible instead of a silent daemon schedule;
// (2) evaluation is pure and unit-testable against fixture rows without a
// daemon; (3) rules are versioned YAML applied uniformly on connect, SIGHUP,
// and Settings save. Trade-offs: granularity is one poll interval (a torrent
// can overshoot a ratio between polls), enforcement only runs while
// Blackbird is connected, and stopping/erasing acts through the same
// XML-RPC calls an operator could issue by hand.
package seeding

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
)

// Conditions, in evaluation order. The first met condition fires.
const (
	ConditionMinRatio  = "min_ratio"
	ConditionMaxRatio  = "max_ratio"
	ConditionMinUpload = "min_upload"
	ConditionMaxTime   = "max_time"
)

// queueCap bounds pending enforcement jobs. Detection runs inside the
// poller's critical section, so enqueueing must never block.
const queueCap = 64

// Daemon executes seeding-policy actions.
type Daemon interface {
	Stop(ctx context.Context, hash string) error
	SetLabel(ctx context.Context, hash, label string) error
	Erase(ctx context.Context, hash string) error
	RemoveWithData(ctx context.Context, hash string, allowedDirs []string) (string, error)
}

// Options configures the Engine.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon executes stop/label/erase actions.
	Daemon Daemon
	// History records outcomes on the torrent's Logger tab.
	History *history.Log
	// Roots supplies the download roots bounding erase_with_data.
	Roots func() []string
	// CleanupGuard serializes cleanup with durable preservation pins.
	CleanupGuard func(string, func() error) error
}

// Job is one triggered group action awaiting execution.
type Job struct {
	Hash      string
	Name      string
	Group     string
	Condition string
	Action    string
	Label     string
}

// Engine executes triggered seeding actions on a small worker pool so a
// slow erase never stalls the poller or other actions.
type Engine struct {
	opts Options

	queue chan Job
}

// New builds an Engine. Run starts the workers; Enqueue is safe before that.
func New(opts Options) *Engine {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Engine{opts: opts, queue: make(chan Job, queueCap)}
}

// Enqueue hands a triggered action to the workers. Non-blocking: the caller
// runs inside the poller's critical section.
// Enqueue hands a triggered action to the workers, reporting whether it was
// accepted. A false result means the queue was full and the caller must not
// treat the action as fired.
func (e *Engine) Enqueue(job Job) bool {
	select {
	case e.queue <- job:
		return true
	default:
		e.opts.Log.Warn("seeding: queue full, dropping triggered action", "hash", job.Hash, "group", job.Group)
		return false
	}
}

// Run drains the queue until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-e.queue:
					e.execute(ctx, job)
				}
			}
		}()
	}
	wg.Wait()
}

// execute runs one triggered action and records the outcome.
func (e *Engine) execute(ctx context.Context, job Job) {
	cause := e.opts.History.Begin(job.Hash, history.Entry{Kind: history.KindAction, Actor: "seeding", Action: job.Action, Name: job.Name, Message: fmt.Sprintf("group %q met %s", job.Group, describeCondition(job)), After: map[string]string{"group": job.Group, "label": job.Label}})
	e.opts.Log.Info("seeding: enforcing", "group", job.Group, "action", job.Action, "hash", job.Hash, "name", job.Name)
	var err error
	switch job.Action {
	case config.SeedingStopAndSetLabel:
		if err = e.opts.Daemon.Stop(ctx, job.Hash); err == nil {
			err = e.opts.Daemon.SetLabel(ctx, job.Hash, job.Label)
		}
	case config.SeedingErase:
		err = e.guard(job.Hash, func() error { return e.opts.Daemon.Erase(ctx, job.Hash) })
	case config.SeedingEraseWithData:
		roots := []string{}
		if e.opts.Roots != nil {
			roots = e.opts.Roots()
		}
		err = e.guard(job.Hash, func() error { _, e := e.opts.Daemon.RemoveWithData(ctx, job.Hash, roots); return e })
	default: // config.SeedingStop and any unknown value fail safe to stop
		err = e.opts.Daemon.Stop(ctx, job.Hash)
	}
	if err != nil {
		e.opts.Log.Warn("seeding: action failed", "group", job.Group, "action", job.Action, "hash", job.Hash, "err", err)
	} else {
		e.opts.Log.Info("seeding: action ok", "group", job.Group, "action", job.Action, "hash", job.Hash)
	}
	e.record(job, err, cause)
}

// record writes one history entry for an enforcement outcome.
func (e *Engine) record(job Job, err error, cause string) {
	if e.opts.History == nil {
		return
	}
	result := "ok"
	message := fmt.Sprintf("group %q met %s; %s", job.Group, describeCondition(job), describeAction(job))
	if err != nil {
		result = "failed"
		message += ": " + err.Error()
	}
	e.opts.History.Add(job.Hash, history.Entry{
		CauseID: cause, Phase: "rpc_result",
		Kind: history.KindAction, Actor: "seeding", Action: job.Action, Result: result, Message: message,
		Name: job.Name,
	})
}

func describeCondition(job Job) string {
	switch job.Condition {
	case ConditionMinRatio:
		return "min_ratio"
	case ConditionMaxRatio:
		return "max_ratio"
	case ConditionMinUpload:
		return "min_upload"
	case ConditionMaxTime:
		return "max_time"
	default:
		return job.Condition
	}
}

func describeAction(job Job) string {
	switch job.Action {
	case config.SeedingStopAndSetLabel:
		return "stopped and labeled " + job.Label
	case config.SeedingErase:
		return "erased from session"
	case config.SeedingEraseWithData:
		return "erased with data"
	default:
		return "stopped"
	}
}

// Trigger is one met condition awaiting the fired-marker check.
type Trigger struct {
	Group     config.SeedingGroup
	Condition string
	Detail    string
}

// Evaluate returns the first met condition for a seeding torrent assigned to
// the group. Pure and unit-testable: no I/O, no clock beyond the passed now.
func Evaluate(group config.SeedingGroup, t rtorrent.Torrent, now time.Time) (Trigger, bool) {
	if group.MinRatio > 0 && t.Ratio >= group.MinRatio {
		return Trigger{Group: group, Condition: ConditionMinRatio,
			Detail: fmt.Sprintf("ratio %.3f >= min %.3f", t.Ratio, group.MinRatio)}, true
	}
	if group.MaxRatio > 0 && t.Ratio >= group.MaxRatio {
		return Trigger{Group: group, Condition: ConditionMaxRatio,
			Detail: fmt.Sprintf("ratio %.3f >= max %.3f", t.Ratio, group.MaxRatio)}, true
	}
	if group.MinUploadBytes > 0 && t.UploadedBytes >= group.MinUploadBytes {
		return Trigger{Group: group, Condition: ConditionMinUpload,
			Detail: fmt.Sprintf("uploaded %d >= min %d", t.UploadedBytes, group.MinUploadBytes)}, true
	}
	if group.MaxSeedingTime > 0 && !t.FinishedAt.IsZero() && now.Sub(t.FinishedAt) >= group.MaxSeedingTime {
		return Trigger{Group: group, Condition: ConditionMaxTime,
			Detail: fmt.Sprintf("seeding %s >= max %s", now.Sub(t.FinishedAt).Round(time.Second), group.MaxSeedingTime)}, true
	}
	return Trigger{}, false
}

// FindGroup returns the group carrying an exact assignment name.
func FindGroup(groups []config.SeedingGroup, name string) *config.SeedingGroup {
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i]
		}
	}
	return nil
}

func (e *Engine) guard(hash string, action func() error) error {
	if e.opts.CleanupGuard != nil {
		return e.opts.CleanupGuard(hash, action)
	}
	return action()
}
