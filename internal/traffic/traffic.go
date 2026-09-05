// Package traffic implements PAR-5.2 transfer accounting: per-day and
// per-hour down/up totals derived from the daemon's throttle.global_*.total
// counters, persisted to a compact append-only file.
//
// Buckets are keyed in UTC so daylight-saving transitions never split or
// duplicate a day. Counter resets (daemon restarts zero the totals) are
// detected per counter: a backward step counts the current value as new
// traffic rather than going negative. A poll straddling midnight attributes
// its whole delta to the sample's day and hour — at most one poll interval
// of skew, documented here instead of worked around.
package traffic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// flushInterval bounds crash loss: in-memory deltas flush to disk this often.
const flushInterval = 5 * time.Minute

// compactLines rewrites the file on load past this many lines.
const compactLines = 10000

// counters is one bucket's accumulated bytes plus the flushed position.
type counters struct {
	Down, Up               int64
	FlushedDown, FlushedUp int64
}

// record is one appended JSON line: a flushed delta, not a total.
type record struct {
	Day  string `json:"day,omitempty"`  // YYYY-MM-DD (UTC)
	Hour string `json:"hour,omitempty"` // YYYY-MM-DDTHH (UTC)
	Down int64  `json:"down"`
	Up   int64  `json:"up"`
	// Prev marks a previous-totals line (no bucket, Down/Up reused) so a
	// restart bridges the shutdown gap instead of re-baselining.
	Prev bool `json:"prev,omitempty"`
}

// Options configures a Tracker.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Path is the append-only JSONL file.
	Path string
	// RetentionDays bounds kept buckets; <= 0 disables persistence (the
	// tracker still counts the live session in memory).
	RetentionDays int
	// Now is the clock, overridable for tests.
	Now func() time.Time
	// FlushInterval overrides the disk flush cadence (tests).
	FlushInterval time.Duration
}

// DayTotal is one UTC day's traffic.
type DayTotal struct {
	Day      string
	Down, Up int64
}

// HourTotal is one UTC hour's traffic.
type HourTotal struct {
	Hour     string // YYYY-MM-DDTHH
	Down, Up int64
}

// Tracker accumulates daemon total-counter deltas into UTC buckets.
type Tracker struct {
	opts Options

	mu       sync.Mutex
	hasPrev  bool
	prevDown int64
	prevUp   int64
	// flushedPrev tracks the previous totals already on disk, so restarts
	// bridge the shutdown gap instead of re-baselining.
	flushedPrevDown int64
	flushedPrevUp   int64
	days            map[string]*counters
	hours           map[string]*counters
	dirtyDays       map[string]bool
	dirtyHrs        map[string]bool
	prunedDay       string
}

// New builds a Tracker, loading persisted buckets. Corrupt lines are
// skipped with a warning; a missing file starts empty.
func New(opts Options) (*Tracker, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = flushInterval
	}
	t := &Tracker{
		opts:      opts,
		days:      map[string]*counters{},
		hours:     map[string]*counters{},
		dirtyDays: map[string]bool{},
		dirtyHrs:  map[string]bool{},
	}
	if opts.RetentionDays < 0 {
		opts.RetentionDays = 0
		t.opts.RetentionDays = 0
	}
	if err := t.load(); err != nil {
		return nil, err
	}
	return t, nil
}

func dayKey(t time.Time) string  { return t.UTC().Format("2006-01-02") }
func hourKey(t time.Time) string { return t.UTC().Format("2006-01-02T15") }

// Feed records one poll's daemon totals. Deltas accrue to the sample's UTC
// day and hour buckets.
func (t *Tracker) Feed(at time.Time, downTotal, upTotal int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasPrev {
		t.prevDown, t.prevUp, t.hasPrev = downTotal, upTotal, true
		return
	}
	down, up := downTotal-t.prevDown, upTotal-t.prevUp
	t.prevDown, t.prevUp = downTotal, upTotal
	if down < 0 {
		down = downTotal // counter reset (daemon restart): count current as new
	}
	if up < 0 {
		up = upTotal
	}
	if down == 0 && up == 0 {
		return
	}
	day, hour := dayKey(at), hourKey(at)
	t.bucket(t.days, t.dirtyDays, day, down, up)
	t.bucket(t.hours, t.dirtyHrs, hour, down, up)
	t.maybePruneLocked(at)
}

func (t *Tracker) bucket(all map[string]*counters, dirty map[string]bool, key string, down, up int64) {
	c := all[key]
	if c == nil {
		c = &counters{}
		all[key] = c
	}
	c.Down += down
	c.Up += up
	dirty[key] = true
}

