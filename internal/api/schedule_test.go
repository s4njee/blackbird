package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/history"
	"blackbird/internal/schedule"
)

// stubSchedulerDaemon records limit applications for the schedule API tests.
type stubSchedulerDaemon struct {
	globals  map[string]int64
	channels map[string][2]int64
}

func (d *stubSchedulerDaemon) SetGlobalRateKB(_ context.Context, setter string, n int64) error {
	if d.globals == nil {
		d.globals = map[string]int64{}
	}
	d.globals[setter] = n
	return nil
}

func (d *stubSchedulerDaemon) SetThrottleUp(_ context.Context, name string, kb int64) error {
	if d.channels == nil {
		d.channels = map[string][2]int64{}
	}
	pair := d.channels[name]
	pair[0] = kb
	d.channels[name] = pair
	return nil
}

func (d *stubSchedulerDaemon) SetThrottleDown(_ context.Context, name string, kb int64) error {
	if d.channels == nil {
		d.channels = map[string][2]int64{}
	}
	pair := d.channels[name]
	pair[1] = kb
	d.channels[name] = pair
	return nil
}

func fullDayGrid(name string) []string {
	out := make([]string, 24)
	for i := range out {
		out[i] = name
	}
	return out
}

func newScheduleTestServer(t *testing.T, cfg config.Schedule) (*httptest.Server, *schedule.Scheduler, *stubSchedulerDaemon) {
	t.Helper()
	daemon := &stubSchedulerDaemon{}
	current := cfg
	svc := schedule.New(schedule.Options{
		Daemon: daemon,
		Config: func() config.Schedule { return current },
	})
	srv := New(Options{Schedule: svc, History: history.New(history.Options{})}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return ts, svc, daemon
}

func TestScheduleStatusEndpoint(t *testing.T) {
	cfg := config.Schedule{
		Bandwidth: config.BandwidthSchedule{
			Profiles: []config.BandwidthProfile{{Name: "day", DownKB: 1000, UpKB: 500}},
			Grid:     map[string][]string{"mon": fullDayGrid("day")},
		},
	}
	ts, svc, _ := newScheduleTestServer(t, cfg)

	// Monday 2026-09-07 10:00 UTC applies "day" on demand.
	svc.Tick(time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC))
	resp, err := http.Get(ts.URL + "/api/schedule")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		ActiveProfile string `json:"activeProfile"`
		Overridden    bool   `json:"overridden"`
		NextProfile   string `json:"nextProfile"`
		NextChange    string `json:"nextChange"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ActiveProfile != "day" || body.Overridden {
		t.Fatalf("status = %+v", body)
	}
	if body.NextProfile != "" {
		// The whole test grid is "day" on Monday only; other days are empty,
		// so no change is scheduled within the scan.
		t.Fatalf("next = %+v", body)
	}
	_ = svc
}

func TestScheduleOverrideEndpoints(t *testing.T) {
	cfg := config.Schedule{
		Bandwidth: config.BandwidthSchedule{
			Profiles: []config.BandwidthProfile{{Name: "day", DownKB: 1000, UpKB: 500}},
			Grid:     map[string][]string{"mon": fullDayGrid("day")},
		},
	}
	ts, svc, daemon := newScheduleTestServer(t, cfg)
	svc.Tick(time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC))

	// Invalid override rejected.
	resp, _ := postJSON(t, ts.URL+"/api/schedule/override", map[string]any{"minutes": 0})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Valid override applies immediately and reports.
	resp, body := postJSON(t, ts.URL+"/api/schedule/override", map[string]any{"minutes": 30, "downKb": 100, "upKb": 50})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("override = %d %+v", resp.StatusCode, body)
	}
	if daemon.globals["throttle.global_down.max_rate.set_kb"] != 100 {
		t.Fatalf("override not applied: %+v", daemon.globals)
	}
	if overridden, _ := body["overridden"].(bool); !overridden {
		t.Fatalf("status = %+v", body)
	}

	// Cancel restores scheduling on next tick (forced here via status shape).
	resp, _ = postJSON(t, ts.URL+"/api/schedule/override", map[string]any{"minutes": 30, "downKb": 0, "upKb": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-override = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/schedule/override", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d", delResp.StatusCode)
	}
	var cleared struct {
		Overridden bool `json:"overridden"`
	}
	if err := json.NewDecoder(delResp.Body).Decode(&cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Overridden {
		t.Fatal("override not cleared")
	}
}

func TestScheduleEndpointsUnavailableWithoutService(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	resp, err := http.Get(ts.URL + "/api/schedule")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// TestScheduleSettingsRoundTrip asserts the schedule section persists with
// validation through the settings API.
func TestScheduleSettingsRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	grid := map[string]any{}
	for _, day := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
		row := make([]any, 24)
		for h := range row {
			row[h] = "day"
		}
		grid[day] = row
	}
	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{},
		"schedule": map[string]any{
			"timezone": "America/Chicago",
			"bandwidth": map[string]any{
				"profiles": []map[string]any{{"name": "day", "color": "#f59e0b", "down_kb": 1000, "up_kb": 500}},
				"grid":     grid,
			},
		},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	got := st.store.Get().Schedule
	if got.Timezone != "America/Chicago" || len(got.Bandwidth.Profiles) != 1 || len(got.Bandwidth.Grid["sun"]) != 24 {
		t.Fatalf("schedule = %+v", got.Bandwidth)
	}
}
