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

func getBandwidth(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestBandwidthRoundTrip asserts the PAR-4.4 contract: POST applies exact
// KiB/s caps via set_kb and GET reads them back with live rates.
func TestBandwidthRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	resp, body := postJSON(t, st.ts.URL+"/api/bandwidth", map[string]any{"downKb": 1000, "upKb": 500})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST = %d %+v", resp.StatusCode, body)
	}
	if body["downKb"] != float64(1000) || body["upKb"] != float64(500) {
		t.Fatalf("POST response = %+v", body)
	}
	found := false
	for _, m := range st.daemon.CallMethods() {
		if m == "throttle.global_down.max_rate.set_kb" {
			found = true
		}
	}
	if !found {
		t.Fatalf("daemon never received set_kb: %v", st.daemon.CallMethods())
	}

	got := getBandwidth(t, st.ts.URL+"/api/bandwidth")
	if got["downKb"] != float64(1000) || got["upKb"] != float64(500) {
		t.Fatalf("GET = %+v", got)
	}
	// Live rates come from the canned fixture.
	if got["downRateBps"] != float64(41200000) || got["upRateBps"] != float64(12800000) {
		t.Fatalf("rates = %+v", got)
	}

	// Negative limits are rejected.
	resp, _ = postJSON(t, st.ts.URL+"/api/bandwidth", map[string]any{"downKb": -1, "upKb": 0})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative status = %d", resp.StatusCode)
	}

	// Unlimited round-trips as zero.
	resp, body = postJSON(t, st.ts.URL+"/api/bandwidth", map[string]any{"downKb": 0, "upKb": 0})
	if resp.StatusCode != http.StatusOK || body["downKb"] != float64(0) {
		t.Fatalf("unlimited = %d %+v", resp.StatusCode, body)
	}
}

// TestBandwidthRoutesIntoOverride asserts a POST while a scheduler override
// is active updates the override (keeping expiry) instead of diverging the
// daemon from the recorded override.
func TestBandwidthRoutesIntoOverride(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	daemon := &stubSchedulerDaemon{}
	svc := schedule.New(schedule.Options{
		Daemon: daemon,
		Config: func() config.Schedule { return config.Schedule{} },
	})
	ctx := context.Background()
	if err := svc.SetOverride(ctx, 100, 50, time.Now().Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Poller:   st.p,
		RTorrent: st.rtc,
		Store:    st.store,
		History:  history.New(history.Options{}),
		Schedule: svc,
	}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)

	resp, body := postJSON(t, ts.URL+"/api/bandwidth", map[string]any{"downKb": 200, "upKb": 100})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST = %d %+v", resp.StatusCode, body)
	}
	if daemon.globals["throttle.global_down.max_rate.set_kb"] != 200 {
		t.Fatalf("override not updated: %+v", daemon.globals)
	}
	st2 := svc.Status(time.Now())
	if !st2.Overridden || st2.OverrideDownKB != 200 {
		t.Fatalf("override status = %+v", st2)
	}
}

func TestBandwidthUnavailableWithoutDaemon(t *testing.T) {
	srv := New(Options{}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()
	resp, err := http.Get(ts.URL + "/api/bandwidth")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