// RetentionDays reports the configured retention window in days
// (0 = persistence disabled, memory-only counting).
func (t *Tracker) RetentionDays() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.opts.RetentionDays
}

// SetRetentionDays updates the retention window live (Settings saves and
// SIGHUP reloads) and prunes buckets now outside it. A non-positive value
// disables persistence; in-memory counting continues.
func (t *Tracker) SetRetentionDays(days int) {
	if days < 0 {
		days = 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.opts.RetentionDays = days
	t.prunedDay = ""
	t.pruneLocked(t.opts.Now())
}

// maybePruneLocked drops buckets past retention when the UTC day rolls.
// Caller holds mu.
func (t *Tracker) maybePruneLocked(at time.Time) {
	if t.opts.RetentionDays <= 0 {
		return
	}
	day := dayKey(at)
	if day == t.prunedDay {
		return
	}
	t.prunedDay = day
	t.pruneLocked(at)
}

// Days returns daily totals for [from, to] (UTC dates, inclusive, ascending).
// Unknown days are zero rows so charts render continuously.
func (t *Tracker) Days(from, to time.Time) []DayTotal {
	t.mu.Lock()
	defer t.mu.Unlock()
	start := time.Date(from.UTC().Year(), from.UTC().Month(), from.UTC().Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(to.UTC().Year(), to.UTC().Month(), to.UTC().Day(), 0, 0, 0, 0, time.UTC)
	var out []DayTotal
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		total := DayTotal{Day: key}
		if c := t.days[key]; c != nil {
			total.Down, total.Up = c.Down, c.Up
		}
		out = append(out, total)
	}
	return out
}

// Hours returns the 24 hourly totals for a UTC day (ascending, zero-filled).
func (t *Tracker) Hours(day time.Time) []HourTotal {
	t.mu.Lock()
	defer t.mu.Unlock()
	base := time.Date(day.UTC().Year(), day.UTC().Month(), day.UTC().Day(), 0, 0, 0, 0, time.UTC)
	out := make([]HourTotal, 0, 24)
	for h := 0; h < 24; h++ {
		key := base.Add(time.Duration(h) * time.Hour).Format("2006-01-02T15")
		total := HourTotal{Hour: key}
		if c := t.hours[key]; c != nil {
			total.Down, total.Up = c.Down, c.Up
		}
		out = append(out, total)
	}
	return out
}

// Flush appends dirty buckets to the file. No-op when persistence is
// disabled or nothing changed.
func (t *Tracker) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.flushLocked()
}

