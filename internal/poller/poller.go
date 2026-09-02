// Package poller maintains the normalized in-memory session model: it polls
// rtorrent on an interval, computes per-cycle deltas for WebSocket clients,
// keeps a rolling rate history for the graphs, and tracks connection health
// with capped exponential backoff.
package poller

import (
	"context"
	"sort"
	"sync"
	"time"

	"blackbird/internal/rtorrent"
)

// ConnStatus is the rTorrent connection health shown in the status bar.
type ConnStatus string

const (
	StatusConnected    ConnStatus = "connected"
	StatusDisconnected ConnStatus = "disconnected"
)

// Client is the slice of the rtorrent client the poller needs.
type Client interface {
	ListTorrents(ctx context.Context) ([]rtorrent.Torrent, error)
	GlobalStats(ctx context.Context) (rtorrent.GlobalStats, error)
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
	Status   map[rtorrent.State]int `json:"status"`
	Labels   map[string]int         `json:"labels"`
	Trackers map[string]int         `json:"trackers"`
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
	Added         []rtorrent.Torrent `json:"added,omitempty"`
	Changed       []rtorrent.Torrent `json:"changed,omitempty"`
	Removed       []string           `json:"removed,omitempty"`
	GlobalChanged bool               `json:"globalChanged,omitempty"`
	// Global carries the current global stats on every successful poll, so
	// WebSocket clients can tick rates/counters and history without a full
	// refetch. Nil only on status-transition deltas (e.g. disconnect).
	Global *rtorrent.GlobalStats `json:"global,omitempty"`
	// Status is set only on connected/disconnected transitions.
	Status ConnStatus `json:"status,omitempty"`
	At     time.Time  `json:"at"`
}

// Options configures the poller.
type Options struct {
	Interval       time.Duration    // full torrent list poll (default 2s)
	DetailInterval time.Duration    // focused-hash detail refresh (default 1s)
	VolumeInterval time.Duration    // statfs refresh (default 30s)
	Volumes        []string         // configured mount paths
	BackoffBase    time.Duration    // reconnect backoff start (default 500ms)
	BackoffCap     time.Duration    // reconnect backoff cap (default 30s)
	Now            func() time.Time // overridable clock for tests
	// OnConnect runs once per successful (re)connection, before the first
	// snapshot is published — where declared YAML tuning is applied to the
	// daemon.
	OnConnect func(ctx context.Context)
}

// Poller is the session cache. Run drives it; the accessors are safe for
// concurrent use.
type Poller struct {
	client Client
	opts   Options

	mu         sync.RWMutex
	snapshot   Snapshot
	history    []Sample
	detail     map[string]rtorrent.Detail
	focusRefs  map[string]int
	lastDetail time.Time
	lastVolume time.Time
	subs       map[int]func(Delta)
	nextSubID  int

	backoff time.Duration
}

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
	if o.Now == nil {
		o.Now = time.Now
	}
}

// New builds a poller for the client.
func New(client Client, opts Options) *Poller {
	opts.setDefaults()
	return &Poller{
		client:    client,
		opts:      opts,
		detail:    map[string]rtorrent.Detail{},
		focusRefs: map[string]int{},
		subs:      map[int]func(Delta){},
		snapshot:  Snapshot{Status: StatusDisconnected},
	}
}

