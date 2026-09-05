package unpack

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
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

// fakeRunner is a scripted Runner for service tests.
type fakeRunner struct {
	mu         sync.Mutex
	available  bool
	lists      map[string][]string
	listErr    map[string]error
	extractErr map[string]error
	outputs    map[string][]string // archive → files to create in dest
	progress   []int
	block      chan struct{} // when non-nil, Extract waits here or on ctx
	extracted  []extractCall
}

type extractCall struct {
	archive string
	dest    string
}

func (f *fakeRunner) Available() (string, bool) {
	if !f.available {
		return "", false
	}
	return "/usr/bin/7z", true
}

func (f *fakeRunner) List(_ context.Context, archive string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.listErr[archive]; err != nil {
		return nil, err
	}
	return append([]string(nil), f.lists[archive]...), nil
}

func (f *fakeRunner) Extract(ctx context.Context, archive, dest string, progress func(pct int)) error {
	f.mu.Lock()
	f.extracted = append(f.extracted, extractCall{archive, dest})
	outputs := append([]string(nil), f.outputs[archive]...)
	steps := append([]int(nil), f.progress...)
	block := f.block
	err := f.extractErr[archive]
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err != nil {
		return err
	}
	for _, step := range steps {
		progress(step)
	}
	for _, name := range outputs {
		if err := os.WriteFile(filepath.Join(dest, name), []byte("extracted"), 0o644); err != nil {
			return err
		}
	}
	progress(100)
	return nil
}

func (f *fakeRunner) calls() []extractCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]extractCall(nil), f.extracted...)
}

func testService(t *testing.T, runner *fakeRunner, hist *history.Log, cfg config.UnpackConfig, rows func() []rtorrent.Torrent, roots []string) *Service {
	t.Helper()
	if hist == nil {
		hist = history.New(history.Options{})
	}
	svc := New(Options{
		Log:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		History:  hist,
		Runner:   runner,
		Config:   func() config.UnpackConfig { return cfg },
		Snapshot: rows,
		Roots:    func() []string { return roots },
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)
	return svc
}

func rowsWith(data map[string]rtorrent.Torrent) func() []rtorrent.Torrent {
	return func() []rtorrent.Torrent {
		var out []rtorrent.Torrent
		for _, t := range data {
			out = append(out, t)
		}
		return out
	}
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func unpackHistory(t *testing.T, hist *history.Log, hash string) []history.Entry {
	t.Helper()
	var out []history.Entry
	for _, e := range hist.ForHash(hash) {
		if e.Actor == "unpack" {
			out = append(out, e)
		}
	}
	return out
}

func TestIsArchiveHead(t *testing.T) {
	cases := map[string]bool{
		"movie.zip": true, "MOVIE.ZIP": true,
		"movie.rar": true, "movie.RAR": true,
		"movie.part1.rar": true, "movie.part01.rar": true, "movie.part001.rar": true,
		"movie.part2.rar": false, "movie.part02.rar": false, "movie.part10.rar": false,
		"movie.r00": false, "movie.r01": false, "movie.r99": false,
		"movie.rev": false, "movie.sfv": false, "movie.nfo": false,
		"movie.mkv": false, "movie.rar.txt": false, "rar": false,
	}
	for name, want := range cases {
		if got := isArchiveHead("/data/" + name); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
}

func TestArchiveFamily(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"movie.rar", "movie.r00", "movie.r01", "movie.nfo", "other.r00", "show.part01.rar", "show.part02.rar", "show.part03.rar", "show.sfv", "arch.zip"} {
		writeFile(t, filepath.Join(dir, name), "x")
	}
	rarFamily := archiveFamily(filepath.Join(dir, "movie.rar"))
	if len(rarFamily) != 3 {
		t.Fatalf("rar family = %v", rarFamily)
	}
	partFamily := archiveFamily(filepath.Join(dir, "show.part01.rar"))
	if len(partFamily) != 3 {
		t.Fatalf("part family = %v", partFamily)
	}
	// Siblings of other sets and non-volumes are excluded.
	for _, f := range append(rarFamily, partFamily...) {
		base := filepath.Base(f)
		if base == "other.r00" || base == "movie.nfo" || base == "show.sfv" {
			t.Fatalf("family polluted: %v", f)
		}
	}
	zipFamily := archiveFamily(filepath.Join(dir, "arch.zip"))
	if len(zipFamily) != 1 || zipFamily[0] != filepath.Join(dir, "arch.zip") {
		t.Fatalf("zip family = %v", zipFamily)
	}
}

func TestValidateEntries(t *testing.T) {
	if err := validateEntries([]string{"video.mkv", "subs/eng.srt", "CD1/", "a b/c+d[e].mkv"}); err != nil {
		t.Fatalf("clean entries refused: %v", err)
	}
	for _, evil := range []string{"../evil.sh", "a/../../b", "/abs/path", `C:\win\evil`, `..\evil`, "a/b/../../../c"} {
		if err := validateEntries([]string{"ok.mkv", evil}); err == nil {
			t.Errorf("malicious entry accepted: %q", evil)
		}
	}
}

// TestZipSlipRealZip builds an actual malicious zip with archive/zip and
// proves the listing path refuses it (not just string matching).
func TestZipSlipRealZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, name := range []string{"ok.txt", "../evil.sh", "sub/../../escape"} {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte("x"))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	for _, file := range r.File {
		names = append(names, file.Name)
	}
	if err := validateEntries(names); err == nil {
		t.Fatalf("real zip-slip zip accepted: %v", names)
	}
}

func TestParseSltList(t *testing.T) {
	sample := `Path = /data/release.rar
Type = rar
Physical Size = 100

----------
Path = video.mkv
Folder = -
Size = 50
----------
Path = subs/eng.srt
Folder = -
Size = 1
`
	got := parseSltList(sample)
	if len(got) != 2 || got[0] != "video.mkv" || got[1] != "subs/eng.srt" {
		t.Fatalf("entries = %v", got)
	}
}

func TestServiceSuccessInPlace(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "release.rar"), "rar-bytes")
	writeFile(t, filepath.Join(data, "release.nfo"), "nfo")
	archive := filepath.Join(data, "release.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
	}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Release", Label: "tv", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1", Name: "Release"})
	waitFor(t, "extraction", func() bool { return len(runner.calls()) == 1 })
	waitFor(t, "history", func() bool { return len(unpackHistory(t, hist, "h1")) >= 2 })

	call := runner.calls()[0]
	if call.dest != data {
		t.Fatalf("in-place dest = %q, want %q", call.dest, data)
	}
	if _, err := os.Stat(filepath.Join(data, "video.mkv")); err != nil {
		t.Fatal("extracted file missing")
	}
	// Without delete_archives the rar stays.
	if _, err := os.Stat(archive); err != nil {
		t.Fatal("archive removed without delete_archives")
	}
	entries := unpackHistory(t, hist, "h1")
	var okCount int
	for _, e := range entries {
		if e.Actor != "unpack" {
			t.Fatalf("entry = %+v", e)
		}
		if e.Action == "unpack" && e.Result == "ok" {
			okCount++
		}
	}
	if okCount < 2 { // start note + per-archive result
		t.Fatalf("history entries = %+v", entries)
	}
}

