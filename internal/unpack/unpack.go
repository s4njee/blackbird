// Package unpack implements PAR-3.4 unpack-on-completion: when a completed
// torrent matches an unpack rule, its archives (.zip, .rar including
// multi-part) are extracted with an external 7z-compatible binary, either in
// place or under a configured extract root. Extraction runs in a bounded,
// low-priority worker pool; results and throttled progress land in the
// per-torrent history log, and a failed extraction leaves a .failed marker
// next to the partial output. Archive listings are validated in Go before
// extraction so entries escaping the destination (zip-slip) are refused.
package unpack

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
)

const (
	// queueCap bounds pending unpack jobs. The automation hook must never
	// block, so an overflowing queue drops the job and logs.
	queueCap = 64
	// failedMarker is written next to partial output on extraction failure.
	failedMarker = ".failed"
	// progressStep records a history milestone every N percent.
	progressStep = 25
)

// Options configures the Service.
type Options struct {
	// CleanupGuard protects watched source archives from automatic deletion.
	CleanupGuard func(string, func() error) error

	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// History records progress and results on the torrent's Logger tab.
	History *history.Log
	// Runner extracts archives (7z in production, fake in tests).
	Runner Runner
	// Config returns the live unpack section (rules re-read per job, so
	// Settings saves and SIGHUP reloads apply without a restart).
	Config func() config.UnpackConfig
	// Snapshot returns live session rows for path resolution.
	Snapshot func() []rtorrent.Torrent
	// Roots returns the configured download roots; extract roots must stay
	// inside them, like the PAR-2.2 move engine.
	Roots func() []string
}

// Runner extracts one archive. List feeds the zip-slip check; Extract
// reports progress percentages (0-100) as it runs.
type Runner interface {
	// Available reports whether extraction can run at all.
	Available() (binary string, ok bool)
	// List returns the archive's entry paths.
	List(ctx context.Context, archive string) ([]string, error)
	// Extract unpacks archive into dest (created if needed).
	Extract(ctx context.Context, archive, dest string, progress func(pct int)) error
}

// Job is one torrent awaiting extraction.
type Job struct {
	Hash string
	Name string
}

// JobStatus is one in-flight extraction for the status endpoint.
type JobStatus struct {
	Hash      string
	Name      string
	Rule      string
	Archive   string
	DestDir   string
	Percent   int
	StartedAt time.Time
}

// Status is the service overview served by GET /api/unpack.
type Status struct {
	Available bool
	Binary    string
	Workers   int
	Queue     int
	Jobs      []JobStatus
}

// Service extracts completed torrents on a bounded worker pool.
type Service struct {
	opts    Options
	workers int
	timeout time.Duration

	mu     sync.Mutex
	queue  chan Job
	active map[string]*JobStatus
}

// New builds a Service. Enqueue is safe before Run.
func New(opts Options) *Service {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	s := &Service{
		opts:   opts,
		queue:  make(chan Job, queueCap),
		active: map[string]*JobStatus{},
	}
	cfg := config.UnpackConfig{}
	if opts.Config != nil {
		cfg = opts.Config()
	}
	s.workers = cfg.EffectiveWorkers()
	s.timeout = cfg.EffectiveTimeout()
	return s
}

// Enqueue hands a completed torrent to the pool. Non-blocking: the caller
// runs inside the automation worker, which must stay responsive.
func (s *Service) Enqueue(job Job) {
	select {
	case s.queue <- job:
	default:
		s.opts.Log.Warn("unpack: queue full, dropping completed torrent", "hash", job.Hash, "name", job.Name)
	}
}

// QueueDepth reports pending jobs (status endpoint, tests).
func (s *Service) QueueDepth() int { return len(s.queue) }

// Run starts the worker pool until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-s.queue:
					s.processJob(ctx, job)
				}
			}
		}()
	}
	wg.Wait()
}

