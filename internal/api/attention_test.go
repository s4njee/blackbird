package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/attention"
	"blackbird/internal/history"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

func TestAttentionAPIStateControlsSummaryAndEvidence(t *testing.T) {
	now := time.Now().Add(-time.Second)
	inbox, err := attention.Open(attention.Options{Path: filepath.Join(t.TempDir(), "attention.json"), Interval: time.Hour, Source: func() attention.Input {
		return attention.Input{Snapshot: &poller.Snapshot{GeneratedAt: now, Status: poller.StatusConnected, Torrents: []rtorrent.Torrent{{Hash: "abc", State: rtorrent.StateError, Message: "Tracker: failure", TrackerHost: "tracker.example"}}}}
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go inbox.Run(ctx)
	defer func() { cancel(); _ = inbox.Wait(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for len(inbox.Snapshot().Incidents) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(inbox.Snapshot().Incidents) != 1 {
		t.Fatal("incident not observed")
	}
	log := history.New(history.Options{})
	log.Add("abc", history.Entry{At: now, Kind: history.KindComplete, Action: "complete", Result: "ok"})
	log.Add("abc", history.Entry{At: now, Kind: history.KindMove, Action: "move_files", Result: "ok"})
	log.Add("abc", history.Entry{At: now, Kind: history.KindAction, Actor: "automation", Action: "set_label", Result: "ok"})
	log.Add("abc", history.Entry{At: now, Kind: history.KindMove, Action: "move_files", Result: "failed"})
	srv := New(Options{Attention: inbox, History: log}, NewAuth("", "", nil))
	defer srv.Close()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/api/v1/attention", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		State       attention.State       `json:"state"`
		Completed   []attentionCompletion `json:"completed"`
		GeneratedAt time.Time             `json:"generatedAt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Completed) != 3 {
		t.Fatalf("summary omits successes or includes failure: %s", w.Body.String())
	}
	id := response.State.Incidents[0].ID
	for _, action := range []string{"snooze", "acknowledge"} {
		w = call("POST", "/api/v1/attention", fmt.Sprintf(`{"id":%q,"episode":1,"action":%q,"seconds":3600}`, id, action))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	if inbox.Snapshot().Incidents[0].Status != "acknowledged" {
		t.Fatal("control did not persist")
	}
	w = call("POST", "/api/v1/attention", fmt.Sprintf(`{"action":"visit","visitedAt":%q}`, response.GeneratedAt.Format(time.RFC3339Nano)))
	if w.Code != 200 || inbox.Snapshot().LastVisit == nil {
		t.Fatal("visit not saved")
	}
	w = call("GET", "/api/v1/attention", "")
	if !strings.Contains(w.Body.String(), `"completedCount":0`) {
		t.Fatal("since last visit not applied")
	}
	w = call("GET", "/api/v1/attention?summary=1", "")
	if strings.Contains(w.Body.String(), "tracker.example") || !strings.Contains(w.Body.String(), `"open":0`) {
		t.Fatal("notice summary should stay compact")
	}
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/v1/attention", fmt.Sprintf(`{"id":%q,"episode":2,"action":"acknowledge"}`, id), 409},
		{"/api/v1/attention", fmt.Sprintf(`{"id":%q,"episode":1,"action":"snooze","seconds":-1}`, id), 400},
		{"/api/v1/attention", fmt.Sprintf(`{"id":%q,"episode":1,"action":"snooze","seconds":9999999999999}`, id), 400},
		{"/api/v1/attention", `{"action":"visit","visitedAt":"2999-01-01T00:00:00Z"}`, 400},
	} {
		w = call("POST", tc.path, tc.body)
		if w.Code != tc.code {
			t.Fatalf("bad action: %d, want %d: %s", w.Code, tc.code, w.Body.String())
		}
	}
	if w = call("GET", "/api/v1/attention?since=bad", ""); w.Code != 400 {
		t.Fatal("invalid since accepted")
	}
}
func TestAttentionAPIUnavailableAndAuth(t *testing.T) {
	srv := New(Options{}, NewAuth("", "", nil))
	defer srv.Close()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/attention", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable: %d", w.Code)
	}
	auth := NewAuth("operator", "not-a-real-hash", nil)
	secured := New(Options{}, auth)
	defer secured.Close()
	for _, method := range []string{"GET", "POST"} {
		w = httptest.NewRecorder()
		secured.Handler().ServeHTTP(w, httptest.NewRequest(method, "/api/v1/attention", strings.NewReader(`{"action":"visit"}`)))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("auth %s: %d", method, w.Code)
		}
	}
}