func TestServiceExtractRoot(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "downloads", "Release")
	writeFile(t, filepath.Join(data, "release.rar"), "rar-bytes")
	archive := filepath.Join(data, "release.rar")
	// The extract root must exist (it is symlink-resolved like the move
	// engine's destinations).
	root := filepath.Join(dir, "extracted")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
	}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "tv", Label: "tv", Destination: root}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Some/Release", Label: "TV", BasePath: data}}),
		[]string{filepath.Join(dir, "downloads"), root},
	)

	svc.Enqueue(Job{Hash: "h1", Name: "Some/Release"})
	waitFor(t, "extraction", func() bool { return len(runner.calls()) == 1 })
	dest := filepath.Join(root, "Some_Release")
	if call := runner.calls()[0]; call.dest != dest {
		t.Fatalf("dest = %q, want %q", call.dest, dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "video.mkv")); err != nil {
		t.Fatal("extracted file missing from root")
	}
	// Source dir untouched apart from the scan.
	if _, err := os.Stat(filepath.Join(data, "video.mkv")); !os.IsNotExist(err) {
		t.Fatal("in-place file leaked into source dir")
	}
}

func TestServiceDeleteArchives(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "movie.rar"), "x")
	writeFile(t, filepath.Join(data, "movie.r00"), "x")
	writeFile(t, filepath.Join(data, "movie.r01"), "x")
	writeFile(t, filepath.Join(data, "movie.nfo"), "x")
	archive := filepath.Join(data, "movie.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
	}
	svc := testService(t, runner, nil,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all", DeleteArchives: true}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "M", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "extraction", func() bool { return len(runner.calls()) == 1 })
	waitFor(t, "archives deleted", func() bool {
		_, errRar := os.Stat(archive)
		_, errR00 := os.Stat(filepath.Join(data, "movie.r00"))
		_, errR01 := os.Stat(filepath.Join(data, "movie.r01"))
		return os.IsNotExist(errRar) && os.IsNotExist(errR00) && os.IsNotExist(errR01)
	})
	if _, err := os.Stat(filepath.Join(data, "movie.nfo")); err != nil {
		t.Fatal("non-archive file deleted")
	}
}

