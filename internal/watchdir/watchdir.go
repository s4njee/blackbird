// Package watchdir implements PAR-3.1 watch directories: .torrent files
// dropped into configured directories are loaded into rTorrent with the same
// load.* + trailing-command semantics as the Add API, then renamed to
// <name>.loaded or deleted per the entry's delete_after_load flag.
//
// Malformed or unreadable files are moved into the watch directory's failed/
// subdirectory with the rejection reason logged, and a torrent whose infohash
// was already loaded by this watcher is skipped without error spam.
//
// Delivery uses fsnotify where the filesystem supports it and falls back to a
// polled directory scan (network filesystems, bind mounts that do not deliver
// events) so a dropped file is never missed.
package watchdir

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

const (
	// DefaultPollInterval is the scan cadence when a directory cannot be
	// watched with fsnotify (network filesystems, some bind mounts) and the
	// entry has no explicit poll_interval.
	DefaultPollInterval = 5 * time.Second
	// settleDelay is how long a failed parse waits before re-reading the
	// file once: fsnotify fires on create, so a file caught mid-write would
	// otherwise be declared malformed from its partial content.
	settleDelay = 250 * time.Millisecond
	// maxTorrentSize bounds how large a .torrent file we will read.
	maxTorrentSize = 16 << 20
	// loadedSuffix marks consumed .torrent files.
	loadedSuffix = ".loaded"
	// failedDirName is the subdirectory (inside each watch directory) where
	// malformed or unreadable files are preserved with the reason logged.
	failedDirName = "failed"
	// loadedHashCap bounds how many recently loaded infohashes are remembered
	// for duplicate suppression.
	loadedHashCap = 4096
)

