package history

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	RecorderVersion      = 1
	DefaultRecorderBytes = 16 << 20
	maxRecordBytes       = 32 << 10
	maxRecordedEvents    = 20000
)

type RecorderOptions struct {
	Path          string
	MaxBytes      int64
	Retention     time.Duration
	FlushInterval time.Duration
	QueueSize     int
	Now           func() time.Time
	// Write replaces the durable snapshot; injectable for disk-full/blocked-I/O tests.
	Write func(string, []byte) error
}

type RecorderStatus struct {
	Enabled          bool       `json:"enabled"`
	Error            string     `json:"error,omitempty"`
	Dropped          uint64     `json:"dropped"`
	Pruned           uint64     `json:"pruned"`
	Pending          int        `json:"pending"`
	DurableThrough   uint64     `json:"durableThrough"`
	LastPersistedAt  *time.Time `json:"lastPersistedAt"`
	MaxBytes         int64      `json:"maxBytes"`
	RetentionSeconds int64      `json:"retentionSeconds"`
}

type Recording struct {
	Version  int            `json:"version"`
	Events   []Event        `json:"events"`
	Status   RecorderStatus `json:"status"`
	Coverage []string       `json:"coverage"`
}

type diskHeader struct {
	Version   int       `json:"recorderVersion"`
	Sequence  uint64    `json:"sequence"`
	Dropped   uint64    `json:"dropped"`
	Pruned    uint64    `json:"pruned"`
	WrittenAt time.Time `json:"writtenAt"`
}

// Recorder owns disk I/O in one background worker. Producers never take its
// mutex or wait for I/O; a full bounded queue increments an explicit gap count.
type Recorder struct {
	opts              RecorderOptions
	queue             chan Event
	samples           chan sessionObservation
	stop              chan struct{}
	done              chan struct{}
	flush             chan chan error
	closed            atomic.Bool
	dropped           atomic.Uint64
	serial            atomic.Uint64
	revision          atomic.Value
	prefix            string
	mu                sync.Mutex // only UI readers and the worker; never held during I/O
	events            []Event
	status            RecorderStatus
	seq               uint64
	bytes             int64
	sizes             []int64
	loadFailed        bool
	lastWrittenPruned uint64
	lockFile          *os.File
	previous          map[string]observedTorrent // worker-owned
	connection        string
}

