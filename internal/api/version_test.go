package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// POL-8.7: /api/version reports the stamped build plus live daemon versions,
// degrading to dev/none/unknown and empty daemon fields when unwired.
func TestVersionEndpoint(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	resp, err := http.Get(ts.URL + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Blackbird.Version == "" || out.Blackbird.Commit == "" || out.Blackbird.BuildDate == "" {
		t.Fatalf("build not defaulted: %+v", out.Blackbird)
	}
	if out.Connection != "connected" || out.Torrents == 0 {
		t.Fatalf("snapshot part missing: %+v", out)
	}
	if out.RTorrent.Version == "" {
		t.Fatalf("daemon version missing: %+v", out.RTorrent)
	}
}

func TestVersionEndpointUnwired(t *testing.T) {
	srv := New(Options{Build: BuildInfo{}}, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()

	resp, err := http.Get(ts.URL + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Blackbird.Version != "dev" || out.Blackbird.Commit != "none" || out.Blackbird.BuildDate != "unknown" {
		t.Fatalf("defaults wrong: %+v", out.Blackbird)
	}
	if out.RTorrent.Version != "" || out.Connection != "" || out.Torrents != 0 {
		t.Fatalf("unwired daemon should be empty: %+v", out)
	}
}

func TestVersionEndpointStamped(t *testing.T) {
	srv := New(Options{Build: BuildInfo{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-09-03"}}, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()

	resp, err := http.Get(ts.URL + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Blackbird.Version != "1.2.3" || out.Blackbird.Commit != "abc123" || out.Blackbird.BuildDate != "2026-09-03" {
		t.Fatalf("stamped build not reported: %+v", out.Blackbird)
	}
}

// POL-8.8: every REST route serves under /api/v1 with the same handler, and
// /api/v1/version advertises the contract versions.
func TestV1PrefixParity(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	for _, path := range []string{"/api/version", "/api/v1/version"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var out VersionResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
		if out.API.Version != "v1" || out.WS.Current != 2 || out.WS.Min != 1 {
			t.Fatalf("%s contract = %+v", path, out)
		}
	}

	// Legacy and versioned health agree.
	for _, path := range []string{"/api/health", "/api/v1/health"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var h HealthInfo
		if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if h.Connection != "connected" {
			t.Fatalf("%s = %+v", path, h)
		}
	}
}
