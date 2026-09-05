package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"blackbird/internal/preservation"
)

func (s *Server) preservationHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opts.Preservation == nil {
		writeAPIError(w, 503, "unavailable", "preservation watchlist is not configured")
		return
	}
	hash := r.URL.Query().Get("hash")
	watches, err := s.opts.Preservation.Snapshot(hash)
	writeJSON(w, 200, map[string]any{"watches": watches, "error": err, "generatedAt": time.Now(), "coverage": "Opt-in, five-minute cached observations. Up to 128 watches, 288 samples each (24 hours), 8 current tracker sources and 32 retained tracker observations per watch. Rankings use the last six hours. Inactive, missing and stale observations are excluded; gaps reduce coverage. Tracker report age is unknown and tracker counts do not affect the ranking."})
}
func (s *Server) preservationUpdateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opts.Preservation == nil {
		writeAPIError(w, 503, "unavailable", "preservation watchlist is not configured")
		return
	}
	var body preservation.Change
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeAPIError(w, 400, "bad_request", "invalid preservation change")
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeAPIError(w, 400, "bad_request", "expected one preservation change")
		return
	}
	if body.Action == "watch" {
		found := false
		if s.opts.Poller != nil {
			for _, t := range s.opts.Poller.Snapshot().Torrents {
				if strings.EqualFold(t.Hash, body.Hash) {
					body.Name = t.Name
					found = true
					break
				}
			}
		}
		if !found {
			writeAPIError(w, 400, "bad_request", "choose a torrent currently in the cached session")
			return
		}
	}
	if err := s.opts.Preservation.Change(body, actorFromRequest(r, s.auth)); err != nil {
		code := 503
		if errors.Is(err, preservation.ErrInvalid) {
			code = 400
		}
		if errors.Is(err, preservation.ErrConflict) || errors.Is(err, preservation.ErrPinned) {
			code = 409
		}
		message := err.Error()
		if code == 503 {
			message = "Preservation change could not be confirmed. Refresh before retrying; the watchlist may be full or its storage unavailable."
		}
		writeAPIError(w, code, "preservation_update", message)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) guardPreservation(hash string, fn func() error) error {
	if s.opts.Preservation != nil {
		return s.opts.Preservation.Guard(hash, fn)
	}
	return fn()
}
