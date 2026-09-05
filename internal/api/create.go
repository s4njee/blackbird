package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"blackbird/internal/mktorrent"
	"blackbird/internal/rtorrent"
)

// ---- POST /api/torrents/create and friends (PAR-5.4) ----

type createRequest struct {
	Source       string   `json:"source"`
	Name         string   `json:"name,omitempty"`
	Trackers     []string `json:"trackers,omitempty"`
	PieceLength  int64    `json:"piece_length,omitempty"` // 0 = automatic
	Private      bool     `json:"private,omitempty"`
	Comment      string   `json:"comment,omitempty"`
	SourceTag    string   `json:"source_tag,omitempty"` // info.source
	AddToSession bool     `json:"add_to_session,omitempty"`
	Start        *bool    `json:"start,omitempty"` // default true when adding
	Label        string   `json:"label,omitempty"`
}

type createAddRequest struct {
	Start *bool  `json:"start,omitempty"` // default true
	Label string `json:"label,omitempty"`
}

type createAddResponse struct {
	Hash    string `json:"hash"`
	Started bool   `json:"started"`
}

// createStartHandler validates a creation request synchronously (bad input
// fails fast with 400) and queues the hashing on the bounded worker pool.
func (s *Server) createStartHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Create
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "torrent creation not wired")
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "source is required")
		return
	}
	trackers := trimNonEmpty(req.Trackers)
	start := true
	if req.Start != nil {
		start = *req.Start
	}
	job, err := svc.Submit(mktorrent.Spec{
		Input: mktorrent.Input{
			Source: source, Name: strings.TrimSpace(req.Name), Trackers: trackers,
			PieceLength: req.PieceLength, Private: req.Private,
			Comment: req.Comment, SourceTag: strings.TrimSpace(req.SourceTag),
		},
		AddToSession: req.AddToSession, Start: start, Label: strings.TrimSpace(req.Label),
	}, actorFromRequest(r, s.auth))
	if err != nil {
		status, code, msg := createErrorFor(err)
		writeAPIError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// createErrorFor maps creation validation/resolution failures to API errors.
// Sources outside the download roots share the SEC-2.3 code with the move
// engine and the directory browser.
func createErrorFor(err error) (int, string, string) {
	if errors.Is(err, rtorrent.ErrPathOutsideDownloadDirs) {
		return http.StatusBadRequest, "path_outside_download_dirs", err.Error()
	}
	return http.StatusBadRequest, "bad_request", err.Error()
}

func (s *Server) createStatusHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Create
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "torrent creation not wired")
		return
	}
	job, ok := svc.Status(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "create_not_found", "creation job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) createCancelHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Create
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "torrent creation not wired")
		return
	}
	job, ok := svc.Cancel(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "create_not_found", "creation job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// createDownloadHandler streams a finished .torrent as an attachment. The
// server-side source path is never exposed; the filename comes from the
// torrent name.
func (s *Server) createDownloadHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Create
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "torrent creation not wired")
		return
	}
	data, name, ok := svc.Data(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "create_not_found", "finished torrent not found (job unknown, unfinished, or evicted)")
		return
	}
	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeFilename(name+".torrent")))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// createAddHandler loads a finished job's .torrent into the session, tied
// to the source path and started unless told otherwise.
func (s *Server) createAddHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Create
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "torrent creation not wired")
		return
	}
	var req createAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	start := true
	if req.Start != nil {
		start = *req.Start
	}
	hash, err := svc.Add(r.PathValue("id"), start, strings.TrimSpace(req.Label), actorFromRequest(r, s.auth))
	if err != nil {
		var stateErr *mktorrent.JobStateError
		switch {
		case errors.Is(err, mktorrent.ErrJobUnknown):
			writeAPIError(w, http.StatusNotFound, "create_not_found", err.Error())
		case errors.As(err, &stateErr):
			writeAPIError(w, http.StatusConflict, "create_not_ready", err.Error())
		default:
			status, code, msg := createErrorFor(err)
			writeAPIError(w, status, code, msg)
		}
		return
	}
	writeJSON(w, http.StatusOK, createAddResponse{Hash: hash, Started: start})
}

func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
