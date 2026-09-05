// Package attention groups sampled symptoms into durable operator incidents.
// Its independent worker reads published caches; it never runs on the poll loop.
package attention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"blackbird/internal/history"
	"blackbird/internal/poller"
)

const (
	Version      = 1
	MaxIncidents = 256
	MaxAffected  = 100
	MaxBytes     = 8 << 20
)

type Incident struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Title          string     `json:"title"`
	Evidence       string     `json:"evidence"`
	NextStep       string     `json:"nextStep"`
	Hashes         []string   `json:"hashes"`
	Affected       int        `json:"affected"`
	FirstSeen      time.Time  `json:"firstSeen"`
	LastSeen       time.Time  `json:"lastSeen"`
	EpisodeStarted time.Time  `json:"episodeStarted"`
	Episode        uint64     `json:"episode"`
	Active         bool       `json:"active"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	SnoozedUntil   *time.Time `json:"snoozedUntil,omitempty"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
	HealthySince   *time.Time `json:"healthySince,omitempty"`
	Status         string     `json:"status"`
	// Notice increments only when a new episode opens or a snooze expires.
	Notice uint64 `json:"notice"`
}

type State struct {
	Version        int        `json:"version"`
	Instance       string     `json:"instance"`
	Incidents      []Incident `json:"incidents"`
	LastVisit      *time.Time `json:"lastVisit"`
	ObservedAt     *time.Time `json:"observedAt"`
	Omitted        int        `json:"omitted"`
	Pruned         uint64     `json:"pruned"`
	NoticeSequence uint64     `json:"noticeSequence"`
	Error          string     `json:"error,omitempty"`
	SavedAt        *time.Time `json:"savedAt"`
	Coverage       []string   `json:"coverage"`
}

type Input struct {
	Snapshot   *poller.Snapshot
	Volumes    []string
	StaleAfter time.Duration
}
type Options struct {
	Path     string
	Source   func() Input
	History  *history.Log
	Interval time.Duration
	Now      func() time.Time
	Write    func(string, []byte) error
}

type request struct {
	ctx        context.Context
	id, action string
	episode    uint64
	duration   time.Duration
	visited    time.Time
	result     chan error
}
type Store struct {
	opts       Options
	mu         sync.RWMutex
	published  State
	state      State // owned by Run
	commands   chan request
	done       chan struct{}
	lock       *os.File
	loadFailed bool
	lastSample time.Time
	startedAt  time.Time
}

func Open(opts Options) (*Store, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.Write == nil {
		opts.Write = writeState
	}
	s := &Store{opts: opts, commands: make(chan request, 16), done: make(chan struct{}), state: State{Version: Version, Instance: identity(fmt.Sprint(time.Now().UnixNano(), opts.Path)), Incidents: []Incident{}}}
	s.startedAt = opts.Now()
	err := s.openFile()
	if err != nil {
		s.loadFailed = true
		s.state.Error = "Attention state could not be opened exclusively or loaded. Changes cannot be saved; correct storage and restart."
	}
	// A process gap cannot count toward confirmed recovery.
	for i := range s.state.Incidents {
		s.state.Incidents[i].HealthySince = nil
	}
	s.publish()
	return s, err
}

func (s *Store) openFile() error {
	if err := os.MkdirAll(filepath.Dir(s.opts.Path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.opts.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return err
	}
	s.lock = f
	entries, err := os.ReadDir(filepath.Dir(s.opts.Path))
	if err != nil {
		return err
	}
	prefix := "." + filepath.Base(s.opts.Path) + ".tmp-"
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) >= len(prefix) && entry.Name()[:len(prefix)] == prefix {
			if err := os.Remove(filepath.Join(filepath.Dir(s.opts.Path), entry.Name())); err != nil {
				return err
			}
		}
	}
	f, err = os.Open(s.opts.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > MaxBytes {
		return errors.New("attention state exceeds byte bound")
	}
	var loaded State
	decoder := json.NewDecoder(f)
	if err = decoder.Decode(&loaded); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing data in attention state")
	}
	if loaded.Version != Version || len(loaded.Incidents) > MaxIncidents {
		return errors.New("invalid attention state")
	}
	ids := map[string]bool{}
	for _, in := range loaded.Incidents {
		if in.ID == "" || ids[in.ID] || len(in.Hashes) > MaxAffected || in.Episode == 0 || in.FirstSeen.IsZero() {
			return errors.New("invalid incident")
		}
		ids[in.ID] = true
	}
	s.state = loaded
	s.state.Error = ""
	return nil
}

