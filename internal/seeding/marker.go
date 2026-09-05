package seeding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// markerMaxEntries bounds the persisted fired set; the oldest entries are
// dropped once it overflows.
const markerMaxEntries = 10000

// Marker persists which (torrent, group) pairs already triggered so a rule
// acts at most once per torrent per group across restarts (PAR-4.2). The set
// lives in a small JSON file next to the config. Writes are whole-file and
// atomic; triggers are rare (once per torrent per group ever), so no
// debouncing is needed.
type Marker struct {
	mu      sync.Mutex
	path    string
	cap     int
	done    map[string]int64 // "hash\x00group" → unix seconds when fired
	lastErr error
}

type markerFile struct {
	Fired map[string]int64 `json:"fired"`
}

// NewMarker loads (or starts fresh) the marker set stored at path.
func NewMarker(path string) (*Marker, error) {
	m := &Marker{path: path, cap: markerMaxEntries, done: map[string]int64{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("read seeding marker: %w", err)
	}
	var file markerFile
	if err := json.Unmarshal(data, &file); err != nil {
		// A corrupt marker must not wedge policy; start fresh.
		return m, fmt.Errorf("parse seeding marker %s: %w", path, err)
	}
	if file.Fired != nil {
		m.done = file.Fired
	}
	return m, nil
}

func markerKey(hash, group string) string { return hash + "\x00" + group }

// Fire records (hash, group) as triggered. It reports whether this call
// fired first — only the first caller acts. Safe to call under the poller
// lock: the map check is in-memory and the file write happens only on a
// first-ever trigger.
func (m *Marker) Fire(hash, group string, at time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := markerKey(hash, group)
	if _, ok := m.done[key]; ok {
		return false
	}
	m.done[key] = at.Unix()
	if len(m.done) > m.cap {
		m.pruneLocked()
	}
	if err := m.saveLocked(); err != nil {
		// Persisting failed, but the in-memory mark still prevents a second
		// trigger this process lifetime; surface the error for logging.
		m.lastErr = err
	}
	return true
}

// Unfire removes a mark set by Fire. The poller uses it to roll back when
// the worker queue rejects the job: without it a dropped job stays marked
// as done on disk and its action never runs, on this run or any future one.
func (m *Marker) Unfire(hash, group string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := markerKey(hash, group)
	if _, ok := m.done[key]; !ok {
		return
	}
	delete(m.done, key)
	if err := m.saveLocked(); err != nil {
		m.lastErr = err
	}
}

// SaveError reports the last persist failure, if any.
func (m *Marker) SaveError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// Len reports the number of recorded pairs (tests).
func (m *Marker) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.done)
}

// pruneLocked drops the oldest entries down to the cap. Caller holds mu.
func (m *Marker) pruneLocked() {
	type kv struct {
		key string
		at  int64
	}
	all := make([]kv, 0, len(m.done))
	for key, at := range m.done {
		all = append(all, kv{key, at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	for _, entry := range all[:len(all)-m.cap] {
		delete(m.done, entry.key)
	}
}

// saveLocked writes the file atomically. Caller holds mu.
func (m *Marker) saveLocked() error {
	data, err := json.Marshal(markerFile{Fired: m.done})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
