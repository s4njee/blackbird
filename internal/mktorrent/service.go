package mktorrent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

// Bounded background execution: at most Workers creations hash at once;
// terminal jobs are retained for download and post-hoc session adds.
const (
	DefaultWorkers = 2
	DefaultRetain  = 20
)

// Daemon is the slice of the rtorrent client creation needs: loading a
// finished .torrent into the session.
type Daemon interface {
	AddTorrentFile(ctx context.Context, data []byte, opts rtorrent.AddOptions) error
}

// Spec is one creation request: the build input plus session handling.
type Spec struct {
	Input
	AddToSession bool
	Start        bool // start immediately when adding (default true)
	Label        string
}

// Options configures the Service.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon loads finished torrents into the session; nil disables adds.
	Daemon Daemon
	// History records creations and session adds; nil disables logging.
	History *history.Log
	// Roots returns the configured download roots (the safety boundary).
	Roots func() []string
	// Workers bounds concurrent hashing; <=0 means DefaultWorkers.
	Workers int
	// Retain caps kept terminal jobs; <=0 means DefaultRetain.
	Retain int
}

// Job is one creation's observable state. Data travels only through the
// download/add paths, never the status payload.
type Job struct {
	ID           string   `json:"id"`
	Status       string   `json:"status"` // running | completed | failed | cancelled
	Source       string   `json:"source"`
	Name         string   `json:"name"`
	Trackers     []string `json:"trackers"`
	Private      bool     `json:"private"`
	TotalBytes   int64    `json:"totalBytes"`
	FileCount    int      `json:"fileCount"`
	PieceLength  int64    `json:"pieceLength"`
	PieceCount   int      `json:"pieceCount"`
	BytesHashed  int64    `json:"bytesHashed"`
	PiecesDone   int      `json:"piecesDone"`
	CurrentFile  string   `json:"currentFile"`
	Infohash     string   `json:"infohash"`
	TorrentSize  int64    `json:"torrentSize"`
	Error        string   `json:"error,omitempty"`
	Added        bool     `json:"added"`
	AddedHash    string   `json:"addedHash,omitempty"`
	AddError     string   `json:"addError,omitempty"`
	AddToSession bool     `json:"addToSession"`

	actor    string // history actor captured at submit
	resolved string // symlink-resolved source (add-time directory base)
	multi    bool
	addNow   bool   // session add requested at submit
	start    bool   // start immediately on session add
	label    string // label applied on session add
	data     []byte
	cancel   context.CancelFunc
}

// snapshot copies the observable state (never the torrent bytes).
func (j *Job) snapshot() Job {
	cp := *j
	cp.data = nil
	cp.cancel = nil
	if cp.Trackers == nil {
		cp.Trackers = []string{}
	}
	return cp
}

// ErrJobUnknown reports an add/status against an unknown (evicted or
// never-existing) creation job.
var ErrJobUnknown = errors.New("unknown creation job")

// JobStateError reports an add against a job that has not completed.
type JobStateError struct {
	Status string
}

func (e *JobStateError) Error() string { return fmt.Sprintf("creation job is %s", e.Status) }

// Service runs creations on a bounded worker pool and retains results.
type Service struct {
	opts Options
	sem  chan struct{}

	mu   sync.Mutex
	jobs map[string]*Job
	seq  uint64
}

// New builds a Service.
func New(opts Options) *Service {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = DefaultWorkers
	}
	retain := opts.Retain
	if retain <= 0 {
		retain = DefaultRetain
	}
	opts.Workers, opts.Retain = workers, retain
	return &Service{opts: opts, sem: make(chan struct{}, workers), jobs: map[string]*Job{}}
}

func (s *Service) roots() []string {
	if s.opts.Roots == nil {
		return nil
	}
	return s.opts.Roots()
}

