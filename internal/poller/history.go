package poller

import (
	"time"
)

// Sample is one global rate observation backing the sparkline and
// throughput graph.
type Sample struct {
	At       time.Time `json:"at"`
	DownRate int64     `json:"downRate"`
	UpRate   int64     `json:"upRate"`
}

// historyWindow is the rolling buffer length (60 minutes, per the design).
const historyWindow = 60 * time.Minute

// historyCap bounds the buffer when polls are faster than one per second.
const historyCap = 3600

// historyRing is the bounded global rate history: a fixed-capacity ring, so
// pushes never re-slice and steady-state cycles allocate nothing for it
// (PERF-6.4). Stale samples fall off the front by pointer bump on push;
// over-capacity pushes overwrite the oldest.
type historyRing struct {
	buf   []Sample
	start int // index of the oldest live sample
	n     int // live sample count
}

func newHistoryRing() historyRing {
	return historyRing{buf: make([]Sample, historyCap)}
}

// push appends a sample, evicting anything older than the window first.
func (r *historyRing) push(s Sample, now time.Time) {
	cutoff := now.Add(-historyWindow)
	for r.n > 0 && r.buf[r.start].At.Before(cutoff) {
		r.start = (r.start + 1) % len(r.buf)
		r.n--
	}
	if r.n == len(r.buf) {
		r.start = (r.start + 1) % len(r.buf)
		r.n--
	}
	r.buf[(r.start+r.n)%len(r.buf)] = s
	r.n++
}

// samples returns the live contents oldest→newest. The result is freshly
// allocated per call; readers are rare (stats page, connect seeding) next
// to per-cycle pushes.
func (r *historyRing) samples() []Sample {
	out := make([]Sample, 0, r.n)
	for i := 0; i < r.n; i++ {
		out = append(out, r.buf[(r.start+i)%len(r.buf)])
	}
	return out
}

// len returns the live sample count (tests).
func (r *historyRing) len() int { return r.n }
