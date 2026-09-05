package api

import (
	"encoding/json"
	"net/http"

	"blackbird/internal/rtorrent"
)

// ---- GET /api/bandwidth, POST /api/bandwidth (PAR-4.4) ----

type bandwidthDTO struct {
	DownKB      int64 `json:"downKb"`      // current daemon cap, KB/s (0 = unlimited)
	UpKB        int64 `json:"upKb"`        // current daemon cap, KB/s (0 = unlimited)
	DownRateBps int64 `json:"downRateBps"` // live throughput, bytes/s
	UpRateBps   int64 `json:"upRateBps"`   // live throughput, bytes/s
}

// bandwidthHandler reports the live global limits and throughput in one
// multicall. Getter names are bare (no `=` suffix): the suffixed form is
// undefined on rTorrent 0.16 (verified live).
func (s *Server) bandwidthHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil || s.opts.RTorrent == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "bandwidth service is not running")
		return
	}
	results, err := s.opts.RTorrent.MultiCall(r.Context(), []rtorrent.Request{
		{Method: "throttle.global_down.max_rate"},
		{Method: "throttle.global_up.max_rate"},
		{Method: "throttle.global_down.rate"},
		{Method: "throttle.global_up.rate"},
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "rtorrent_unreachable", err.Error())
		return
	}
	value := func(i int) int64 {
		if i < len(results) && results[i].Err == nil && len(results[i].Values) > 0 {
			return results[i].Values[0].Int
		}
		return 0
	}
	out := bandwidthDTO{
		DownKB:      bytesToKB(value(0)),
		UpKB:        bytesToKB(value(1)),
		DownRateBps: value(2),
		UpRateBps:   value(3),
	}
	writeJSON(w, http.StatusOK, out)
}

// bytesToKB converts a daemon byte cap to KB/s (0 and negatives stay
// unlimited, matching the daemon's convention).
func bytesToKB(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return bytes / 1024
}

type bandwidthSetRequest struct {
	DownKB int64 `json:"downKb"`
	UpKB   int64 `json:"upKb"`
}

// bandwidthSetHandler applies global limits immediately via the exact-KiB/s
// .set_kb variant (verified live; plain .set carries bytes with >>10
// rounding). While a scheduler override owns the limits, the change updates
// the override instead so the status display stays truthful.
func (s *Server) bandwidthSetHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil || s.opts.RTorrent == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "bandwidth service is not running")
		return
	}
	var req bandwidthSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if req.DownKB < 0 || req.UpKB < 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "limits must be >= 0 (0 = unlimited)")
		return
	}
	if svc := s.opts.Schedule; svc != nil && svc.OverrideActive() {
		if !svc.SetOverrideValues(r.Context(), req.DownKB, req.UpKB) {
			writeAPIError(w, http.StatusBadGateway, "override_failed", "scheduler override no longer active")
			return
		}
		s.bandwidthHandler(w, r)
		return
	}
	if err := s.opts.RTorrent.SetGlobalRateKB(r.Context(), "throttle.global_down.max_rate.set_kb", req.DownKB); err != nil {
		writeAPIError(w, http.StatusBadGateway, "rtorrent_fault", err.Error())
		return
	}
	if err := s.opts.RTorrent.SetGlobalRateKB(r.Context(), "throttle.global_up.max_rate.set_kb", req.UpKB); err != nil {
		writeAPIError(w, http.StatusBadGateway, "rtorrent_fault", err.Error())
		return
	}
	s.bandwidthHandler(w, r)
}
