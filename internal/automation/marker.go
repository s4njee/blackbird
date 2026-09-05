package automation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// markerMaxEntries bounds the persisted completed-hash set; the oldest
// entries are dropped once it overflows.
const markerMaxEntries = 10000

// Marker persists which torrents the completion rules already handled so a
// rule never runs twice for the same hash across restarts (PAR-3.2). The set
// lives in a small JSON file next to the config, not in the torrent's custom
// slots. Writes are whole-file and atomic; completions are rare, so no
// debouncing is needed.
type Marker struct {
	mu   sync.Mutex
	path string
	cap  int              // max entries; oldest are pruned past this
	done map[string]int64 // hash → unix seconds when processed
}

type markerFile struct {
	Completed map[string]int64 `json:"completed"`
}

// NewMarker loads (or starts fresh) the marker set stored at path.
func NewMarker(path string) (*Marker, error) {
	m := &Marker{path: path, cap: markerMaxEntries, done: map[string]int64{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("read completion marker: %w", err)
	}
	var file markerFile
	if err := json.Unmarshal(data, &file); err != nil {
		// A corrupt marker must not wedge the rules; start fresh.
		return m, fmt.Errorf("parse completion marker %s: %w", path, err)
	}
	if file.Completed != nil {
		m.done = file.Completed
	}
	return m, nil
}

// Seen reports whether the hash was already processed.
func (m *Marker) Seen(hash string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.done[hash]
	return ok
}

// Mark records the hash as processed and persists the file.
func (m *Marker) Mark(hash string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.done[hash]; ok {
		return nil
	}
	m.done[hash] = at.Unix()
	if len(m.done) > m.cap {
		m.pruneLocked()
	}
	return m.saveLocked()
}

// Len reports the number of recorded hashes (tests).
func (m *Marker) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.done)
}

// pruneLocked drops the oldest entries down to the cap. Caller holds mu.
func (m *Marker) pruneLocked() {
	type kv struct {
		hash string
		at   int64
	}
	all := make([]kv, 0, len(m.done))
	for hash, at := range m.done {
		all = append(all, kv{hash, at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	for _, entry := range all[:len(all)-m.cap] {
		delete(m.done, entry.hash)
	}
}

// saveLocked writes the file atomically. Caller holds mu.
func (m *Marker) saveLocked() error {
	data, err := json.Marshal(markerFile{Completed: m.done})
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
