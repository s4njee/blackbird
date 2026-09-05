package api

import (
	"encoding/json"
	"net/http"

	"blackbird/internal/config"
	"blackbird/internal/seeding"
)

// ---- GET /api/seeding, POST /api/seeding/dry-run (PAR-4.2) ----

type seedingGroupDTO struct {
	Name           string  `json:"name"`
	MinRatio       float64 `json:"minRatio"`
	MaxRatio       float64 `json:"maxRatio"`
	MinUploadBytes int64   `json:"minUploadBytes"`
	MaxSeedingTime int64   `json:"maxSeedingTimeNs"`
	Action         string  `json:"action"`
	Label          string  `json:"label,omitempty"`
}

type seedingInfoDTO struct {
	CustomSlot string            `json:"customSlot"`
	Groups     []seedingGroupDTO `json:"groups"`
}

// seedingHandler reports the live seeding policy: the assignment slot and
// group definitions. The context menu reads group names from here.
func (s *Server) seedingHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "config store is not running")
		return
	}
	cfg := s.opts.Store.Get().Seeding
	out := seedingInfoDTO{CustomSlot: cfg.EffectiveSlot(), Groups: []seedingGroupDTO{}}
	for _, g := range cfg.Groups {
		out.Groups = append(out.Groups, seedingGroupDTO{
			Name: g.Name, MinRatio: g.MinRatio, MaxRatio: g.MaxRatio,
			MinUploadBytes: g.MinUploadBytes, MaxSeedingTime: int64(g.MaxSeedingTime),
			Action: g.Action, Label: g.Label,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type seedingDryRunRequest struct {
	CustomSlot string                `json:"customSlot"`
	Groups     []config.SeedingGroup `json:"groups"`
}

type seedingDryRunMatch struct {
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	Condition string `json:"condition"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
}

type seedingDryRunResponse struct {
	Matches   []seedingDryRunMatch `json:"matches"`
	Evaluated int                  `json:"evaluated"`
}

// seedingDryRunHandler evaluates draft groups against the current session
// snapshot so the Settings editor can preview what would act now. Markers
// are ignored: the preview shows what WOULD trigger, not what will.
func (s *Server) seedingDryRunHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "poller is not running")
		return
	}
	var req seedingDryRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	slot := req.CustomSlot
	if slot == "" {
		slot = config.DefaultSeedingSlot
	}
	now := s.opts.Poller.Snapshot().GeneratedAt
	resp := seedingDryRunResponse{Matches: []seedingDryRunMatch{}}
	for _, t := range s.opts.Poller.Snapshot().Torrents {
		if !t.Complete || !t.IsOpen {
			continue
		}
		resp.Evaluated++
		groupName := t.SlotValue(slot)
		if groupName == "" {
			continue
		}
		group := seeding.FindGroup(req.Groups, groupName)
		if group == nil {
			continue
		}
		trigger, ok := seeding.Evaluate(*group, t, now)
		if !ok {
			continue
		}
		resp.Matches = append(resp.Matches, seedingDryRunMatch{
			Hash: t.Hash, Name: t.Name, Group: group.Name,
			Condition: trigger.Condition, Action: group.Action, Detail: trigger.Detail,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
