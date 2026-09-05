package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"blackbird/internal/history"
	"blackbird/internal/portcheck"
)

// ---- GET /api/port-check, POST /api/port-check (PAR-5.5) ----

type portCheckResponse struct {
	Enabled bool              `json:"enabled"`
	Result  *portcheck.Result `json:"result,omitempty"`
}

// portCheckStatusHandler reports whether a probe is configured and the last
// user-initiated verdict. It never probes: external requests happen only on
// an explicit POST.
func (s *Server) portCheckStatusHandler(w http.ResponseWriter, r *http.Request) {
	cfg := s.portCheckConfig()
	s.portMu.Lock()
	last := s.lastPortCheck
	s.portMu.Unlock()
	writeJSON(w, http.StatusOK, portCheckResponse{Enabled: strings.TrimSpace(cfg.URL) != "", Result: last})
}

// portCheckRunHandler runs one user-initiated reachability check against the
// configured probe and remembers the verdict for the status bar and
// Settings. Probe failures are 502s; the previous verdict is kept.
func (s *Server) portCheckRunHandler(w http.ResponseWriter, r *http.Request) {
	cfg := s.portCheckConfig()
	if strings.TrimSpace(cfg.URL) == "" {
		writeAPIError(w, http.StatusBadRequest, "portcheck_disabled", "no port-check probe is configured (portcheck.url)")
		return
	}
	port, err := s.resolveCheckPort()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	res, err := portcheck.Check(r.Context(), cfg.URL, port, cfg.EffectiveTimeout())
	actor := actorFromRequest(r, s.auth)
	if err != nil {
		if s.history != nil {
			s.history.Add("", history.Entry{
				Kind: history.KindAction, Actor: actor, Action: "port_check",
				Result: "failed", Message: err.Error(),
			})
		}
		if errors.Is(err, portcheck.ErrDisabled) {
			writeAPIError(w, http.StatusBadRequest, "portcheck_disabled", err.Error())
			return
		}
		writeAPIError(w, http.StatusBadGateway, "portcheck_failed", err.Error())
		return
	}
	s.portMu.Lock()
	s.lastPortCheck = &res
	s.portMu.Unlock()
	if s.history != nil {
		state := "closed"
		if res.Reachable {
			state = "reachable"
		}
		s.history.Add("", history.Entry{
			Kind: history.KindAction, Actor: actor, Action: "port_check", Result: "ok",
			Message: "port " + strconv.Itoa(res.Port) + " " + state + " via " + res.Method,
		})
	}
	writeJSON(w, http.StatusOK, portCheckResponse{Enabled: true, Result: &res})
}

// resolveCheckPort prefers the live daemon port (it accounts for
// port_random), falling back to the configured port_range start.
func (s *Server) resolveCheckPort() (int, error) {
	if s.opts.Poller != nil {
		if p := s.opts.Poller.Snapshot().Global.Port; p >= 1 && p <= 65535 {
			return p, nil
		}
	}
	if s.opts.Store != nil {
		var raw string
		if pr := s.opts.Store.Get().Tuning.PortRange; pr != nil {
			raw = strings.TrimSpace(*pr)
		}
		if raw != "" {
			first := raw
			if i := strings.Index(first, "-"); i >= 0 {
				first = first[:i]
			}
			if p, err := strconv.Atoi(strings.TrimSpace(first)); err == nil && p >= 1 && p <= 65535 {
				return p, nil
			}
		}
	}
	return 0, portcheck.ErrNoPort
}
