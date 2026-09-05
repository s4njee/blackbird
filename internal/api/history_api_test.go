package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/history"
	"blackbird/internal/schedule"
)

const historyTestHash = "aaaa1111aaaa1111"
const historyTestName = "ubuntu-24.04.2-desktop-amd64.iso"

type historyPage struct {
	Events []struct {
		Seq    uint64 `json:"seq"`
		At     string `json:"at"`
		Hash   string `json:"hash"`
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Actor  string `json:"actor"`
		Action string `json:"action"`
		Result string `json:"result"`
	} `json:"events"`
	NextBeforeSeq uint64 `json:"nextBeforeSeq"`
	HasMore       bool   `json:"hasMore"`
}

func getHistory(t *testing.T, url string) (int, historyPage) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page historyPage
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, page
}

// TestHistoryEndpointEndToEnd drives real API actions and asserts they land
// in the paginated global log with torrent names resolved.
func TestHistoryEndpointEndToEnd(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	if status, page := getHistory(t, st.ts.URL+"/api/history"); status != http.StatusOK || len(page.Events) != 0 {
		t.Fatalf("empty history = %d %+v", status, page)
	}

	// A batch action logs with the session name resolved.
	resp, _ := postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{"action": "start", "hashes": []string{historyTestHash}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("action status = %d", resp.StatusCode)
	}
	status, page := getHistory(t, st.ts.URL+"/api/history")
	if status != http.StatusOK || len(page.Events) != 1 {
		t.Fatalf("history = %d %+v", status, page)
	}
	ev := page.Events[0]
	if ev.Kind != "action" || ev.Action != "start" || ev.Hash != historyTestHash || ev.Name != historyTestName || ev.Actor != "local" || ev.Seq == 0 {
		t.Fatalf("event = %+v", ev)
	}

	// A magnet add logs under its btih hash even though the daemon assigns
	// the display name later.
	magnet := "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=smoke"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("magnets", magnet); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, st.ts.URL+"/api/torrents/add", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	addResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	addResp.Body.Close()
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d", addResp.StatusCode)
	}
	status, page = getHistory(t, st.ts.URL+"/api/history?kind=add")
	if status != http.StatusOK || len(page.Events) != 1 || page.Events[0].Hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("magnet history = %d %+v", status, page)
	}

	// Filters compose: kind + free-text search, and unknown kinds 400.
	if _, page := getHistory(t, st.ts.URL+"/api/history?kind=action&q=ubuntu"); len(page.Events) != 1 {
		t.Fatalf("filtered = %+v", page.Events)
	}
	if _, page := getHistory(t, st.ts.URL+"/api/history?q=nomatch"); len(page.Events) != 0 {
		t.Fatalf("unmatched search = %+v", page.Events)
	}
	if status, _ := getHistory(t, st.ts.URL+"/api/history?kind=bogus"); status != http.StatusBadRequest {
		t.Fatalf("bad kind status = %d", status)
	}
	if status, _ := getHistory(t, st.ts.URL+"/api/history?limit=nope"); status != http.StatusBadRequest {
		t.Fatalf("bad limit status = %d", status)
	}
}

// TestHistoryPagination walks two pages through the stack's log and proves
// the cursor is stable.
func TestHistoryPagination(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	for _, action := range []string{"start", "pause", "stop", "recheck"} {
		resp, _ := postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{"action": action, "hashes": []string{historyTestHash}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("action %s status = %d", action, resp.StatusCode)
		}
	}
	status, p1 := getHistory(t, st.ts.URL+"/api/history?limit=3")
	if status != http.StatusOK || len(p1.Events) != 3 || !p1.HasMore || p1.NextBeforeSeq == 0 {
		t.Fatalf("page1 = %d %+v", status, p1)
	}
	if p1.Events[0].Action != "recheck" {
		t.Fatalf("newest first broken: %+v", p1.Events[0])
	}
	_, p2 := getHistory(t, st.ts.URL+"/api/history?limit=3&before_seq="+strconv.FormatUint(p1.NextBeforeSeq, 10))
	if len(p2.Events) != 1 || p2.HasMore || p2.Events[0].Action != "start" {
		t.Fatalf("page2 = %+v", p2)
	}
}

// TestSettingsGlobalEntriesRoundTrip proves history.global_entries is
// configurable through the settings API with validation and live bounds.
func TestSettingsGlobalEntriesRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})

	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning":  map[string]any{},
		"history": map[string]any{"global_entries": 100},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	if got := st.store.Get(); got.History.GlobalEntries != 100 {
		t.Fatalf("stored history = %+v", got.History)
	}

	getResp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		History config.History `json:"history"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&settings); err != nil {
		getResp.Body.Close()
		t.Fatal(err)
	}
	getResp.Body.Close()
	if settings.History.GlobalEntries != 100 {
		t.Fatalf("GET history = %+v", settings.History)
	}

	resp, _ = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning":  map[string]any{},
		"history": map[string]any{"global_entries": -1},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative retention status = %d", resp.StatusCode)
	}
}

// TestScheduleOverrideLogsHistory wires a scheduler into a second server on
// the same stack and asserts user-initiated overrides land in the history
// with the request actor.
func TestScheduleOverrideLogsHistory(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	log := history.New(history.Options{})
	svc := schedule.New(schedule.Options{
		Daemon:  st.rtc,
		Config:  func() config.Schedule { return config.Schedule{} },
		History: log,
	})
	srv := New(Options{
		Poller:   st.p,
		RTorrent: st.rtc,
		Store:    st.store,
		History:  log,
		Schedule: svc,
	}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)

	resp, _ := postJSON(t, ts.URL+"/api/schedule/override", map[string]any{"minutes": 30, "downKb": 100, "upKb": 50})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("override status = %d", resp.StatusCode)
	}
	res := log.Query(history.Filter{}, 10, 0)
	if len(res.Events) != 1 || res.Events[0].Actor != "local" || res.Events[0].Action != "schedule_override" {
		t.Fatalf("override history = %+v", res.Events)
	}

	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/schedule/override", nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d", delResp.StatusCode)
	}
	res = log.Query(history.Filter{}, 10, 0)
	if len(res.Events) != 2 || res.Events[0].Action != "schedule_override_clear" {
		t.Fatalf("clear history = %+v", res.Events)
	}
}

// TestSchedulerLogsProfileApply proves automatic profile applications are
// recorded with the scheduler actor (unit level, fake daemon).
func TestSchedulerLogsProfileApply(t *testing.T) {
	daemon := &stubSchedulerDaemon{}
	log := history.New(history.Options{})
	grid := map[string][]string{"mon": {"day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day"}}
	svc := schedule.New(schedule.Options{
		Daemon: daemon,
		Config: func() config.Schedule {
			return config.Schedule{Bandwidth: config.BandwidthSchedule{
				Profiles: []config.BandwidthProfile{{Name: "day", DownKB: 100, UpKB: 50}},
				Grid:     grid,
			}}
		},
		History: log,
		Now:     func() time.Time { return time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC) }, // a Monday
	})
	svc.Tick(time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC))
	res := log.Query(history.Filter{Actor: "scheduler"}, 10, 0)
	if len(res.Events) != 1 || res.Events[0].Action != "schedule_profile" || res.Events[0].Hash != "" {
		t.Fatalf("scheduler history = %+v", res.Events)
	}
	// Re-tick with the same profile: no duplicate event.
	svc.Tick(time.Date(2026, 9, 7, 10, 1, 0, 0, time.UTC))
	if n := len(log.Query(history.Filter{}, 10, 0).Events); n != 1 {
		t.Fatalf("duplicate profile events = %d", n)
	}
}
