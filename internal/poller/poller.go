// Package poller maintains the normalized in-memory session model: it polls
// rtorrent on an interval, computes per-cycle deltas for WebSocket clients,
// keeps a rolling rate history for the graphs, and tracks connection health
// with capped exponential backoff.
package poller

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
	"blackbird/internal/seeding"
)

// ConnStatus is the rTorrent connection health shown in the status bar.
type ConnStatus string

const (
	StatusConnected    ConnStatus = "connected"
	StatusDisconnected ConnStatus = "disconnected"
)

// Client is the slice of the rtorrent client the poller needs.
//
// Ownership rule: rows and detail values returned for a cycle must not be
// mutated by the client afterwards. The poller indexes rows by pointer and
// publishes the slices to Snapshot readers, so reuse-or-mutate of a
// returned backing array corrupts both the next diff and live readers —
// always return fresh (or provably stable) storage per call.
type Client interface {
	// ListAndGlobals fetches the list rows and the global stats in one
	// system.multicall (PERF-6.3): a single SCGI round trip per poll cycle.
	ListAndGlobals(ctx context.Context) ([]rtorrent.Torrent, rtorrent.GlobalStats, error)
	FetchDetail(ctx context.Context, hash string) (rtorrent.Detail, error)
}

// Volume is one configured mount's statfs snapshot.
type Volume struct {
	Path       string `json:"path"`
	TotalBytes uint64 `json:"totalBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
}

// UsedPercent returns how full the volume is (0–100).
func (v Volume) UsedPercent() float64 {
	if v.TotalBytes == 0 {
		return 0
	}
	return float64(v.TotalBytes-v.FreeBytes) / float64(v.TotalBytes) * 100
}

// Aggregates are the sidebar filter counts, computed per cycle.
type Aggregates struct {
	Status    map[rtorrent.State]int `json:"status"`
	Labels    map[string]int         `json:"labels"`
	Trackers  map[string]int         `json:"trackers"`
	Throttles map[string]int         `json:"throttles"`
}

// Snapshot is the full session state handed to consumers.
type Snapshot struct {
	GeneratedAt    time.Time            `json:"generatedAt"`
	Torrents       []rtorrent.Torrent   `json:"torrents"` // sorted by hash for stable diffing
	Global         rtorrent.GlobalStats `json:"global"`
	Aggregates     Aggregates           `json:"aggregates"`
	Volumes        []Volume             `json:"volumes"`
	Status         ConnStatus           `json:"status"`
	LastError      string               `json:"lastError,omitempty"`
	Stale          bool                 `json:"stale"` // true after a failed poll; data is the last good set
	ConnectedSince time.Time            `json:"connectedSince"`
}

// Delta is one poll cycle's change set for the WebSocket hub.
type Delta struct {
	Added   []rtorrent.Torrent `json:"added,omitempty"`
	Changed []rtorrent.Torrent `json:"changed,omitempty"`
	// ChangedPatches carries the same changed rows as field-level patches
	// ({hash, fields}) for v2 WebSocket clients (PERF-6.2): steady-state
	// ticks mostly touch rates/counts, so patches are far smaller than whole
	// objects. Changed (whole) is kept during the v1→v2 transition.
	ChangedPatches []TorrentPatch `json:"changedPatches,omitempty"`
	Removed        []string       `json:"removed,omitempty"`
	GlobalChanged  bool           `json:"globalChanged,omitempty"`
	// Global carries the current global stats on every successful poll, so
	// WebSocket clients can tick rates/counters and history without a full
	// refetch. Nil only on status-transition deltas (e.g. disconnect).
	Global *rtorrent.GlobalStats `json:"global,omitempty"`
	// Status is set only on connected/disconnected transitions.
	Status ConnStatus `json:"status,omitempty"`
	// Aggregates is included with every successful delta so the sidebar and
	// table use the same server-defined category membership.
	Aggregates *Aggregates `json:"aggregates,omitempty"`
	At         time.Time   `json:"at"`
}

// TorrentPatch is one changed row as a field-level patch: the torrent hash
// plus only the fields that differ, keyed by their Torrent JSON names so the
// client merges them onto its stored row with Object.assign.
type TorrentPatch struct {
	Hash   string         `json:"hash"`
	Fields map[string]any `json:"fields"`
}

// StringMapPatch is the updated/removed-key diff of one dynamic aggregates
// map (labels, trackers, throttles). Status counts use a fixed key set, so
// they travel as a whole map instead.
type StringMapPatch struct {
	Updated map[string]int `json:"updated,omitempty"`
	Removed []string       `json:"removed,omitempty"`
}

// AggregatesPatch is the field-level diff of two Aggregates values for v2
// clients: the full status map when any status count changed, per-map
// updated/removed keys otherwise. Nil means nothing changed.
type AggregatesPatch struct {
	Status    map[rtorrent.State]int `json:"status,omitempty"`
	Labels    *StringMapPatch        `json:"labels,omitempty"`
	Trackers  *StringMapPatch        `json:"trackers,omitempty"`
	Throttles *StringMapPatch        `json:"throttles,omitempty"`
}

// Empty reports whether the patch carries no changes.
func (p *AggregatesPatch) Empty() bool {
	return p == nil || (len(p.Status) == 0 && p.Labels == nil && p.Trackers == nil && p.Throttles == nil)
}

// DiffAggregates returns the patch turning old into new, or nil when equal.
// A nil new means nothing to send (nil); a nil old with a non-nil new sends
// the full maps.
func DiffAggregates(old, new *Aggregates) *AggregatesPatch {
	if new == nil {
		return nil
	}
	if old == nil {
		return &AggregatesPatch{
			Status:    new.Status,
			Labels:    fullMapPatch(new.Labels),
			Trackers:  fullMapPatch(new.Trackers),
			Throttles: fullMapPatch(new.Throttles),
		}
	}
	var out AggregatesPatch
	changed := false
	if !equalStateCounts(old.Status, new.Status) {
		out.Status = new.Status
		changed = true
	}
	if p := diffStringMap(old.Labels, new.Labels); p != nil {
		out.Labels = p
		changed = true
	}
	if p := diffStringMap(old.Trackers, new.Trackers); p != nil {
		out.Trackers = p
		changed = true
	}
	if p := diffStringMap(old.Throttles, new.Throttles); p != nil {
		out.Throttles = p
		changed = true
	}
	if !changed {
		return nil
	}
	return &out
}

func equalStateCounts(a, b map[rtorrent.State]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func fullMapPatch(m map[string]int) *StringMapPatch {
	if len(m) == 0 {
		return nil
	}
	updated := make(map[string]int, len(m))
	for k, v := range m {
		updated[k] = v
	}
	return &StringMapPatch{Updated: updated}
}

func diffStringMap(old, new map[string]int) *StringMapPatch {
	var out StringMapPatch
	changed := false
	for k, v := range new {
		if old[k] != v {
			if out.Updated == nil {
				out.Updated = map[string]int{}
			}
			out.Updated[k] = v
			changed = true
		}
	}
	for k := range old {
		if _, ok := new[k]; !ok {
			out.Removed = append(out.Removed, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	sort.Strings(out.Removed)
	return &out
}

// Options configures the poller.
type Options struct {
	Interval       time.Duration // full torrent list poll (default 2s)
	DetailInterval time.Duration // focused-hash detail refresh (default 1s)
	VolumeInterval time.Duration // statfs refresh (default 30s)
	Volumes        []string      // configured mount paths
	BackoffBase    time.Duration // reconnect backoff start (default 500ms)
	BackoffCap     time.Duration // reconnect backoff cap (default 30s)
	// MaxInterval caps the adaptive idle stretch: with no visible client the
	// poll backs off toward this (default 30s); the first active client
	// snaps back to Interval (PERF-6.3). Changed live via SetMaxInterval.
	MaxInterval time.Duration
	// Active reports whether at least one visible client is connected; nil
	// means always active. The poller stretches toward MaxInterval while
	// inactive and snaps back on the first active cycle (PERF-6.3).
	Active func() bool
	Now    func() time.Time // overridable clock for tests
	// OnConnect runs once per successful (re)connection, before the first
	// snapshot is published — where declared YAML tuning is applied to the
	// daemon.
	OnConnect func(ctx context.Context)
	// OnTorrentMessage is invoked when a torrent's d.message transitions from
	// empty to non-empty (a new daemon error/notice). The API layer uses it to
	// feed the Logger tab's message history.
	OnTorrentMessage func(hash, message string)
	// OnTorrentComplete is invoked when a torrent's d.complete transitions
	// from false to true between polls (PAR-3.2 completion rules). It fires
	// while the poller lock is held, so the callback must not block; hand the
	// torrent to a bounded queue or goroutine.
	OnTorrentComplete func(hash string, t rtorrent.Torrent)
	// OnGlobalStats is invoked once per successful poll with the fresh global
	// counters (PAR-5.2 traffic accounting). It fires after the poller lock
	// is released, so the callback must be quick and non-blocking; the
	// traffic tracker Feed is ~O(1).
	OnGlobalStats func(g rtorrent.GlobalStats, at time.Time)
	// SeedingGroups returns the live ratio-group policies (PAR-4.2); nil or
	// empty disables enforcement. Read once per poll cycle, outside the
	// poller lock.
	SeedingGroups func() []config.SeedingGroup
	// SeedingSlot returns the custom slot holding group assignment.
	SeedingSlot func() string
	// SeedingMarker persists fired (torrent, group) pairs so rules act at
	// most once per pair across restarts.
	SeedingMarker *seeding.Marker
	// OnSeedingTrigger enqueues a triggered group action for the worker. It
	// fires while the poller lock is held, so it must not block.
	OnSeedingTrigger func(job seeding.Job) bool
}

// Poller is the session cache. Run drives it; the accessors are safe for
// concurrent use.
type Poller struct {
	client Client
	opts   Options

	mu         sync.RWMutex
	snapshot   *Snapshot // published cycle; replaced wholesale, never mutated
	history    historyRing
	detail     map[string]rtorrent.Detail
	focusRefs  map[string]int
	speed      *speedRings
	lastVolume time.Time
	subs       map[int]func(Delta)
	nextSubID  int

	// Cycle scratch buffers reused across polls (PERF-6.4): the two hash
	// indexes alternate (prev holds the last published rows, scratch is
	// cleared and refilled), and the delta slices reset in place. None of
	// these escape the cycle except by value copy — the hub merges delta
	// rows into its own maps synchronously, so reuse is safe. Subscribers
	// that retain Delta slices must copy them.
	prevIndex    map[string]*rtorrent.Torrent
	scratchIndex map[string]*rtorrent.Torrent
	deltaAdded   []rtorrent.Torrent
	deltaChanged []rtorrent.Torrent
	deltaRemoved []string

	// detailState tracks per-focused-hash refresh pacing for change-driven
	// detail (PERF-6.3). Entries are created on first schedule and dropped
	// on unfocus.
	detailState map[string]*detailRefreshState

	backoff time.Duration
	// idleWait is the current adaptive poll wait while no visible client is
	// connected; reset to Interval on activity or error (PERF-6.3).
	idleWait time.Duration
	// maxIntervalNanos is the live idle-stretch cap (SetMaxInterval lets
	// SIGHUP apply it without a restart).
	maxIntervalNanos atomic.Int64
}

// detailRefreshState paces one focused hash: consecutive unchanged payloads
// stretch the refresh by powers of two (up to maxDetailCalmShift), and any
// change snaps back to the base detail interval.
type detailRefreshState struct {
	lastFetch time.Time
	fetchedAt time.Time
	lastHash  uint64
	calm      int
}

// maxDetailCalmShift caps the change-driven detail stretch at 8x the base
// detail interval: quiet torrents cost 1/8th the SCGI, busy ones stay live.
const maxDetailCalmShift = 3

// DefaultPollMaxInterval caps the adaptive idle stretch when unconfigured.
const DefaultPollMaxInterval = 30 * time.Second

func (o *Options) setDefaults() {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	if o.DetailInterval <= 0 {
		o.DetailInterval = time.Second
	}
	if o.VolumeInterval <= 0 {
		o.VolumeInterval = 30 * time.Second
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = 500 * time.Millisecond
	}
	if o.BackoffCap <= 0 {
		o.BackoffCap = 30 * time.Second
	}
	if o.MaxInterval <= 0 {
		o.MaxInterval = DefaultPollMaxInterval
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// New builds a poller for the client.
func New(client Client, opts Options) *Poller {
	opts.setDefaults()
	p := &Poller{
		client:       client,
		opts:         opts,
		history:      newHistoryRing(),
		detail:       map[string]rtorrent.Detail{},
		focusRefs:    map[string]int{},
		speed:        newSpeedRings(),
		detailState:  map[string]*detailRefreshState{},
		prevIndex:    map[string]*rtorrent.Torrent{},
		scratchIndex: map[string]*rtorrent.Torrent{},
		subs:         map[int]func(Delta){},
		snapshot:     &Snapshot{Status: StatusDisconnected},
	}
	p.maxIntervalNanos.Store(int64(opts.MaxInterval))
	return p
}

// SetMaxInterval applies a new idle-stretch cap without a restart (SIGHUP).
func (p *Poller) SetMaxInterval(d time.Duration) {
	if d <= 0 {
		d = DefaultPollMaxInterval
	}
	p.maxIntervalNanos.Store(int64(d))
}

// Run drives the poll loop until ctx is cancelled. On failure it retries
// with capped exponential backoff instead of waiting the full interval; on
// recovery it reconnects automatically and refreshes the snapshot. While no
// visible client is connected the wait stretches toward the max interval
// and snaps back on the first active client (PERF-6.3).
func (p *Poller) Run(ctx context.Context) {
	for {
		err := p.pollOnce(ctx)
		wait := p.nextWait(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// nextWait computes the wait after a cycle: failures back off (resetting
// the idle stretch), active clients poll at Interval, and idle cycles
// double toward the live max cap.
func (p *Poller) nextWait(pollErr error) time.Duration {
	interval := p.opts.Interval
	if pollErr != nil {
		if p.backoff == 0 {
			p.backoff = p.opts.BackoffBase
		} else {
			p.backoff *= 2
		}
		if p.backoff > p.opts.BackoffCap {
			p.backoff = p.opts.BackoffCap
		}
		p.idleWait = 0
		return p.backoff
	}
	p.backoff = 0
	if p.opts.Active == nil || p.opts.Active() {
		p.idleWait = 0
		return interval
	}
	maxInterval := time.Duration(p.maxIntervalNanos.Load())
	if maxInterval <= 0 {
		maxInterval = DefaultPollMaxInterval
	}
	if p.idleWait <= 0 {
		p.idleWait = interval
	}
	p.idleWait *= 2
	if p.idleWait > maxInterval {
		p.idleWait = maxInterval
	}
	return p.idleWait
}

func (p *Poller) now() time.Time { return p.opts.Now() }

func (p *Poller) pollOnce(ctx context.Context) error {
	// One SCGI round trip per cycle: the list rows and the globals arrive
	// in a single system.multicall (PERF-6.3). Any failure (including the
	// typed size/timeout errors) disconnects and backs off.
	torrents, g, err := p.client.ListAndGlobals(ctx)
	if err != nil {
		p.onDisconnect(err)
		return err
	}

	// First successful poll after (re)connect: apply declared tuning before
	// the new snapshot is published.
	p.mu.RLock()
	wasConnected := p.snapshot.Status == StatusConnected
	p.mu.RUnlock()
	if !wasConnected && p.opts.OnConnect != nil {
		p.opts.OnConnect(ctx)
	}

	sort.Slice(torrents, func(i, j int) bool { return torrents[i].Hash < torrents[j].Hash })

	// Seeding policy (PAR-4.2) reads live config outside the poller lock;
	// evaluation itself runs under the lock below.
	var seedGroups []config.SeedingGroup
	seedSlot := config.DefaultSeedingSlot
	if p.opts.SeedingGroups != nil {
		seedGroups = p.opts.SeedingGroups()
	}
	if p.opts.SeedingSlot != nil {
		if slot := p.opts.SeedingSlot(); slot != "" {
			seedSlot = slot
		}
	}
	// The Ratio group column and enforcement both follow the configured
	// custom slot (custom1 is the label and never a group slot). The rows are
	// copied first: ListTorrents callers may reuse backing storage, and the
	// snapshot owns its slice. The default slot is already resolved by
	// mapTorrent, so the common path copies nothing.
	if seedSlot != config.DefaultSeedingSlot {
		torrents = resolveRatioGroups(torrents, seedSlot)
	}

	now := p.now()
	p.mu.Lock()
	// Hash indexes reuse two alternating maps (PERF-6.4): prevIndex carries
	// the last published rows, scratchIndex is cleared and refilled, and
	// they rotate after the diff so no per-cycle map allocs remain once the
	// buckets stabilize.
	prev := p.prevIndex
	fillIndex(prev, p.snapshot.Torrents)
	next := p.scratchIndex
	fillIndex(next, torrents)

	// Transitions are only collected here; the callbacks fire after
	// p.mu is released (with onGlobal and the delta subscribers below).
	// OnTorrentComplete reaches the WebSocket hub, which locks the hub and
	// then each client, while a client holding its own lock calls
	// Snapshot() and needs p.mu. Invoking them under the lock closes that
	// cycle (p.mu -> h.mu -> c.mu -> p.mu) and wedges the whole process.
	var messageEvents []torrentMessageEvent
	if p.opts.OnTorrentMessage != nil {
		for hash, t := range next {
			old, ok := prev[hash]
			if !ok || old.Message == t.Message {
				continue
			}
			if t.Message != "" {
				messageEvents = append(messageEvents, torrentMessageEvent{hash: hash, message: t.Message})
			}
		}
	}

	var completeEvents []torrentCompleteEvent
	if p.opts.OnTorrentComplete != nil {
		for hash, t := range next {
			old, ok := prev[hash]
			if !ok || old.Complete || !t.Complete {
				continue
			}
			// Copied by value: the backing rows are reused next cycle.
			completeEvents = append(completeEvents, torrentCompleteEvent{hash: hash, torrent: *t})
		}
	}

	// Seeding enforcement (PAR-4.2): pure per-cycle evaluation over seeding
	// torrents with a group assignment. The fired-marker check-and-set is
	// atomic under the poller lock so a trigger enqueues exactly once; the
	// worker executes outside the lock.
	if len(seedGroups) > 0 && p.opts.OnSeedingTrigger != nil && p.opts.SeedingMarker != nil {
		for _, t := range torrents {
			// Complete and open torrents are subject to seeding policy,
			// regardless of UI state: a seeding torrent with a tracker
			// warning normalizes to StateError but is still seeding.
			// Stopped (closed) torrents need no action.
			if !t.Complete || !t.IsOpen || t.RatioGroup == "" {
				continue
			}
			group := seeding.FindGroup(seedGroups, t.RatioGroup)
			if group == nil {
				continue
			}
			trigger, ok := seeding.Evaluate(*group, t, now)
			if !ok {
				continue
			}
			if !p.opts.SeedingMarker.Fire(t.Hash, group.Name, now) {
				continue
			}
			accepted := p.opts.OnSeedingTrigger(seeding.Job{
				Hash: t.Hash, Name: t.Name, Group: group.Name,
				Condition: trigger.Condition, Action: group.Action, Label: group.Label,
			})
			if !accepted {
				// The worker queue was full. Roll the mark back so the
				// trigger is retried next cycle: leaving it set would
				// record the action as done forever without running it.
				p.opts.SeedingMarker.Unfire(t.Hash, group.Name)
			}
		}
	}

	statusChanged := p.snapshot.Status != StatusConnected
	delta := computeDelta(prev, next, now, p.deltaAdded, p.deltaChanged, p.deltaRemoved)
	p.deltaAdded, p.deltaChanged, p.deltaRemoved = delta.Added, delta.Changed, delta.Removed
	// Field-level patches for v2 clients ride alongside the whole rows.
	if len(delta.Changed) > 0 {
		delta.ChangedPatches = make([]TorrentPatch, 0, len(delta.Changed))
		for _, t := range delta.Changed {
			if fields := diffTorrentFields(*prev[t.Hash], t); len(fields) > 0 {
				delta.ChangedPatches = append(delta.ChangedPatches, TorrentPatch{Hash: t.Hash, Fields: fields})
			}
		}
	}
	// Rotate the indexes: this cycle's rows become the next cycle's prev.
	// prev/next (and the delta slices above) must not be retained past this
	// point except by value copy — the hub merges synchronously.
	p.prevIndex, p.scratchIndex = p.scratchIndex, p.prevIndex
	// Global always rides along on successful polls so WebSocket clients tick
	// rates/counters and their sparkline history at the server's sample
	// cadence even when nothing changed. GlobalChanged stays a cheap hint.
	gg := g
	delta.Global = &gg
	if g != p.snapshot.Global || statusChanged {
		delta.GlobalChanged = true
	}
	if statusChanged {
		delta.Status = StatusConnected
	}

	agg := computeAggregates(torrents)
	delta.Aggregates = &agg
	// Copy-on-publish (PERF-6.1): the new cycle is built from fresh slices
	// and maps, then swapped in as one pointer. Published snapshots are never
	// mutated afterwards, so Snapshot readers share them without per-caller
	// copies.
	p.snapshot = &Snapshot{
		GeneratedAt: now,
		Torrents:    torrents,
		Global:      g,
		Aggregates:  agg,
		Volumes:     p.snapshot.Volumes,
		Status:      StatusConnected,
		LastError:   "",
		Stale:       false,
		// Keep the existing timestamp; only a fresh connection starts the
		// clock. The arguments were reversed here, and since now is never
		// zero orFirst always returned it — so the status bar's uptime
		// reset to ~0 on every poll.
		ConnectedSince: orFirst(p.snapshot.ConnectedSince, now),
	}
	if now.Sub(p.lastVolume) >= p.opts.VolumeInterval || len(p.snapshot.Volumes) == 0 {
		p.snapshot.Volumes = statVolumes(p.opts.Volumes)
		p.lastVolume = now
	}
	p.history.push(Sample{At: now, DownRate: g.DownRate, UpRate: g.UpRate}, now)
	p.sampleFocusedSpeedLocked(torrents, now)
	p.pruneSpeedLocked(now)
	// Detail refresh is SCGI I/O: capture the due focused set under the lock,
	// then fetch after unlocking. Each result swaps in under its own short
	// critical section, so a stalled detail fetch never blocks Snapshot
	// readers or the next poll cycle (PERF-6.1). Scheduling is change-driven
	// (PERF-6.3): consecutive unchanged payloads stretch the per-hash
	// refresh up to 8x the base interval; any change snaps back.
	var detailHashes []string
	for hash := range p.focusRefs {
		st := p.detailState[hash]
		if st == nil {
			st = &detailRefreshState{}
			p.detailState[hash] = st
		}
		if now.Sub(st.lastFetch) >= p.opts.DetailInterval<<min(st.calm, maxDetailCalmShift) {
			st.lastFetch = now
			detailHashes = append(detailHashes, hash)
		}
	}
	subs := make([]func(Delta), 0, len(p.subs))
	for _, fn := range p.subs {
		subs = append(subs, fn)
	}
	onGlobal := p.opts.OnGlobalStats
	onMessage := p.opts.OnTorrentMessage
	onComplete := p.opts.OnTorrentComplete
	p.mu.Unlock()

	// Outside the lock: these reach the API server, the automation engine,
	// and the history log, any of which may read back from the poller.
	for _, e := range messageEvents {
		onMessage(e.hash, e.message)
	}
	for _, e := range completeEvents {
		onComplete(e.hash, e.torrent)
	}

	p.fetchDetails(ctx, detailHashes)

	if onGlobal != nil {
		onGlobal(g, now)
	}
	for _, fn := range subs {
		fn(delta)
	}
	return nil
}

// fetchDetails fetches focused-torrent detail without holding the poller
// lock and swaps each result in under a short critical section. A hash
// unfocused mid-fetch is dropped rather than resurrected. The payload hash
// feeds change-driven refresh (PERF-6.3): an unchanged payload grows the
// hash's calm count (stretching its next refresh), a changed one resets it.
func (p *Poller) fetchDetails(ctx context.Context, hashes []string) {
	for _, hash := range hashes {
		d, err := p.client.FetchDetail(ctx, hash)
		if err != nil {
			continue
		}
		h := hashDetail(d)
		p.mu.Lock()
		if _, focused := p.focusRefs[hash]; focused {
			p.detail[hash] = d
			if st, ok := p.detailState[hash]; ok {
				st.fetchedAt = p.now()
				if h == st.lastHash {
					st.calm = min(st.calm+1, maxDetailCalmShift)
				} else {
					st.calm, st.lastHash = 0, h
				}
			}
		}
		p.mu.Unlock()
	}
}

// hashDetail fingerprints a fetched detail payload for change detection.
// The bitfield hex is excluded by its json:"-" tag (PAR-2.6): piece progress
// surfaces through the files/peers counters, so a static torrent still
// reads as calm.
func hashDetail(d rtorrent.Detail) uint64 {
	raw, err := json.Marshal(d)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write(raw)
	return h.Sum64()
}

func (p *Poller) onDisconnect(err error) {
	p.mu.Lock()
	statusChanged := p.snapshot.Status != StatusDisconnected
	// Publish a modified copy rather than mutating the shared snapshot, so
	// previously returned pointers keep showing the last good cycle.
	next := *p.snapshot
	next.Status = StatusDisconnected
	next.Stale = true
	next.LastError = err.Error()
	// Clear the connection clock so the next successful cycle restarts it:
	// uptime describes the current connection, not the process.
	next.ConnectedSince = time.Time{}
	p.snapshot = &next
	subs := make([]func(Delta), 0, len(p.subs))
	for _, fn := range p.subs {
		subs = append(subs, fn)
	}
	var delta Delta
	if statusChanged {
		delta = Delta{Status: StatusDisconnected, At: p.now()}
	}
	p.mu.Unlock()
	if statusChanged {
		for _, fn := range subs {
			fn(delta)
		}
	}
}

// orFirst returns a if non-zero, else b.
func orFirst(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

// ---- accessors ----

// Snapshot returns the latest published session snapshot (the last good
// data on disconnect, flagged stale). The pointer is produced once per
// cycle and shared: no per-caller deep copy happens here, so callers must
// treat it as immutable and must not retain it past the next cycle if they
// need stability — each cycle publishes a new pointer and never mutates a
// published one.
func (p *Poller) Snapshot() *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshot
}

// History returns rate samples within the window (max 60 minutes).
func (p *Poller) History() []Sample {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.history.samples()
}

// Subscribe registers a delta callback; the returned func unsubscribes.
func (p *Poller) Subscribe(fn func(Delta)) (unsub func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.nextSubID
	p.nextSubID++
	p.subs[id] = fn
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.subs, id)
	}
}

// Focus marks a hash as focused so its detail is fetched lazily on the
// detail interval and its rate ring is sampled. Refcounted so overlapping
// subscribers coexist.
func (p *Poller) Focus(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.focusRefs[hash]++
	st := p.speed.byHash[hash]
	if st == nil {
		st = &ringState{ring: newPerTorrentRing(speedCap)}
		p.speed.byHash[hash] = st
	}
	st.unfocused = time.Time{} // focused again
}

// Unfocus releases one Focus reference.
func (p *Poller) Unfocus(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n, ok := p.focusRefs[hash]; ok {
		if n <= 1 {
			delete(p.focusRefs, hash)
			delete(p.detail, hash)
			delete(p.detailState, hash) // fresh pacing on refocus
			// Keep the speed ring for the retention window after unfocus so a
			// quick refocus keeps the graph (PAR-2.5). pruneSpeedLocked drops
			// it once the deadline passes.
			if st := p.speed.byHash[hash]; st != nil && st.unfocused.IsZero() {
				st.unfocused = p.now()
			}
		} else {
			p.focusRefs[hash] = n - 1
		}
	}
}

// SpeedHistory returns the retained rate samples for a hash within the last
// speedWindow, oldest→newest. A hash with no ring (never focused) returns nil.
func (p *Poller) SpeedHistory(hash string) []Sample {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := p.speed.byHash[hash]
	if st == nil {
		return nil
	}
	cutoff := p.now().Add(-speedWindow)
	raw := st.ring.window(cutoff)
	out := make([]Sample, len(raw))
	for i, s := range raw {
		out[i] = Sample(s)
	}
	return out
}

// sampleFocusedSpeedLocked appends a rate sample per poll cycle for every
// focused hash (from the current torrent rows, which are already in hand).
// Caller holds p.mu.
func (p *Poller) sampleFocusedSpeedLocked(torrents []rtorrent.Torrent, now time.Time) {
	if len(p.focusRefs) == 0 {
		return
	}
	for _, t := range torrents {
		if _, focused := p.focusRefs[t.Hash]; focused {
			st := p.speed.byHash[t.Hash]
			if st == nil {
				st = &ringState{ring: newPerTorrentRing(speedCap)}
				p.speed.byHash[t.Hash] = st
			}
			st.ring.push(rateSample{At: now, DownRate: t.DownRate, UpRate: t.UpRate})
		}
	}
}

// pruneSpeedLocked drops speed rings whose unfocus retention has elapsed.
// Caller holds p.mu.
func (p *Poller) pruneSpeedLocked(now time.Time) {
	for hash, st := range p.speed.byHash {
		if _, focused := p.focusRefs[hash]; focused {
			continue
		}
		if !st.unfocused.IsZero() && now.Sub(st.unfocused) >= speedRetainAfterUnfocus {
			delete(p.speed.byHash, hash)
		}
	}
}

// Detail returns the cached detail for a focused hash.
func (p *Poller) Detail(hash string) (rtorrent.Detail, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.detail[hash]
	return d, ok
}

// DetailHash returns the cached detail plus its change-detection hash
// (PERF-6.3): per-client senders skip re-sending an unchanged payload. The
// hash is computed on every fetch and stored alongside the detail.
func (p *Poller) DetailHash(hash string) (rtorrent.Detail, uint64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.detail[hash]
	if !ok {
		return rtorrent.Detail{}, 0, false
	}
	var h uint64
	if st, ok := p.detailState[hash]; ok {
		h = st.lastHash
	}
	return d, h, true
}

// ---- pure helpers (unit-tested directly) ----

// fillIndex clears m and indexes ts by hash. Clearing retains the buckets
// and pointer values allocate nothing, so a stable session stops allocating
// for indexes after warmup (PERF-6.4). The pointers alias the caller's
// slice: like the published snapshot rows, indexed rows must not be mutated
// after return (see the Client contract).
func fillIndex(m map[string]*rtorrent.Torrent, ts []rtorrent.Torrent) {
	clear(m)
	for i := range ts {
		m[ts[i].Hash] = &ts[i]
	}
}

// computeDelta diffs two normalized torrent sets by hash. A torrent counts
// as changed when any compared field differs. The added/changed/removed
// slices are scratch buffers reset in place: pass the poller's cycle
// buffers (or nil) and do not retain the returned slices past the cycle
// except by value copy.
func computeDelta(prev, next map[string]*rtorrent.Torrent, at time.Time, added, changed []rtorrent.Torrent, removed []string) Delta {
	var d Delta
	d.At = at
	added = added[:0]
	changed = changed[:0]
	removed = removed[:0]
	for hash, t := range next {
		old, ok := prev[hash]
		if !ok {
			added = append(added, *t)
			continue
		}
		if torrentChanged(*old, *t) {
			changed = append(changed, *t)
		}
	}
	for hash := range prev {
		if _, ok := next[hash]; !ok {
			removed = append(removed, hash)
		}
	}
	d.Added, d.Changed, d.Removed = added, changed, removed
	return d
}

func torrentChanged(a, b rtorrent.Torrent) bool {
	// Torrent contains only comparable scalar/time fields. Comparing the
	// complete value keeps every shipped list field (including timestamps and
	// privacy state) in delta detection as the catalogue grows.
	return a != b
}

// diffTorrentFields returns the fields that differ between two rows of the
// same torrent, keyed by their Torrent JSON names for the v2 wire format
// (PERF-6.2). Every field except the hash key is compared explicitly — no
// reflection in the hot path. A test below guards that the catalogue cannot
// grow a field without diff coverage.
func diffTorrentFields(old, new rtorrent.Torrent) map[string]any {
	fields := map[string]any{}
	if old.Name != new.Name {
		fields["name"] = new.Name
	}
	if old.SizeBytes != new.SizeBytes {
		fields["sizeBytes"] = new.SizeBytes
	}
	if old.CompletedBytes != new.CompletedBytes {
		fields["completedBytes"] = new.CompletedBytes
	}
	if old.LeftBytes != new.LeftBytes {
		fields["leftBytes"] = new.LeftBytes
	}
	if old.DownloadedBytes != new.DownloadedBytes {
		fields["downloadedBytes"] = new.DownloadedBytes
	}
	if old.UploadedBytes != new.UploadedBytes {
		fields["uploadedBytes"] = new.UploadedBytes
	}
	if old.Percent != new.Percent {
		fields["percent"] = new.Percent
	}
	if old.Complete != new.Complete {
		fields["complete"] = new.Complete
	}
	if old.IsOpen != new.IsOpen {
		fields["isOpen"] = new.IsOpen
	}
	if old.State != new.State {
		fields["state"] = new.State
	}
	if old.Message != new.Message {
		fields["message"] = new.Message
	}
	if old.CheckingPercent != new.CheckingPercent {
		fields["checkingPercent"] = new.CheckingPercent
	}
	if old.Seeds != new.Seeds {
		fields["seeds"] = new.Seeds
	}
	if old.Peers != new.Peers {
		fields["peers"] = new.Peers
	}
	if old.DownRate != new.DownRate {
		fields["downRate"] = new.DownRate
	}
	if old.UpRate != new.UpRate {
		fields["upRate"] = new.UpRate
	}
	if old.EtaSeconds != new.EtaSeconds {
		fields["etaSeconds"] = new.EtaSeconds
	}
	if old.Ratio != new.Ratio {
		fields["ratio"] = new.Ratio
	}
	if old.Label != new.Label {
		fields["label"] = new.Label
	}
	if old.Custom2 != new.Custom2 {
		fields["custom2"] = new.Custom2
	}
	if old.Custom3 != new.Custom3 {
		fields["custom3"] = new.Custom3
	}
	if old.Custom4 != new.Custom4 {
		fields["custom4"] = new.Custom4
	}
	if old.Custom5 != new.Custom5 {
		fields["custom5"] = new.Custom5
	}
	if old.RatioGroup != new.RatioGroup {
		fields["ratioGroup"] = new.RatioGroup
	}
	if old.Throttle != new.Throttle {
		fields["throttle"] = new.Throttle
	}
	if old.TiedToFile != new.TiedToFile {
		fields["tiedToFile"] = new.TiedToFile
	}
	if old.SkippedBytes != new.SkippedBytes {
		fields["skippedBytes"] = new.SkippedBytes
	}
	if old.PeersAccounted != new.PeersAccounted {
		fields["peersAccounted"] = new.PeersAccounted
	}
	if old.ChunksHashed != new.ChunksHashed {
		fields["chunksHashed"] = new.ChunksHashed
	}
	if old.IsMultiFile != new.IsMultiFile {
		fields["isMultiFile"] = new.IsMultiFile
	}
	if old.Directory != new.Directory {
		fields["directory"] = new.Directory
	}
	if old.Connection != new.Connection {
		fields["connection"] = new.Connection
	}
	if !old.AddedAt.Equal(new.AddedAt) {
		fields["addedAt"] = new.AddedAt
	}
	if !old.FinishedAt.Equal(new.FinishedAt) {
		fields["finishedAt"] = new.FinishedAt
	}
	if !old.CreationDate.Equal(new.CreationDate) {
		fields["creationDate"] = new.CreationDate
	}
	if old.TrackerHost != new.TrackerHost {
		fields["trackerHost"] = new.TrackerHost
	}
	if old.TrackerStatus != new.TrackerStatus {
		fields["trackerStatus"] = new.TrackerStatus
	}
	if old.IsPrivate != new.IsPrivate {
		fields["isPrivate"] = new.IsPrivate
	}
	if old.BasePath != new.BasePath {
		fields["basePath"] = new.BasePath
	}
	if old.Priority != new.Priority {
		fields["priority"] = new.Priority
	}
	if old.Superseeding != new.Superseeding {
		fields["superseeding"] = new.Superseeding
	}
	if old.Sequential != new.Sequential {
		fields["sequential"] = new.Sequential
	}
	return fields
}

// resolveRatioGroups returns a copy of the rows with RatioGroup resolved
// from the configured custom slot. The copy keeps snapshot ownership even
// when the list source reuses backing storage.
func resolveRatioGroups(torrents []rtorrent.Torrent, slot string) []rtorrent.Torrent {
	resolved := make([]rtorrent.Torrent, len(torrents))
	copy(resolved, torrents)
	for i := range resolved {
		resolved[i].RatioGroup = resolved[i].SlotValue(slot)
	}
	return resolved
}

func computeAggregates(ts []rtorrent.Torrent) Aggregates {
	agg := Aggregates{
		Status: map[rtorrent.State]int{
			"all": 0, "completed": 0, "active": 0, "inactive": 0,
			rtorrent.StateDownloading: 0, rtorrent.StateSeeding: 0, rtorrent.StateStopped: 0,
			rtorrent.StateQueued: 0, rtorrent.StateChecking: 0, rtorrent.StateError: 0,
		},
		Labels:    map[string]int{},
		Trackers:  map[string]int{},
		Throttles: map[string]int{},
	}
	for _, t := range ts {
		agg.Status["all"]++
		agg.Status[t.State]++
		if t.Complete {
			agg.Status["completed"]++
		}
		if t.DownRate > 0 || t.UpRate > 0 {
			agg.Status["active"]++
		}
		if t.DownRate == 0 && t.UpRate == 0 && t.IsOpen {
			agg.Status["inactive"]++
		}
		if t.Label == "" {
			agg.Labels[""]++ // unlabeled bucket
		} else {
			agg.Labels[t.Label]++
		}
		if t.TrackerHost != "" {
			agg.Trackers[t.TrackerHost]++
		}
		if t.Throttle != "" {
			agg.Throttles[t.Throttle]++
		}
	}
	return agg
}

// torrentMessageEvent and torrentCompleteEvent buffer one poll cycle's
// transitions so the callbacks can be invoked after p.mu is released.
type torrentMessageEvent struct {
	hash    string
	message string
}

type torrentCompleteEvent struct {
	hash    string
	torrent rtorrent.Torrent
}

// CachedTrackers returns only already fetched tracker fields and the successful
// cache-read time, never a scrape timestamp. It issues no RPCs.
func (p *Poller) CachedTrackers(hash string) ([]rtorrent.Tracker, time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.detail[hash]
	st := p.detailState[hash]
	if !ok || st == nil {
		return nil, time.Time{}
	}
	// Published detail slices are immutable, as with Detail and Snapshot.
	return d.Trackers, st.fetchedAt
}
