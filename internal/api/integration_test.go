package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

// testStore is a ConfigStore stub over an in-memory config.
type testStore struct {
	cfg config.Config
}

func (s *testStore) Get() config.Config { return s.cfg }

func (s *testStore) SaveTuning(t config.Tuning) error {
	s.cfg.Tuning = t
	return nil
}

func (s *testStore) SaveSettings(c config.Config) error {
	s.cfg = c
	return nil
}

func (s *testStore) ConfigPath() string     { return "test.yml" }
func (s *testStore) DownloadDirs() []string { return []string{"/mnt/data"} }

// testStack bundles everything newTestAPI builds so tests can script the
// fake daemon (faults, stopped torrents, disconnect/reconnect).
type testStack struct {
	ts     *httptest.Server
	srv    *Server
	p      *poller.Poller
	rtc    *rtorrent.Client
	daemon *fakertorrent.Daemon
	sock   string
	store  *testStore
}

// newTestStack builds a full API stack (fake rtorrent → client → poller → API)
// with an optional auth gate and fake-daemon options. Returns the pieces so
// callers can script the daemon.
func newTestStack(t *testing.T, passwordHash string, opts fakertorrent.Options) *testStack {
	t.Helper()
	// Short socket dir: darwin caps unix socket paths at 104 bytes and
	// t.TempDir() embeds long test names.
	dir, err := os.MkdirTemp("", "bb-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "rt.sock")
	daemon, err := fakertorrent.StartOpts(sock, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(daemon.Stop)
	rtc, err := rtorrent.New("unix://"+sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p := poller.New(rtc, poller.Options{Interval: 20 * time.Millisecond, Volumes: []string{"/"}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.Run(ctx)

	store := &testStore{cfg: config.Config{
		Server:   config.Server{Listen: "127.0.0.1:0", BaseURL: "/"},
		Log:      config.Log{Level: "info"},
		RTorrent: config.RTorrent{SCGI: "unix:///tmp/rt.sock", Timeout: 5 * time.Second},
		Poll: config.Poll{
			Interval: 2 * time.Second, DetailInterval: time.Second, VolumeInterval: 30 * time.Second,
		},
		Directories: config.Directories{Default: "/mnt/data"},
		Volumes:     []string{"/"},
	}}
	srv := New(Options{
		Poller:   p,
		RTorrent: rtc,
		Store:    store,
		Health: func() HealthInfo {
			snap := p.Snapshot()
			return HealthInfo{Connection: string(snap.Status), Stale: snap.Stale, Torrents: len(snap.Torrents)}
		},
	}, NewAuth("op", passwordHash, nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return &testStack{ts: ts, srv: srv, p: p, rtc: rtc, daemon: daemon, sock: sock, store: store}
}

// newTestAPI builds a full API stack (fake rtorrent → client → poller → API)
// with an optional auth gate. Returns the HTTP test server and the poller.
func newTestAPI(t *testing.T, passwordHash string) (*httptest.Server, *poller.Poller) {
	st := newTestStack(t, passwordHash, fakertorrent.Options{})
	return st.ts, st.p
}

func waitForConnected(t *testing.T, p *poller.Poller) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p.Snapshot().Status == poller.StatusConnected && len(p.Snapshot().Torrents) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("poller never connected")
}

// TestHealthWithFakeRTorrent exercises the Epic 2 chain end to end:
// SCGI transport → XML-RPC codec → typed rtorrent client → poller → API.
func TestHealthWithFakeRTorrent(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var h HealthInfo
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if h.Connection != "connected" || h.Torrents != 3 {
		t.Fatalf("health = %+v", h)
	}
}

func TestHealthWithoutRTorrent(t *testing.T) {
	rtc, err := rtorrent.New("unix:///tmp/blackbird-test-no-daemon.sock", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	p := poller.New(rtc, poller.Options{Interval: 10 * time.Millisecond, BackoffBase: 10 * time.Millisecond, BackoffCap: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	srv := New(Options{Health: func() HealthInfo {
		s := p.Snapshot()
		return HealthInfo{Connection: string(s.Status), LastError: s.LastError, Stale: s.Stale, Torrents: len(s.Torrents)}
	}}, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, _ := http.Get(ts.URL + "/api/health")
		var h HealthInfo
		_ = json.NewDecoder(resp.Body).Decode(&h)
		resp.Body.Close()
		if h.Connection == "disconnected" && h.LastError != "" && h.Stale {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("health never reported disconnected: %+v", h)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
