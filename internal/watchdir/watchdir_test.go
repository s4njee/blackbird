package watchdir

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
)

// realTorrentFile is a minimal, parseable .torrent for a single file "x".
const realTorrentFile = "d4:infod4:name1:x6:lengthi1eee"

// recordingLoader is a Loader stub that records the calls it receives.
type recordingLoader struct {
	mu    sync.Mutex
	calls []loadCall
	fail  map[string]string // content key -> load error message
}

type loadCall struct {
	data  []byte
	entry config.WatchDir
}

func (r *recordingLoader) Load(_ context.Context, data []byte, entry config.WatchDir) LoadResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg, ok := r.fail[string(data)]; ok {
		return LoadResult{Err: &loadErr{msg}}
	}
	r.calls = append(r.calls, loadCall{data: append([]byte(nil), data...), entry: entry})
	return LoadResult{Hash: "aaaa1111aaaa1111"}
}

type loadErr struct{ msg string }

func (e *loadErr) Error() string { return e.msg }

func (r *recordingLoader) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingLoader) Entries() []loadCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]loadCall(nil), r.calls...)
}

func startWatcher(t *testing.T, load Loader, entries ...config.WatchDir) *Watcher {
	t.Helper()
	logDest := io.Discard
	if os.Getenv("BB_WATCH_DEBUG") != "" {
		logDest = os.Stderr
	}
	opts := Options{Log: slog.New(slog.NewTextHandler(logDest, nil))}
	w := New(load, opts)
	for i := range entries {
		// Short poll cadence keeps the polling fallback snappy in tests (the
		// default 5s would make every drop wait for the next scan).
		if entries[i].PollInterval == 0 {
			entries[i].PollInterval = 250 * time.Millisecond
		}
	}
	w.SetEntries(entries)
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	t.Cleanup(cancel)
	// Let the initial apply() register the directory goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(w.Dirs()) == len(entries) {
			return w
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("watcher never registered entries")
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func dropFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadsDroppedTorrentAndRenames(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	startWatcher(t, load, config.WatchDir{Path: dir})

	drop := dropFile(t, dir, "drop.torrent", realTorrentFile)

	waitFor(t, "torrent loaded", func() bool { return load.CallCount() == 1 })
	waitFor(t, "file renamed to .loaded", func() bool {
		_, err := os.Stat(drop + loadedSuffix)
		return err == nil
	})
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Fatal("original file still present after load")
	}
	calls := load.Entries()
	if len(calls) != 1 || string(calls[0].data) != realTorrentFile {
		t.Fatalf("loader calls = %+v", calls)
	}
}

func TestLoadsWithLabelDestinationAndPaused(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	start := false
	startWatcher(t, load, config.WatchDir{
		Path: dir, Label: "iso", Destination: "/mnt/data/iso", Start: &start,
	})

	dropFile(t, dir, "x.torrent", realTorrentFile)
	waitFor(t, "torrent loaded", func() bool { return load.CallCount() == 1 })
	calls := load.Entries()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	entry := calls[0].entry
	if entry.Label != "iso" || entry.Destination != "/mnt/data/iso" {
		t.Fatalf("entry options not passed: %+v", entry)
	}
	if entry.Starts() {
		t.Fatal("start=false was not honored")
	}
}

func TestMalformedTorrentMovesToFailedDir(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	var mu sync.Mutex
	var events []Event
	w := startWatcher(t, load, config.WatchDir{Path: dir})
	unsub := w.Subscribe(func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	defer unsub()

	bad := dropFile(t, dir, "bad.torrent", "this is not bencode")
	waitFor(t, "malformed event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			if e.Kind == "malformed" && strings.Contains(e.Message, "not a valid .torrent") {
				return true
			}
		}
		return false
	})
	if load.CallCount() != 0 {
		t.Fatal("malformed file reached the loader")
	}
	// The malformed file is preserved in the failed/ subdirectory, not
	// renamed to .loaded or deleted.
	waitFor(t, "malformed file moved to failed directory", func() bool {
		_, err := os.Stat(filepath.Join(dir, "failed", "bad.torrent"))
		return err == nil
	})
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("original malformed file still in the watch directory")
	}
	if _, err := os.Stat(bad + loadedSuffix); !os.IsNotExist(err) {
		t.Fatal("malformed file was renamed to .loaded instead of failed/")
	}
	// The preserved file still parses as whatever it was; verify the content
	// survived the move.
	kept, err := os.ReadFile(filepath.Join(dir, "failed", "bad.torrent"))
	if err != nil || string(kept) != "this is not bencode" {
		t.Fatalf("failed/ copy content = %q, err = %v", kept, err)
	}
}