// Status snapshots availability, pool depth, and in-flight jobs.
func (s *Service) Status() Status {
	binary, ok := "", false
	if s.opts.Runner != nil {
		binary, ok = s.opts.Runner.Available()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]JobStatus, 0, len(s.active))
	for _, st := range s.active {
		jobs = append(jobs, *st)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].StartedAt.Before(jobs[j].StartedAt) })
	return Status{Available: ok, Binary: binary, Workers: s.workers, Queue: len(s.queue), Jobs: jobs}
}

// processJob resolves one torrent to archives and extracts them.
func (s *Service) processJob(ctx context.Context, job Job) {
	cfg := config.UnpackConfig{}
	if s.opts.Config != nil {
		cfg = s.opts.Config()
	}
	row, ok := s.findRow(job.Hash)
	if !ok {
		s.opts.Log.Warn("unpack: torrent left the session before extraction", "hash", job.Hash, "name", job.Name)
		return
	}
	rule, ok := matchRule(cfg.Rules, row.Label)
	if !ok {
		return
	}
	name := row.Name
	if name == "" {
		name = job.Name
	}

	if s.opts.Runner == nil {
		s.fail(job.Hash, name, rule.Name, "", "unpack extractor is not configured")
		return
	}
	if _, ok := s.opts.Runner.Available(); !ok {
		s.fail(job.Hash, name, rule.Name, "", "unpack extractor not found: install p7zip (container/host Linux) or sevenzip (macOS) and ensure 7z is on PATH")
		return
	}

	info, err := os.Stat(row.BasePath)
	if err != nil {
		s.fail(job.Hash, name, rule.Name, "", "data path is missing: "+row.BasePath)
		return
	}
	archives, err := findArchives(row.BasePath, info)
	if err != nil {
		s.fail(job.Hash, name, rule.Name, "", "scan failed: "+err.Error())
		return
	}
	if len(archives) == 0 {
		s.opts.Log.Info("unpack: nothing to extract", "rule", rule.Name, "hash", job.Hash, "name", name)
		return
	}

	var destRoot string
	if rule.Destination != "" {
		roots := []string{}
		if s.opts.Roots != nil {
			roots = s.opts.Roots()
		}
		if err := rtorrent.CheckWithin(rule.Destination, roots); err != nil {
			s.fail(job.Hash, name, rule.Name, "", "extract root refused: "+err.Error())
			return
		}
		destRoot = filepath.Join(rule.Destination, safeName(name))
	}

	jobCtx, cancel := context.WithTimeout(ctx, cfg.EffectiveTimeout())
	defer cancel()

	s.opts.Log.Info("unpack: started", "rule", rule.Name, "hash", job.Hash, "name", name, "archives", len(archives))
	s.record(job.Hash, name, rule.Name, "unpack", "ok", fmt.Sprintf("extracting %d archive(s)", len(archives)))

	failed := 0
	for _, archive := range archives {
		destDir := filepath.Dir(archive)
		if destRoot != "" {
			destDir = destRoot
		}
		s.track(job, name, rule.Name, archive, destDir)
		if err := s.extractOne(jobCtx, job.Hash, name, rule, archive, destDir); err != nil {
			failed++
		}
		s.untrack(job.Hash)
	}
	if failed == 0 {
		s.opts.Log.Info("unpack: completed", "rule", rule.Name, "hash", job.Hash, "name", name)
	}
}

