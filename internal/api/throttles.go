package api

import (
	"net/http"

	"blackbird/internal/rtorrent"
	"blackbird/internal/scgi/xmlrpc"
)

// ---- GET /api/throttles (PAR-4.1) ----

type throttleChannelDTO struct {
	Name        string `json:"name"`
	UpKB        int64  `json:"upKB"`
	DownKB      int64  `json:"downKB"`
	UpRateBps   int64  `json:"upRateBps"`
	DownRateBps int64  `json:"downRateBps"`
	InUse       int    `json:"inUse"`
}

type throttlesResponse struct {
	Channels []throttleChannelDTO `json:"channels"`
}

// throttlesHandler reports configured channels with their YAML caps, live
// daemon throughput (throttle.up.rate/down.rate in bytes/s), and session
// usage counts. It drives the toolbar/menu channel lists and the Settings
// live-usage display.
func (s *Server) throttlesHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil || s.opts.RTorrent == nil || s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "throttle service is not running")
		return
	}
	cfg := s.opts.Store.Get()
	channels := cfg.Tuning.Throttles
	inUse := map[string]int{}
	for _, t := range s.opts.Poller.Snapshot().Torrents {
		if t.Throttle != "" {
			inUse[t.Throttle]++
		}
	}

	// One multicall for every channel's live rates (2 requests per channel).
	type slot struct {
		idx  int
		up   bool
		name string
	}
	var reqs []rtorrent.Request
	var slots []slot
	for i, ch := range channels {
		reqs = append(reqs,
			rtorrent.Request{Method: "throttle.up.rate", Params: targetParams(ch.Name)},
			rtorrent.Request{Method: "throttle.down.rate", Params: targetParams(ch.Name)},
		)
		slots = append(slots, slot{idx: i, up: true, name: ch.Name}, slot{idx: i, up: false, name: ch.Name})
	}
	rates := map[string][2]int64{}
	if len(reqs) > 0 {
		if results, err := s.opts.RTorrent.MultiCall(r.Context(), reqs); err == nil {
			for i, res := range results {
				if i >= len(slots) || res.Err != nil || len(res.Values) == 0 {
					continue
				}
				pair := rates[slots[i].name]
				if slots[i].up {
					pair[0] = res.Values[0].Int
				} else {
					pair[1] = res.Values[0].Int
				}
				rates[slots[i].name] = pair
			}
		}
	}

	out := throttlesResponse{Channels: []throttleChannelDTO{}}
	for _, ch := range channels {
		live := rates[ch.Name]
		out.Channels = append(out.Channels, throttleChannelDTO{
			Name:        ch.Name,
			UpKB:        ch.UpKB,
			DownKB:      ch.DownKB,
			UpRateBps:   live[0],
			DownRateBps: live[1],
			InUse:       inUse[ch.Name],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// targetParams builds the empty-target + name params the throttle.*
// commands require (verified against rTorrent 0.16.18).
func targetParams(name string) []xmlrpc.Value {
	return []xmlrpc.Value{{Type: "string", Str: ""}, {Type: "string", Str: name}}
}