func (t *Tracker) flushLocked() error {
	if t.opts.RetentionDays <= 0 || t.opts.Path == "" {
		t.dirtyDays = map[string]bool{}
		t.dirtyHrs = map[string]bool{}
		return nil
	}
	if len(t.dirtyDays) == 0 && len(t.dirtyHrs) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.opts.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(t.opts.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	flushBucket := func(rec record, c *counters) error {
		down, up := c.Down-c.FlushedDown, c.Up-c.FlushedUp
		c.FlushedDown, c.FlushedUp = c.Down, c.Up
		if down == 0 && up == 0 {
			return nil
		}
		rec.Down, rec.Up = down, up
		return enc.Encode(rec)
	}
	var firstErr error
	for _, key := range sortedKeys(t.dirtyDays) {
		if c := t.days[key]; c != nil {
			if err := flushBucket(record{Day: key}, c); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, key := range sortedKeys(t.dirtyHrs) {
		if c := t.hours[key]; c != nil {
			if err := flushBucket(record{Hour: key}, c); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	// Bridge restarts: persist previous totals when they moved, so the next
	// process attributes shutdown-gap traffic instead of re-baselining.
	if t.hasPrev && (t.prevDown != t.flushedPrevDown || t.prevUp != t.flushedPrevUp) {
		if err := enc.Encode(record{Prev: true, Down: t.prevDown, Up: t.prevUp}); err != nil && firstErr == nil {
			firstErr = err
		} else if err == nil {
			t.flushedPrevDown, t.flushedPrevUp = t.prevDown, t.prevUp
		}
	}
	closeErr := f.Close()
	t.dirtyDays = map[string]bool{}
	t.dirtyHrs = map[string]bool{}
	if firstErr != nil {
		return firstErr
	}
	return closeErr
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// load sums the file into buckets; flushed positions start at the loaded
// totals so the next flush only appends new deltas.
func (t *Tracker) load() error {
	if t.opts.RetentionDays <= 0 || t.opts.Path == "" {
		return nil
	}
	f, err := os.Open(t.opts.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read traffic file: %w", err)
	}
	defer f.Close()
	lines := 0
	skipped := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++
			continue
		}
		if rec.Day != "" {
			c := t.dayBucket(rec.Day)
			c.Down += rec.Down
			c.Up += rec.Up
		} else if rec.Hour != "" {
			c := t.hourBucket(rec.Hour)
			c.Down += rec.Down
			c.Up += rec.Up
		} else if rec.Prev {
			t.hasPrev = true
			t.prevDown, t.prevUp = rec.Down, rec.Up
			t.flushedPrevDown, t.flushedPrevUp = rec.Down, rec.Up
		} else {
			skipped++
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read traffic file: %w", err)
	}
	if skipped > 0 {
		t.opts.Log.Warn("traffic: skipped corrupt lines", "path", t.opts.Path, "skipped", skipped)
	}
	for _, buckets := range []map[string]*counters{t.days, t.hours} {
		for _, c := range buckets {
			c.FlushedDown, c.FlushedUp = c.Down, c.Up
		}
	}
	t.pruneLocked(t.opts.Now())
	if lines > compactLines {
		return t.rewriteLocked()
	}
	return nil
}

// rewriteLocked compacts the file to one line per bucket. Caller holds mu.
func (t *Tracker) rewriteLocked() error {
	if t.opts.RetentionDays <= 0 || t.opts.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.opts.Path), 0o700); err != nil {
		return err
	}
	tmp := t.opts.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	var firstErr error
	for _, key := range sortedBucketKeys(t.days) {
		c := t.days[key]
		if err := enc.Encode(record{Day: key, Down: c.Down, Up: c.Up}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, key := range sortedBucketKeys(t.hours) {
		c := t.hours[key]
		if err := enc.Encode(record{Hour: key, Down: c.Down, Up: c.Up}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Preserve the restart bridge across compactions.
	if t.hasPrev {
		if err := enc.Encode(record{Prev: true, Down: t.prevDown, Up: t.prevUp}); err != nil && firstErr == nil {
			firstErr = err
		} else if err == nil {
			t.flushedPrevDown, t.flushedPrevUp = t.prevDown, t.prevUp
		}
	}
	if err := f.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		_ = os.Remove(tmp)
		return firstErr
	}
	if err := os.Rename(tmp, t.opts.Path); err != nil {
		return err
	}
	// Everything on disk now matches memory.
	for _, buckets := range []map[string]*counters{t.days, t.hours} {
		for _, c := range buckets {
			c.FlushedDown, c.FlushedUp = c.Down, c.Up
		}
	}
	t.dirtyDays = map[string]bool{}
	t.dirtyHrs = map[string]bool{}
	return nil
}

func sortedBucketKeys(m map[string]*counters) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (t *Tracker) dayBucket(key string) *counters {
	c := t.days[key]
	if c == nil {
		c = &counters{}
		t.days[key] = c
	}
	return c
}

func (t *Tracker) hourBucket(key string) *counters {
	c := t.hours[key]
	if c == nil {
		c = &counters{}
		t.hours[key] = c
	}
	return c
}

// pruneLocked drops buckets past retention and rewrites when anything was
// dropped. Caller holds mu.
func (t *Tracker) pruneLocked(now time.Time) {
	if t.opts.RetentionDays <= 0 {
		return
	}
	cutoff := now.UTC().AddDate(0, 0, -t.opts.RetentionDays).Format("2006-01-02")
	pruned := false
	for key := range t.days {
		if key < cutoff {
			delete(t.days, key)
			delete(t.dirtyDays, key)
			pruned = true
		}
	}
	for key := range t.hours {
		if key < cutoff+"T00" {
			delete(t.hours, key)
			delete(t.dirtyHrs, key)
			pruned = true
		}
	}
	if pruned {
		if err := t.rewriteLocked(); err != nil {
			t.opts.Log.Warn("traffic: compact after prune failed", "err", err)
		}
	}
}

// Run flushes periodically until ctx is cancelled, with a final flush on
// exit so at most one interval is ever lost.
func (t *Tracker) Run(ctx context.Context) {
	ticker := time.NewTicker(t.opts.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := t.Flush(); err != nil {
				t.opts.Log.Warn("traffic: final flush failed", "err", err)
			}
			return
		case <-ticker.C:
			if err := t.Flush(); err != nil {
				t.opts.Log.Warn("traffic: flush failed", "err", err)
			}
		}
	}
}