// extractOne lists, validates, extracts, and cleans up a single archive.
func (s *Service) extractOne(ctx context.Context, hash, name string, rule config.UnpackRule, archive, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return s.failArchive(hash, name, rule, archive, destDir, "cannot create destination: "+err.Error())
	}
	entries, err := s.opts.Runner.List(ctx, archive)
	if err != nil {
		return s.failArchive(hash, name, rule, archive, destDir, "listing failed: "+err.Error())
	}
	if err := validateEntries(entries); err != nil {
		return s.failArchive(hash, name, rule, archive, destDir, "refused unsafe archive: "+err.Error())
	}

	milestone := progressStep
	err = s.opts.Runner.Extract(ctx, archive, destDir, func(pct int) {
		// Milestones cover genuine incremental progress; the completion
		// entry below covers 100% so fast jobs stay quiet.
		if pct < 100 {
			for milestone <= pct && milestone < 100 {
				s.record(hash, name, rule.Name, "unpack-progress", "ok", fmt.Sprintf("%s: %d%%", filepath.Base(archive), milestone))
				milestone += progressStep
			}
		}
		s.mu.Lock()
		if st := s.active[hash]; st != nil && pct > st.Percent {
			st.Percent = pct
		}
		s.mu.Unlock()
	})
	if err != nil {
		return s.failArchive(hash, name, rule, archive, destDir, "extraction failed: "+err.Error())
	}

	// Success clears any stale failure marker from an earlier attempt.
	if err := os.Remove(filepath.Join(destDir, failedMarker)); err != nil && !os.IsNotExist(err) {
		s.opts.Log.Warn("unpack: cannot clear stale failure marker", "dir", destDir, "err", err)
	}
	s.record(hash, name, rule.Name, "unpack", "ok", fmt.Sprintf("extracted %s to %s", filepath.Base(archive), destDir))
	s.opts.Log.Info("unpack: archive extracted", "rule", rule.Name, "archive", archive, "dest", destDir)

	if rule.DeleteArchives {
		cleanup := func() error {
			for _, victim := range archiveFamily(archive) {
				if err := os.Remove(victim); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		var err error
		if s.opts.CleanupGuard != nil {
			err = s.opts.CleanupGuard(hash, cleanup)
		} else {
			err = cleanup()
		}
		if err != nil {
			s.record(hash, name, rule.Name, "archive_cleanup", "failed", err.Error())
		}
	}
	return nil
}

// failArchive writes the .failed marker, records history, and returns the error.
func (s *Service) failArchive(hash, name string, rule config.UnpackRule, archive, destDir, reason string) error {
	marker := failedMarkerContent(name, hash, rule.Name, archive, reason)
	if err := os.WriteFile(filepath.Join(destDir, failedMarker), []byte(marker), 0o644); err != nil {
		s.opts.Log.Warn("unpack: cannot write failure marker", "dir", destDir, "err", err)
	}
	s.record(hash, name, rule.Name, "unpack", "failed", fmt.Sprintf("%s: %s", filepath.Base(archive), reason))
	s.opts.Log.Warn("unpack: archive failed", "rule", rule.Name, "archive", archive, "err", reason)
	return fmt.Errorf("%s", reason)
}

// fail records a job-level failure (no archive was attempted).
func (s *Service) fail(hash, name, rule, archive, reason string) {
	s.record(hash, name, rule, "unpack", "failed", reason)
	s.opts.Log.Warn("unpack: job failed", "rule", rule, "hash", hash, "name", name, "err", reason)
}

// record writes one history entry for an unpack outcome.
func (s *Service) record(hash, name, rule, action, result, message string) {
	if s.opts.History == nil {
		return
	}
	s.opts.History.Add(hash, history.Entry{
		Kind: history.KindAction, Actor: "unpack", Action: action, Result: result,
		Message: fmt.Sprintf("rule %q: %s", rule, message), Name: name,
	})
}

// track/untrack publish in-flight progress for the status endpoint.
func (s *Service) track(job Job, name, rule, archive, destDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[job.Hash] = &JobStatus{
		Hash: job.Hash, Name: name, Rule: rule,
		Archive: archive, DestDir: destDir, StartedAt: time.Now(),
	}
}

func (s *Service) untrack(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, hash)
}

