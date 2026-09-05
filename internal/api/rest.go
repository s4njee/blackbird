package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
	"blackbird/internal/tuning"
)

// ConfigStore persists settings: the Settings UI saves through it.
// SaveSettings writes the whole YAML atomically (temp + rename) and swaps
// the in-memory config; comments are not preserved (documented in
// example.yml).
type ConfigStore interface {
	Get() config.Config
	SaveSettings(c config.Config) error
	ConfigPath() string
	DownloadDirs() []string
}

// ---- GET /api/session ----

func (s *Server) sessionHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "poller not wired")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Poller.Snapshot())
}

// ---- GET /api/stats (cards + volumes + space-by-label + history) ----

type statCard struct {
	Value  string `json:"value"`
	Detail string `json:"detail"`
}

type statsVolume struct {
	Path        string  `json:"path"`
	TotalBytes  uint64  `json:"totalBytes"`
	FreeBytes   uint64  `json:"freeBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

type labelUsage struct {
	Label     string `json:"label"`
	SizeBytes int64  `json:"sizeBytes"`
	Count     int    `json:"count"`
}

type statsSample struct {
	At       time.Time `json:"at"`
	DownRate int64     `json:"downRate"`
	UpRate   int64     `json:"upRate"`
}

type statsResponse struct {
	Cards struct {
		Download statCard `json:"download"`
		Upload   statCard `json:"upload"`
		Ratio    statCard `json:"sessionRatio"`
		Torrents statCard `json:"torrents"`
		Disk     statCard `json:"diskFree"`
	} `json:"cards"`
	Volumes    []statsVolume `json:"volumes"`
	LabelUsage []labelUsage  `json:"labelUsage"`
	History    []statsSample `json:"history"`
}

func (s *Server) statsHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "poller not wired")
		return
	}
	snap := s.opts.Poller.Snapshot()
	hist := s.opts.Poller.History()

	var resp statsResponse
	resp.Cards.Download = statCard{Value: formatRate(snap.Global.DownRate), Detail: plural(snap.Aggregates.Status["downloading"], "active")}
	resp.Cards.Upload = statCard{Value: formatRate(snap.Global.UpRate), Detail: plural(snap.Aggregates.Status["seeding"], "seeding")}
	resp.Cards.Ratio = statCard{Value: trimFloat(snap.Global.SessionRatio), Detail: formatBytes(snap.Global.SessionUpTotal) + " up / " + formatBytes(snap.Global.SessionDownTotal) + " down"}
	resp.Cards.Torrents = statCard{
		Value:  itoa(len(snap.Torrents)),
		Detail: itoa(snap.Aggregates.Status["stopped"]) + " stopped · " + itoa(snap.Aggregates.Status["error"]) + " errored",
	}
	var free, total uint64
	for _, v := range snap.Volumes {
		free += v.FreeBytes
		total += v.TotalBytes
	}
	resp.Cards.Disk = statCard{Value: formatBytes(int64(free)), Detail: "of " + formatBytes(int64(total)) + " across " + itoa(len(snap.Volumes)) + " volumes"}

	for _, v := range snap.Volumes {
		resp.Volumes = append(resp.Volumes, statsVolume{Path: v.Path, TotalBytes: v.TotalBytes, FreeBytes: v.FreeBytes, UsedPercent: v.UsedPercent()})
	}

	// Space by label: sum torrent sizes per label (unlabeled bucket included);
	// the UI scales bars relative to the largest.
	usage := map[string]*labelUsage{}
	for _, t := range snap.Torrents {
		key := t.Label
		if key == "" {
			key = "unlabeled"
		}
		u, ok := usage[key]
		if !ok {
			u = &labelUsage{Label: key}
			usage[key] = u
		}
		u.SizeBytes += t.SizeBytes
		u.Count++
	}
	for _, u := range usage {
		resp.LabelUsage = append(resp.LabelUsage, *u)
	}
	sort.Slice(resp.LabelUsage, func(i, j int) bool { return resp.LabelUsage[i].SizeBytes > resp.LabelUsage[j].SizeBytes })

	for _, smp := range hist {
		resp.History = append(resp.History, statsSample{At: smp.At, DownRate: smp.DownRate, UpRate: smp.UpRate})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- GET /api/torrents/{hash} ----

// detailHandler serves the focused-torrent views. The default response is the
// full Detail (files/peers/trackers/transfer). A ?view= query selects a
// lighter single-tab payload: general, logger, speed, or explanation. (These share the
// /api/torrents/{hash} route because Go's ServeMux cannot host both a
// {hash} wildcard and literal sub-routes like move/{id} without ambiguity.)
func (s *Server) detailHandler(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	switch r.URL.Query().Get("view") {
	case "explanation":
		s.explanationHandler(w, r)
		return
	case "general":
		s.generalHandler(w, r, hash)
		return
	case "logger":
		s.loggerHandler(w, r, hash)
		return
	case "speed":
		s.speedHandler(w, r, hash)
		return
	}
	d, err := s.opts.RTorrent.FetchDetail(r.Context(), hash)
	if err != nil {
		status, code, msg := errorFor(err)
		writeAPIError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ---- POST /api/torrents/action ----

type actionRequest struct {
	Action       string   `json:"action"` // start|force_start|pause|stop|recheck|remove|remove_with_data|set_label|move_data|priority|superseed|sequential|save_session|set_custom|file_priority|tracker_add|tracker_enable|reannounce|peer_ban|peer_snub|peer_unsnub|peer_disconnect|set_throttle
	Hashes       []string `json:"hashes"`
	Label        string   `json:"label,omitempty"`
	Priority     *int     `json:"priority,omitempty"`
	Destination  string   `json:"destination,omitempty"`
	FileIndex    *int     `json:"fileIndex,omitempty"`
	TrackerIndex *int     `json:"trackerIndex,omitempty"`
	TrackerURL   string   `json:"trackerUrl,omitempty"`
	TrackerGroup *int     `json:"trackerGroup,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
	CustomField  string   `json:"customField,omitempty"`
	CustomValue  string   `json:"customValue,omitempty"`
	Name         string   `json:"name,omitempty"` // rename target (d.name.set)
	// Throttle assigns a named throttle channel (d.throttle_name.set); empty
	// clears the assignment back to the global limits.
	Throttle string `json:"throttle,omitempty"`
	// PeerID targets one peer (p.id) within each hash for peer moderation.
	// Peer actions act on every hash in Hashes × this one peer.
	PeerID string `json:"peerId,omitempty"`
}

