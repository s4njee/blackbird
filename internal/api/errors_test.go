package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"blackbird/internal/fakertorrent"
	"blackbird/internal/rtorrent"
	"blackbird/internal/scgi/xmlrpc"
)

// TestErrorForFaultPassthrough covers the structured-error contract: an
// rtorrent fault maps to 502 and its message is passed through verbatim.
func TestErrorForFaultPassthrough(t *testing.T) {
	f := &xmlrpc.Fault{Code: 1, String: "torrent not found"}
	status, code, msg := errorFor(f)
	if status != http.StatusBadGateway || code != "rtorrent_fault" || !strings.Contains(msg, "torrent not found") {
		t.Fatalf("errorFor(fault) = (%d, %q, %q)", status, code, msg)
	}

	// Wrapped faults are still recognized (errors.As).
	wrapped := fmt.Errorf("fetch detail for %s: %w", "aaaa", f)
	status, code, msg = errorFor(wrapped)
	if status != http.StatusBadGateway || code != "rtorrent_fault" || !strings.Contains(msg, "torrent not found") {
		t.Fatalf("errorFor(wrapped fault) = (%d, %q, %q)", status, code, msg)
	}
}

// TestErrorForPathOutsideDownloadDirs maps the remove/move safety refusal to
// a 400 the UI can surface as a field error.
func TestErrorForPathOutsideDownloadDirs(t *testing.T) {
	err := fmt.Errorf("resolve base path: %w", rtorrent.ErrPathOutsideDownloadDirs)
	status, code, msg := errorFor(err)
	if status != http.StatusBadRequest || code != "path_outside_download_dirs" || msg == "" {
		t.Fatalf("errorFor(path) = (%d, %q, %q)", status, code, msg)
	}
}

// TestErrorForTransportError keeps transport problems distinct from faults.
func TestErrorForTransportError(t *testing.T) {
	status, code, msg := errorFor(errors.New("scgi: dial unix:///x: connection refused"))
	if status != http.StatusBadGateway || code != "rtorrent_unreachable" || msg == "" {
		t.Fatalf("errorFor(transport) = (%d, %q, %q)", status, code, msg)
	}
}

// TestDetailEndpointFaultStructured confirms the detail route surfaces daemon
// faults as a structured 502 rather than a generic error.
func TestDetailEndpointFaultStructured(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{Fail: &fakertorrent.Failure{
		Method:  "system.multicall",
		Hashes:  []string{"aaaa1111aaaa1111"},
		Message: "no such torrent",
	}})
	waitForConnected(t, st.p)

	resp, err := http.Get(st.ts.URL + "/api/torrents/aaaa1111aaaa1111")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d body=%+v", resp.StatusCode, out)
	}
	if out.Error.Code != "rtorrent_fault" || !strings.Contains(out.Error.Message, "no such torrent") {
		t.Fatalf("structured error = %+v", out.Error)
	}
}
