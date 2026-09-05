package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"blackbird/internal/history"
)

// ---- GET /api/schedule, POST /api/schedule/override (PAR-4.3) ----

type scheduleStatusDTO struct {
	ActiveProfile  string `json:"activeProfile"`
	Overridden     bool   `json:"overridden"`
	OverrideUntil  string `json:"overrideUntil,omitempty"`
	OverrideDownKB int64  `json:"overrideDownKb,omitempty"`
	OverrideUpKB   int64  `json:"overrideUpKb,omitempty"`
	Timezone       string `json:"timezone"`
	NextProfile    string `json:"nextProfile,omitempty"`
	NextChange     string `json:"nextChange,omitempty"`
}

// scheduleStatusHandler reports the active profile, override state, and next
// change for the status bar and the Scheduler settings section.
func (s *Server) scheduleStatusHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Schedule
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "scheduler is not running")
		return
	}
	st := svc.Status(time.Now())
	out := scheduleStatusDTO{
		ActiveProfile:  st.ActiveProfile,
		Overridden:     st.Overridden,
		OverrideDownKB: st.OverrideDownKB,
		OverrideUpKB:   st.OverrideUpKB,
		Timezone:       st.Timezone,
		NextProfile:    st.NextProfile,
	}
	if !st.OverrideUntil.IsZero() {
		out.OverrideUntil = st.OverrideUntil.Format(time.RFC3339)
	}
	if !st.NextChange.IsZero() {
		out.NextChange = st.NextChange.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

type scheduleOverrideRequest struct {
	Minutes int   `json:"minutes"`
	DownKB  int64 `json:"downKb"`
	UpKB    int64 `json:"upKb"`
}

// scheduleOverrideHandler installs a manual global-limit override that
// pauses the schedule until it expires.
func (s *Server) scheduleOverrideHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Schedule
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "scheduler is not running")
		return
	}
	var req scheduleOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if req.Minutes < 1 || req.Minutes > 24*60 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "minutes must be 1-1440")
		return
	}
	if req.DownKB < 0 || req.UpKB < 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "limits must be >= 0 (0 = unlimited)")
		return
	}
	cause := s.history.Begin("", history.Entry{Actor: actorFromRequest(r, s.auth), Action: "schedule_override", After: map[string]string{"downKB": fmt.Sprint(req.DownKB), "upKB": fmt.Sprint(req.UpKB)}, Message: "Requested temporary bandwidth override."})
	if err := svc.SetOverride(r.Context(), req.DownKB, req.UpKB, time.Now().Add(time.Duration(req.Minutes)*time.Minute)); err != nil {
		if recorder := s.history.Recorder(); recorder != nil {
			recorder.Record("", history.Entry{Phase: "rpc_result", Actor: actorFromRequest(r, s.auth), Action: "schedule_override", CauseID: cause, Result: "failed", Message: err.Error()})
		}
		writeAPIError(w, http.StatusBadGateway, "override_failed", err.Error())
		return
	}
	if s.history != nil {
		s.history.Add("", history.Entry{
			Kind: history.KindAction, Actor: actorFromRequest(r, s.auth), Action: "schedule_override",
			CauseID: cause, Phase: "rpc_result",
			Result: "ok", Message: fmt.Sprintf("down %d KB/s, up %d KB/s for %d minutes", req.DownKB, req.UpKB, req.Minutes),
		})
	}
	s.scheduleStatusHandler(w, r)
}

// scheduleOverrideClearHandler cancels the manual override; the next minute
// tick re-applies the scheduled profile.
func (s *Server) scheduleOverrideClearHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Schedule
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "scheduler is not running")
		return
	}
	cause := s.history.Begin("", history.Entry{Actor: actorFromRequest(r, s.auth), Action: "schedule_override_clear"})
	svc.ClearOverride()
	if s.history != nil {
		s.history.Add("", history.Entry{
			Kind: history.KindAction, Actor: actorFromRequest(r, s.auth), Action: "schedule_override_clear",
			CauseID: cause, Phase: "outcome",
			Result: "ok",
		})
	}
	s.scheduleStatusHandler(w, r)
}
