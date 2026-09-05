package ipfilter

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/config"
)

func TestCountRulesFormats(t *testing.T) {
	body := strings.Join([]string{
		"# a comment",
		"",
		"1.2.3.4",
		"10.0.0.0/8",
		"192.168.1.1 - 192.168.1.254",
		"005.006.007.008 - 005.006.007.010 , 000 , eMule entry",
		"Bad Guys:11.0.0.1-11.0.0.9",
		"not an ip",
		"999.1.1.1",
		"10.0.0.5 - 10.0.0.1", // inverted range
		"2001:db8::1",         // IPv6 is not an IPv4 rule
		"10.0.0.0/33",         // bad CIDR
	}, "\n")
	n, err := CountRules(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("rules = %d, want 5", n)
	}
}

// stubDaemon records ipv4_filter.load paths.
type stubDaemon struct {
	paths []string
	err   error
}

func (d *stubDaemon) LoadIPFilter(_ context.Context, path string) error {
	if d.err != nil {
		return d.err
	}
	d.paths = append(d.paths, path)
	return nil
}

func writeList(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipfilter.dat")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyFileSource(t *testing.T) {
	path := writeList(t, "1.2.3.4\n10.0.0.0/8\n# comment\n")
	daemon := &stubDaemon{}
	cfg := config.IPFilter{Path: path}
	svc := New(Options{Daemon: daemon, Config: func() config.IPFilter { return cfg }})
	if err := svc.ApplyNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(daemon.paths) != 1 || daemon.paths[0] != path {
		t.Fatalf("daemon loads = %+v", daemon.paths)
	}
	st := svc.Status()
	if !st.Enabled || st.Source != "file" || st.Rules != 2 || st.LastLoad == nil || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyDisabledIsNoOp(t *testing.T) {
	daemon := &stubDaemon{}
	svc := New(Options{Daemon: daemon, Config: func() config.IPFilter { return config.IPFilter{} }})
	if err := svc.ApplyNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(daemon.paths) != 0 {
		t.Fatalf("daemon loads = %+v", daemon.paths)
	}
	if st := svc.Status(); st.Enabled {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyMissingFileRecordsError(t *testing.T) {
	daemon := &stubDaemon{}
	svc := New(Options{Daemon: daemon, Config: func() config.IPFilter {
		return config.IPFilter{Path: "/nonexistent/ipfilter.dat"}
	}})
	if err := svc.ApplyNow(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(daemon.paths) != 0 {
		t.Fatalf("daemon loads = %+v", daemon.paths)
	}
	st := svc.Status()
	if !st.Enabled || st.LastError == "" || st.LastLoad != nil {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyDaemonFailureRecordsError(t *testing.T) {
	path := writeList(t, "1.2.3.4\n")
	daemon := &stubDaemon{err: errors.New("boom")}
	svc := New(Options{Daemon: daemon, Config: func() config.IPFilter {
		return config.IPFilter{Path: path}
	}})
	if err := svc.ApplyNow(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	st := svc.Status()
	if !strings.Contains(st.LastError, "ipv4_filter.load") {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyURLSourceFetchesAndCaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("9.9.9.9\n10.10.0.0/16\n"))
	}))
	t.Cleanup(srv.Close)
	daemon := &stubDaemon{}
	cache := filepath.Join(t.TempDir(), "cache.dat")
	svc := New(Options{
		Daemon:    daemon,
		CachePath: cache,
		Config:    func() config.IPFilter { return config.IPFilter{URL: srv.URL + "/list"} },
	})
	if err := svc.ApplyNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(daemon.paths) != 1 || daemon.paths[0] != cache {
		t.Fatalf("daemon loads = %+v", daemon.paths)
	}
	st := svc.Status()
	if st.Source != "url" || st.Rules != 2 || st.Path != cache {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyGzipURL(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("7.7.7.7\n"))
	_ = zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)
	daemon := &stubDaemon{}
	cache := filepath.Join(t.TempDir(), "cache.dat")
	svc := New(Options{
		Daemon:    daemon,
		CachePath: cache,
		Config:    func() config.IPFilter { return config.IPFilter{URL: srv.URL + "/ipfilter.dat.gz"} },
	})
	if err := svc.ApplyNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := svc.Status(); st.Rules != 1 {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyURLFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	daemon := &stubDaemon{}
	svc := New(Options{
		Daemon:    daemon,
		CachePath: filepath.Join(t.TempDir(), "cache.dat"),
		Config:    func() config.IPFilter { return config.IPFilter{URL: srv.URL} },
	})
	if err := svc.ApplyNow(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(daemon.paths) != 0 {
		t.Fatalf("daemon loads = %+v", daemon.paths)
	}
	if st := svc.Status(); st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestReconcileReloadsOnChangeAndRefresh(t *testing.T) {
	now := time.Now()
	current := now
	daemon := &stubDaemon{}
	cfg := config.IPFilter{URL: "http://example.invalid/list", RefreshInterval: time.Hour}
	var fetches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		_, _ = w.Write([]byte("1.1.1.1\n"))
	}))
	t.Cleanup(srv.Close)
	cfg.URL = srv.URL
	svc := New(Options{
		Daemon:    daemon,
		CachePath: filepath.Join(t.TempDir(), "cache.dat"),
		Config:    func() config.IPFilter { return cfg },
		Now:       func() time.Time { return current },
	})
	ctx := context.Background()
	svc.reconcile(ctx) // first apply (changed from empty)
	if len(daemon.paths) != 1 {
		t.Fatalf("loads = %d", len(daemon.paths))
	}
	svc.reconcile(ctx) // steady state: no reload
	if len(daemon.paths) != 1 {
		t.Fatalf("loads = %d, want no reload", len(daemon.paths))
	}
	current = now.Add(2 * time.Hour)
	svc.reconcile(ctx) // refresh due
	if len(daemon.paths) != 2 || fetches != 2 {
		t.Fatalf("loads = %d fetches = %d", len(daemon.paths), fetches)
	}
}

func TestConfigValidation(t *testing.T) {
	base := func() *config.Config {
		cfg := &config.Config{}
		cfg.Server.Listen = ":8222"
		cfg.Server.BaseURL = "/"
		cfg.Log.Level = "info"
		cfg.RTorrent.SCGI = "unix:///tmp/rt.sock"
		cfg.RTorrent.Timeout = time.Second
		cfg.Poll.Interval = 2 * time.Second
		cfg.Poll.DetailInterval = time.Second
		cfg.Poll.VolumeInterval = 30 * time.Second
		return cfg
	}
	// Disabled is valid.
	if errs := config.Validate(base()); len(errs) != 0 {
		t.Fatalf("disabled errs = %+v", errs)
	}
	// Both path and URL is rejected.
	bad := base()
	bad.Network.IPFilter = config.IPFilter{Path: "/data/list.dat", URL: "https://example.com/list"}
	if errs := config.Validate(bad); len(errs) == 0 {
		t.Fatal("expected error for path+url")
	}
	// Relative path rejected.
	bad = base()
	bad.Network.IPFilter = config.IPFilter{Path: "relative/list.dat"}
	if errs := config.Validate(bad); len(errs) == 0 {
		t.Fatal("expected error for relative path")
	}
	// Non-http URL rejected.
	bad = base()
	bad.Network.IPFilter = config.IPFilter{URL: "ftp://example.com/list"}
	if errs := config.Validate(bad); len(errs) == 0 {
		t.Fatal("expected error for ftp URL")
	}
	// Negative refresh rejected.
	bad = base()
	bad.Network.IPFilter = config.IPFilter{Path: "/data/list.dat", RefreshInterval: -time.Hour}
	if errs := config.Validate(bad); len(errs) == 0 {
		t.Fatal("expected error for negative refresh")
	}
	// Valid file and URL sources pass.
	for _, f := range []config.IPFilter{
		{Path: "/data/list.dat"},
		{URL: "https://example.com/list", RefreshInterval: time.Hour},
	} {
		ok := base()
		ok.Network.IPFilter = f
		if errs := config.Validate(ok); len(errs) != 0 {
			t.Fatalf("valid %+v errs = %+v", f, errs)
		}
	}
}

func TestEffectiveRefresh(t *testing.T) {
	if got := (config.IPFilter{URL: "https://example.com/x"}).EffectiveRefresh(); got != 24*time.Hour {
		t.Fatalf("url default = %v", got)
	}
	if got := (config.IPFilter{Path: "/x"}).EffectiveRefresh(); got != 0 {
		t.Fatalf("file default = %v", got)
	}
	if got := (config.IPFilter{URL: "https://example.com/x", RefreshInterval: time.Hour}).EffectiveRefresh(); got != time.Hour {
		t.Fatalf("override = %v", got)
	}
}
