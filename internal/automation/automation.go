// Package automation implements PAR-3.2 completion rules: when the poller
// observes a torrent's d.complete flip 0→1, an ordered rule list from
// automation.on_complete is evaluated (first match wins) and the rule's
// actions run once for that hash. Results land in the per-torrent history
// log; failures are fanned out to open consoles as toasts. A persisted
// marker file prevents rules from running twice for the same hash across
// restarts.
package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
)

// queueCap bounds how many completed torrents wait for the worker. The
// poller hook must never block (it fires while the poller lock is held), so
// an overflowing queue drops the torrent and logs.
const queueCap = 64

// webhookTimeout bounds a webhook POST.
const webhookTimeout = 10 * time.Second

// Daemon is the rTorrent client slice the rule actions need.
type Daemon interface {
	SetLabel(ctx context.Context, hash, label string) error
	AddTracker(ctx context.Context, hash, url string, group int) error
}

// Mover moves a torrent's data with the PAR-2.2 move engine (copy/verify
// across devices, stop/restart of running torrents). The API server
// implements it; the interface keeps the package testable.
type Mover interface {
	MoveForAutomation(ctx context.Context, hash, destination string) error
}

// Options configures the Engine.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon applies set_label and add_tracker actions.
	Daemon Daemon
	// Mover applies move_to actions via the PAR-2.2 engine.
	Mover Mover
	// History records per-action outcomes on the torrent's Logger tab.
	History *history.Log
	// Marker persists which hashes were already processed.
	Marker *Marker
	// Rules returns the live ordered rule list (read under the caller's
	// config lock; wired in main.go).
	Rules func() []config.CompletionRule
	// Unpack hands a completed hash to the unpack service (PAR-3.4). Set
	// via SetUnpack after construction to avoid an init-order cycle.
	Unpack func(hash string)
	// HasUnpack reports whether any unpack rule exists. Set via SetUnpack.
	HasUnpack func() bool
}

// Notice is one rule outcome, fanned out to WebSocket clients as a toast.
// Only failures must be toasted, but successes are broadcast so the UI can
// choose to surface them later.
type Notice struct {
	At      time.Time `json:"at"`
	Hash    string    `json:"hash"`
	Torrent string    `json:"torrent,omitempty"`
	Rule    string    `json:"rule"`
	Kind    string    `json:"kind"` // completed | failed
	Message string    `json:"message,omitempty"`
}

// Engine evaluates completion rules on a single worker goroutine so rule
// actions never contend with the poller.
type Engine struct {
	opts Options

	mu        sync.Mutex
	mover     Mover
	unpack    func(hash string)
	hasUnpack func() bool
	queue     chan rtorrent.Torrent
	subs      map[int]func(Notice)
	nextSub   int
}

// New builds an Engine. Run starts the worker; Enqueue is safe before that.
func New(opts Options) *Engine {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Engine{
		opts:      opts,
		unpack:    opts.Unpack,
		hasUnpack: opts.HasUnpack,
		queue:     make(chan rtorrent.Torrent, queueCap),
		subs:      map[int]func(Notice){},
	}
}

// SetMover installs the PAR-2.2 move engine (wired after the API server is
// constructed).
func (e *Engine) SetMover(m Mover) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mover = m
}

// SetUnpack installs the PAR-3.4 unpack handoff (wired after the unpack
// service is constructed). has reports whether any unpack rule exists.
func (e *Engine) SetUnpack(fn func(hash string), has func() bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unpack = fn
	e.hasUnpack = has
}

// Subscribe registers a callback for every rule outcome (toast fan-out).
// Returns the unsubscribe function.
func (e *Engine) Subscribe(fn func(Notice)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.nextSub
	e.nextSub++
	e.subs[id] = fn
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.subs, id)
	}
}

// Enqueue hands a torrent that just flipped to complete to the worker.
// Non-blocking: the caller runs inside the poller's critical section.
func (e *Engine) Enqueue(t rtorrent.Torrent) {
	select {
	case e.queue <- t:
	default:
		e.opts.Log.Warn("automation: queue full, dropping completed torrent", "hash", t.Hash, "name", t.Name)
	}
}

