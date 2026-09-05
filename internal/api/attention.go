package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"blackbird/internal/attention"
	"blackbird/internal/history"
)

type attentionCompletion struct {
	ID     string    `json:"id"`
	Hash   string    `json:"hash"`
	Action string    `json:"action"`
	At     time.Time `json:"at"`
}

func (s *Server) attentionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opts.Attention == nil {
		writeAPIError(w, 503, "unavailable", "attention inbox is not configured")
		return
	}
	st := s.opts.Attention.Snapshot()
	now := time.Now()
	open := 0
	for _, in := range st.Incidents {
		if in.Status == "open" {
			open++
		}
	}
	if r.URL.Query().Get("summary") == "1" {
		writeJSON(w, 200, map[string]any{"noticeSequence": st.NoticeSequence, "open": open, "error": st.Error, "instance": st.Instance})
		return
	}
	since := now.Add(-24 * time.Hour)
	if st.LastVisit != nil {
		since = *st.LastVisit
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil || at.After(now) {
			writeAPIError(w, 400, "bad_request", "since must be a past RFC3339 timestamp")
			return
		}
		since = at
	}
	complete := []attentionCompletion{}
	// The durable recorder provides outcome evidence across restarts. The
	// legacy ring is a fallback for installations without the recorder.
	events := []history.Event{}
	if rec := s.history.Recorder(); rec != nil {
		events = rec.Snapshot().Events
	} else {
		events = s.history.Query(history.Filter{}, 200, 0).Events
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	total := 0
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if !e.At.After(since) || e.At.After(now) {
			continue
		}
		important := e.Kind == history.KindComplete || ((e.Result == "ok" || e.Result == "completed" || e.Result == "saved") && (e.Kind == history.KindMove || e.Actor == "automation" || e.Action == "settings_save" || e.Action == "incident_recovered"))
		if !important || e.Phase == "intent" {
			continue
		}
		total++
		if len(complete) < 100 {
			complete = append(complete, attentionCompletion{e.ID, e.Hash, e.Action, e.At})
		}
	}
	writeJSON(w, 200, map[string]any{"state": st, "generatedAt": now, "since": since, "completed": complete, "completedCount": total, "summaryCoverage": "Completed downloads and successful move, rule-effect, settings-save and recovery outcomes are shown from retained history only (up to 100). Successful requests do not establish later daemon state. Expired or unrecorded actions cannot be reconstructed."})
}
func (s *Server) attentionUpdateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opts.Attention == nil {
		writeAPIError(w, 503, "unavailable", "attention inbox is not configured")
		return
	}
	var body struct {
		ID        string    `json:"id"`
		Action    string    `json:"action"`
		Episode   uint64    `json:"episode"`
		Seconds   int64     `json:"seconds"`
		VisitedAt time.Time `json:"visitedAt"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, 400, "bad_request", "invalid attention action")
		return
	}
	if body.Seconds < 0 || body.Seconds > 7*24*60*60 {
		writeAPIError(w, 400, "bad_request", "snooze must be at most seven days")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := s.opts.Attention.Update(ctx, body.ID, body.Action, body.Episode, time.Duration(body.Seconds)*time.Second, body.VisitedAt)
	if err != nil {
		code := 503
		switch {
		case errors.Is(err, attention.ErrConflict):
			code = 409
		case errors.Is(err, attention.ErrNotFound):
			code = 404
		case errors.Is(err, attention.ErrInvalid):
			code = 400
		}
		message := err.Error()
		if code == 503 {
			message = "Attention change was not confirmed. Refresh to check its state before retrying."
		}
		writeAPIError(w, code, "attention_update", message)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
