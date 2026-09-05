package api

import (
	"net/http"

	"blackbird/internal/history"
	"blackbird/internal/ipfilter"
)

// ---- GET /api/ipfilter, POST /api/ipfilter/reload (PAR-5.6) ----

// ipfilterStatusHandler reports the blocklist state for Settings: whether a
// source is configured, the rule count, the last load time, and errors. It
// never fetches or loads; only POST reloads.
func (s *Server) ipfilterStatusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ipfilterStatus())
}

// ipfilterReloadHandler re-fetches (URL sources) and re-loads the daemon
// table now, then reports the status. Failures are 502s carrying the error
// in the status payload; they are also history events.
func (s *Server) ipfilterReloadHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.IPFilter == nil {
		writeAPIError(w, http.StatusBadRequest, "ipfilter_disabled", "no blocklist source is configured (network.ipfilter)")
		return
	}
	if s.opts.Store != nil && !s.opts.Store.Get().Network.IPFilter.Enabled() {
		writeAPIError(w, http.StatusBadRequest, "ipfilter_disabled", "no blocklist source is configured (network.ipfilter)")
		return
	}
	if err := s.opts.IPFilter.ApplyNow(r.Context()); err != nil {
		if s.history != nil {
			s.history.Add("", history.Entry{
				Kind: history.KindAction, Actor: actorFromRequest(r, s.auth),
				Action: "ipfilter_reload", Result: "failed", Message: err.Error(),
			})
		}
		writeAPIError(w, http.StatusBadGateway, "ipfilter_failed", err.Error())
		return
	}
	if s.history != nil {
		st := s.opts.IPFilter.Status()
		s.history.Add("", history.Entry{
			Kind: history.KindAction, Actor: actorFromRequest(r, s.auth),
			Action: "ipfilter_reload", Result: "ok",
			Message: itoa(st.Rules) + " rules loaded",
		})
	}
	writeJSON(w, http.StatusOK, s.ipfilterStatus())
}

// ipfilterStatus renders the status shape with the service behind a nil
// guard so tests without the service still get a disabled payload.
func (s *Server) ipfilterStatus() ipfilter.Status {
	if s.opts.IPFilter == nil {
		return ipfilter.Status{}
	}
	return s.opts.IPFilter.Status()
}