// Submit validates and queues one creation, returning its job snapshot.
// Validation and source resolution happen synchronously so bad requests
// fail fast with 400; hashing runs in the background.
func (s *Service) Submit(spec Spec, actor string) (Job, error) {
	in := spec.Input
	if err := Validate(in); err != nil {
		return Job{}, err
	}
	resolved, err := filepath.EvalSymlinks(in.Source)
	if err != nil {
		return Job{}, err
	}
	if err := rtorrent.CheckWithin(resolved, s.roots()); err != nil {
		return Job{}, err
	}
	files, multi, err := Collect(resolved)
	if err != nil {
		return Job{}, err
	}
	if TotalBytes(files) <= 0 {
		return Job{}, fmt.Errorf("nothing to package: source holds no data")
	}
	name := in.Name
	if name == "" {
		name = filepath.Base(filepath.Clean(resolved))
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:     fmt.Sprintf("create-%d", atomic.AddUint64(&s.seq, 1)),
		Status: "running", Source: in.Source, Name: name,
		Trackers: append([]string(nil), in.Trackers...), Private: in.Private,
		TotalBytes: TotalBytes(files), FileCount: len(files),
		AddToSession: spec.AddToSession,
		actor:        actor, resolved: resolved, multi: multi,
		addNow: spec.AddToSession, start: spec.Start, label: spec.Label,
		cancel: cancel,
	}

	s.mu.Lock()
	s.jobs[job.ID] = job
	s.evictLocked()
	// Copy under the lock and before the worker starts: run's progress
	// callback writes these fields under s.mu, so snapshotting after the
	// "go" below raced the goroutine this call had just launched.
	snap := job.snapshot()
	s.mu.Unlock()

	go s.run(ctx, job, in, files)
	return snap, nil
}

// evictLocked drops the oldest terminal jobs beyond Retain. Caller holds mu.
func (s *Service) evictLocked() {
	var terminal []*Job
	for _, j := range s.jobs {
		if j.Status != "running" {
			terminal = append(terminal, j)
		}
	}
	if len(terminal) <= s.opts.Retain {
		return
	}
	sort.Slice(terminal, func(a, b int) bool { return terminal[a].ID < terminal[b].ID })
	for _, j := range terminal[:len(terminal)-s.opts.Retain] {
		delete(s.jobs, j.ID)
	}
}

func (s *Service) run(ctx context.Context, job *Job, in Input, files []File) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		s.finish(job, "cancelled", "")
		return
	}
	// Re-check the safety boundary: roots may have changed while queued.
	if err := rtorrent.CheckWithin(job.resolved, s.roots()); err != nil {
		s.finish(job, "failed", err.Error())
		return
	}
	res, err := Build(ctx, in, files, job.multi, func(p Progress) {
		s.mu.Lock()
		job.BytesHashed, job.PiecesDone = p.BytesHashed, p.PiecesDone
		job.PieceCount, job.CurrentFile = p.PieceCount, p.CurrentFile
		s.mu.Unlock()
	})
	if err != nil {
		if ctx.Err() != nil {
			s.finish(job, "cancelled", "")
		} else {
			s.finish(job, "failed", err.Error())
		}
		return
	}
	s.mu.Lock()
	job.data = res.Data
	job.Infohash, job.TorrentSize = res.Infohash, int64(len(res.Data))
	job.PieceLength, job.PieceCount = res.PieceLength, res.PieceCount
	job.BytesHashed, job.PiecesDone = res.TotalBytes, res.PieceCount
	s.mu.Unlock()

	s.log(job, history.Entry{
		Kind: history.KindAction, Actor: job.actor, Action: "create_torrent", Result: "ok",
		Message: fmt.Sprintf("%d files, %d bytes, %d pieces", res.FileCount, res.TotalBytes, res.PieceCount),
		Name:    res.Name,
	})

	if job.addNow {
		s.addLocked(job, res)
	}
	s.finish(job, "completed", "")
}

// finish marks a job terminal. Caller must not hold mu.
func (s *Service) finish(job *Job, status, detail string) {
	s.mu.Lock()
	job.Status = status
	job.Error = detail
	job.CurrentFile = ""
	s.evictLocked()
	s.mu.Unlock()
	if status == "failed" && detail != "" {
		s.opts.Log.Warn("mktorrent: creation failed", "id", job.ID, "source", job.Source, "err", detail)
		s.log(job, history.Entry{
			Kind: history.KindAction, Actor: job.actor, Action: "create_torrent", Result: "failed",
			Message: detail, Name: job.Name,
		})
	}
}

func (s *Service) log(job *Job, e history.Entry) {
	if s.opts.History == nil {
		return
	}
	s.opts.History.Add(job.Infohash, e)
}