// Event is one watch-directory outcome, fanned out to WebSocket clients as a
// toast and recorded in the per-torrent history log.
type Event struct {
	At       time.Time `json:"at"`
	WatchDir string    `json:"watchDir"`
	File     string    `json:"file"` // the consumed file name (base)
	Kind     string    `json:"kind"` // loaded | duplicate | malformed | load_error | watch_error
	Hash     string    `json:"hash,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// Loader is the file-ingestion contract the watcher drives. The production
// implementation (DefaultLoad) parses metadata, logs to the per-torrent
// history, and loads through the rTorrent client; tests substitute a stub.
type Loader interface {
	Load(ctx context.Context, data []byte, entry config.WatchDir) LoadResult
}

// OnMeta receives parsed .torrent metadata for a successfully parsed file
// before it is loaded. The API layer uses it to back the General tab.
type OnMeta func(hash string, meta torrentfile.Meta)

// LoaderClient is the rTorrent client slice used by DefaultLoad.
type LoaderClient interface {
	AddTorrentFile(ctx context.Context, data []byte, opts rtorrent.AddOptions) error
}

// DefaultLoad is the standard loader wiring: it parses metadata, records the
// outcome in the per-torrent history log, then loads through the rTorrent
// client with the entry's trailing commands.
type DefaultLoad struct {
	Client  LoaderClient
	OnMeta  OnMeta
	History *history.Log
}

// LoadResult reports one file's ingestion outcome.
type LoadResult struct {
	Hash string
	Name string
	Err  error
}

// Load implements the watcher's file ingestion contract.
func (d DefaultLoad) Load(ctx context.Context, data []byte, entry config.WatchDir) LoadResult {
	var hash, name string
	if parsed, err := torrentfile.Parse(data); err == nil {
		hash, name = parsed.Infohash, parsed.Name
		if d.OnMeta != nil {
			d.OnMeta(hash, *parsed)
		}
	}
	opts := rtorrent.AddOptions{Start: entry.Starts()}
	if entry.Destination != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.directory.set="+entry.Destination)
	}
	if entry.Label != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.custom1.set="+entry.Label)
	}
	if err := d.Client.AddTorrentFile(ctx, data, opts); err != nil {
		if d.History != nil && hash != "" {
			d.History.Add(hash, history.Entry{
				Kind: history.KindAction, Actor: "watch", Action: "load",
				Result: "failed", Message: err.Error(), Name: name,
			})
		}
		return LoadResult{Hash: hash, Name: name, Err: err}
	}
	if d.History != nil && hash != "" {
		d.History.Add(hash, history.Entry{
			Kind: history.KindAdd, Actor: "watch", Action: "add",
			Result: "ok", Message: "watch directory: " + entry.Path, Name: name,
		})
	}
	return LoadResult{Hash: hash, Name: name}
}

// Dir is one configured watch directory with its runtime state.
type Dir struct {
	Entry     config.WatchDir
	PollEvery time.Duration
}

// Options configures a Watcher.
type Options struct {
	// Now is the clock, overridable for tests.
	Now func() time.Time
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
}

// Watcher supervises every configured watch directory. Entries are registered
// before Run; Run reloads the list on a cadence so config changes (Settings
// saves, SIGHUP) apply without a restart.
type Watcher struct {
	opts Options

	loaded *hashRing
	// applyNow wakes Run's reconcile loop as soon as the entry list changes
	// so Settings saves apply immediately instead of on the next tick.
	applyNow chan struct{}

	mu      sync.Mutex
	load    Loader
	entries []Dir
	source  func() []config.WatchDir
	subs    map[int]func(Event)
	nextSub int
}

// New builds a Watcher around a Loader (the production implementation is
// DefaultLoad, which records history and metadata as it loads).
func New(load Loader, opts Options) *Watcher {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Watcher{opts: opts, load: load, loaded: newHashRing(loadedHashCap), applyNow: make(chan struct{}, 1), subs: map[int]func(Event){}}
}

// SetSource installs a function that returns the live configured entries. When
// set, Run refreshes the entry list from it every second, so Settings saves
// and SIGHUP reloads apply without a restart.
func (w *Watcher) SetSource(fn func() []config.WatchDir) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.source = fn
}

// SetEntries swaps the full list of watched directories (called on config
// load/reload, or directly by tests) and wakes the reconcile loop.
func (w *Watcher) SetEntries(entries []config.WatchDir) {
	w.mu.Lock()
	w.setEntriesLocked(entries)
	w.mu.Unlock()
	w.signalApply()
}

// signalApply nudges the reconcile loop without blocking.
func (w *Watcher) signalApply() {
	select {
	case w.applyNow <- struct{}{}:
	default:
	}
}

func (w *Watcher) setEntriesLocked(entries []config.WatchDir) {
	next := make([]Dir, 0, len(entries))
	for _, e := range entries {
		next = append(next, Dir{Entry: e, PollEvery: e.EffectivePollInterval(DefaultPollInterval)})
	}
	w.entries = next
}

// refreshSource re-reads the configured entries through the source function
// (if any). Returns true when the list changed.
func (w *Watcher) refreshSource() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.source == nil {
		return false
	}
	entries := w.source()
	changed := len(entries) != len(w.entries)
	if !changed {
		for i := range entries {
			if entries[i] != w.entries[i].Entry {
				changed = true
				break
			}
		}
	}
	if changed {
		w.setEntriesLocked(entries)
		w.signalApply()
	}
	return changed
}

// Entries returns a snapshot of the current list.
func (w *Watcher) Entries() []config.WatchDir {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]config.WatchDir, len(w.entries))
	for i, d := range w.entries {
		out[i] = d.Entry
	}
	return out
}

// Dirs returns the runtime directory list (entry plus computed poll cadence).
func (w *Watcher) Dirs() []Dir {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Dir(nil), w.entries...)
}

// Subscribe registers a callback for every watch event (toast fan-out).
// Returns the unsubscribe function.
func (w *Watcher) Subscribe(fn func(Event)) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	id := w.nextSub
	w.nextSub++
	w.subs[id] = fn
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.subs, id)
	}
}

func (w *Watcher) emit(e Event) {
	if e.At.IsZero() {
		e.At = w.opts.Now()
	}
	w.mu.Lock()
	subs := make([]func(Event), 0, len(w.subs))
	for _, fn := range w.subs {
		subs = append(subs, fn)
	}
	w.mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
}

// Run supervises the watched directories until ctx is cancelled. Each
// directory runs its own goroutine (fsnotify watcher plus a poller that
// re-lists the directory when the event stream is unreliable). Registration
// diffs are applied every second so Settings changes land promptly.
func (w *Watcher) Run(ctx context.Context) {
	var mu sync.Mutex
	cancel := map[string]context.CancelFunc{}
	running := map[string]Dir{}

	apply := func() {
		w.refreshSource()
		mu.Lock()
		defer mu.Unlock()
		dirs := w.Dirs()
		next := map[string]bool{}
		for _, e := range dirs {
			path := e.Entry.Path
			next[path] = true
			prev, ok := running[path]
			if ok {
				if prev == e {
					continue
				}
				// Entry options changed (Settings save, SIGHUP): restart the
				// directory goroutine so the new options apply at once.
				cancel[path]()
				w.opts.Log.Info("watch directory updated", "path", path)
			} else {
				w.opts.Log.Info("watch directory enabled", "path", path)
			}
			dirCtx, stop := context.WithCancel(ctx)
			cancel[path] = stop
			running[path] = e
			go w.watchOne(dirCtx, e)
		}
		for path := range running {
			if next[path] {
				continue
			}
			cancel[path]()
			delete(cancel, path)
			delete(running, path)
			w.opts.Log.Info("watch directory disabled", "path", path)
		}
	}

	apply()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, stop := range cancel {
				stop()
			}
			mu.Unlock()
			return
		case <-ticker.C:
			apply()
		case <-w.applyNow:
			apply()
		}
	}
}

// watchOne supervises one directory for the lifetime of ctx. A poller always
// runs (immediate startup scan, then the entry's cadence); fsnotify is a
// best-effort accelerator layered on top. When fsnotify cannot attach (network
// filesystems), the watcher logs once and polling alone carries delivery.
func (w *Watcher) watchOne(ctx context.Context, entry Dir) {
	path := entry.Entry.Path
	if err := os.MkdirAll(path, 0o755); err != nil {
		w.emit(Event{WatchDir: path, Kind: "watch_error", Message: "cannot create watch directory: " + err.Error()})
		w.opts.Log.Warn("watch directory unavailable", "path", path, "err", err)
		return
	}

	inFlight := newSeenSet()

	// Polling fallback re-lists the directory every poll interval. When the
	// fsnotify watcher is running this still acts as a safety net for files
	// that appeared before the watcher attached or were missed while the
	// daemon was down.
	go w.pollLoop(ctx, entry, inFlight)

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		w.opts.Log.Warn("watch directory: fsnotify unavailable, polling only", "path", path, "err", err)
		<-ctx.Done()
		return
	}
	defer fw.Close()
	if err := fw.Add(path); err != nil {
		w.opts.Log.Warn("watch directory: fsnotify unavailable, polling only", "path", path, "err", err)
		<-ctx.Done()
		return
	}
	w.opts.Log.Info("watch directory: fsnotify active", "path", path)

	// fsnotify's platform reader blocks on its own syscall, so it cannot be
	// selected on with ctx. Closing the watcher from this goroutine unblocks
	// the reader and closes the Events channel; the loop below then exits.
	go func() {
		<-ctx.Done()
		fw.Close()
	}()

	for {
		select {
		case ev, ok := <-fw.Events:
			if !ok {
				<-ctx.Done()
				return
			}
			w.handleEvent(entry, inFlight, ev.Name, ev.Op)
		case err, ok := <-fw.Errors:
			if !ok {
				<-ctx.Done()
				return
			}
			w.opts.Log.Warn("watch directory: fsnotify error", "path", path, "err", err)
		}
	}
}

// pollLoop repeatedly scans the directory until ctx is cancelled.
func (w *Watcher) pollLoop(ctx context.Context, entry Dir, inFlight *seenSet) {
	ticker := time.NewTicker(entry.PollEvery)
	defer ticker.Stop()
	w.scan(entry, inFlight) // startup backlog / pre-existing files
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scan(entry, inFlight)
		}
	}
}

// handleEvent consumes a fsnotify event for a candidate file.
func (w *Watcher) handleEvent(entry Dir, inFlight *seenSet, name string, op fsnotify.Op) {
	if op&(fsnotify.Create|fsnotify.Write) == 0 {
		return
	}
	w.tryConsume(entry, inFlight, name)
}

// scan lists the directory and consumes any .torrent files not already seen
// or in flight. Files with the .loaded suffix are ignored (tryConsume also
// guards, so the fsnotify path and the poller share the same rules) and the
// failed/ subdirectory is skipped with the other directories.
func (w *Watcher) scan(entry Dir, inFlight *seenSet) {
	entries, err := os.ReadDir(entry.Entry.Path)
	if err != nil {
		w.opts.Log.Warn("watch directory scan failed", "path", entry.Entry.Path, "err", err)
		return
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		w.tryConsume(entry, inFlight, filepath.Join(entry.Entry.Path, de.Name()))
	}
}

// tryConsume loads one candidate file exactly once (guarded by the seen set),
// then renames or deletes it per the entry options. Files that are not .torrent
// files or that already carry the .loaded suffix are ignored so a rename does
// not feed back into the watcher. Parsed-but-already-loaded infohashes are
// skipped as duplicates; malformed files move to the failed/ subdirectory.
func (w *Watcher) tryConsume(entry Dir, inFlight *seenSet, path string) {
	base := filepath.Base(path)
	if !strings.HasSuffix(strings.ToLower(base), ".torrent") || strings.HasSuffix(base, loadedSuffix) {
		return
	}
	if !inFlight.tryAdd(path) {
		return
	}
	// A duplicate delivery (fsnotify racing the poller) for a file that has
	// already been consumed and renamed away must stay silent.
	if _, err := os.Stat(path); err != nil {
		inFlight.remove(path)
		return
	}
	w.opts.Log.Info("watch directory: loading", "path", entry.Entry.Path, "file", base)

	data, err := readLimited(path)
	if err != nil {
		w.emit(Event{WatchDir: entry.Entry.Path, File: base, Kind: "malformed", Message: "cannot read file: " + err.Error()})
		w.finishFailed(entry, inFlight, path, "cannot read file: "+err.Error())
		return
	}
	parsed, err := torrentfile.Parse(data)
	if err != nil {
		// The file may still be mid-write (fsnotify fires on create). Give
		// it one chance to settle before declaring it malformed; a genuinely
		// broken file fails again and moves on unchanged.
		time.Sleep(settleDelay)
		if again, readErr := readLimited(path); readErr == nil {
			if reparsed, parseErr := torrentfile.Parse(again); parseErr == nil {
				data, parsed, err = again, reparsed, nil
			}
		}
	}
	if err != nil {
		w.emit(Event{WatchDir: entry.Entry.Path, File: base, Kind: "malformed", Message: "not a valid .torrent: " + err.Error()})
		w.finishFailed(entry, inFlight, path, "not a valid .torrent: "+err.Error())
		return
	}
	if w.loaded.has(parsed.Infohash) {
		w.emit(Event{WatchDir: entry.Entry.Path, File: base, Kind: "duplicate", Hash: parsed.Infohash, Message: "already loaded in this session; skipped"})
		w.opts.Log.Info("watch directory: duplicate torrent skipped", "path", path, "hash", parsed.Infohash)
		w.finish(entry, inFlight, path)
		return
	}

	result := w.load.Load(context.Background(), data, entry.Entry)
	switch {
	case result.Err != nil:
		w.emit(Event{WatchDir: entry.Entry.Path, File: base, Kind: "load_error", Message: result.Err.Error()})
		w.opts.Log.Warn("watch directory: load failed", "path", path, "err", result.Err)
	default:
		w.loaded.add(parsed.Infohash)
		w.emit(Event{WatchDir: entry.Entry.Path, File: base, Kind: "loaded", Hash: parsed.Infohash, Message: "added to session"})
	}
	w.finish(entry, inFlight, path)
}

// finish renames the consumed file to <name>.loaded or deletes it and
// releases the path in the seen set.
func (w *Watcher) finish(entry Dir, inFlight *seenSet, path string) {
	defer inFlight.remove(path)
	if entry.Entry.DeleteAfterLoad {
		if err := os.Remove(path); err != nil {
			w.opts.Log.Warn("watch directory: delete after load failed", "path", path, "err", err)
		}
		return
	}
	dest := path + loadedSuffix
	if err := os.Rename(path, dest); err != nil {
		w.opts.Log.Warn("watch directory: rename to .loaded failed", "path", path, "err", err)
	}
}

// finishFailed moves a malformed or unreadable file into the watch
// directory's failed/ subdirectory (PAR-3.1) so it is neither retried every
// scan nor silently discarded; the reason is logged. When the subdirectory
// cannot be created or the move fails, it falls back to the .loaded rename so
// the watcher does not re-consume the same file forever.
func (w *Watcher) finishFailed(entry Dir, inFlight *seenSet, path, reason string) {
	defer inFlight.remove(path)
	failedDir := filepath.Join(entry.Entry.Path, failedDirName)
	if err := os.MkdirAll(failedDir, 0o755); err != nil {
		w.opts.Log.Warn("watch directory: cannot create failed directory", "dir", failedDir, "err", err)
		w.finish(entry, inFlight, path)
		return
	}
	base := filepath.Base(path)
	dest := filepath.Join(failedDir, base)
	for i := 1; ; i++ {
		if _, err := os.Lstat(dest); err != nil {
			break
		}
		dest = filepath.Join(failedDir, fmt.Sprintf("%s.%d", base, i))
	}
	if err := os.Rename(path, dest); err != nil {
		w.opts.Log.Warn("watch directory: move to failed directory failed", "path", path, "err", err)
		w.finish(entry, inFlight, path)
		return
	}
	w.opts.Log.Warn("watch directory: file moved to failed directory", "file", dest, "reason", reason)
}

// seenSet tracks files currently being processed or already consumed so a
// file delivered by both fsnotify and the polling fallback is loaded once.
type seenSet struct {
	mu    sync.Mutex
	known map[string]bool
}

func newSeenSet() *seenSet { return &seenSet{known: map[string]bool{}} }

func (s *seenSet) tryAdd(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.known[path] {
		return false
	}
	s.known[path] = true
	return true
}

// remove releases a consumed path so a file dropped later under the same
// name is processed again. Safe because the caller only removes after the
// file has been renamed or deleted; a stale duplicate delivery for the same
// path finds no file and exits silently (see tryConsume).
func (s *seenSet) remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.known, path)
}

// hashRing is a bounded set of infohashes loaded by this watcher process, so
// the same torrent dropped again (a new file name, a re-download) is
// recognized as a duplicate instead of reloading it or spamming errors. The
// ring evicts the oldest hash once the cap is reached; the .loaded rename
// already prevents same-file replays across restarts.
type hashRing struct {
	mu   sync.Mutex
	cap  int
	seen map[string]bool
	fifo []string
}

func newHashRing(capacity int) *hashRing {
	if capacity < 1 {
		capacity = 1
	}
	return &hashRing{cap: capacity, seen: map[string]bool{}}
}

func (h *hashRing) add(hash string) {
	if hash == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen[hash] {
		return
	}
	h.seen[hash] = true
	h.fifo = append(h.fifo, hash)
	for len(h.fifo) > h.cap {
		delete(h.seen, h.fifo[0])
		h.fifo = h.fifo[1:]
	}
}

func (h *hashRing) has(hash string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen[hash]
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxTorrentSize))
}