func clone(st State) State {
	out := st
	out.Incidents = append([]Incident{}, st.Incidents...)
	out.Coverage = append([]string{}, st.Coverage...)
	for i := range out.Incidents {
		out.Incidents[i].Hashes = append([]string{}, st.Incidents[i].Hashes...)
	}
	return out
}

func (s *Store) Snapshot() State { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.published) }
func (s *Store) publish() {
	s.state.Coverage = []string{
		"Groups describe shared symptoms, not proven root causes. Recovery requires two distinct healthy samples at least 30 seconds apart; stale or disconnected data never resolves torrent or volume incidents.",
		"Up to 256 incidents, 100 listed hashes per incident and 30 days of resolved incidents are retained. Omitted counts show capacity limits; recurrence after eviction begins a new incident.",
		"Attention samples cached state every five seconds. Short-lived failures between samples and external actions may be missing. Volume evidence reflects the last cached filesystem refresh.",
	}
	s.mu.Lock()
	s.published = clone(s.state)
	s.mu.Unlock()
}

func (s *Store) Run(ctx context.Context) {
	defer close(s.done)
	defer func() {
		if s.lock != nil {
			_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
			_ = s.lock.Close()
		}
	}()
	tick := time.NewTicker(s.opts.Interval)
	defer tick.Stop()
	s.refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.refresh()
		case r := <-s.commands:
			if err := r.ctx.Err(); err != nil {
				r.result <- err
				continue
			}
			before := clone(s.state)
			err := s.change(r)
			if err == nil {
				err = s.save()
				if err != nil {
					message := s.state.Error
					s.state = before
					s.state.Error = message
				}
			}
			s.publish()
			r.result <- err
		}
	}
}
func (s *Store) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Update replies only after the state has been durably replaced. A timeout may
// have committed: clients refresh the inbox before retrying, never assume failure.
func (s *Store) Update(ctx context.Context, id, action string, episode uint64, duration time.Duration, visited time.Time) error {
	r := request{ctx, id, action, episode, duration, visited, make(chan error, 1)}
	select {
	case s.commands <- r:
	case <-s.done:
		return errors.New("attention store stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-r.result:
		return err
	case <-s.done:
		return errors.New("attention store stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}

var ErrConflict = errors.New("incident changed; refresh the inbox")
var ErrNotFound = errors.New("incident not found")
var ErrInvalid = errors.New("invalid attention action")

func (s *Store) change(r request) error {
	now := s.opts.Now()
	if r.action == "visit" {
		if r.visited.IsZero() || r.visited.After(now) {
			return ErrInvalid
		}
		if s.state.LastVisit == nil || r.visited.After(*s.state.LastVisit) {
			at := r.visited
			s.state.LastVisit = &at
		}
		return nil
	}
	for i := range s.state.Incidents {
		in := &s.state.Incidents[i]
		if in.ID != r.id {
			continue
		}
		if in.Episode != r.episode || !in.Active {
			return ErrConflict
		}
		switch r.action {
		case "acknowledge":
			in.AcknowledgedAt = &now
			in.SnoozedUntil = nil
		case "snooze":
			if r.duration < time.Minute || r.duration > 7*24*time.Hour {
				return ErrInvalid
			}
			until := now.Add(r.duration)
			in.SnoozedUntil = &until
		case "resume":
			in.SnoozedUntil = nil
		default:
			return ErrInvalid
		}
		in.Status = status(*in, now)
		return nil
	}
	return ErrNotFound
}
func status(in Incident, now time.Time) string {
	if !in.Active {
		return "resolved"
	}
	if in.SnoozedUntil != nil && in.SnoozedUntil.After(now) {
		return "snoozed"
	}
	if in.AcknowledgedAt != nil {
		return "acknowledged"
	}
	return "open"
}

func (s *Store) refresh() {
	if s.opts.Source == nil {
		return
	}
	s.reconcile(s.opts.Source(), s.opts.Now())
	_ = s.save()
	s.publish()
}
func (s *Store) save() error {
	if s.loadFailed {
		return errors.New(s.state.Error)
	}
	now := s.opts.Now()
	out := clone(s.state)
	out.SavedAt = &now
	out.Error = ""
	out.Coverage = nil
	data, err := json.Marshal(out)
	if err == nil && len(data) > MaxBytes {
		err = errors.New("attention state exceeds byte bound")
	}
	if err == nil {
		err = s.opts.Write(s.opts.Path, data)
	}
	if err != nil {
		s.state.Error = "Attention changes could not be saved. The last durable file is preserved; new observations may be lost on restart."
		return err
	}
	s.state.Error = ""
	s.state.SavedAt = &now
	return nil
}
func writeState(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func identity(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

func (s *Store) reconcile(input Input, now time.Time) {
	snap := input.Snapshot
	if snap == nil {
		return
	}
	// Allow the first daemon poll to arrive before treating startup as an
	// outage. Existing incidents remain intact while observation is unknown.
	if snap.GeneratedAt.IsZero() && now.Sub(s.startedAt) < 30*time.Second {
		return
	}
	if input.StaleAfter <= 0 {
		input.StaleAfter = 30 * time.Second
	}
	fresh := snap.Status == poller.StatusConnected && !snap.Stale && !snap.GeneratedAt.IsZero() && !snap.GeneratedAt.After(now) && now.Sub(snap.GeneratedAt) <= input.StaleAfter
	distinct := snap.GeneratedAt.After(s.lastSample)
	groups := symptoms(input, fresh)
	occurredAt := now
	if fresh {
		occurredAt = snap.GeneratedAt
	}
	if fresh && distinct {
		s.lastSample = snap.GeneratedAt
		at := snap.GeneratedAt
		s.state.ObservedAt = &at
	}
	present := map[string]bool{}
	s.state.Omitted = 0
	// A deterministic order keeps capacity decisions stable through bursts.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		g := groups[key]
		id := identity(key)
		present[id] = true
		var in *Incident
		for i := range s.state.Incidents {
			if s.state.Incidents[i].ID == id {
				in = &s.state.Incidents[i]
				break
			}
		}
		if in == nil {
			if len(s.state.Incidents) >= MaxIncidents {
				oldest := -1
				for i, v := range s.state.Incidents {
					if !v.Active && (oldest < 0 || v.LastSeen.Before(s.state.Incidents[oldest].LastSeen)) {
						oldest = i
					}
				}
				if oldest >= 0 {
					s.state.Incidents = append(s.state.Incidents[:oldest], s.state.Incidents[oldest+1:]...)
					s.state.Pruned++
				} else {
					s.state.Omitted++
					continue
				}
			}
			s.state.Incidents = append(s.state.Incidents, Incident{ID: id, Kind: g.Kind, FirstSeen: occurredAt})
			in = &s.state.Incidents[len(s.state.Incidents)-1]
		}
		if !in.Active {
			// Reopening needs a fresh observation after confirmed recovery.
			in.Active = true
			in.Episode++
			in.EpisodeStarted = occurredAt
			in.ResolvedAt = nil
			in.AcknowledgedAt = nil
			in.SnoozedUntil = nil
			s.state.NoticeSequence++
			in.Notice = s.state.NoticeSequence
			if s.opts.History != nil {
				s.opts.History.Add("", history.Entry{Kind: history.KindAction, Actor: "attention", Action: "incident_opened", Result: "info", Message: g.Title, After: map[string]string{"incidentId": in.ID, "episode": fmt.Sprint(in.Episode)}})
			}
		}
		in.Title = g.Title
		in.Evidence = g.Evidence
		in.NextStep = g.NextStep
		in.Hashes = g.Hashes
		in.Affected = g.Affected
		in.HealthySince = nil
		if fresh {
			in.LastSeen = snap.GeneratedAt
		} else {
			in.LastSeen = now
		}
	}
	keep := s.state.Incidents[:0]
	for _, in := range s.state.Incidents {
		if !present[in.ID] && in.Active {
			if !fresh {
				in.HealthySince = nil
			} else if distinct {
				if in.HealthySince == nil {
					at := snap.GeneratedAt
					in.HealthySince = &at
				} else if snap.GeneratedAt.Sub(*in.HealthySince) >= 30*time.Second {
					in.Active = false
					at := snap.GeneratedAt
					in.ResolvedAt = &at
					in.SnoozedUntil = nil
					in.HealthySince = nil
					if s.opts.History != nil {
						s.opts.History.Add("", history.Entry{Kind: history.KindAction, Actor: "attention", Action: "incident_recovered", Result: "ok", Message: fmt.Sprintf("Observed recovery: %s (episode %d)", in.Title, in.Episode)})
					}
				}
			}
		}
		if in.Active && in.SnoozedUntil != nil && !in.SnoozedUntil.After(now) {
			in.SnoozedUntil = nil
			if in.AcknowledgedAt == nil {
				s.state.NoticeSequence++
				in.Notice = s.state.NoticeSequence
			}
		}
		in.Status = status(in, now)
		if !in.Active && in.ResolvedAt != nil && now.Sub(*in.ResolvedAt) > 30*24*time.Hour {
			s.state.Pruned++
			continue
		}
		keep = append(keep, in)
	}
	s.state.Incidents = keep
}
