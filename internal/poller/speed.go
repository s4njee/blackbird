package poller

import "time"

// rateSample is one per-torrent rate observation for the Speed tab. It mirrors
// Sample but is kept separate so the per-torrent ring stays independent of the
// global throughput history.
type rateSample struct {
	At       time.Time
	DownRate int64
	UpRate   int64
}

// perTorrentRing is the bounded, time-windowed ring of rate samples for a
// single infohash. It is a proper ring buffer (head/tail over a fixed array),
// not a re-sliced slice, so the poll path stays allocation-stable (PERF-6.4).
type perTorrentRing struct {
	buf  []rateSample
	head int // next write index
	size int // live entries
}

// newPerTorrentRing allocates a ring that can hold cap samples.
func newPerTorrentRing(cap int) *perTorrentRing {
	if cap < 2 {
		cap = 2
	}
	return &perTorrentRing{buf: make([]rateSample, cap)}
}

// push appends a sample, evicting the oldest once full.
func (r *perTorrentRing) push(s rateSample) {
	r.buf[r.head] = s
	r.head = (r.head + 1) % len(r.buf)
	if r.size < len(r.buf) {
		r.size++
	}
}

// window prunes entries older than cutoff and returns them oldest→newest.
func (r *perTorrentRing) window(cutoff time.Time) []rateSample {
	// Compact in place: walk from oldest to newest and drop stale entries.
	oldest := (r.head - r.size + len(r.buf)) % len(r.buf)
	out := make([]rateSample, 0, r.size)
	for i := 0; i < r.size; i++ {
		idx := (oldest + i) % len(r.buf)
		if r.buf[idx].At.Before(cutoff) {
			continue
		}
		out = append(out, r.buf[idx])
	}
	return out
}

// speedRings holds the per-hash speed rings plus their retention deadline.
type speedRings struct {
	byHash map[string]*ringState
}

type ringState struct {
	ring      *perTorrentRing
	unfocused time.Time // zero while focused; set when the last focus released
}

func newSpeedRings() *speedRings {
	return &speedRings{byHash: map[string]*ringState{}}
}

// speedWindow is the 60-minute window the Speed tab shows (per PAR-2.5).
const speedWindow = 60 * time.Minute

// speedCap bounds each ring when polls are faster than one per second.
const speedCap = 3600

// speedRetainAfterUnfocus is how long a ring survives after the last client
// unfocuses the torrent before it is pruned (PAR-2.5: retained 60 min).
const speedRetainAfterUnfocus = 60 * time.Minute