type hashResult struct {
	Hash  string `json:"hash"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type actionResponse struct {
	Results []hashResult `json:"results"`
}

func validCustomField(field string) bool {
	switch field {
	case "custom2", "custom3", "custom4", "custom5":
		return true
	default:
		return false
	}
}

// isPeerAction reports whether an action targets one peer within each hash
// (peer moderation needs a peerId payload).
func isPeerAction(action string) bool {
	switch action {
	case "peer_ban", "peer_snub", "peer_unsnub", "peer_disconnect":
		return true
	default:
		return false
	}
}

func (s *Server) actionHandler(w http.ResponseWriter, r *http.Request) {
	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Hashes) == 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "hashes must not be empty")
		return
	}
	switch req.Action {
	case "start", "force_start", "pause", "stop", "recheck", "remove", "remove_with_data", "set_label", "priority", "move_data", "superseed", "sequential", "save_session", "set_custom", "file_priority", "tracker_add", "tracker_remove", "tracker_enable", "reannounce", "peer_ban", "peer_snub", "peer_unsnub", "peer_disconnect", "rename", "set_throttle":
	default:
		writeAPIError(w, http.StatusBadRequest, "bad_request", "unknown action "+req.Action)
		return
	}
	if req.Action == "priority" && (req.Priority == nil || *req.Priority < 0 || *req.Priority > 3) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "priority must be 0-3")
		return
	}
	if req.Action == "move_data" && req.Destination == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "move_data requires destination")
		return
	}
	if req.Action == "file_priority" && (req.FileIndex == nil || req.Priority == nil || *req.FileIndex < 0 || *req.Priority < 0 || *req.Priority > 2) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "file_priority requires fileIndex and priority 0-2")
		return
	}
	if req.Action == "tracker_add" && req.TrackerURL == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "tracker_add requires trackerUrl")
		return
	}
	if req.Action == "tracker_add" && req.TrackerGroup != nil && *req.TrackerGroup < 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "trackerGroup must be non-negative")
		return
	}
	if req.Action == "tracker_remove" && (req.TrackerIndex == nil || *req.TrackerIndex < 0) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "tracker_remove requires trackerIndex")
		return
	}
	if req.Action == "tracker_enable" && (req.TrackerIndex == nil || req.Enabled == nil || *req.TrackerIndex < 0) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "tracker_enable requires trackerIndex and enabled")
		return
	}
	if (req.Action == "superseed" || req.Action == "sequential") && req.Enabled == nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", req.Action+" requires enabled")
		return
	}
	if req.Action == "set_custom" && !validCustomField(req.CustomField) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "set_custom requires customField custom2-custom5")
		return
	}
	if isPeerAction(req.Action) && strings.TrimSpace(req.PeerID) == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", req.Action+" requires peerId")
		return
	}
	if req.Action == "rename" && strings.TrimSpace(req.Name) == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "rename requires name")
		return
	}

	ctx := r.Context()
	rtc := s.opts.RTorrent
	resp := actionResponse{Results: make([]hashResult, 0, len(req.Hashes))}

	// Batch actions are per-hash atomic: every hash is attempted and the
	// outcome reported individually, so partial failures surface.
	for _, hash := range req.Hashes {
		before := map[string]string{}
		if s.opts.Poller != nil && s.history.Recorder() != nil {
			snapshot := s.opts.Poller.Snapshot()
			for _, t := range snapshot.Torrents {
				if t.Hash == hash {
					before = history.TorrentValues(t)
					before["observedAt"] = snapshot.GeneratedAt.Format(time.RFC3339Nano)
					break
				}
			}
		}
		intention := s.history.Begin(hash, history.Entry{Kind: history.KindAction, Actor: actorFromRequest(r, s.auth), Action: req.Action, Before: before, After: actionIntentValues(req), Message: "Requested action; before values are from the last cached poll."})
		var err error
		switch req.Action {
		case "start":
			err = rtc.Start(ctx, hash)
		case "force_start":
			err = rtc.ForceStart(ctx, hash)
		case "pause":
			err = rtc.Pause(ctx, hash)
		case "stop":
			err = rtc.Stop(ctx, hash)
		case "recheck":
			err = rtc.Recheck(ctx, hash)
		case "remove":
			err = s.guardPreservation(hash, func() error { return rtc.Erase(ctx, hash) })
		case "remove_with_data":
			// Refuses base paths outside configured download dirs.
			err = s.guardPreservation(hash, func() error { _, e := rtc.RemoveWithData(ctx, hash, s.opts.Store.DownloadDirs()); return e })
		case "set_label":
			err = rtc.SetLabel(ctx, hash, req.Label)
		case "priority":
			err = rtc.SetPriority(ctx, hash, *req.Priority)
		case "superseed":
			err = rtc.SetSuperseeding(ctx, hash, *req.Enabled)
		case "sequential":
			err = rtc.SetSequential(ctx, hash, *req.Enabled)
		case "save_session":
			err = rtc.SaveSession(ctx, hash)
		case "set_custom":
			err = rtc.SetCustom(ctx, hash, req.CustomField, req.CustomValue)
		case "move_data":
			err = s.moveData(ctx, hash, req.Destination)
		case "file_priority":
			err = rtc.SetFilePriority(ctx, hash, *req.FileIndex, *req.Priority)
		case "tracker_add":
			group := 0
			if req.TrackerGroup != nil {
				group = *req.TrackerGroup
			}
			err = rtc.AddTracker(ctx, hash, req.TrackerURL, group)
		case "tracker_remove":
			err = rtc.RemoveTracker(ctx, hash, *req.TrackerIndex)
		case "tracker_enable":
			err = rtc.SetTrackerEnabled(ctx, hash, *req.TrackerIndex, *req.Enabled)
		case "reannounce":
			err = rtc.Announce(ctx, hash)
		case "rename":
			err = rtc.Rename(ctx, hash, req.Name)
		case "peer_ban":
			err = rtc.BanPeer(ctx, hash, req.PeerID)
		case "peer_snub":
			err = rtc.SetPeerSnubbed(ctx, hash, req.PeerID, true)
		case "peer_unsnub":
			err = rtc.SetPeerSnubbed(ctx, hash, req.PeerID, false)
		case "peer_disconnect":
			err = rtc.DisconnectPeer(ctx, hash, req.PeerID)
		case "set_throttle":
			err = s.setThrottleName(ctx, hash, req.Throttle)
		}
		res := hashResult{Hash: hash, OK: err == nil}
		if err != nil {
			res.Error = err.Error()
		}
		resp.Results = append(resp.Results, res)
		// Record the action in the per-torrent log (Logger tab) and the
		// global History view. Peer actions are logged under the peer's
		// owning torrent; the action string is the batch verb, and details
		// carry the target (file/tracker/peer).
		if s.history != nil {
			result := "ok"
			if err != nil {
				result = "failed"
			}
			s.history.Add(hash, history.Entry{
				CauseID: intention, Phase: "rpc_result",
				Kind:    history.KindAction,
				Actor:   actorFromRequest(r, s.auth),
				Action:  req.Action,
				Result:  result,
				Message: res.Error,
				Name:    s.torrentName(hash),
			})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// moveData keeps the legacy batch action available while using the safer
// PAR-2.2 move engine. New UI clients use the cancellable job endpoint.
func (s *Server) moveData(ctx context.Context, hash, destination string) error {
	return s.moveTorrent(ctx, hash, destination, moveFiles)
}

// setThrottleName assigns a torrent to a named throttle channel (PAR-4.1).
// Verified against rTorrent 0.16.18: d.throttle_name.set faults on an active
// download ("Cannot set throttle on active download"), so running torrents
// are stopped first and restarted afterwards, mirroring the move engine. An
// empty name clears the assignment back to the global limits.
func (s *Server) setThrottleName(ctx context.Context, hash, name string) (err error) {
	var torrentFound, running bool
	for _, torrent := range s.opts.Poller.Snapshot().Torrents {
		if torrent.Hash == hash {
			torrentFound = true
			running = torrent.State != rtorrent.StateStopped
			break
		}
	}
	if !torrentFound {
		return fmt.Errorf("torrent %s is not in the current session", hash)
	}
	if running {
		if err := s.opts.RTorrent.Stop(ctx, hash); err != nil {
			return fmt.Errorf("stop torrent before throttle change: %w", err)
		}
		defer func() {
			if startErr := s.opts.RTorrent.Start(context.Background(), hash); startErr != nil && err == nil {
				err = fmt.Errorf("throttle assigned but restart failed: %w", startErr)
			}
		}()
	}
	return s.opts.RTorrent.SetThrottleName(ctx, hash, name)
}

// ---- POST /api/torrents/add ----

type addItemResult struct {
	Source string `json:"source"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// addHandler accepts multipart/form-data:
//
//	magnets         (optional; one magnet/URL per line, may repeat)
//	files           (optional; one or more .torrent files)
//	destination     (optional)
//	label           (optional)
//	start           ("true"/"false", default true)
//	skip_hash_check ("true"/"false")
//	sequential      ("true"/"false")
//
// Every item is attempted and reported individually.
func (s *Server) addHandler(w http.ResponseWriter, r *http.Request) {
	const maxBody = 64 << 20 // torrent files are small; guard anyway
	if err := r.ParseMultipartForm(maxBody); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "expected multipart/form-data: "+err.Error())
		return
	}

	opts := rtorrent.AddOptions{Start: true}
	if r.FormValue("start") == "false" {
		opts.Start = false
	}
	skipHashCheck := r.FormValue("skip_hash_check") == "true"
	sequential := r.FormValue("sequential") == "true"
	destination := r.FormValue("destination")
	label := r.FormValue("label")

	if destination != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.directory.set="+destination)
	}
	if label != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.custom1.set="+label)
	}
	if skipHashCheck {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.hashing.set=0")
	}
	if sequential {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.sequential.set=1")
	}

	var results []addItemResult

	// Magnet/URL lines.
	for _, field := range r.Form["magnets"] {
		for _, line := range strings.Split(field, "\n") {
			uri := strings.TrimSpace(line)
			if uri == "" {
				continue
			}
			if err := validateMagnetOrURL(uri); err != nil {
				results = append(results, addItemResult{Source: uri, OK: false, Error: err.Error()})
				continue
			}
			if err := s.opts.RTorrent.AddMagnet(r.Context(), uri, opts); err != nil {
				results = append(results, addItemResult{Source: uri, OK: false, Error: err.Error()})
				continue
			}
			// Magnets carry no local metadata; key the add by the btih
			// hash when present so it still lands in the history.
			if s.history != nil {
				s.history.Add(magnetHash(uri), history.Entry{
					Kind: history.KindAdd, Actor: actorFromRequest(r, s.auth), Action: "add",
					Result: "ok", Message: uri,
				})
			}
			results = append(results, addItemResult{Source: uri, OK: true})
		}
	}

	// .torrent files.
	if r.MultipartForm != nil {
		for _, headers := range r.MultipartForm.File {
			for _, fh := range headers {
				name := fh.Filename
				if !strings.EqualFold(filepath.Ext(name), ".torrent") {
					results = append(results, addItemResult{Source: name, OK: false, Error: "not a .torrent file"})
					continue
				}
				f, err := fh.Open()
				if err != nil {
					results = append(results, addItemResult{Source: name, OK: false, Error: err.Error()})
					continue
				}
				data, err := ioReadAllLimit(f, 16<<20)
				f.Close()
				if err != nil {
					results = append(results, addItemResult{Source: name, OK: false, Error: err.Error()})
					continue
				}
				// Capture .torrent metadata (comment, created-by, creation date)
				// keyed by infohash for the General tab, and log the add.
				if hash, meta := metaFromBytes(data); hash != "" {
					s.meta.put(hash, meta)
					s.history.Add(hash, history.Entry{
						Kind: history.KindAdd, Actor: actorFromRequest(r, s.auth), Action: "add",
						Result: "ok", Message: name, Name: name,
					})
				}
				if err := s.opts.RTorrent.AddTorrentFile(r.Context(), data, opts); err != nil {
					results = append(results, addItemResult{Source: name, OK: false, Error: err.Error()})
					continue
				}
				results = append(results, addItemResult{Source: name, OK: true})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// validateMagnetOrURL accepts magnet URIs and http(s) .torrent URLs, matching
// the add-dialog's inline validation.
func validateMagnetOrURL(uri string) error {
	if strings.HasPrefix(uri, "magnet:?xt=urn:btih:") {
		return nil
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		// Require a .torrent-looking path; the daemon fetches it.
		u := uri
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			u = u[:i]
		}
		if strings.HasSuffix(strings.ToLower(u), ".torrent") {
			return nil
		}
		return errors.New("http(s) URLs must point to a .torrent file")
	}
	return errors.New("expected magnet:?xt=urn:btih:… or an http(s) .torrent URL")
}

// ---- GET /api/settings ----

type settingsResponse struct {
	Tuning       config.Tuning      `json:"tuning"`
	Daemon       map[string]string  `json:"daemon"` // rtorrent key → live value
	History      config.History     `json:"history"`
	Stats        config.Stats       `json:"stats"`
	PortCheck    config.PortCheck   `json:"portcheck"`
	Network      config.Network     `json:"network"`
	Capabilities capabilities       `json:"capabilities"`
	Directories  config.Directories `json:"directories"`
	Automation   config.Automation  `json:"automation"`
	Seeding      config.Seeding     `json:"seeding"`
	Schedule     config.Schedule    `json:"schedule"`
	Labels       []config.Label     `json:"labels"`
	UI           config.UI          `json:"ui"`
}

func (s *Server) settingsGetHandler(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Store.Get()
	resp := settingsResponse{
		Tuning:       cfg.Tuning,
		Daemon:       map[string]string{},
		History:      cfg.History,
		Stats:        cfg.Stats,
		PortCheck:    cfg.PortCheck,
		Network:      cfg.Network,
		Capabilities: s.probeCapabilities(r),
		Directories:  cfg.Directories,
		Automation:   redactAutomation(cfg.Automation),
		Seeding:      cfg.Seeding,
		Schedule:     cfg.Schedule,
		Labels:       cfg.Labels,
		UI:           cfg.UI,
	}

	// Live daemon values for every tuning key, batched in one multicall.
	// Iterated in stable table order (POL-8.8) so the request is deterministic.
	var reqs []rtorrent.Request
	var order []string
	for _, key := range tuning.Keys() {
		getter, ok := tuning.GetterFor(key)
		if !ok {
			continue
		}
		reqs = append(reqs, rtorrent.Request{Method: getter})
		order = append(order, key)
	}
	if len(reqs) > 0 {
		if results, err := s.opts.RTorrent.MultiCall(r.Context(), reqs); err == nil {
			for i, key := range order {
				if i < len(results) && results[i].Err == nil && len(results[i].Values) > 0 {
					v := results[i].Values[0]
					if v.Type == "int" {
						resp.Daemon[key] = strconv.FormatInt(v.Int, 10)
					} else {
						resp.Daemon[key] = v.Str
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- POST /api/settings ----

type settingsSaveRequest struct {
	Tuning      *config.Tuning      `json:"tuning,omitempty"`
	History     *config.History     `json:"history,omitempty"`
	Stats       *config.Stats       `json:"stats,omitempty"`
	PortCheck   *config.PortCheck   `json:"portcheck,omitempty"`
	Network     *config.Network     `json:"network,omitempty"`
	Directories *config.Directories `json:"directories,omitempty"`
	Automation  *config.Automation  `json:"automation,omitempty"`
	Seeding     *config.Seeding     `json:"seeding,omitempty"`
	Schedule    *config.Schedule    `json:"schedule,omitempty"`
	Labels      *[]config.Label     `json:"labels,omitempty"`
	UI          *config.UI          `json:"ui,omitempty"`
}

type settingsSaveResponse struct {
	Results []settingsApplyResult `json:"results"`
	Saved   bool                  `json:"saved"`
	Error   string                `json:"error,omitempty"`
}

type settingsApplyResult struct {
	Key   string `json:"key"`
	Error string `json:"error,omitempty"`
}

func (s *Server) settingsSaveHandler(w http.ResponseWriter, r *http.Request) {
	var req settingsSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}

	current := s.opts.Store.Get()
	// Validate every editable YAML-backed section in a full-config context.
	changed := current
	// An omitted "tuning" section leaves the persisted tuning alone. It used
	// to be assigned unconditionally from a value type, so any partial body
	// (e.g. {"ui":{...}}) silently zeroed every tuning key on disk — and
	// because the resulting diff was empty the daemon kept running the old
	// values, hiding the loss until the next restart.
	if req.Tuning != nil {
		changed.Tuning = *req.Tuning
		// A missing throttles list leaves daemon channels untouched; an
		// explicit (even empty) list replaces them.
		if req.Tuning.Throttles == nil {
			changed.Tuning.Throttles = current.Tuning.Throttles
		}
	}
	if req.History != nil {
		changed.History = *req.History
	}
	if req.Stats != nil {
		changed.Stats = *req.Stats
	}
	if req.PortCheck != nil {
		changed.PortCheck = *req.PortCheck
	}
	if req.Network != nil {
		changed.Network = *req.Network
	}
	if req.Directories != nil {
		changed.Directories = *req.Directories
	}
	if req.Automation != nil {
		changed.Automation = *req.Automation
		// Masked secrets keep their stored values; see secretMask.
		restoreRSSSecrets(&changed.Automation, current.Automation)
	}
	if req.Seeding != nil {
		changed.Seeding = *req.Seeding
	}
	if req.Schedule != nil {
		changed.Schedule = *req.Schedule
	}
	if req.Labels != nil {
		changed.Labels = *req.Labels
	}
	if req.UI != nil {
		changed.UI = *req.UI
	}
	if verrs := config.Validate(&changed); len(verrs) > 0 {
		msgs := make([]string, len(verrs))
		for i, v := range verrs {
			msgs[i] = v.String()
		}
		writeAPIError(w, http.StatusBadRequest, "validation", strings.Join(msgs, "; "))
		return
	}

	intention := s.history.Begin("", history.Entry{Actor: actorFromRequest(r, s.auth), Action: "settings_save", Before: history.ConfigValues(current), After: history.ConfigValues(changed), Message: "Requested settings change; not yet applied or saved."})
	settingsRecorded := false
	defer func() {
		if !settingsRecorded && s.history.Recorder() != nil {
			s.history.Recorder().Record("", history.Entry{Phase: "rpc_result", Actor: actorFromRequest(r, s.auth), Action: "settings_save", CauseID: intention, Result: "failed", Message: "Settings request ended before save; some daemon operations may have occurred."})
		}
	}()
	// Apply only changed keys to the live daemon; per-key outcomes reported.
	diff := tuning.Diff(current.Tuning, changed.Tuning)
	results := tuning.ApplySequential(r.Context(), s.opts.RTorrent, diff)
	if changed.Directories.Default != current.Directories.Default && changed.Directories.Default != "" {
		results = append(results, tuning.Result{Key: "directory.default", Err: s.opts.RTorrent.SetDefaultDirectory(r.Context(), changed.Directories.Default)})
	}

	// Named throttle channels: upsert creations/updates, refuse removals
	// still referenced by the session (nothing is persisted on refusal).
	upsert, removed := tuning.ChannelDiff(current.Tuning.Throttles, changed.Tuning.Throttles)
	if len(upsert) > 0 || len(removed) > 0 {
		inUse := map[string]int{}
		if s.opts.Poller != nil {
			inUse = tuning.InUse(s.opts.Poller.Snapshot().Torrents)
		}
		for _, name := range removed {
			if n := inUse[name]; n > 0 {
				writeAPIError(w, http.StatusBadRequest, "throttle_in_use", fmt.Sprintf("throttle channel %q is still used by %d torrent(s); unassign them first", name, n))
				return
			}
		}
		for _, cr := range tuning.ApplyChannels(r.Context(), s.opts.RTorrent, upsert, removed, inUse) {
			item := tuning.Result{Key: cr.Name, Err: cr.Err}
			results = append(results, item)
		}
	}

	// Persist the whole YAML atomically and swap the in-memory config.
	err := s.opts.Store.SaveSettings(changed)
	if err == nil {
		s.history.RecordConfig(changed, actorFromRequest(r, s.auth), intention)
	}
	if recorder := s.history.Recorder(); recorder != nil {
		outcomes := map[string]string{}
		for _, result := range results {
			if result.Err != nil {
				outcomes[result.Key] = "failed"
			} else {
				outcomes[result.Key] = "ok"
			}
		}
		result := "saved"
		if err != nil {
			result = "save_failed"
		}
		recorder.Record("", history.Entry{Phase: "rpc_result", Actor: actorFromRequest(r, s.auth), Action: "settings_save", CauseID: intention, Result: result, After: outcomes, Message: "Per-key daemon request outcomes; successful save does not mean every daemon setting applied."})
	}
	settingsRecorded = true
	// A changed blocklist source reloads immediately (the refresh loop
	// would otherwise pick it up on its next tick).
	networkChanged := changed.Network != current.Network
	if err == nil && networkChanged && s.opts.IPFilter != nil && changed.Network.IPFilter.Enabled() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = s.opts.IPFilter.ApplyNow(ctx)
		}()
	}
	// Retention edits apply to the live tracker without a restart.
	if err == nil && s.opts.Traffic != nil {
		s.opts.Traffic.SetRetentionDays(changed.Stats.EffectiveTrafficDays())
	}
	// History bounds apply to the live log without a restart.
	if err == nil && s.history != nil {
		s.history.SetBounds(changed.History.ActionLogEntries, changed.History.ActionLogRetention, changed.History.EffectiveGlobalEntries())
	}
	uiResults := make([]settingsApplyResult, 0, len(results))
	for _, result := range results {
		item := settingsApplyResult{Key: result.Key}
		if result.Err != nil {
			item.Error = result.Err.Error()
		}
		uiResults = append(uiResults, item)
	}
	resp := settingsSaveResponse{Results: uiResults, Saved: err == nil}
	if err != nil {
		resp.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

var rpcMethodRe = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

type settingsExecuteRequest struct {
	Method string   `json:"method"`
	Params []string `json:"params"`
}

// settingsExecuteHandler is the explicitly-confirmed Advanced-tab escape
// hatch. It accepts only a method identifier and string arguments, and logs
// the method name (never values) for operator auditability.
func (s *Server) settingsExecuteHandler(w http.ResponseWriter, r *http.Request) {
	// Opt-in per deployment: this reaches the daemon's whole command
	// surface, which is a categorically different power from the tuning
	// keys the Advanced tab exists to edit.
	if s.opts.Store == nil || !s.opts.Store.Get().Server.AllowExecute {
		writeAPIError(w, http.StatusForbidden, "execute_disabled",
			"raw XML-RPC is disabled; set server.allow_execute: true to enable it")
		return
	}
	var req settingsExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if !rpcMethodRe.MatchString(req.Method) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "method must contain only letters, numbers, dots, and underscores")
		return
	}
	if deniedRPCMethod(req.Method) {
		slog.Warn("operator XML-RPC method refused", "method", req.Method)
		writeAPIError(w, http.StatusForbidden, "method_denied",
			"this method runs external programs or rewrites the daemon's command table and is never allowed here")
		return
	}
	slog.Info("operator XML-RPC method executed", "method", req.Method)
	if err := s.opts.RTorrent.Execute(r.Context(), req.Method, req.Params...); err != nil {
		status, code, msg := errorFor(err)
		writeAPIError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deniedRPCMethodPrefixes are XML-RPC families that run external programs
// or rewrite the daemon's own command table. They stay blocked even when
// server.allow_execute is on: the escape hatch exists to reach daemon
// settings, and "execute2" is a shell, not a setting. Blocking them means a
// future hole in the surrounding auth or origin checks cannot escalate to
// running commands on the daemon host.
var deniedRPCMethodPrefixes = []string{
	"execute",              // execute, execute2, execute.throw, execute.capture, ...
	"system.method.set",    // redefines an existing command
	"system.method.insert", // defines a new one, which may wrap execute
	"import",               // sources an arbitrary rc file
	"try_import",
	"schedule", // schedule/schedule2 can run the above on a timer
}

// deniedRPCMethod reports whether a method name falls in a blocked family.
func deniedRPCMethod(method string) bool {
	lower := strings.ToLower(method)
	for _, prefix := range deniedRPCMethodPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// ---- small formatting helpers ----

func itoa(n int) string { return strconv.Itoa(n) }

func formatBytes(n int64) string {
	const unit = 1024.0
	f := float64(n)
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	return trimFloat(f) + " " + units[i]
}

func formatRate(bytesPerSecond int64) string {
	if bytesPerSecond == 0 {
		return "—"
	}
	return formatBytes(bytesPerSecond) + "/s"
}

func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word
}

func ioReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}
