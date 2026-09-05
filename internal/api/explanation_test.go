package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/history"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
	"blackbird/internal/schedule"
)

func explanationFixture() (rtorrent.Torrent, *poller.Snapshot, *config.Config, time.Time) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	t := rtorrent.Torrent{Hash: "abc", Name: "Archive", State: rtorrent.StateStopped, Complete: true, Ratio: 2.5, Custom3: "archive", Throttle: "slow", SkippedBytes: 1024}
	down, up := int64(100), int64(50)
	cfg := &config.Config{
		Tuning:  config.Tuning{GlobalDownRateKB: &down, GlobalUpRateKB: &up, Throttles: []config.ThrottleChannel{{Name: "slow", DownKB: 10, UpKB: 5}}},
		Seeding: config.Seeding{CustomSlot: "custom3", Groups: []config.SeedingGroup{{Name: "archive", MinRatio: 2, Action: config.SeedingStop}}},
	}
	return t, &poller.Snapshot{Status: poller.StatusConnected, GeneratedAt: now.Add(-time.Second)}, cfg, now
}

func findExplanation(t *testing.T, out explanationResponse, id string) explanationFinding {
	t.Helper()
	for _, f := range out.Findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("missing %s in %+v", id, out.Findings)
	return explanationFinding{}
}