// Run drains the queue until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-e.queue:
			e.process(ctx, t)
		}
	}
}

// process runs the first matching rule once for the torrent, then hands the
// hash to the unpack service (PAR-3.4) when unpack rules exist. The unpack
// handoff runs after rule actions so moves are already applied; the unpack
// service resolves paths from a fresh snapshot.
func (e *Engine) process(ctx context.Context, t rtorrent.Torrent) {
	rules := []config.CompletionRule{}
	if e.opts.Rules != nil {
		rules = e.opts.Rules()
	}
	unpackRules := false
	if has := e.currentHasUnpack(); has != nil {
		unpackRules = has()
	}
	if len(rules) == 0 && !unpackRules {
		return
	}
	if e.opts.Marker != nil && e.opts.Marker.Seen(t.Hash) {
		return
	}

	// Mark regardless of match once rules exist, so a rule added later does
	// not retroactively grab already-completed torrents.
	markErr := error(nil)
	if e.opts.Marker != nil {
		markErr = e.opts.Marker.Mark(t.Hash, time.Now())
	}
	if markErr != nil {
		e.opts.Log.Warn("automation: persisting completion marker failed", "hash", t.Hash, "err", markErr)
	}

	if len(rules) > 0 {
		e.runRule(ctx, t, rules)
	}

	if unpackRules {
		if fn := e.currentUnpack(); fn != nil {
			fn(t.Hash)
		}
	}
}

// runRule runs the first matching on-complete rule for the torrent.
func (e *Engine) runRule(ctx context.Context, t rtorrent.Torrent, rules []config.CompletionRule) {
	idx, rule, ok := MatchFirst(rules, t)
	if !ok {
		return
	}
	e.opts.Log.Info("automation: completion rule matched", "rule", rule.Name, "index", idx, "hash", t.Hash, "name", t.Name)
	cause := e.opts.History.Begin(t.Hash, history.Entry{Kind: history.KindAction, Actor: "automation", Action: "completion_rule", Name: t.Name, Before: history.TorrentValues(t), After: map[string]string{"rule": rule.Name, "setLabel": rule.SetLabel, "moveRequested": fmt.Sprint(rule.MoveTo != ""), "trackerRequested": fmt.Sprint(rule.AddTracker != "" && !t.IsPrivate), "webhookRequested": fmt.Sprint(rule.Webhook != "")}, Message: "Matched completion rule; effects are requests, not observed daemon state."})
	record := func(action, result, message string) { e.record(t, rule.Name, action, result, message, cause) }

	failed := ""
	fail := func(action string, err error) {
		if failed == "" {
			failed = fmt.Sprintf("%s: %v", action, err)
		}
		record(action, "failed", err.Error())
	}

	if rule.SetLabel != "" {
		if err := e.opts.Daemon.SetLabel(ctx, t.Hash, rule.SetLabel); err != nil {
			fail("set_label", err)
		} else {
			record("set_label", "ok", "label set to "+rule.SetLabel)
		}
	}
	if rule.AddTracker != "" && !t.IsPrivate {
		if err := e.opts.Daemon.AddTracker(ctx, t.Hash, rule.AddTracker, 0); err != nil {
			fail("add_tracker", err)
		} else {
			record("add_tracker", "ok", "tracker added: "+rule.AddTracker)
		}
	}
	if rule.MoveTo != "" {
		mover := e.currentMover()
		if mover == nil {
			fail("move_to", fmt.Errorf("move engine unavailable"))
		} else if err := mover.MoveForAutomation(ctx, t.Hash, rule.MoveTo); err != nil {
			fail("move_to", err)
		} else {
			record("move", "ok", "data moved to "+rule.MoveTo)
		}
	}
	if rule.Webhook != "" {
		if err := postWebhook(ctx, rule.Webhook, rule.Name, t); err != nil {
			fail("webhook", err)
		} else {
			record("webhook", "ok", "payload delivered to "+rule.Webhook)
		}
	}

	notice := Notice{Hash: t.Hash, Torrent: t.Name, Rule: rule.Name, Kind: "completed"}
	if failed != "" {
		notice = Notice{Hash: t.Hash, Torrent: t.Name, Rule: rule.Name, Kind: "failed", Message: failed}
	}
	e.emit(notice)
}