func TestServiceFailureLeavesMarker(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "broken.rar"), "x")
	writeFile(t, filepath.Join(data, "partial.mkv"), "partial")
	archive := filepath.Join(data, "broken.rar")

	runner := &fakeRunner{
		available:  true,
		lists:      map[string][]string{archive: {"video.mkv"}},
		extractErr: map[string]error{archive: fmt.Errorf("ERROR: CRC Failed")},
	}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Broken", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "failure recorded", func() bool {
		for _, e := range unpackHistory(t, hist, "h1") {
			if e.Result == "failed" {
				return true
			}
		}
		return false
	})
	// The partial directory keeps its files AND gains the marker.
	marker, err := os.ReadFile(filepath.Join(data, failedMarker))
	if err != nil {
		t.Fatalf("failure marker missing: %v", err)
	}
	for _, want := range []string{"Broken", "h1", "broken.rar", "CRC Failed"} {
		if !strings.Contains(string(marker), want) {
			t.Errorf("marker missing %q:\n%s", want, marker)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "partial.mkv")); err != nil {
		t.Fatal("partial output was cleaned instead of marked")
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatal("failed archive was deleted")
	}
}

func TestServiceSlipRefused(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "evil.rar"), "x")
	archive := filepath.Join(data, "evil.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"../evil.sh"}},
	}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Evil", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "refusal recorded", func() bool {
		for _, e := range unpackHistory(t, hist, "h1") {
			if e.Result == "failed" && strings.Contains(e.Message, "refused unsafe archive") {
				return true
			}
		}
		return false
	})
	if len(runner.calls()) != 0 {
		t.Fatal("unsafe archive reached the extractor")
	}
	if _, err := os.Stat(filepath.Join(data, failedMarker)); err != nil {
		t.Fatal("refusal left no .failed marker")
	}
}

func TestServiceMissingExtractor(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{available: false}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "X", BasePath: dir}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "disabled recorded", func() bool { return len(unpackHistory(t, hist, "h1")) == 1 })
	entry := unpackHistory(t, hist, "h1")[0]
	if entry.Result != "failed" || !strings.Contains(entry.Message, "not found") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestServiceNoMatchingRule(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{available: true}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "tv", Label: "tv"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "X", Label: "movies", BasePath: dir}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	time.Sleep(200 * time.Millisecond)
	if len(runner.calls()) != 0 {
		t.Fatal("non-matching torrent reached the extractor")
	}
	if len(unpackHistory(t, hist, "h1")) != 0 {
		t.Fatal("non-matching torrent logged history")
	}
}

func TestServiceStaleMarkerCleared(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "release.rar"), "x")
	writeFile(t, filepath.Join(data, failedMarker), "old failure")
	archive := filepath.Join(data, "release.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
	}
	svc := testService(t, runner, nil,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "R", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "extraction", func() bool { return len(runner.calls()) == 1 })
	waitFor(t, "marker cleared", func() bool {
		_, err := os.Stat(filepath.Join(data, failedMarker))
		return os.IsNotExist(err)
	})
}

func TestServiceProgressMilestones(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "big.rar"), "x")
	archive := filepath.Join(data, "big.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
		progress:  []int{5, 30, 55, 80, 99},
	}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Big", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "milestones", func() bool {
		marks := map[string]bool{}
		for _, e := range unpackHistory(t, hist, "h1") {
			if e.Action == "unpack-progress" {
				for _, pct := range []string{"25%", "50%", "75%"} {
					if strings.Contains(e.Message, pct) {
						marks[pct] = true
					}
				}
			}
		}
		return len(marks) == 3
	})
}

func TestServiceTimeout(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "slow.rar"), "x")
	archive := filepath.Join(data, "slow.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		block:     make(chan struct{}),
	}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Timeout: 100 * time.Millisecond, Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Slow", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "timeout recorded", func() bool {
		for _, e := range unpackHistory(t, hist, "h1") {
			if e.Result == "failed" && strings.Contains(e.Message, "deadline") {
				return true
			}
		}
		return false
	})
	if _, err := os.Stat(filepath.Join(data, failedMarker)); err != nil {
		t.Fatal("timed-out job left no .failed marker")
	}
}