func TestFailedDirCollisionKeepsBoth(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	startWatcher(t, load, config.WatchDir{Path: dir})

	// Two malformed drops with the same name must both survive in failed/.
	dropFile(t, dir, "bad.torrent", "first broken file")
	waitFor(t, "first malformed file in failed directory", func() bool {
		_, err := os.Stat(filepath.Join(dir, "failed", "bad.torrent"))
		return err == nil
	})
	dropFile(t, dir, "bad.torrent", "second broken file")
	waitFor(t, "second malformed file in failed directory", func() bool {
		_, err := os.Stat(filepath.Join(dir, "failed", "bad.torrent.1"))
		return err == nil
	})
	first, err := os.ReadFile(filepath.Join(dir, "failed", "bad.torrent"))
	if err != nil || string(first) != "first broken file" {
		t.Fatalf("failed/bad.torrent = %q, err = %v", first, err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "failed", "bad.torrent.1"))
	if err != nil || string(second) != "second broken file" {
		t.Fatalf("failed/bad.torrent.1 = %q, err = %v", second, err)
	}
}

func TestDuplicateTorrentSkipsReload(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	var mu sync.Mutex
	var events []Event
	w := startWatcher(t, load, config.WatchDir{Path: dir})
	unsub := w.Subscribe(func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	defer unsub()

	// The same torrent under a second file name must not reload: the watcher
	// recognizes the already-loaded infohash and finishes the file silently.
	dropFile(t, dir, "one.torrent", realTorrentFile)
	waitFor(t, "first torrent loaded", func() bool { return load.CallCount() == 1 })

	dropFile(t, dir, "two.torrent", realTorrentFile)
	waitFor(t, "duplicate event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			if e.Kind == "duplicate" && e.File == "two.torrent" {
				return true
			}
		}
		return false
	})
	time.Sleep(400 * time.Millisecond)
	if load.CallCount() != 1 {
		t.Fatalf("duplicate torrent loaded %d times, want 1", load.CallCount())
	}
	waitFor(t, "duplicate file renamed", func() bool {
		_, err := os.Stat(filepath.Join(dir, "two.torrent"+loadedSuffix))
		return err == nil
	})
}

func TestDeleteAfterLoadRemovesFile(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	startWatcher(t, load, config.WatchDir{Path: dir, DeleteAfterLoad: true})

	drop := dropFile(t, dir, "gone.torrent", realTorrentFile)
	waitFor(t, "torrent loaded", func() bool { return load.CallCount() == 1 })
	waitFor(t, "file deleted", func() bool {
		_, err := os.Stat(drop)
		return os.IsNotExist(err)
	})
	if _, err := os.Stat(drop + loadedSuffix); !os.IsNotExist(err) {
		t.Fatal(".loaded leftover despite delete_after_load")
	}
}

func TestDuplicateDeliveryLoadedOnce(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	startWatcher(t, load, config.WatchDir{Path: dir})

	// Give the watcher a moment to attach, then drop one file. Both the
	// fsnotify event path and the polling fallback race to consume it; the
	// seen set must ensure exactly one load.
	time.Sleep(150 * time.Millisecond)
	dropFile(t, dir, "dup.torrent", realTorrentFile)
	waitFor(t, "torrent loaded", func() bool { return load.CallCount() == 1 })
	// Give the poller a chance to double-deliver; it must not.
	time.Sleep(500 * time.Millisecond)
	if load.CallCount() != 1 {
		t.Fatalf("torrent loaded %d times, want 1", load.CallCount())
	}
}

func TestPollingFallbackLoadsStartupBacklog(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	// A pre-existing file is picked up by the immediate first scan.
	dropFile(t, dir, "backlog.torrent", realTorrentFile)

	startWatcher(t, load, config.WatchDir{Path: dir})
	waitFor(t, "backlog torrent loaded", func() bool { return load.CallCount() == 1 })
}

func TestIgnoredNonTorrentAndLoadedSuffix(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	startWatcher(t, load, config.WatchDir{Path: dir})

	dropFile(t, dir, "notes.txt", "hi")
	dropFile(t, dir, "done.torrent.loaded", realTorrentFile)
	time.Sleep(400 * time.Millisecond)
	if load.CallCount() != 0 {
		t.Fatalf("ignored files reached the loader: %+v", load.Entries())
	}
}

