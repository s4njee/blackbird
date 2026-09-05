package automation

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
	"blackbird/internal/unpack"
)

// TestCompletionToUnpackLive is the PAR-3.4 integration proof: a d.complete
// transition flows through the engine into the real unpack service and real
// extractor, and the result lands in the history log. Skipped without a 7z
// binary (the fake-runner unit tests in the unpack package cover the logic).
func TestCompletionToUnpackLive(t *testing.T) {
	if _, ok := (&unpack.SevenZipRunner{}).Available(); !ok {
		t.Skip("no 7z-compatible extractor on PATH")
	}

	dir := t.TempDir()
	data := filepath.Join(dir, "downloads", "Show")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(data, "show.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, err := w.Create("episode.mkv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("fake-video"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	daemon := &fakeDaemon{}
	mover := &fakeMover{}
	hist := newTestHistory(t)
	svc := unpack.New(unpack.Options{
		History: hist,
		Runner:  &unpack.SevenZipRunner{},
		Config: func() config.UnpackConfig {
			return config.UnpackConfig{Rules: []config.UnpackRule{{Name: "tv", Label: "tv"}}}
		},
		Snapshot: func() []rtorrent.Torrent {
			return []rtorrent.Torrent{{Hash: "live1", Name: "Show", Label: "tv", BasePath: data}}
		},
		Roots: func() []string { return []string{filepath.Join(dir, "downloads")} },
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)

	e, _ := newTestEngineWithHistory(t, func() []config.CompletionRule { return nil }, daemon, mover, hist)
	e.SetUnpack(func(hash string) {
		svc.Enqueue(unpack.Job{Hash: hash})
	}, func() bool { return true })

	torrent := rtorrent.Torrent{Hash: "live1", Name: "Show", Label: "tv", SizeBytes: 100}
	e.Enqueue(torrent)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(data, "episode.mkv")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(data, "episode.mkv")); err != nil {
		t.Fatal("live extraction produced no output")
	}
	waitForCond(t, "unpack history", func() bool {
		for _, entry := range hist.ForHash("live1") {
			if entry.Actor == "unpack" && entry.Result == "ok" {
				return true
			}
		}
		return false
	})
}