// record writes one history entry and logs the outcome.
func (e *Engine) record(t rtorrent.Torrent, rule, action, result, message, cause string) {
	if e.opts.History != nil {
		e.opts.History.Add(t.Hash, history.Entry{
			CauseID: cause, Phase: "rpc_result",
			Kind:    history.KindAction,
			Actor:   "automation",
			Action:  action,
			Result:  result,
			Message: fmt.Sprintf("rule %q: %s", rule, message),
			Name:    t.Name,
		})
	}
	if result == "ok" {
		e.opts.Log.Info("automation: action ok", "rule", rule, "action", action, "hash", t.Hash)
	} else {
		e.opts.Log.Warn("automation: action failed", "rule", rule, "action", action, "hash", t.Hash, "err", message)
	}
}

func (e *Engine) currentMover() Mover {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mover
}

func (e *Engine) currentUnpack() func(hash string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.unpack
}

func (e *Engine) currentHasUnpack() func() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hasUnpack
}

func (e *Engine) emit(n Notice) {
	if n.At.IsZero() {
		n.At = time.Now()
	}
	e.mu.Lock()
	subs := make([]func(Notice), 0, len(e.subs))
	for _, fn := range e.subs {
		subs = append(subs, fn)
	}
	e.mu.Unlock()
	for _, fn := range subs {
		fn(n)
	}
}

// MatchFirst returns the first rule in order whose conditions all pass.
func MatchFirst(rules []config.CompletionRule, t rtorrent.Torrent) (int, config.CompletionRule, bool) {
	for i := range rules {
		ok, err := Match(rules[i], t)
		if err != nil {
			// Invalid rules (bad regex) are rejected by config.Validate; skip
			// rather than abort the whole evaluation.
			continue
		}
		if ok {
			return i, rules[i], true
		}
	}
	return 0, config.CompletionRule{}, false
}

// Match reports whether every non-empty condition of the rule passes.
func Match(rule config.CompletionRule, t rtorrent.Torrent) (bool, error) {
	if rule.Label != "" && !strings.EqualFold(t.Label, rule.Label) {
		return false, nil
	}
	if rule.Tracker != "" && !strings.Contains(strings.ToLower(t.TrackerHost), strings.ToLower(rule.Tracker)) {
		return false, nil
	}
	if rule.NameRegex != "" {
		re, err := regexp.Compile(rule.NameRegex)
		if err != nil {
			return false, fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		if !re.MatchString(t.Name) {
			return false, nil
		}
	}
	if rule.MinSize > 0 && t.SizeBytes < rule.MinSize {
		return false, nil
	}
	if rule.MaxSize > 0 && t.SizeBytes > rule.MaxSize {
		return false, nil
	}
	if rule.Private != nil && t.IsPrivate != *rule.Private {
		return false, nil
	}
	return true, nil
}

// webhookPayload is the JSON body POSTed by the webhook action.
type webhookPayload struct {
	Rule        string `json:"rule"`
	Hash        string `json:"hash"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"sizeBytes"`
	Label       string `json:"label"`
	TrackerHost string `json:"trackerHost"`
	Private     bool   `json:"private"`
	At          string `json:"at"`
}

func postWebhook(ctx context.Context, url, rule string, t rtorrent.Torrent) error {
	body, err := json.Marshal(webhookPayload{
		Rule:        rule,
		Hash:        t.Hash,
		Name:        t.Name,
		SizeBytes:   t.SizeBytes,
		Label:       t.Label,
		TrackerHost: t.TrackerHost,
		Private:     t.IsPrivate,
		At:          time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
