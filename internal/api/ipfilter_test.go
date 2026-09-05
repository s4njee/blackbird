package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/ipfilter"
)

// ipfilterPage mirrors the status payload.
type ipfilterPage struct {
	Enabled   bool    `json:"enabled"`
	Source    string  `json:"source"`
	Rules     int     `json:"rules"`
	LastLoad  *string `json:"lastLoad"`
	LastError string  `json:"lastError"`
}

func getIPFilter(t *testing.T, url string) (int, ipfilterPage) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page ipfilterPage
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, page
}

func postIPFilterReload(t *testing.T, url string) (int, ipfilterPage, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page ipfilterPage
	var raw map[string]any
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	} else {
		_ = json.NewDecoder(resp.Body).Decode(&raw)
	}
	return resp.StatusCode, page, raw
}

func writeBlocklist(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipfilter.dat")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// stackWithIPFilter wires the blocklist service into a test stack.
func stackWithIPFilter(t *testing.T, cfg config.IPFilter) *testStack {
	t.Helper()
	st := newTestStack(t, "", fakertorrent.Options{})
	st.store.cfg.Network.IPFilter = cfg
	svc := ipfilter.New(ipfilter.Options{
		Daemon:    st.rtc,
		CachePath: filepath.Join(t.TempDir(), "cache.dat"),
		Config:    func() config.IPFilter { return st.store.cfg.Network.IPFilter },
	})
	st.srv.opts.IPFilter = svc
	return st
}

// TestIPFilterDisabled proves the default state: GET reports disabled and
// POST refuses without touching the daemon.
func TestIPFilterDisabled(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})

	status, page := getIPFilter(t, st.ts.URL+"/api/ipfilter")
	if status != http.StatusOK || page.Enabled {
		t.Fatalf("GET = %d %+v", status, page)
	}
	status, _, raw := postIPFilterReload(t, st.ts.URL+"/api/ipfilter/reload")
	if status != http.StatusBadRequest {
		t.Fatalf("POST status = %d", status)
	}
	errObj, _ := raw["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "ipfilter_disabled" {
		t.Fatalf("code = %+v", raw)
	}
}

// TestIPFilterFileRoundTrip loads a local DAT file: the fake daemon sees
// ipv4_filter.load with the path and "unwanted", and GET reports the rule
// count and load time.
func TestIPFilterFileRoundTrip(t *testing.T) {
	path := writeBlocklist(t, "1.2.3.0/24\nBad:5.6.7.8-5.6.7.9\n# comment\n\n")
	st := stackWithIPFilter(t, config.IPFilter{Path: path})
	waitForConnected(t, st.p)

	status, page, _ := postIPFilterReload(t, st.ts.URL+"/api/ipfilter/reload")
	if status != http.StatusOK {
		t.Fatalf("POST = %d %+v", status, page)
	}
	if !page.Enabled || page.Source != "file" || page.Rules != 2 || page.LastLoad == nil || page.LastError != "" {
		t.Fatalf("page = %+v", page)
	}

	status, page = getIPFilter(t, st.ts.URL+"/api/ipfilter")
	if status != http.StatusOK || page.Rules != 2 || page.LastLoad == nil {
		t.Fatalf("GET = %d %+v", status, page)
	}
}

// TestIPFilterLoadFailure surfaces a missing file as a 502 with the error
// in the status payload.
func TestIPFilterLoadFailure(t *testing.T) {
	st := stackWithIPFilter(t, config.IPFilter{Path: "/nonexistent/ipfilter.dat"})
	waitForConnected(t, st.p)

	status, _, raw := postIPFilterReload(t, st.ts.URL+"/api/ipfilter/reload")
	if status != http.StatusBadGateway {
		t.Fatalf("POST status = %d", status)
	}
	errObj, _ := raw["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "ipfilter_failed" {
		t.Fatalf("code = %+v", raw)
	}
	_, page := getIPFilter(t, st.ts.URL+"/api/ipfilter")
	if !page.Enabled || page.LastError == "" || page.LastLoad != nil {
		t.Fatalf("page = %+v", page)
	}
}

// TestSettingsNetworkRoundTrip proves the blocklist source is configurable
// through the settings API with validation.
func TestSettingsNetworkRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})

	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning":  map[string]any{},
		"network": map[string]any{"ipfilter": map[string]any{"path": "/data/filters/ipfilter.dat", "refresh_interval": 3_600_000_000_000}},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	got := st.store.Get()
	if got.Network.IPFilter.Path != "/data/filters/ipfilter.dat" {
		t.Fatalf("stored = %+v", got.Network.IPFilter)
	}

	getResp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Network config.Network `json:"network"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&settings); err != nil {
		getResp.Body.Close()
		t.Fatal(err)
	}
	getResp.Body.Close()
	if settings.Network.IPFilter.Path != "/data/filters/ipfilter.dat" {
		t.Fatalf("GET = %+v", settings.Network.IPFilter)
	}

	for _, bad := range []map[string]any{
		{"path": "/data/list.dat", "url": "https://example.com/list"},
		{"path": "relative/list.dat"},
		{"url": "ftp://example.com/list"},
		{"path": "/data/list.dat", "refresh_interval": -1},
	} {
		resp, _ := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
			"tuning":  map[string]any{},
			"network": map[string]any{"ipfilter": bad},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid %v status = %d", bad, resp.StatusCode)
		}
	}
}

// TestIPFilterURLSource proves a remote list is fetched to the cache and
// loaded from there (the daemon only ever sees a local path).
func TestIPFilterURLSource(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8\n"))
	}))
	t.Cleanup(feed.Close)
	st := stackWithIPFilter(t, config.IPFilter{URL: feed.URL + "/list"})
	waitForConnected(t, st.p)

	status, page, _ := postIPFilterReload(t, st.ts.URL+"/api/ipfilter/reload")
	if status != http.StatusOK || page.Source != "url" || page.Rules != 1 {
		t.Fatalf("POST = %d %+v", status, page)
	}
}
