package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
)

// seedTraffic feeds two UTC days of deterministic deltas into the test
// stack's tracker: Sep 1 gains 1000/2000, Sep 2 gains 500/100.
func seedTraffic(t *testing.T, st *testStack) {
	t.Helper()
	day1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	st.traffic.Feed(day1, 0, 0) // baseline
	st.traffic.Feed(day1.Add(time.Minute), 1000, 2000)
	st.traffic.Feed(day2, 1000, 2000) // no delta across midnight
	st.traffic.Feed(day2.Add(time.Minute), 1500, 2100)
}

func TestTrafficDaysRange(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	seedTraffic(t, st)

	resp, err := http.Get(st.ts.URL + "/api/traffic?from=2026-09-01&to=2026-09-02")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		From          string `json:"from"`
		To            string `json:"to"`
		RetentionDays int    `json:"retentionDays"`
		Days          []struct {
			Day  string `json:"day"`
			Down int64  `json:"down"`
			Up   int64  `json:"up"`
		} `json:"days"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.From != "2026-09-01" || body.To != "2026-09-02" || body.RetentionDays != 90 {
		t.Fatalf("envelope = %+v", body)
	}
	if len(body.Days) != 2 || body.Days[0].Down != 1000 || body.Days[0].Up != 2000 || body.Days[1].Down != 500 || body.Days[1].Up != 100 {
		t.Fatalf("days = %+v", body.Days)
	}
}

func TestTrafficHoursAndCSV(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	seedTraffic(t, st)

	// Hourly view: the Sep-2 10:00 UTC bucket holds 500/100.
	resp, err := http.Get(st.ts.URL + "/api/traffic?granularity=hour&day=2026-09-02")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Day   string `json:"day"`
		Hours []struct {
			Hour string `json:"hour"`
			Down int64  `json:"down"`
			Up   int64  `json:"up"`
		} `json:"hours"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Day != "2026-09-02" || len(body.Hours) != 24 {
		t.Fatalf("hours envelope = %+v (n=%d)", body.Day, len(body.Hours))
	}
	var ten *struct {
		Hour string `json:"hour"`
		Down int64  `json:"down"`
		Up   int64  `json:"up"`
	}
	for i := range body.Hours {
		if body.Hours[i].Hour == "2026-09-02T10" {
			ten = &body.Hours[i]
		}
	}
	if ten == nil || ten.Down != 500 || ten.Up != 100 {
		t.Fatalf("10:00 bucket = %+v", ten)
	}

	// CSV export carries the same numbers as downloadable text.
	csvResp, err := http.Get(st.ts.URL + "/api/traffic?from=2026-09-01&to=2026-09-02&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer csvResp.Body.Close()
	if ct := csvResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := csvResp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Fatalf("content-disposition = %q", cd)
	}
	raw, err := io.ReadAll(csvResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := "day,down_bytes,up_bytes\n2026-09-01,1000,2000\n2026-09-02,500,100\n"
	if string(raw) != want {
		t.Fatalf("csv = %q", raw)
	}

	// Hourly CSV.
	hcsvResp, err := http.Get(st.ts.URL + "/api/traffic?granularity=hour&day=2026-09-02&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer hcsvResp.Body.Close()
	hraw, err := io.ReadAll(hcsvResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(hraw)), "\n")
	if len(lines) != 25 || lines[0] != "hour,down_bytes,up_bytes" {
		t.Fatalf("hour csv lines = %d first = %q", len(lines), lines[0])
	}
	if !strings.Contains(string(hraw), "2026-09-02T10,500,100") {
		t.Fatalf("hour csv missing 10:00 row: %q", hraw)
	}
}

func TestTrafficBadRequests(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	for url, want := range map[string]int{
		"/api/traffic?from=not-a-date":                   http.StatusBadRequest,
		"/api/traffic?to=2026-13-99":                     http.StatusBadRequest,
		"/api/traffic?from=2026-09-02&to=2026-09-01":     http.StatusBadRequest,
		"/api/traffic?from=2020-01-01&to=2026-09-02":     http.StatusBadRequest, // >366 days
		"/api/traffic?granularity=hour&day=bogus":        http.StatusBadRequest,
		"/api/traffic?granularity=hour":                  http.StatusOK, // day defaults to today
		"/api/traffic?granularity=fortnightly&day=bogus": http.StatusOK, // unknown granularity → daily view
		"/api/traffic": http.StatusOK, // defaults to last 30 days
	} {
		resp, err := http.Get(st.ts.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", url, resp.StatusCode, want)
		}
	}
}

func TestTrafficUnwired(t *testing.T) {
	// A server built without a tracker answers 503, like the other
	// optional-service routes.
	srv := New(Options{Store: &dirTestStore{}}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	resp, err := http.Get(ts.URL + "/api/traffic")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHostEndpoint(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	resp, err := http.Get(st.ts.URL + "/api/host")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Load1     float64 `json:"load1"`
		LoadOK    bool    `json:"loadOK"`
		MemTotal  uint64  `json:"memTotal"`
		MemOK     bool    `json:"memOK"`
		HeapBytes uint64  `json:"heapBytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.HeapBytes == 0 {
		t.Fatal("heap bytes missing")
	}
	// linux/darwin CI runners expose load and memory; other platforms degrade.
	if body.LoadOK && body.Load1 < 0 {
		t.Fatalf("negative load: %+v", body)
	}
	if body.MemOK && body.MemTotal == 0 {
		t.Fatalf("memory total missing: %+v", body)
	}
}

// TestSettingsStatsRoundTrip proves stats.traffic_days is configurable
// through the settings API: GET advertises it, POST persists it, validation
// rejects negatives, and the live tracker picks up the new window.
func TestSettingsStatsRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})

	getResp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Stats config.Stats `json:"stats"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&settings); err != nil {
		getResp.Body.Close()
		t.Fatal(err)
	}
	getResp.Body.Close()
	if settings.Stats.TrafficDays != nil {
		t.Fatalf("default traffic_days should be absent (nil = 90), got %+v", settings.Stats)
	}

	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{},
		"stats":  map[string]any{"traffic_days": 30},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	got := st.store.Get()
	if got.Stats.TrafficDays == nil || *got.Stats.TrafficDays != 30 {
		t.Fatalf("stored stats = %+v", got.Stats)
	}
	if st.traffic.RetentionDays() != 30 {
		t.Fatalf("live tracker retention = %d", st.traffic.RetentionDays())
	}

	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{},
		"stats":  map[string]any{"traffic_days": -1},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative retention status = %d body = %+v", resp.StatusCode, body)
	}
}
