package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/fakertorrent"
	"blackbird/internal/history"
)

func TestFlightAPIRecordsManualIntentAndResultAndExportsWindow(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	recorder, err := history.OpenRecorder(history.RecorderOptions{Path: filepath.Join(t.TempDir(), "flight.jsonl"), FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = recorder.Close(ctx)
	})
	log := history.New(history.Options{Recorder: recorder})
	log.RecordConfig(st.store.Get(), "configuration", "")
	srv := New(Options{Poller: st.p, RTorrent: st.rtc, Store: st.store, History: log}, NewAuth("", "", nil))
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, _ := postJSON(t, ts.URL+"/api/v1/torrents/action", map[string]any{"hashes": []string{historyTestHash}, "action": "start"})
	if resp.StatusCode != 200 {
		t.Fatalf("action=%d", resp.StatusCode)
	}
	recorder.Record("other", history.Entry{Phase: "observation", Message: "exclude other torrent"})
	recorder.Record(historyTestHash, history.Entry{Phase: "observation", Name: "secret-name", Message: "token=abc123 http://user:pwd@example.test/passkey"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recorder.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var view history.Recording
	response, err := http.Get(ts.URL + "/api/v1/history/flight?hash=" + historyTestHash)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	var intent, result history.Event
	for _, e := range view.Events {
		if e.Hash == "other" {
			t.Fatal("hash filter leaked another torrent")
		}
		if e.Action == "start" && e.Phase == "intent" {
			intent = e
		}
		if e.Action == "start" && e.Phase == "rpc_result" {
			result = e
		}
	}
	if intent.ID == "" || result.CauseID != intent.ID || intent.Before["state"] == "" || result.Result != "ok" || intent.Revision == "" {
		t.Fatalf("incomplete intent/result: %+v %+v", intent, result)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/history/flight?hash="+historyTestHash+"&export=1", nil))
	for _, secret := range []string{"secret-name", "abc123", "user:pwd", "passkey"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("export leaked %s", secret)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing cache header")
	}
	for _, q := range []string{"from=bad", "limit=0", "limit=1001", "from=2026-09-04T00:00:00Z&to=2026-09-03T00:00:00Z"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/history/flight?"+q, nil))
		if w.Code != 400 {
			t.Fatalf("invalid query %s returned %d", q, w.Code)
		}
	}
}

func TestFlightAPINoRecorderAndAuthentication(t *testing.T) {
	srv := New(Options{}, nil)
	defer srv.Close()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/history/flight", nil))
	if w.Code != 503 {
		t.Fatalf("status=%d", w.Code)
	}
	secure := New(Options{}, NewAuth("op", "configured-hash", nil))
	defer secure.Close()
	w = httptest.NewRecorder()
	secure.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/history/flight?export=1", nil))
	if w.Code != 401 {
		t.Fatalf("auth=%d", w.Code)
	}
}