func hasExplanation(out explanationResponse, id string) bool {
	for _, f := range out.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func TestExplanationKeepsConstraintsSeparateFromHistoricalCause(t *testing.T) {
	row, snap, cfg, now := explanationFixture()
	events := []history.Entry{
		{At: now.Add(-5 * time.Second), Kind: history.KindAction, Actor: "op", Action: "start", Result: "failed"},
		{At: now.Add(-time.Minute), Kind: history.KindAction, Actor: "seeding", Action: "stop", Result: "ok", Message: `group "previous" met min_ratio`},
	}
	sched := &schedule.Status{Overridden: true, OverrideDownKB: 200, OverrideUpKB: 20, OverrideUntil: now.Add(time.Hour)}
	out := explainTorrent(row, snap, cfg, sched, events, nil, now)
	if out.Stale {
		t.Fatal("fresh snapshot marked stale")
	}
	if f := findExplanation(t, out, "stop-cause"); f.Kind != "unknown" {
		t.Fatalf("inferred cause: %+v", f)
	}
	if f := findExplanation(t, out, "seeding"); f.Kind != "constraint" || !strings.Contains(f.Title, "condition is met") {
		t.Fatalf("policy match: %+v", f)
	}
	for _, id := range []string{"global-limits", "channel", "override", "skipped"} {
		if f := findExplanation(t, out, id); f.Kind != "constraint" || f.Target == nil {
			t.Fatalf("missing constraint/navigation: %+v", f)
		}
	}
	for _, id := range []string{"action-0", "action-1"} {
		if f := findExplanation(t, out, id); f.Kind != "recorded_action" {
			t.Fatalf("action reclassified: %+v", f)
		}
	}
	if f := findExplanation(t, out, "action-0"); !strings.Contains(f.Evidence[0].Value, "result=failed") {
		t.Fatalf("failed request misrepresented: %+v", f)
	}
	for _, f := range out.Findings {
		if len(f.Evidence) == 0 || f.Evidence[0].ObservedAt == nil {
			t.Fatalf("missing timestamped evidence: %+v", f)
		}
	}
}

func TestExplanationMissingAndExternalEvidence(t *testing.T) {
	row, snap, cfg, now := explanationFixture()
	out := explainTorrent(row, snap, cfg, nil, nil, nil, now)
	if hasExplanation(out, "action-0") || findExplanation(t, out, "stop-cause").Kind != "unknown" {
		t.Fatal("fabricated historical action from a current policy match")
	}
	if !strings.Contains(strings.Join(out.Coverage, " "), "No retained transport action") {
		t.Fatal("missing history not disclosed")
	}
	out = explainTorrent(row, snap, nil, nil, nil, nil, now)
	findExplanation(t, out, "missing-config")
	row.Message, row.State = "Disk read error", rtorrent.StateError
	out = explainTorrent(row, snap, cfg, nil, nil, nil, now)
	if f := findExplanation(t, out, "message"); f.Evidence[0].Value != row.Message || f.Kind != "observation" {
		t.Fatalf("lost daemon evidence: %+v", f)
	}
	row.Message = ""
	out = explainTorrent(row, snap, cfg, nil, nil, nil, now)
	findExplanation(t, out, "missing-error")
}

func TestExplanationStaleSamplesDoNotSupportStallClaims(t *testing.T) {
	for _, scenario := range []string{"disconnected", "flag", "aged", "missing-time", "future-time"} {
		t.Run(scenario, func(t *testing.T) {
			row, snap, cfg, now := explanationFixture()
			switch scenario {
			case "disconnected":
				snap.Status = poller.StatusDisconnected
			case "flag":
				snap.Stale = true
			case "aged":
				snap.GeneratedAt = now.Add(-5 * time.Minute)
			case "missing-time":
				snap.GeneratedAt = time.Time{}
			case "future-time":
				snap.GeneratedAt = now.Add(time.Minute)
			}
			out := explainTorrent(row, snap, cfg, nil, nil, nil, now)
			if !out.Stale || hasExplanation(out, "quiet-window") {
				t.Fatalf("stale evidence mishandled: %+v", out)
			}
		})
	}
}

func TestExplanationRateCoverageAndPeerUncertainty(t *testing.T) {
	row, snap, cfg, now := explanationFixture()
	row.State, row.Complete = rtorrent.StateDownloading, false
	cfg.Poll.Interval, cfg.Poll.MaxInterval = 2*time.Second, 2*time.Second
	samples := []poller.Sample{}
	for i := 12; i >= 0; i -= 2 {
		samples = append(samples, poller.Sample{At: snap.GeneratedAt.Add(-time.Duration(i) * time.Second)})
	}
	out := explainTorrent(row, snap, cfg, nil, nil, samples, now)
	findExplanation(t, out, "quiet-window")
	if f := findExplanation(t, out, "peers"); f.Kind != "hypothesis" || !strings.Contains(f.Summary, "does not establish global availability") {
		t.Fatalf("overclaimed availability: %+v", f)
	}
	for _, scenario := range []string{"short", "gap", "activity", "old", "disconnected"} {
		t.Run(scenario, func(t *testing.T) {
			copySamples := append([]poller.Sample{}, samples...)
			copySnap := *snap
			switch scenario {
			case "short":
				copySamples = copySamples[len(copySamples)-1:]
			case "gap":
				copySamples = []poller.Sample{copySamples[0], copySamples[len(copySamples)-1]}
			case "activity":
				copySamples[2].DownRate = 100
			case "old":
				for i := range copySamples {
					copySamples[i].At = copySamples[i].At.Add(-20 * time.Second)
				}
			case "disconnected":
				copySnap.Stale = true
			}
			out := explainTorrent(row, &copySnap, cfg, nil, nil, copySamples, now)
			if hasExplanation(out, "quiet-window") {
				t.Fatal("insufficient evidence produced sustained zero-rate claim")
			}
		})
	}
}

func TestExplanationScheduleAndUnavailableDefinitions(t *testing.T) {
	row, snap, cfg, now := explanationFixture()
	cfg.Schedule.Bandwidth.Profiles = []config.BandwidthProfile{{Name: "night", DownKB: 500, Throttles: []config.ThrottleChannel{{Name: "slow", DownKB: 50}}}}
	sched := &schedule.Status{ActiveProfile: "night"}
	out := explainTorrent(row, snap, cfg, sched, nil, nil, now)
	f := findExplanation(t, out, "schedule")
	if !strings.Contains(f.Evidence[0].Value, "channel=slow: download=50") || !strings.Contains(f.Evidence[0].Value, "unlimited (0)") {
		t.Fatalf("profile channel/unlimited missing: %+v", f)
	}
	sched.Overridden, sched.OverrideUntil = true, now.Add(-time.Second)
	out = explainTorrent(row, snap, cfg, sched, nil, nil, now)
	if f := findExplanation(t, out, "override"); f.Kind != "unknown" || hasExplanation(out, "schedule") {
		t.Fatal("expired override reported as current or conflicting profile")
	}
	row.Throttle, row.Custom3 = "external", "external"
	out = explainTorrent(row, snap, cfg, nil, nil, nil, now)
	if f := findExplanation(t, out, "channel"); !strings.Contains(f.Evidence[1].Value, "limits unknown") {
		t.Fatal("unknown daemon channel invented limits")
	}
	if f := findExplanation(t, out, "seeding"); f.Kind != "unknown" {
		t.Fatal("unknown group treated as configured")
	}
}

func TestExplanationIgnoresExpiredHistory(t *testing.T) {
	row, snap, cfg, now := explanationFixture()
	cfg.History.ActionLogRetention = time.Hour
	events := []history.Entry{{At: now.Add(-2 * time.Hour), Kind: history.KindAction, Action: "stop", Result: "ok"}}
	out := explainTorrent(row, snap, cfg, nil, events, nil, now)
	if hasExplanation(out, "action-0") {
		t.Fatal("expired history included")
	}
}

func TestExplanationEndpointUsesCacheWithoutDaemonClient(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	// This server has no RPC client. Both route spellings must still work.
	srv := New(Options{Poller: st.p, Store: st.store}, nil)
	defer srv.Close()
	for _, prefix := range []string{"/api", "/api/v1"} {
		r := httptest.NewRequest(http.MethodGet, prefix+"/torrents/"+historyTestHash+"?view=explanation", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		var out explanationResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Hash != historyTestHash || len(out.Findings) == 0 {
			t.Fatalf("explanation=%d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing cache policy")
		}
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/torrents/missing?view=explanation", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing torrent status=%d", w.Code)
	}
}

func TestExplanationEndpointUnavailableAndAuthenticated(t *testing.T) {
	srv := New(Options{}, nil)
	defer srv.Close()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/torrents/abc?view=explanation", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing poller=%d", w.Code)
	}
	// Authentication must gate the new route before it can return any data.
	secure := New(Options{}, NewAuth("op", "$2a$10$invalid-but-nonempty", nil))
	defer secure.Close()
	w = httptest.NewRecorder()
	secure.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/torrents/abc?view=explanation", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated route=%d", w.Code)
	}
}

func BenchmarkExplainTorrent(b *testing.B) {
	row, snap, cfg, now := explanationFixture()
	b.ReportAllocs()
	for b.Loop() {
		explainTorrent(row, snap, cfg, nil, nil, nil, now)
	}
}