func TestServiceStatusTracksActive(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "slow.rar"), "x")
	archive := filepath.Join(data, "slow.rar")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
		block:     make(chan struct{}),
	}
	svc := testService(t, runner, nil,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "Slow", BasePath: data}}),
		[]string{dir},
	)

	status := svc.Status()
	if !status.Available || status.Workers != 2 || status.Binary != "/usr/bin/7z" {
		t.Fatalf("status = %+v", status)
	}
	svc.Enqueue(Job{Hash: "h1", Name: "Slow"})
	waitFor(t, "job active", func() bool { return len(svc.Status().Jobs) == 1 })
	job := svc.Status().Jobs[0]
	if job.Archive != archive || job.Rule != "all" || job.DestDir != data {
		t.Fatalf("job = %+v", job)
	}
	close(runner.block)
	waitFor(t, "job done", func() bool { return len(svc.Status().Jobs) == 0 })
}

func TestServiceExtractRootOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	writeFile(t, filepath.Join(data, "release.rar"), "x")

	runner := &fakeRunner{available: true}
	hist := history.New(history.Options{})
	svc := testService(t, runner, hist,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "evil", Destination: "/etc/unpack"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "R", BasePath: data}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "refusal recorded", func() bool {
		for _, e := range unpackHistory(t, hist, "h1") {
			if e.Result == "failed" && strings.Contains(e.Message, "outside the configured download") {
				return true
			}
		}
		return false
	})
	if len(runner.calls()) != 0 {
		t.Fatal("out-of-roots extraction reached the runner")
	}
}

func TestServiceSingleFileBase(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "movie.rar")
	writeFile(t, archive, "x")

	runner := &fakeRunner{
		available: true,
		lists:     map[string][]string{archive: {"video.mkv"}},
		outputs:   map[string][]string{archive: {"video.mkv"}},
	}
	svc := testService(t, runner, nil,
		config.UnpackConfig{Rules: []config.UnpackRule{{Name: "all"}}},
		rowsWith(map[string]rtorrent.Torrent{"h1": {Hash: "h1", Name: "M", BasePath: archive}}),
		[]string{dir},
	)

	svc.Enqueue(Job{Hash: "h1"})
	waitFor(t, "extraction", func() bool { return len(runner.calls()) == 1 })
	if call := runner.calls()[0]; call.dest != dir {
		t.Fatalf("dest = %q, want parent dir %q", call.dest, dir)
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName("Some/Release"); got != "Some_Release" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "  ", ".", ".."} {
		if got := safeName(bad); got != "data" {
			t.Fatalf("%q → %q", bad, got)
		}
	}
}

// TestSevenZipLive exercises the real extractor when one is installed
// (container image, or sevenzip/p7zip on the host); otherwise it skips. It
// pins the -slt listing format the slip check depends on.
func TestSevenZipLive(t *testing.T) {
	runner := &SevenZipRunner{}
	bin, ok := runner.Available()
	if !ok {
		t.Skip("no 7z-compatible extractor on PATH")
	}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, err := w.Create("video.mkv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("fake-video-bytes"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	entries, err := runner.List(ctx, zipPath)
	if err != nil {
		t.Fatalf("list with %s: %v", bin, err)
	}
	if len(entries) != 1 || entries[0] != "video.mkv" {
		t.Fatalf("entries = %v", entries)
	}
	if err := validateEntries(entries); err != nil {
		t.Fatalf("clean entries refused: %v", err)
	}

	dest := filepath.Join(dir, "out")
	var last int
	if err := runner.Extract(ctx, zipPath, dest, func(pct int) { last = pct }); err != nil {
		t.Fatalf("extract with %s: %v", bin, err)
	}
	content, err := os.ReadFile(filepath.Join(dest, "video.mkv"))
	if err != nil || string(content) != "fake-video-bytes" {
		t.Fatalf("extracted content = %q, err = %v", content, err)
	}
	if last != 100 {
		t.Fatalf("final progress = %d", last)
	}
}

func TestPreservationKeepsSourceArchiveAfterExtraction(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "movie.rar")
	writeFile(t, archive, "source")
	runner := &fakeRunner{available: true, lists: map[string][]string{archive: {"video.mkv"}}, outputs: map[string][]string{archive: {"video.mkv"}}}
	log := history.New(history.Options{})
	svc := New(Options{Runner: runner, History: log, CleanupGuard: func(string, func() error) error { return errors.New("preservation pin") }})
	if err := svc.extractOne(context.Background(), "pinned", "Movie", config.UnpackRule{Name: "all", DeleteArchives: true}, archive, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatal("source archive removed despite pin")
	}
	if _, err := os.Stat(filepath.Join(dir, "video.mkv")); err != nil {
		t.Fatal("pin prevented extraction")
	}
}
