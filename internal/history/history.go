// Package history provides the bounded event log backing the per-torrent
// Logger detail tab (PAR-2.5) and the global History view (PAR-5.3). It
// records Blackbird-side actions (with the actor and outcome), torrent adds,
// moves, completions, scheduler applications, and torrent message transitions
// (d.message). Entries are kept per infohash (pruned by count and age) and in
// a global recency ring (same count/age bounds) so the History view pages
// newest-first from the same source the Logger tab reads.
package history

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind classifies a log entry for the Logger tab's filter chips and the
// History view's kind filter.
type Kind string

const (
	KindAction   Kind = "action"   // a Blackbird-side action taken by a user
	KindMessage  Kind = "message"  // a d.message state change observed by the poller
	KindAdd      Kind = "add"      // a torrent added through Blackbird
	KindMove     Kind = "move"     // a move-data job result
	KindComplete Kind = "complete" // a d.complete 0→1 transition observed by the poller
)

// DefaultMaxEvents caps the global recency ring when unconfigured.
const DefaultMaxEvents = 5000

// Entry is one immutable event for a torrent.
type Entry struct {
	ID       string            `json:"id,omitempty"`
	Phase    string            `json:"phase,omitempty"` // intent | rpc_result | observation | checkpoint | gap | configuration
	CauseID  string            `json:"causeId,omitempty"`
	Revision string            `json:"revision,omitempty"`
	Before   map[string]string `json:"before,omitempty"`
	After    map[string]string `json:"after,omitempty"`
	At       time.Time         `json:"at"`
	Kind     Kind              `json:"kind"`
	Actor    string            `json:"actor,omitempty"`  // e.g. the authenticated user, "watch", "scheduler"
	Action   string            `json:"action,omitempty"` // verb for KindAction/KindMove, e.g. "start", "move"
	Result   string            `json:"result,omitempty"` // ok | failed | completed | cancelled | info
	Message  string            `json:"message,omitempty"`
	Name     string            `json:"name,omitempty"` // torrent name at event time ("" when unknown)
}

// Event is one global-ring entry: the per-torrent Entry plus its hash and a
// stable sequence number used as the History view's pagination cursor.
type Event struct {
	Seq  uint64 `json:"seq"`
	Hash string `json:"hash"`
	Entry
}