// Status returns a job snapshot, or false when unknown (finished long ago
// and evicted, or never existed).
func (s *Service) Status(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return job.snapshot(), true
}

// Cancel aborts a running job; terminal jobs are returned unchanged.
func (s *Service) Cancel(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	if job.Status == "running" {
		job.cancel()
	}
	return job.snapshot(), true
}

// Data returns the finished .torrent bytes for download.
func (s *Service) Data(id string) (data []byte, name string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, found := s.jobs[id]
	if !found || job.Status != "completed" {
		return nil, "", false
	}
	return job.data, job.Name, true
}

// Add loads a finished job's .torrent into the session, tied to the source
// path (its parent becomes the download directory so the data is found),
// optionally starting it. It records the add in the history.
func (s *Service) Add(id string, start bool, label, actor string) (hash string, err error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return "", ErrJobUnknown
	}
	if job.Status != "completed" {
		s.mu.Unlock()
		return "", &JobStateError{Status: job.Status}
	}
	data, resolved, name := job.data, job.resolved, job.Name
	s.mu.Unlock()

	if s.opts.Daemon == nil {
		return "", fmt.Errorf("session adds are unavailable")
	}
	dir := filepath.Dir(resolved)
	if err := rtorrent.CheckWithin(dir, s.roots()); err != nil {
		return "", fmt.Errorf("cannot tie to source path: %w", err)
	}
	opts := rtorrent.AddOptions{Start: start}
	opts.ExtraCommands = append(opts.ExtraCommands, "d.directory.set="+dir)
	if strings.TrimSpace(label) != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.custom1.set="+label)
	}
	// Re-resolve the source in case it moved mid-hash; the daemon needs the
	// data where the directory points.
	if _, serr := os.Stat(resolved); serr != nil {
		return "", fmt.Errorf("source is gone: %w", serr)
	}
	if err := s.opts.Daemon.AddTorrentFile(context.Background(), data, opts); err != nil {
		s.mu.Lock()
		job.AddError = err.Error()
		s.mu.Unlock()
		return "", err
	}
	infohash := job.Infohash
	if meta, merr := torrentfile.Parse(data); merr == nil && meta.Infohash != "" {
		infohash = meta.Infohash
	}
	s.mu.Lock()
	job.Added, job.AddedHash, job.AddError = true, infohash, ""
	s.mu.Unlock()
	if s.opts.History != nil {
		s.opts.History.Add(infohash, history.Entry{
			Kind: history.KindAdd, Actor: actor, Action: "add",
			Result: "ok", Message: "created from " + job.Source, Name: name,
		})
	}
	return infohash, nil
}

// addLocked performs the at-creation session add (job already holds its
// result). Caller must not hold mu.
func (s *Service) addLocked(job *Job, res Result) {
	if s.opts.Daemon == nil {
		s.mu.Lock()
		job.AddError = "session adds are unavailable"
		s.mu.Unlock()
		return
	}
	dir := filepath.Dir(job.resolved)
	if err := rtorrent.CheckWithin(dir, s.roots()); err != nil {
		s.mu.Lock()
		job.AddError = fmt.Sprintf("cannot tie to source path: %s", err)
		s.mu.Unlock()
		return
	}
	opts := rtorrent.AddOptions{Start: job.start}
	opts.ExtraCommands = append(opts.ExtraCommands, "d.directory.set="+dir)
	if strings.TrimSpace(job.label) != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.custom1.set="+job.label)
	}
	if err := s.opts.Daemon.AddTorrentFile(context.Background(), res.Data, opts); err != nil {
		s.mu.Lock()
		job.AddError = err.Error()
		s.mu.Unlock()
		s.opts.Log.Warn("mktorrent: session add failed", "id", job.ID, "err", err)
		return
	}
	s.mu.Lock()
	job.Added, job.AddedHash = true, res.Infohash
	s.mu.Unlock()
	if s.opts.History != nil {
		s.opts.History.Add(res.Infohash, history.Entry{
			Kind: history.KindAdd, Actor: job.actor, Action: "add",
			Result: "ok", Message: "created from " + job.Source, Name: res.Name,
		})
	}
}
