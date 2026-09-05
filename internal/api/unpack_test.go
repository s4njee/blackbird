package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"blackbird/internal/history"
	"blackbird/internal/unpack"
)

// stubRunner is a minimal unpack.Runner for the status endpoint test.
type stubRunner struct{ available bool }

func (s *stubRunner) Available() (string, bool) {
	if !s.available {
		return "", false
	}
	return "/usr/bin/7z", true
}

func (s *stubRunner) List(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *stubRunner) Extract(_ context.Context, _, _ string, _ func(int)) error {
	return nil
}

func newUnpackTestServer(t *testing.T, runner unpack.Runner) *httptest.Server {
	t.Helper()
	svc := unpack.New(unpack.Options{
		History: history.New(history.Options{}),
		Runner:  runner,
	})
	srv := New(Options{Unpack: svc, History: history.New(history.Options{})}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return ts
}

func TestUnpackStatusEndpoint(t *testing.T) {
	ts := newUnpackTestServer(t, &stubRunner{available: true})
	resp, err := http.Get(ts.URL + "/api/unpack")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Available bool   `json:"available"`
		Binary    string `json:"binary"`
		Workers   int    `json:"workers"`
		Queue     int    `json:"queue"`
		Jobs      []any  `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Binary != "/usr/bin/7z" || body.Workers != 2 {
		t.Fatalf("body = %+v", body)
	}

	// Missing extractor surfaces as unavailable (drives the Settings message).
	ts2 := newUnpackTestServer(t, &stubRunner{available: false})
	resp2, err := http.Get(ts2.URL + "/api/unpack")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var body2 struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	if body2.Available {
		t.Fatal("missing extractor reported available")
	}
}

func TestUnpackEndpointUnavailableWithoutService(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	resp, err := http.Get(ts.URL + "/api/unpack")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
