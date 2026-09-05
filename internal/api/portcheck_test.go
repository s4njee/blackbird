package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/history"
)

type portCheckPage struct {
	Enabled bool `json:"enabled"`
	Result  *struct {
		Port      int    `json:"port"`
		Reachable bool   `json:"reachable"`
		Method    string `json:"method"`
		CheckedAt string `json:"checkedAt"`
	} `json:"result"`
}

func getPortCheck(t *testing.T, url string) (int, portCheckPage) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page portCheckPage
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, page
}

func postPortCheck(t *testing.T, url string) (int, portCheckPage, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page portCheckPage
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

func probeStub(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestPortCheckDisabled proves the default state: GET reports disabled with
// no verdict, POST refuses without touching the network.
func TestPortCheckDisabled(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})

	status, page := getPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusOK || page.Enabled || page.Result != nil {
		t.Fatalf("GET = %d %+v", status, page)
	}
	status, _, raw := postPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusBadRequest {
		t.Fatalf("POST status = %d", status)
	}
	errObj, _ := raw["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "portcheck_disabled" {
		t.Fatalf("code = %+v", raw)
	}
}

// TestPortCheckRoundTrip runs a check against a stub probe: the fake daemon
// reports live port 51413, the verdict stores, GET replays it, and the check
// is history.
func TestPortCheckRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	probe := probeStub(t, 200, `{"reachable":true}`)
	st.store.cfg.PortCheck = config.PortCheck{URL: probe.URL + "/check?port={port}"}

	status, page, _ := postPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusOK || page.Result == nil {
		t.Fatalf("POST = %d %+v", status, page)
	}
	res := page.Result
	if res.Port != 51413 || !res.Reachable || !strings.HasPrefix(res.Method, "probe ") || res.CheckedAt == "" {
		t.Fatalf("result = %+v", res)
	}

	status, page = getPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusOK || !page.Enabled || page.Result == nil || !page.Result.Reachable {
		t.Fatalf("GET = %d %+v", status, page)
	}

	events := st.srv.history.Query(history.Filter{}, 10, 0)
	found := false
	for _, ev := range events.Events {
		if ev.Action == "port_check" && ev.Actor == "local" {
			found = true
		}
	}
	if !found {
		t.Fatalf("port_check not in history: %+v", events.Events)
	}
}

// TestPortCheckClosedAndFailures covers an unreachable verdict (stored),
// a probe outage (502, previous verdict kept), and a misconfigured URL.
func TestPortCheckClosedAndFailures(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	closed := probeStub(t, 200, `{"open":false}`)
	st.store.cfg.PortCheck = config.PortCheck{URL: closed.URL + "/{port}"}
	status, page, _ := postPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusOK || page.Result == nil || page.Result.Reachable {
		t.Fatalf("closed = %d %+v", status, page)
	}

	outage := probeStub(t, 502, "bad gateway")
	st.store.cfg.PortCheck = config.PortCheck{URL: outage.URL + "/{port}"}
	status, _, _ = postPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusBadGateway {
		t.Fatalf("outage status = %d", status)
	}
	// The previous verdict survives the failed probe.
	_, page = getPortCheck(t, st.ts.URL+"/api/port-check")
	if page.Result == nil || page.Result.Reachable {
		t.Fatalf("verdict not kept: %+v", page)
	}

	st.store.cfg.PortCheck = config.PortCheck{URL: "https://probe.example/no-placeholder"}
	status, _, raw := postPortCheck(t, st.ts.URL+"/api/port-check")
	if status != http.StatusBadGateway {
		t.Fatalf("bad template status = %d %+v", status, raw)
	}
}

// TestPortCheckPortFallback proves the configured port_range is used when
// the daemon reports no live port.
func TestPortCheckPortFallback(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	var seen []string
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		_, _ = w.Write([]byte(`{"reachable":true}`))
	}))
	t.Cleanup(probe.Close)

	// Point the poller at nothing live: stop it and clear the snapshot port
	// by using a fresh server without a poller but with a configured range.
	rangeStart := "51515-51520"
	st.store.cfg.Tuning.PortRange = &rangeStart
	st.store.cfg.PortCheck = config.PortCheck{URL: probe.URL + "/{port}"}
	srv := New(Options{Store: st.store, History: st.srv.history}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)

	status, page, _ := postPortCheck(t, ts.URL+"/api/port-check")
	if status != http.StatusOK || page.Result == nil || page.Result.Port != 51515 {
		t.Fatalf("fallback = %d %+v", status, page)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "51515") {
		t.Fatalf("probe requests = %+v", seen)
	}
}

// TestSettingsPortCheckRoundTrip proves the probe URL and timeout are
// configurable through the settings API with validation.
func TestSettingsPortCheckRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})

	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning":    map[string]any{},
		"portcheck": map[string]any{"url": "https://probe.example/{port}", "timeout": 5_000_000_000},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	got := st.store.Get()
	if got.PortCheck.URL != "https://probe.example/{port}" {
		t.Fatalf("stored = %+v", got.PortCheck)
	}

	getResp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		PortCheck config.PortCheck `json:"portcheck"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&settings); err != nil {
		getResp.Body.Close()
		t.Fatal(err)
	}
	getResp.Body.Close()
	if settings.PortCheck.URL != "https://probe.example/{port}" {
		t.Fatalf("GET = %+v", settings.PortCheck)
	}

	for _, bad := range []map[string]any{
		{"url": "ftp://probe.example/{port}"},
		{"url": "https://probe.example/no-placeholder"},
		{"url": "https://probe.example/{port}", "timeout": -1},
	} {
		resp, _ := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
			"tuning":    map[string]any{},
			"portcheck": bad,
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid %v status = %d", bad, resp.StatusCode)
		}
	}
}
