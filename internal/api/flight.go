package api

import (
	"net/http"
	"strconv"
	"time"

	"blackbird/internal/history"
)

// actionIntentValues deliberately omits peer addresses, credentials, paths,
// tracker URLs, arbitrary custom values and other free-form request content.
func actionIntentValues(r actionRequest) map[string]string {
	v := map[string]string{}
	if r.Priority != nil {
		v["priority"] = strconv.Itoa(*r.Priority)
	}
	if r.FileIndex != nil {
		v["fileIndex"] = strconv.Itoa(*r.FileIndex)
	}
	if r.Enabled != nil {
		v["enabled"] = strconv.FormatBool(*r.Enabled)
	}
	v["label"], v["throttle"] = r.Label, r.Throttle
	return v
}

// GET /api/v1/history/flight returns a bounded, immutable incident window.
// Global configuration/scheduler/gap events accompany a selected torrent.
func (s *Server) flightHandler(w http.ResponseWriter, r *http.Request) {
	recorder := s.history.Recorder()
	if recorder == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "flight recorder is not configured")
		return
	}
	q := r.URL.Query()
	var from, to time.Time
	for key, target := range map[string]*time.Time{"from": &from, "to": &to} {
		if q.Get(key) == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, q.Get(key))
		if err != nil {
			writeAPIError(w, 400, "bad_request", key+" must be RFC3339")
			return
		}
		*target = parsed
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		writeAPIError(w, 400, "bad_request", "from must not be after to")
		return
	}
	limit := 500
	if q.Get("limit") != "" {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 1000 {
			writeAPIError(w, 400, "bad_request", "limit must be 1-1000")
			return
		}
		limit = n
	}
	view := recorder.Snapshot()
	events := make([]history.Event, 0)
	for _, ev := range view.Events {
		if q.Get("hash") != "" && ev.Hash != "" && ev.Hash != q.Get("hash") {
			continue
		}
		if !from.IsZero() && ev.At.Before(from) || !to.IsZero() && ev.At.After(to) {
			continue
		}
		events = append(events, ev)
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
		view.Coverage = append(view.Coverage, "Window truncated to the latest requested event count. Narrow the time range to inspect older evidence.")
	}
	view.Events = events
	if q.Get("export") == "1" {
		view = history.ExportRecording(view)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}