// Run drives the poll loop until ctx is cancelled. On failure it retries
// with capped exponential backoff instead of waiting the full interval; on
// recovery it reconnects automatically and refreshes the snapshot.
func (p *Poller) Run(ctx context.Context) {
	interval := p.opts.Interval
	for {
		err := p.pollOnce(ctx)
		now := p.now()
		wait := interval
		if err != nil {
			if p.backoff == 0 {
				p.backoff = p.opts.BackoffBase
			} else {
				p.backoff *= 2
			}
			if p.backoff > p.opts.BackoffCap {
				p.backoff = p.opts.BackoffCap
			}
			wait = p.backoff
			_ = now
		} else {
			p.backoff = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (p *Poller) now() time.Time { return p.opts.Now() }

func (p *Poller) pollOnce(ctx context.Context) error {
	torrents, listErr := p.client.ListTorrents(ctx)
	g, gErr := p.client.GlobalStats(ctx)
	if listErr != nil {
		p.onDisconnect(listErr)
		return listErr
	}
	if gErr != nil {
		p.onDisconnect(gErr)
		return gErr
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

	now := p.now()
	p.mu.Lock()
	prev := indexByHash(p.snapshot.Torrents)
	next := indexByHash(torrents)

	statusChanged := p.snapshot.Status != StatusConnected
	delta := computeDelta(prev, next, now)
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
	p.snapshot = Snapshot{
		GeneratedAt:    now,
		Torrents:       torrents,
		Global:         g,
		Aggregates:     agg,
		Volumes:        p.snapshot.Volumes,
		Status:         StatusConnected,
		LastError:      "",
		Stale:          false,
		ConnectedSince: orFirst(now, p.snapshot.ConnectedSince),
	}
	if p.snapshot.ConnectedSince.IsZero() {
		p.snapshot.ConnectedSince = now
	}
	if now.Sub(p.lastVolume) >= p.opts.VolumeInterval || len(p.snapshot.Volumes) == 0 {
		p.snapshot.Volumes = statVolumes(p.opts.Volumes)
		p.lastVolume = now
	}
	p.history = appendSample(p.history, Sample{At: now, DownRate: g.DownRate, UpRate: g.UpRate}, now)
	p.refreshDetailsLocked(ctx, now)
	subs := make([]func(Delta), 0, len(p.subs))
	for _, fn := range p.subs {
		subs = append(subs, fn)
	}
	p.mu.Unlock()

	for _, fn := range subs {
		fn(delta)
	}
	return nil
}

func (p *Poller) onDisconnect(err error) {
	p.mu.Lock()
	statusChanged := p.snapshot.Status != StatusDisconnected
	p.snapshot.Status = StatusDisconnected
	p.snapshot.Stale = true
	p.snapshot.LastError = err.Error()
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

// Snapshot returns the latest session snapshot (the last good data on
// disconnect, flagged stale).
func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := p.snapshot
	s.Torrents = append([]rtorrent.Torrent(nil), p.snapshot.Torrents...)
	s.Volumes = append([]Volume(nil), p.snapshot.Volumes...)
	s.Aggregates.Status = copyCountsState(p.snapshot.Aggregates.Status)
	s.Aggregates.Labels = copyCountsStr(p.snapshot.Aggregates.Labels)
	s.Aggregates.Trackers = copyCountsStr(p.snapshot.Aggregates.Trackers)
	return s
}

// History returns rate samples within the window (max 60 minutes).
func (p *Poller) History() []Sample {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Sample(nil), p.history...)
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
// detail interval. Refcounted so overlapping subscribers coexist.
func (p *Poller) Focus(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.focusRefs[hash]++
}

// Unfocus releases one Focus reference.
func (p *Poller) Unfocus(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n, ok := p.focusRefs[hash]; ok {
		if n <= 1 {
			delete(p.focusRefs, hash)
			delete(p.detail, hash)
		} else {
			p.focusRefs[hash] = n - 1
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

// refreshDetailsLocked re-fetches detail for focused hashes on the detail
// interval. Caller holds p.mu.
func (p *Poller) refreshDetailsLocked(ctx context.Context, now time.Time) {
	if len(p.focusRefs) == 0 {
		return
	}
	if now.Sub(p.lastDetail) < p.opts.DetailInterval {
		return
	}
	p.lastDetail = now
	for hash := range p.focusRefs {
		d, err := p.client.FetchDetail(ctx, hash)
		if err == nil {
			p.detail[hash] = d
		}
	}
}

// ---- pure helpers (unit-tested directly) ----

func indexByHash(ts []rtorrent.Torrent) map[string]rtorrent.Torrent {
	m := make(map[string]rtorrent.Torrent, len(ts))
	for _, t := range ts {
		m[t.Hash] = t
	}
	return m
}

// computeDelta diffs two normalized torrent sets by hash. A torrent counts
// as changed when any compared field differs.
func computeDelta(prev, next map[string]rtorrent.Torrent, at time.Time) Delta {
	var d Delta
	d.At = at
	for hash, t := range next {
		old, ok := prev[hash]
		if !ok {
			d.Added = append(d.Added, t)
			continue
		}
		if torrentChanged(old, t) {
			d.Changed = append(d.Changed, t)
		}
	}
	for hash := range prev {
		if _, ok := next[hash]; !ok {
			d.Removed = append(d.Removed, hash)
		}
	}
	return d
}

func torrentChanged(a, b rtorrent.Torrent) bool {
	// Torrent contains only comparable scalar/time fields. Comparing the
	// complete value keeps every shipped list field (including timestamps and
	// privacy state) in delta detection as the catalogue grows.
	return a != b
}

func computeAggregates(ts []rtorrent.Torrent) Aggregates {
	agg := Aggregates{
		Status:   map[rtorrent.State]int{},
		Labels:   map[string]int{},
		Trackers: map[string]int{},
	}
	for _, t := range ts {
		agg.Status[t.State]++
		if t.Label == "" {
			agg.Labels[""]++ // unlabeled bucket
		} else {
			agg.Labels[t.Label]++
		}
		if t.TrackerHost != "" {
			agg.Trackers[t.TrackerHost]++
		}
	}
	return agg
}

func copyCountsState(m map[rtorrent.State]int) map[rtorrent.State]int {
	out := make(map[rtorrent.State]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyCountsStr(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
