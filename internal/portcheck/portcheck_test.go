package portcheck

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func probeServer(t *testing.T, status int, body string, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestCheckReachable(t *testing.T) {
	ts := probeServer(t, 200, `{"reachable":true}`, 0)
	defer ts.Close()
	res, err := Check(t.Context(), ts.URL+"/check?port={port}", 51413, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable || res.Port != 51413 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.HasPrefix(res.Method, "probe ") || res.CheckedAt.IsZero() {
		t.Fatalf("result = %+v", res)
	}
}

func TestCheckOpenAliasAndClosed(t *testing.T) {
	ts := probeServer(t, 200, `{"open":false}`, 0)
	defer ts.Close()
	res, err := Check(t.Context(), ts.URL+"/{port}", 6881, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reachable {
		t.Fatalf("closed reported open: %+v", res)
	}
}

func TestCheckDisabled(t *testing.T) {
	if _, err := Check(t.Context(), "", 51413, 0); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Check(t.Context(), "   ", 51413, 0); !errors.Is(err, ErrDisabled) {
		t.Fatalf("blank err = %v", err)
	}
}

func TestCheckTemplateValidation(t *testing.T) {
	for url, want := range map[string]string{
		"https://probe.example/check":               "placeholder",
		"ftp://probe.example/{port}":                "http(s)",
		"http:///no-host/{port}":                    "absolute",
		"/relative/{port}":                          "absolute",
		"https://probe.example/{port}?p={port}&x=1": "",
	} {
		_, err := Check(t.Context(), url, 51413, time.Millisecond)
		if want == "" {
			// Valid template: the request fails only on connection, after
			// validation passes. probe.example does not resolve.
			if err == nil || strings.Contains(err.Error(), "placeholder") || strings.Contains(err.Error(), "absolute") {
				t.Errorf("url %q: err = %v", url, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("url %q: err = %v, want %q", url, err, want)
		}
	}
	if _, err := Check(t.Context(), "https://probe.example/{port}", 0, 0); err == nil {
		t.Fatal("port 0 accepted")
	}
	if _, err := Check(t.Context(), "https://probe.example/{port}", 70000, 0); err == nil {
		t.Fatal("port 70000 accepted")
	}
}

func TestCheckProbeFailures(t *testing.T) {
	badStatus := probeServer(t, 502, "bad gateway", 0)
	defer badStatus.Close()
	if _, err := Check(t.Context(), badStatus.URL+"/{port}", 51413, 0); err == nil {
		t.Fatal("non-200 accepted")
	}

	notJSON := probeServer(t, 200, "open-ish", 0)
	defer notJSON.Close()
	if _, err := Check(t.Context(), notJSON.URL+"/{port}", 51413, 0); err == nil {
		t.Fatal("non-JSON accepted")
	}

	noField := probeServer(t, 200, `{"status":"ok"}`, 0)
	defer noField.Close()
	if _, err := Check(t.Context(), noField.URL+"/{port}", 51413, 0); err == nil {
		t.Fatal("field-less answer accepted")
	}

	slow := probeServer(t, 200, `{"reachable":true}`, 300*time.Millisecond)
	defer slow.Close()
	if _, err := Check(t.Context(), slow.URL+"/{port}", 51413, 20*time.Millisecond); err == nil {
		t.Fatal("slow probe not timed out")
	}
}