// Options bound the log.
type Options struct {
	Recorder *Recorder
	// MaxEntriesPerTorrent caps the per-torrent ring size.
	MaxEntriesPerTorrent int
	// Retention is how long entries are kept (age prune).
	Retention time.Duration
	// MaxTorrents bounds the number of tracked torrents (memory cap).
	MaxTorrents int
	// MaxEvents caps the global recency ring backing the History view.
	MaxEvents int
	// Now overridable for tests.
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.MaxEntriesPerTorrent <= 0 {
		o.MaxEntriesPerTorrent = 200
	}
	if o.Retention <= 0 {
		o.Retention = 24 * time.Hour
	}
	if o.MaxTorrents <= 0 {
		o.MaxTorrents = 4096
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = DefaultMaxEvents
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Log is a concurrency-safe torrent event store: per-hash rings for the
// Logger tab plus a global recency ring for the History view. Both rings are
// written by the same Add call, so the two surfaces never disagree.
type Log struct {
	mu   sync.Mutex
	opts Options
	by   map[string][]Entry
	all  []Event // oldest→newest; capped at MaxEvents
	seq  uint64  // last assigned sequence number
}

// New builds an empty log.
func New(opts Options) *Log {
	opts.defaults()
	l := &Log{opts: opts, by: map[string][]Entry{}}
	if opts.Recorder != nil {
		// Restore the legacy views from durable outcomes. Observation/checkpoint
		// traffic belongs to the recorder UI, not the existing action rings.
		for _, ev := range opts.Recorder.Snapshot().Events {
			if ev.Phase == "rpc_result" || ev.Phase == "outcome" {
				l.Add(ev.Hash, ev.Entry)
			}
		}
	}
	return l
}

// Add appends an entry for a hash, pruning by count/age for that hash and
// for the global ring. An empty hash skips the per-torrent ring (daemon-wide
// events such as scheduler applications) but still lands in the global ring.
func (l *Log) Add(hash string, e Entry) {
	if e.At.IsZero() {
		e.At = l.opts.Now()
	}
	if l.opts.Recorder != nil && e.ID == "" {
		if e.Phase == "" {
			e.Phase = "outcome"
			if e.Kind == KindAction {
				e.Phase = "rpc_result"
			}
		}
		e.ID = l.opts.Recorder.Record(hash, e)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if hash != "" {
		ring := l.by[hash]
		ring = append(ring, e)
		cutoff := l.opts.Now().Add(-l.opts.Retention)
		drop := 0
		for drop < len(ring) && ring[drop].At.Before(cutoff) {
			drop++
		}
		if drop > 0 {
			ring = append(ring[:0], ring[drop:]...)
		}
		if len(ring) > l.opts.MaxEntriesPerTorrent {
			ring = ring[len(ring)-l.opts.MaxEntriesPerTorrent:]
		}
		l.by[hash] = ring
	}
	l.seq++
	l.all = append(l.all, Event{Seq: l.seq, Hash: hash, Entry: e})
	l.pruneGlobalLocked()
}

// Begin records intent without adding an extra row to the legacy history.
// A returned ID may refer to a dropped event; recorder coverage reports gaps.
func (l *Log) Begin(hash string, e Entry) string {
	if l == nil || l.opts.Recorder == nil {
		return ""
	}
	e.Phase = "intent"
	return l.opts.Recorder.Record(hash, e)
}

func (l *Log) Recorder() *Recorder {
	if l == nil {
		return nil
	}
	return l.opts.Recorder
}

// pruneGlobalLocked drops age-expired events from the front of the global
// ring and evicts the oldest beyond MaxEvents. Caller holds mu.
func (l *Log) pruneGlobalLocked() {
	cutoff := l.opts.Now().Add(-l.opts.Retention)
	drop := 0
	for drop < len(l.all) && l.all[drop].At.Before(cutoff) {
		drop++
	}
	if drop > 0 {
		l.all = append(l.all[:0], l.all[drop:]...)
	}
	if len(l.all) > l.opts.MaxEvents {
		l.all = l.all[len(l.all)-l.opts.MaxEvents:]
	}
}

// SetBounds updates the retention bounds live (Settings saves and SIGHUP
// reloads) and prunes both rings to the new limits immediately.
func (l *Log) SetBounds(maxPerTorrent int, retention time.Duration, maxEvents int) {
	if l.opts.Recorder != nil {
		l.opts.Recorder.SetRetention(retention)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if maxPerTorrent > 0 {
		l.opts.MaxEntriesPerTorrent = maxPerTorrent
		for hash, ring := range l.by {
			if len(ring) > maxPerTorrent {
				l.by[hash] = ring[len(ring)-maxPerTorrent:]
			}
		}
	}
	if retention > 0 {
		l.opts.Retention = retention
	}
	if maxEvents > 0 {
		l.opts.MaxEvents = maxEvents
	}
	l.pruneGlobalLocked()
}

// AddAction is a convenience for Blackbird-side actions.
func (l *Log) AddAction(hash, actor, action, result, message string) {
	l.Add(hash, Entry{Kind: KindAction, Actor: actor, Action: action, Result: result, Message: message})
}

// ForHash returns the entries for one torrent, newest first. Entries are
// stored oldest→newest, so reversing is stable even when timestamps tie.
func (l *Log) ForHash(hash string) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := l.by[hash]
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[len(entries)-1-i] = e
	}
	return out
}

// Filter narrows a global History query. Empty fields match everything;
// Search is a case-insensitive substring over name, hash, action, and
// message.
type Filter struct {
	Kinds  []Kind
	Actor  string
	Hash   string
	Search string
}

// QueryResult is one History page, newest first.
type QueryResult struct {
	Events []Event
	// NextBeforeSeq pages older: pass it as beforeSeq for the next page.
	// Zero when HasMore is false.
	NextBeforeSeq uint64
	HasMore       bool
}

// maxQueryLimit caps one History page so a hostile client cannot force an
// unbounded response.
const maxQueryLimit = 200

// Query pages the global ring newest-first. beforeSeq is an exclusive
// sequence cursor (0 = latest); sequence cursors stay stable under
// concurrent appends, unlike time cursors when two events share a timestamp.
func (l *Log) Query(f Filter, limit int, beforeSeq uint64) QueryResult {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxQueryLimit {
		limit = maxQueryLimit
	}
	search := strings.ToLower(f.Search)
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Event
	for i := len(l.all) - 1; i >= 0 && len(out) <= limit; i-- {
		ev := l.all[i]
		if beforeSeq != 0 && ev.Seq >= beforeSeq {
			continue
		}
		if len(f.Kinds) > 0 {
			match := false
			for _, k := range f.Kinds {
				if ev.Kind == k {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		if f.Actor != "" && !strings.EqualFold(ev.Actor, f.Actor) {
			continue
		}
		if f.Hash != "" && ev.Hash != f.Hash {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(ev.Name), search) &&
			!strings.Contains(strings.ToLower(ev.Hash), search) &&
			!strings.Contains(strings.ToLower(string(ev.Action)), search) &&
			!strings.Contains(strings.ToLower(ev.Message), search) {
			continue
		}
		out = append(out, ev)
	}
	res := QueryResult{}
	if len(out) > limit {
		res.HasMore = true
		out = out[:limit]
		res.NextBeforeSeq = out[len(out)-1].Seq
	}
	res.Events = out
	return res
}

// PruneTorrent ages out entries for hashes no longer in the session and caps
// the total number of tracked hashes. Call periodically (e.g. each poll).
func (l *Log) PruneTorrent(active map[string]bool, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.opts.Retention)
	for hash, entries := range l.by {
		if active[hash] {
			continue // live torrents keep their bounded ring
		}
		drop := 0
		for drop < len(entries) && entries[drop].At.Before(cutoff) {
			drop++
		}
		if drop == len(entries) {
			delete(l.by, hash)
		}
	}
	if len(l.by) <= l.opts.MaxTorrents {
		return
	}
	// Cap tracked torrents deterministically (sorted hash order) when churn
	// exceeds the bound.
	hashes := make([]string, 0, len(l.by))
	for h := range l.by {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	for _, h := range hashes[:len(hashes)-l.opts.MaxTorrents] {
		delete(l.by, h)
	}
}

// Len reports the number of tracked hashes (tests/observability).
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.by)
}