// findRow resolves the torrent's fresh session row (post-move paths).
func (s *Service) findRow(hash string) (rtorrent.Torrent, bool) {
	if s.opts.Snapshot == nil {
		return rtorrent.Torrent{}, false
	}
	for _, t := range s.opts.Snapshot() {
		if t.Hash == hash {
			return t, true
		}
	}
	return rtorrent.Torrent{}, false
}

// matchRule returns the first rule matching the torrent label.
func matchRule(rules []config.UnpackRule, label string) (config.UnpackRule, bool) {
	for _, r := range rules {
		if r.Matches(label) {
			return r, true
		}
	}
	return config.UnpackRule{}, false
}

// failedMarkerContent renders the .failed marker left beside partial output.
func failedMarkerContent(name, hash, rule, archive, reason string) string {
	return "blackbird unpack failed\n" +
		"torrent: " + name + " (" + hash + ")\n" +
		"rule: " + rule + "\n" +
		"archive: " + archive + "\n" +
		"at: " + time.Now().UTC().Format(time.RFC3339) + "\n" +
		"error: " + reason + "\n"
}

// safeName sanitizes a torrent name for use as a directory name.
func safeName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "data"
	}
	return cleaned
}

// --- Archive discovery ---

var (
	partNumRe = regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)
	rarPartRe = regexp.MustCompile(`(?i)\.r\d\d$`)
)

// isArchiveHead reports whether path starts an extractable set: any .zip,
// any .rar except continuation volumes (.r00+, .part2+).
func isArchiveHead(path string) bool {
	lower := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(lower, ".zip") {
		return true
	}
	if m := partNumRe.FindStringSubmatch(lower); m != nil {
		n := 0
		fmt.Sscanf(m[1], "%d", &n)
		return n <= 1
	}
	if strings.HasSuffix(lower, ".rar") {
		return true
	}
	return false
}

// findArchives collects extractable archive heads under base. A base file is
// only a candidate itself; a base directory is walked recursively. Symlinks
// are skipped so extraction cannot be redirected.
func findArchives(base string, info os.FileInfo) ([]string, error) {
	if !info.IsDir() {
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, nil
		}
		if isArchiveHead(base) {
			return []string{base}, nil
		}
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if isArchiveHead(path) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// archiveFamily returns the on-disk files belonging to one archive head:
// the head plus multi-part siblings (X.r00… for X.rar, X.partN.rar for
// part-sets). Only existing files are returned.
func archiveFamily(head string) []string {
	dir := filepath.Dir(head)
	base := filepath.Base(head)
	lower := strings.ToLower(base)
	out := []string{head}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	var match func(name string) bool
	if m := partNumRe.FindStringSubmatch(lower); m != nil {
		stem := lower[:len(lower)-len(m[0])]
		match = func(name string) bool {
			l := strings.ToLower(name)
			if !strings.HasPrefix(l, stem+".part") || !strings.HasSuffix(l, ".rar") {
				return false
			}
			return partNumRe.MatchString(l)
		}
	} else if strings.HasSuffix(lower, ".rar") {
		stem := lower[:len(lower)-len(".rar")]
		match = func(name string) bool {
			l := strings.ToLower(name)
			return strings.HasPrefix(l, stem+".r") && rarPartRe.MatchString(l[strings.LastIndex(l, "."):])
		}
	} else {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == base {
			continue
		}
		if match(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// --- Zip-slip validation ---

// validateEntries refuses archives whose entries escape the destination:
// parent-directory components, absolute paths, or drive-letter paths.
func validateEntries(entries []string) error {
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if isAbsEntry(trimmed) {
			return fmt.Errorf("absolute entry path %q", entry)
		}
		if len(trimmed) >= 2 && trimmed[1] == ':' {
			return fmt.Errorf("drive-letter entry path %q", entry)
		}
		for _, part := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' }) {
			if part == ".." {
				return fmt.Errorf("parent-directory entry path %q", entry)
			}
		}
	}
	return nil
}

func isAbsEntry(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}
