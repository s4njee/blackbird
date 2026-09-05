package api

import (
	"encoding/json"
	"net/http"

	"blackbird/internal/automation"
	"blackbird/internal/config"
)

// POST /api/automation/dry-run (PAR-3.2): evaluates unsaved draft rules
// against the current session snapshot so the Settings editor can show what
// would match before a save. First-match-wins, exactly like the live engine:
// each torrent appears at most once, under the rule that would handle it.
type dryRunRequest struct {
	Rules []config.CompletionRule `json:"rules"`
}

type dryRunMatch struct {
	Hash        string `json:"hash"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	TrackerHost string `json:"trackerHost"`
	SizeBytes   int64  `json:"sizeBytes"`
	Rule        string `json:"rule"`
}

type dryRunResponse struct {
	Matches   []dryRunMatch `json:"matches"`
	Unmatched int           `json:"unmatched"`
}

func (s *Server) automationDryRunHandler(w http.ResponseWriter, r *http.Request) {
	var req dryRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "poller is not running")
		return
	}
	snapshot := s.opts.Poller.Snapshot()
	resp := dryRunResponse{Matches: []dryRunMatch{}}
	for _, t := range snapshot.Torrents {
		_, rule, ok := automation.MatchFirst(req.Rules, t)
		if !ok {
			resp.Unmatched++
			continue
		}
		resp.Matches = append(resp.Matches, dryRunMatch{
			Hash:        t.Hash,
			Name:        t.Name,
			Label:       t.Label,
			TrackerHost: t.TrackerHost,
			SizeBytes:   t.SizeBytes,
			Rule:        rule.Name,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