func OpenRecorder(opts RecorderOptions) (*Recorder, error) {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultRecorderBytes
	}
	if opts.Retention <= 0 {
		opts.Retention = 24 * time.Hour
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 5 * time.Second
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1024
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Write == nil {
		opts.Write = writeRecording
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	r := &Recorder{opts: opts, prefix: hex.EncodeToString(random[:]), queue: make(chan Event, opts.QueueSize), samples: make(chan sessionObservation, 1), stop: make(chan struct{}), done: make(chan struct{}), flush: make(chan chan error), previous: map[string]observedTorrent{}}
	r.revision.Store("")
	r.status = RecorderStatus{Enabled: true, MaxBytes: opts.MaxBytes, RetentionSeconds: int64(opts.Retention.Seconds())}
	err := r.lock()
	if err == nil {
		err = r.load()
	}
	if err != nil {
		r.loadFailed = true
		r.status.Error = "Recording could not be opened exclusively or loaded; persistence disabled to preserve it."
	}
	r.append(Event{Entry: Entry{ID: r.nextID(), At: opts.Now(), Phase: "gap", Actor: "recorder", Action: "startup", Message: "Observation coverage before this process started is unknown. Retained evidence may end before shutdown or an unflushed crash tail."}})
	go r.run()
	return r, err
}

func (r *Recorder) nextID() string { return fmt.Sprintf("%s-%d", r.prefix, r.serial.Add(1)) }

func (r *Recorder) Record(hash string, e Entry) string {
	if r == nil {
		return ""
	}
	e.ID = r.nextID()
	if e.At.IsZero() {
		e.At = r.opts.Now()
	}
	if e.Revision == "" {
		e.Revision, _ = r.revision.Load().(string)
	}
	size := len(hash) + len(e.Name) + len(e.Message) + len(e.Actor) + len(e.Action) + len(e.CauseID) + len(e.Revision)
	for _, values := range []map[string]string{e.Before, e.After} {
		for k, v := range values {
			size += len(k) + len(v) + 8
		}
	}
	if size > maxRecordBytes-2048 {
		r.dropped.Add(1)
		return e.ID
	}
	// Copy bounded maps so callers cannot mutate queued evidence. Large data
	// is rejected as a whole, rather than silently truncating a checkpoint.
	e.Before = copyValues(e.Before)
	e.After = copyValues(e.After)
	if r.closed.Load() {
		r.dropped.Add(1)
		return e.ID
	}
	select {
	case r.queue <- Event{Hash: hash, Entry: e}:
	default:
		r.dropped.Add(1)
	}
	return e.ID
}

func copyValues(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	if len(values) > 256 {
		return map[string]string{"coverage": "Too many values; omitted"}
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func (r *Recorder) append(e Event) {
	e = redactEvent(e)
	data, err := json.Marshal(e)
	if err != nil || len(data) > maxRecordBytes {
		r.dropped.Add(1)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	r.events = append(r.events, e)
	// Reserve bytes for the assigned sequence and newline.
	size := int64(len(data) + 32)
	r.sizes = append(r.sizes, size)
	r.bytes += size
	r.pruneLocked()
}

func (r *Recorder) pruneLocked() {
	cutoff := r.opts.Now().Add(-r.opts.Retention)
	// Event timestamps can arrive out of order. Filter all expired entries.
	keep := 0
	for i, e := range r.events {
		if e.At.Before(cutoff) {
			r.bytes -= r.sizes[i]
			r.status.Pruned++
			continue
		}
		r.events[keep], r.sizes[keep] = e, r.sizes[i]
		keep++
	}
	clear(r.events[keep:])
	r.events, r.sizes = r.events[:keep], r.sizes[:keep]
	drop := 0
	for drop < len(r.events) && (r.bytes > r.opts.MaxBytes-1024 || len(r.events)-drop > maxRecordedEvents) {
		r.bytes -= r.sizes[drop]
		drop++
		r.status.Pruned++
	}
	if drop > 0 {
		copy(r.events, r.events[drop:])
		clear(r.events[len(r.events)-drop:])
		r.events = r.events[:len(r.events)-drop]
		copy(r.sizes, r.sizes[drop:])
		r.sizes = r.sizes[:len(r.sizes)-drop]
	}
}

func (r *Recorder) Snapshot() Recording {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	st := r.status
	st.Dropped = r.dropped.Load()
	st.Pending = len(r.queue)
	for _, e := range r.events {
		if e.Seq > st.DurableThrough {
			st.Pending++
		}
	}
	events := make([]Event, len(r.events))
	for i, e := range r.events {
		e.Before = copyValues(e.Before)
		e.After = copyValues(e.After)
		events[i] = e
	}
	return Recording{RecorderVersion, events, st, []string{
		"Sequence is recorder ingestion order, not causal order. Only causeId establishes an explicit request/result link; observations do not identify an actor.",
		"Coverage is bounded by age, bytes and event count. A missing predecessor, configuration or checkpoint may have expired; do not infer missing state.",
		"Checkpoints sample state, not every byte or intermediate transition. Unfocused peer lists and peer IPs are not recorded.",
		"Pending events may be lost on crash. Recorder startup, disconnects, dropped input and storage failures leave gaps.",
	}}
}

func (r *Recorder) SetRetention(retention time.Duration) {
	if r == nil || retention < 0 {
		return
	}
	if retention == 0 {
		retention = 24 * time.Hour
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts.Retention = retention
	r.status.RetentionSeconds = int64(retention.Seconds())
	r.pruneLocked()
}

func (r *Recorder) run() {
	defer close(r.done)
	defer func() {
		if r.lockFile != nil {
			_ = syscall.Flock(int(r.lockFile.Fd()), syscall.LOCK_UN)
			_ = r.lockFile.Close()
		}
	}()
	tick := time.NewTicker(r.opts.FlushInterval)
	defer tick.Stop()
	lastDropped := r.dropped.Load()
	gap := func() {
		if n := r.dropped.Load(); n != lastDropped {
			r.append(Event{Entry: Entry{ID: r.nextID(), At: r.opts.Now(), Phase: "gap", Actor: "recorder", Action: "dropped_input", Message: fmt.Sprintf("%d events or session samples could not be recorded", n-lastDropped)}})
			lastDropped = n
		}
	}
	drain := func() {
		select {
		case sample := <-r.samples:
			r.observe(sample)
		default:
		}
		for i := 0; i < cap(r.queue); i++ {
			select {
			case e := <-r.queue:
				r.append(e)
			default:
				return
			}
		}
	}
	for {
		select {
		case e := <-r.queue:
			r.append(e)
		case sample := <-r.samples:
			r.observe(sample)
		case <-tick.C:
			gap()
			_ = r.persist()
		case result := <-r.flush:
			drain()
			gap()
			result <- r.persist()
		case <-r.stop:
			drain()
			gap()
			_ = r.persist()
			return
		}
	}
}

// Flush and Close are bounded by the caller, including a blocked filesystem.
func (r *Recorder) Flush(ctx context.Context) error {
	result := make(chan error, 1)
	select {
	case r.flush <- result:
	case <-r.done:
		return errors.New("recorder closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) Close(ctx context.Context) error {
	if r.closed.CompareAndSwap(false, true) {
		close(r.stop)
	}
	select {
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.status.Error != "" {
			return errors.New(r.status.Error)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) persist() error {
	if r.loadFailed {
		return errors.New("recording load failed")
	}
	r.mu.Lock()
	r.pruneLocked()
	if r.seq == r.status.DurableThrough && r.status.Error == "" && r.status.Pruned == r.lastWrittenPruned {
		r.mu.Unlock()
		return nil
	}
	header := diskHeader{RecorderVersion, r.seq, r.dropped.Load(), r.status.Pruned, r.opts.Now()}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	err := enc.Encode(header)
	for _, e := range r.events {
		if err == nil {
			err = enc.Encode(e)
		}
	}
	r.mu.Unlock()
	if err == nil && int64(buf.Len()) > r.opts.MaxBytes {
		err = errors.New("recording exceeds byte bound")
	}
	if err == nil {
		err = r.opts.Write(r.opts.Path, buf.Bytes())
	}
	r.mu.Lock()
	if err != nil {
		firstFailure := r.status.Error == ""
		r.status.Error = "Recording could not be saved; disk may be full or unavailable. New evidence is not durable."
		r.mu.Unlock()
		if firstFailure {
			r.append(Event{Entry: Entry{ID: r.nextID(), At: r.opts.Now(), Phase: "gap", Actor: "recorder", Action: "storage_failure", Message: "Persistence failed. Evidence accumulated only in memory until a later successful save."}})
		}
		return err
	}
	r.status.Error = ""
	r.status.DurableThrough = header.Sequence
	r.status.LastPersistedAt = &header.WrittenAt
	r.lastWrittenPruned = header.Pruned
	r.mu.Unlock()
	return nil
}

func (r *Recorder) load() error {
	f, err := os.Open(r.opts.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return err
	} else if st.Size() > 256<<20 {
		return errors.New("recording exceeds recovery input bound")
	}
	// Allow reading an older larger configured bound, but never unbounded input.
	scanner := bufio.NewScanner(io.LimitReader(f, 256<<20))
	scanner.Buffer(make([]byte, 4096), maxRecordBytes+1024)
	if !scanner.Scan() {
		return errors.New("missing recorder header")
	}
	var header diskHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil || header.Version != RecorderVersion {
		return errors.New("unsupported or invalid recording header")
	}
	r.seq, r.status.DurableThrough = header.Sequence, header.Sequence
	r.dropped.Store(header.Dropped)
	r.status.Pruned, r.status.LastPersistedAt = header.Pruned, &header.WrittenAt
	r.lastWrittenPruned = header.Pruned
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			// Only a torn final record can be recovered; never skip middle corruption.
			if scanner.Scan() {
				return errors.New("corrupt recording before final line")
			}
			r.dropped.Add(1)
			break
		}
		if e.ID == "" || e.At.IsZero() || e.Seq > header.Sequence {
			return errors.New("invalid recorded event")
		}
		e = redactEvent(e)
		r.events = append(r.events, e)
		size := int64(len(scanner.Bytes()) + 32)
		r.sizes = append(r.sizes, size)
		r.bytes += size
		r.pruneLocked()
	}
	return scanner.Err()
}

// A per-path lock prevents two processes from replacing the same recording.
// While holding it, stale temporary rewrites from a crash can be removed safely.
func (r *Recorder) lock() error {
	if err := os.MkdirAll(filepath.Dir(r.opts.Path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.opts.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return err
	}
	r.lockFile = f
	entries, err := os.ReadDir(filepath.Dir(r.opts.Path))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "."+filepath.Base(r.opts.Path)+".tmp-") {
			if err := os.Remove(filepath.Join(filepath.Dir(r.opts.Path), entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeRecording(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
