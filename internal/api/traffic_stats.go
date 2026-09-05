package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"blackbird/internal/host"
)

// ---- GET /api/traffic (PAR-5.2 transfer history) ----

// trafficDay is one UTC day's down/up totals.
type trafficDay struct {
	Day  string `json:"day"` // YYYY-MM-DD (UTC)
	Down int64  `json:"down"`
	Up   int64  `json:"up"`
}

// trafficHour is one UTC hour's down/up totals.
type trafficHour struct {
	Hour string `json:"hour"` // YYYY-MM-DDTHH (UTC)
	Down int64  `json:"down"`
	Up   int64  `json:"up"`
}

const (
	trafficDayFormat  = "2006-01-02"
	trafficHourFormat = "2006-01-02T15"
	// maxTrafficRangeDays caps one range query so a hostile client cannot
	// force an unbounded JSON response.
	maxTrafficRangeDays = 366
)

// trafficHandler serves daily or hourly transfer totals from the traffic
// tracker, plus a CSV export:
//
//	GET /api/traffic?from=2026-09-01&to=2026-09-30  → {days:[...]}
//	GET /api/traffic?granularity=hour&day=2026-09-02 → {hours:[...]}
//	GET /api/traffic?from=...&to=...&format=csv     → text/csv download
//
// from/to default to the last 30 UTC days (inclusive). Buckets are UTC so
// daylight-saving transitions never split a day.
func (s *Server) trafficHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Traffic == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "traffic tracker not wired")
		return
	}
	q := r.URL.Query()
	if strings.EqualFold(q.Get("granularity"), "hour") {
		s.trafficHoursHandler(w, r)
		return
	}
	to, err := trafficDateParam(q.Get("to"), time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "to must be YYYY-MM-DD")
		return
	}
	from, err := trafficDateParam(q.Get("from"), to.AddDate(0, 0, -29))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "from must be YYYY-MM-DD")
		return
	}
	if from.After(to) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "from must not be after to")
		return
	}
	if int(to.Sub(from).Hours()/24)+1 > maxTrafficRangeDays {
		writeAPIError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("range must not exceed %d days", maxTrafficRangeDays))
		return
	}
	days := s.opts.Traffic.Days(from, to)
	out := make([]trafficDay, 0, len(days))
	for _, d := range days {
		out = append(out, trafficDay{Day: d.Day, Down: d.Down, Up: d.Up})
	}
	if strings.EqualFold(q.Get("format"), "csv") {
		var sb strings.Builder
		sb.WriteString("day,down_bytes,up_bytes\n")
		for _, d := range out {
			fmt.Fprintf(&sb, "%s,%d,%d\n", d.Day, d.Down, d.Up)
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "traffic-"+out[0].Day+"-to-"+out[len(out)-1].Day+".csv"))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sb.String()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":          out[0].Day,
		"to":            out[len(out)-1].Day,
		"retentionDays": s.opts.Traffic.RetentionDays(),
		"days":          out,
	})
}

// trafficHoursHandler serves the 24 hourly totals for one UTC day, with the
// same CSV export as the daily view.
func (s *Server) trafficHoursHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	day, err := trafficDateParam(q.Get("day"), time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "day must be YYYY-MM-DD")
		return
	}
	hours := s.opts.Traffic.Hours(day)
	out := make([]trafficHour, 0, len(hours))
	for _, h := range hours {
		out = append(out, trafficHour{Hour: h.Hour, Down: h.Down, Up: h.Up})
	}
	if strings.EqualFold(q.Get("format"), "csv") {
		var sb strings.Builder
		sb.WriteString("hour,down_bytes,up_bytes\n")
		for _, h := range out {
			fmt.Fprintf(&sb, "%s,%d,%d\n", h.Hour, h.Down, h.Up)
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "traffic-hours-"+day.Format(trafficDayFormat)+".csv"))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sb.String()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"day":           day.Format(trafficDayFormat),
		"retentionDays": s.opts.Traffic.RetentionDays(),
		"hours":         out,
	})
}

// trafficDateParam parses a YYYY-MM-DD query value, falling back to def
// (normalized to its UTC date) when empty.
func trafficDateParam(raw string, def time.Time) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Date(def.UTC().Year(), def.UTC().Month(), def.UTC().Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Parse(trafficDayFormat, strings.TrimSpace(raw))
}

// ---- GET /api/host (PAR-5.2 host cards) ----

type hostResponse struct {
	Load1     float64 `json:"load1"`
	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	LoadOK    bool    `json:"loadOK"`
	MemTotal  uint64  `json:"memTotal"`
	MemAvail  uint64  `json:"memAvail"`
	MemOK     bool    `json:"memOK"`
	SelfBytes uint64  `json:"selfBytes"`
	SelfOK    bool    `json:"selfOK"`
	HeapBytes uint64  `json:"heapBytes"`
}

// hostHandler reports best-effort host load and memory. Unavailable groups
// are flagged (OK=false) and the UI renders a dash — telemetry never fails
// the console. Per-volume free space already rides on GET /api/stats from
// the existing statfs sampler.
func (s *Server) hostHandler(w http.ResponseWriter, r *http.Request) {
	st := host.Snapshot()
	writeJSON(w, http.StatusOK, hostResponse{
		Load1: st.Load1, Load5: st.Load5, Load15: st.Load15, LoadOK: st.LoadOK,
		MemTotal: st.MemTotal, MemAvail: st.MemAvail, MemOK: st.MemOK,
		SelfBytes: st.SelfBytes, SelfOK: st.SelfOK, HeapBytes: st.HeapBytes,
	})
}
