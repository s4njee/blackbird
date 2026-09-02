package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"blackbird/internal/rtorrent"
	"blackbird/internal/scgi/xmlrpc"
)

// apiError is the structured error body: {"error":{"code":"...","message":"..."}}.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

// errorFor maps a backend error onto (status, code, message). rtorrent faults
// pass their message through; transport problems are reported distinctly.
func errorFor(err error) (int, string, string) {
	var fault *xmlrpc.Fault
	if errors.As(err, &fault) {
		return http.StatusBadGateway, "rtorrent_fault", fault.Error()
	}
	if errors.Is(err, rtorrent.ErrPathOutsideDownloadDirs) {
		return http.StatusBadRequest, "path_outside_download_dirs", err.Error()
	}
	return http.StatusBadGateway, "rtorrent_unreachable", err.Error()
}
