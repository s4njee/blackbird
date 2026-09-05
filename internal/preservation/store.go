// Package preservation records bounded, opt-in observations without daemon RPCs.
package preservation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"blackbird/internal/history"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

const (
	MaxWatches  = 128
	MaxSamples  = 288
	MaxTrackers = 8
	MaxBytes    = 8 << 20
	Interval    = 5 * time.Minute
	Window      = 6 * time.Hour
)

var (
	ErrInvalid     = errors.New("invalid watch: use a 40-character torrent hash, a reason up to 500 bytes, and an optional YYYY-MM-DD review date")
	ErrConflict    = errors.New("watch changed; refresh before saving")
	ErrPinned      = errors.New("preservation pin blocks removal; unpin it in Preservation first")
	ErrUnavailable = errors.New("preservation state unavailable; cleanup is blocked until storage is repaired and Blackbird restarted")
)

type Sample struct {
	At         time.Time  `json:"at"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
	Seeds      *int       `json:"seeds"`
	Complete   *bool      `json:"complete"`
	Status     string     `json:"status"`
}
type Tracker struct {
	Source   string    `json:"source"`
	Host     string    `json:"host"`
	CachedAt time.Time `json:"cachedAt"`
	Seeds    *int      `json:"seeds"`
	Enabled  bool      `json:"enabled"`
	// rTorrent's cached scrape fields do not supply the age of the report.
	ReportedAt *time.Time `json:"reportedAt"`
}
type Watch struct {
	Hash            string     `json:"hash"`
	Name            string     `json:"name"`
	Since           time.Time  `json:"since"`
	Revision        uint64     `json:"revision"`
	Pinned          bool       `json:"pinned"`
	Reason          string     `json:"reason"`
	ReviewDate      string     `json:"reviewDate"`
	LastActivity    *time.Time `json:"lastActivity"`
	Samples         []Sample   `json:"samples,omitempty"`
	Trackers        []Tracker  `json:"trackers"`
	TrackerHistory  []Tracker  `json:"trackerHistory,omitempty"`
	TrackersOmitted int        `json:"trackersOmitted"`
}
type Summary struct {
	Watch
	Band      string  `json:"band"`
	Evidence  string  `json:"evidence"`
	Known     int     `json:"known"`
	Low       int     `json:"low"`
	Expected  int     `json:"expected"`
	Coverage  float64 `json:"coverage"`
	Latest    *Sample `json:"latest"`
	ReviewDue bool    `json:"reviewDue"`
}
type State struct {
	Version int     `json:"version"`
	Watches []Watch `json:"watches"`
}
type Options struct {
	Path    string
	History *history.Log
	Now     func() time.Time
	Write   func(string, []byte) error
}
type Input struct {
	Snapshot   *poller.Snapshot
	StaleAfter time.Duration
	Trackers   func(string) ([]rtorrent.Tracker, time.Time)
}
type Change struct {
	Name       string `json:"-"`
	Hash       string `json:"hash"`
	Action     string `json:"action"` // watch, update, unwatch
	Revision   uint64 `json:"revision"`
	Pinned     bool   `json:"pinned"`
	Reason     string `json:"reason"`
	ReviewDate string `json:"reviewDate"`
}
type Store struct {
	opts      Options
	mu        sync.RWMutex
	cleanup   sync.Mutex // serializes pin changes against in-flight removal
	state     State
	lock      *os.File
	failed    bool
	saveError string
}

func Open(opts Options) (*Store, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Write == nil {
		opts.Write = writeState
	}
	s := &Store{opts: opts, state: State{Version: 1, Watches: []Watch{}}}
	err := s.open()
	if err != nil {
		s.failed = true
		s.saveError = ErrUnavailable.Error()
	}
	return s, err
}
func (s *Store) open() error {
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
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
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
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > MaxBytes {
		return errors.New("preservation file exceeds limit")
	}
	var st State
	if err = json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.Version != 1 || len(st.Watches) > MaxWatches {
		return errors.New("unsupported preservation state")
	}
	seen := map[string]bool{}
	for _, w := range st.Watches {
		if !validHash(w.Hash) || seen[w.Hash] || w.Revision == 0 || w.Since.IsZero() || len(w.Samples) > MaxSamples || len(w.Trackers) > MaxTrackers || len(w.TrackerHistory) > 32 || !validFields(w.Reason, w.ReviewDate) {
			return errors.New("invalid preservation state")
		}
		seen[w.Hash] = true
	}
	s.state = st
	return nil
}
func (s *Store) Close() {
	if s.lock != nil {
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
	}
}
func validHash(h string) bool {
	_, err := hex.DecodeString(h)
	return len(h) == 40 && err == nil && h == strings.ToUpper(h)
}
func validFields(reason, date string) bool {
	if len(reason) > 500 {
		return false
	}
	if date == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}
func (s *Store) Change(c Change, actor string) error {
	c.Hash = strings.ToUpper(c.Hash)
	if !validHash(c.Hash) || !validFields(c.Reason, c.ReviewDate) {
		return ErrInvalid
	}
	s.cleanup.Lock()
	defer s.cleanup.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return ErrUnavailable
	}
	st := s.state
	st.Watches = append([]Watch{}, s.state.Watches...)
	idx := -1
	for i, w := range st.Watches {
		if w.Hash == c.Hash {
			idx = i
			break
		}
	}
	switch c.Action {
	case "watch":
		if idx >= 0 || c.Revision != 0 {
			return ErrConflict
		}
		if len(st.Watches) >= MaxWatches {
			return errors.New("watchlist is full (128 torrents)")
		}
		st.Watches = append(st.Watches, Watch{Hash: c.Hash, Name: truncate(c.Name, 512), Since: s.opts.Now(), Revision: 1, Pinned: c.Pinned, Reason: c.Reason, ReviewDate: c.ReviewDate, Trackers: []Tracker{}})
	case "update", "unwatch":
		if idx < 0 || st.Watches[idx].Revision != c.Revision {
			return ErrConflict
		}
		if c.Action == "unwatch" {
			if st.Watches[idx].Pinned {
				return ErrPinned
			}
			st.Watches = append(st.Watches[:idx], st.Watches[idx+1:]...)
		} else {
			st.Watches[idx].Pinned = c.Pinned
			st.Watches[idx].Reason = c.Reason
			st.Watches[idx].ReviewDate = c.ReviewDate
			st.Watches[idx].Revision++
		}
	default:
		return ErrInvalid
	}
	if err := s.save(st); err != nil {
		return err
	}
	s.state = st
	if s.opts.History != nil {
		s.opts.History.Add(c.Hash, history.Entry{Kind: history.KindAction, Actor: actor, Action: "preservation_" + c.Action, Result: "saved", Message: "Preservation watch updated", After: map[string]string{"pinned": fmt.Sprint(c.Pinned), "reviewDate": c.ReviewDate}})
	}
	return nil
}

// Guard holds a removal lease so a successful pin cannot race an already running cleanup.
func (s *Store) Guard(hash string, action func() error) error {
	s.cleanup.Lock()
	defer s.cleanup.Unlock()
	s.mu.RLock()
	blocked := s.failed
	for _, w := range s.state.Watches {
		if w.Hash == strings.ToUpper(hash) && w.Pinned {
			s.mu.RUnlock()
			return ErrPinned
		}
	}
	s.mu.RUnlock()
	if blocked {
		return ErrUnavailable
	}
	return action()
}
func (s *Store) save(st State) error {
	raw, err := json.Marshal(st)
	if err == nil && len(raw) > MaxBytes {
		err = errors.New("preservation file exceeds limit")
	}
	if err == nil {
		err = s.opts.Write(s.opts.Path, raw)
	}
	if err != nil {
		s.saveError = "Preservation changes could not be confirmed on disk. Refresh before retrying; existing pins remain enforced."
		return err
	}
	s.saveError = ""
	return nil
}
func writeState(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) Run(ctx context.Context, source func() Input) {
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	s.Sample(source())
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.Sample(source())
		}
	}
}
func (s *Store) Sample(input Input) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || len(s.state.Watches) == 0 {
		return
	}
	now := s.opts.Now()
	snap := input.Snapshot
	if input.StaleAfter <= 0 {
		input.StaleAfter = time.Minute
	}
	fresh := snap != nil && snap.Status == poller.StatusConnected && !snap.Stale && !snap.GeneratedAt.IsZero() && !snap.GeneratedAt.After(now) && now.Sub(snap.GeneratedAt) <= min(input.StaleAfter, time.Minute)
	// Scan the shared snapshot once, then inspect only the explicitly watched rows.
	rows := map[string]*rtorrent.Torrent{}
	if fresh {
		for i := range snap.Torrents {
			t := &snap.Torrents[i]
			rows[strings.ToUpper(t.Hash)] = t
		}
	}
	for i := range s.state.Watches {
		w := &s.state.Watches[i]
		if len(w.Samples) > 0 && now.Sub(w.Samples[len(w.Samples)-1].At) < Interval {
			continue
		}
		sample := Sample{At: now, Status: "disconnected or stale"}
		if fresh {
			sample.Status = "not in session"
			if t := rows[w.Hash]; t != nil {
				w.Name = truncate(t.Name, 512)
				at := snap.GeneratedAt
				sample.ObservedAt = &at
				complete := t.Complete
				sample.Complete = &complete
				sample.Status = "inactive; connected seeds excluded from ranking"
				if t.IsOpen && (t.State == rtorrent.StateDownloading || t.State == rtorrent.StateSeeding) && t.Seeds >= 0 {
					n := t.Seeds
					sample.Seeds = &n
					sample.Status = "active"
				}
				if t.UpRate > 0 || t.DownRate > 0 {
					w.LastActivity = &at
				}
				if input.Trackers != nil {
					ts, cached := input.Trackers(t.Hash)
					if !cached.IsZero() && !cached.After(now) && now.Sub(cached) <= input.StaleAfter {
						next := trackerObservations(ts, cached)
						if len(w.Trackers) == 0 || !w.Trackers[0].CachedAt.Equal(cached) {
							w.TrackerHistory = append(w.TrackerHistory, next...)
							if len(w.TrackerHistory) > 32 {
								w.TrackerHistory = append([]Tracker{}, w.TrackerHistory[len(w.TrackerHistory)-32:]...)
							}
						}
						w.Trackers = next
						w.TrackersOmitted = max(0, len(ts)-MaxTrackers)
					}
				}
			}
		}
		kept := w.TrackerHistory[:0]
		for _, t := range w.TrackerHistory {
			if now.Sub(t.CachedAt) <= 24*time.Hour {
				kept = append(kept, t)
			}
		}
		w.TrackerHistory = kept
		w.Samples = append(w.Samples, sample)
		first := 0
		for first < len(w.Samples) && (len(w.Samples)-first > MaxSamples || now.Sub(w.Samples[first].At) > 24*time.Hour) {
			first++
		}
		w.Samples = append([]Sample{}, w.Samples[first:]...)
	}
	_ = s.save(s.state)
}
func truncate(s string, n int) string {
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "")
	}
	return s
}
func trackerObservations(ts []rtorrent.Tracker, at time.Time) []Tracker {
	out := []Tracker{}
	for _, t := range ts[:min(len(ts), MaxTrackers)] {
		u, _ := url.Parse(t.URL)
		host := "unknown tracker"
		if u != nil && u.Hostname() != "" {
			host = truncate(u.Hostname(), 200)
		}
		digest := sha256.Sum256([]byte(t.URL))
		source := hex.EncodeToString(digest[:8])
		var seeds *int
		if t.Seeds >= 0 && t.SuccessCount > 0 {
			n := t.Seeds
			seeds = &n
		}
		out = append(out, Tracker{Source: source, Host: host, CachedAt: at, Seeds: seeds, Enabled: t.IsEnabled})
	}
	return out
}
func (s *Store) Snapshot(hash string) ([]Summary, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Summary{}
	now := s.opts.Now()
	for _, w := range s.state.Watches {
		if hash != "" && w.Hash != strings.ToUpper(hash) {
			continue
		}
		v := summarize(w, now)
		v.Trackers = append([]Tracker{}, w.Trackers...)
		if hash == "" {
			v.Samples = nil
			v.TrackerHistory = nil
		} else {
			v.Samples = append([]Sample{}, w.Samples...)
			v.TrackerHistory = append([]Tracker{}, w.TrackerHistory...)
		}
		out = append(out, v)
	}
	rank := map[string]int{"few_seeds": 0, "recent_low": 1, "mixed": 2, "more_seeds": 3, "unknown": 4}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.Band] != rank[b.Band] {
			return rank[a.Band] < rank[b.Band]
		}
		if a.Low != b.Low {
			return a.Low > b.Low
		}
		return a.Hash < b.Hash
	})
	return out, s.saveError
}
func summarize(w Watch, now time.Time) Summary {
	v := Summary{Watch: w, Band: "unknown"}
	start := now.Add(-Window)
	if w.Since.After(start) {
		start = w.Since
	}
	v.Expected = max(1, min(72, int(now.Sub(start)/Interval)+1))
	var first, last time.Time
	seen := map[int64]bool{}
	for _, p := range w.Samples {
		if !p.At.After(now.Add(-Window)) || p.At.Before(start) || p.At.After(now) || p.Seeds == nil || p.ObservedAt == nil || p.ObservedAt.After(p.At) || p.At.Sub(*p.ObservedAt) > time.Minute {
			continue
		}
		bucket := p.At.Unix() / int64(Interval/time.Second)
		if seen[bucket] {
			continue
		}
		seen[bucket] = true
		v.Known++
		if *p.Seeds <= 1 {
			v.Low++
		}
		if first.IsZero() {
			first = p.At
		}
		last = p.At
	}
	v.Coverage = min(1, float64(v.Known)/float64(v.Expected))
	if len(w.Samples) > 0 {
		p := w.Samples[len(w.Samples)-1]
		v.Latest = &p
		if now.Sub(p.At) <= 2*Interval && !p.At.After(now) && p.Seeds != nil {
			switch {
			case v.Known >= 12 && last.Sub(first) >= 55*time.Minute && v.Coverage >= .75 && v.Low*5 >= v.Known*4 && *p.Seeds <= 1:
				v.Band = "few_seeds"
			case *p.Seeds <= 1:
				v.Band = "recent_low"
			case v.Low > 0:
				v.Band = "mixed"
			default:
				v.Band = "more_seeds"
			}
		}
	}
	v.Evidence = fmt.Sprintf("%d of %d eligible active observations had 0–1 connected seeds; %d expected five-minute slots in the last six hours (or since watching).", v.Low, v.Known, v.Expected)
	v.ReviewDue = w.Pinned && w.ReviewDate != "" && w.ReviewDate <= now.UTC().Format("2006-01-02")
	return v
}