func TestLoadErrorEmitsEventAndMovesFile(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{fail: map[string]string{realTorrentFile: "daemon rejected load"}}
	var mu sync.Mutex
	var events []Event
	w := startWatcher(t, load, config.WatchDir{Path: dir})
	unsub := w.Subscribe(func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	defer unsub()

	drop := dropFile(t, dir, "reject.torrent", realTorrentFile)
	waitFor(t, "load_error event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			if e.Kind == "load_error" && strings.Contains(e.Message, "daemon rejected load") {
				return true
			}
		}
		return false
	})
	// The failed file is still renamed so it is not retried every poll.
	waitFor(t, "failed file renamed", func() bool {
		_, err := os.Stat(drop + loadedSuffix)
		return err == nil
	})
}

func TestEntriesReconcileOnSet(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	w := startWatcher(t, load, config.WatchDir{Path: dir})
	_ = w

	// Replacing the entry list with an empty list disables the directory.
	w.SetEntries(nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(w.Dirs()) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("entries were not cleared")
}

// TestEntryEditAppliesWithoutRestart asserts that editing an existing entry's
// options (same path) restarts the directory so later drops use the new
// label/destination — the Settings save path.
func TestEntryEditAppliesWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	load := &recordingLoader{}
	w := startWatcher(t, load, config.WatchDir{Path: dir, Label: "first"})

	time.Sleep(150 * time.Millisecond)
	dropFile(t, dir, "one.torrent", realTorrentFile)
	waitFor(t, "first torrent loaded", func() bool { return load.CallCount() == 1 })

	// Same path, new options: the watcher must restart the directory so the
	// second drop carries the updated label.
	start := false
	w.SetEntries([]config.WatchDir{{Path: dir, Label: "second", Start: &start, DeleteAfterLoad: true}})
	dropFile(t, dir, "two.torrent", "d4:infod4:name1:y6:lengthi2eee")
	waitFor(t, "second torrent loaded", func() bool { return load.CallCount() == 2 })
	calls := load.Entries()
	if calls[1].entry.Label != "second" || calls[1].entry.Starts() {
		t.Fatalf("edited entry options not applied: %+v", calls[1].entry)
	}
	// delete_after_load now applies: no .loaded rename for the second file.
	waitFor(t, "second file deleted", func() bool {
		_, err := os.Stat(filepath.Join(dir, "two.torrent"))
		return os.IsNotExist(err)
	})
	if _, err := os.Stat(filepath.Join(dir, "two.torrent"+loadedSuffix)); !os.IsNotExist(err) {
		t.Fatal("second file renamed despite delete_after_load")
	}
}

// TestDefaultLoadIssuesExpectedCommands exercises the production loader's
// trailing-command construction against a scripted rTorrent client.
func TestDefaultLoadIssuesExpectedCommands(t *testing.T) {
	rt := &fakeRTorrentClient{}
	hist := history.New(history.Options{})
	start := true
	dl := DefaultLoad{Client: rt, History: hist}
	res := dl.Load(context.Background(), []byte(realTorrentFile), config.WatchDir{
		Path: "/watch", Label: "media", Destination: "/mnt/data/media", Start: &start,
	})
	if res.Err != nil {
		t.Fatalf("load = %v", res.Err)
	}
	if res.Hash == "" {
		t.Fatal("load result missing hash")
	}
	method, opts := rt.last()
	if method != "load.raw_start" {
		t.Fatalf("method = %q", method)
	}
	cmds := opts.ExtraCommands
	if len(cmds) != 2 || cmds[0] != "d.directory.set=/mnt/data/media" || cmds[1] != "d.custom1.set=media" {
		t.Fatalf("extra commands = %+v", cmds)
	}
	entries := hist.ForHash(res.Hash)
	if len(entries) == 0 || entries[0].Actor != "watch" {
		t.Fatalf("history entries = %+v", entries)
	}
}

type fakeRTorrentClient struct {
	mu       sync.Mutex
	method   string
	lastOpts rtorrent.AddOptions
}

func (f *fakeRTorrentClient) AddTorrentFile(_ context.Context, _ []byte, opts rtorrent.AddOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.method = "load.raw_start"
	f.lastOpts = opts
	return nil
}

func (f *fakeRTorrentClient) last() (method string, opts rtorrent.AddOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.method, f.lastOpts
}
