// Package api wires the HTTP server: REST handlers, WebSocket hub, auth
// middleware, and the embedded frontend.
package api

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"blackbird/internal/attention"
	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/ipfilter"
	"blackbird/internal/mktorrent"
	"blackbird/internal/poller"
	"blackbird/internal/portcheck"
	"blackbird/internal/preservation"
	"blackbird/internal/rss"
	"blackbird/internal/rtorrent"
	"blackbird/internal/schedule"
	"blackbird/internal/traffic"
	"blackbird/internal/unpack"
)

// Options configures the API server.
type Options struct {
	// Poller, RTorrent and Store power the REST/WS surface; nil leaves the
	// related routes returning 503 (used by tests and during early setup).
	Poller   *poller.Poller
	RTorrent *rtorrent.Client
	Store    ConfigStore

	// History is the per-torrent action/message log backing the Logger tab.
	// When nil, a default-bounded log is created.
	History      *history.Log
	Attention    *attention.Store
	Preservation *preservation.Store

	// RSS is the feed intake service (PAR-3.3); nil leaves the /api/rss
	// routes returning 503 (used by tests).
	RSS *rss.Service

	// Unpack is the extraction service (PAR-3.4); nil leaves /api/unpack
	// returning 503 (used by tests).
	Unpack *unpack.Service

	// Schedule is the bandwidth scheduler (PAR-4.3); nil leaves the
	// /api/schedule routes returning 503 (used by tests).
	Schedule *schedule.Scheduler

	// Traffic is the transfer-history tracker (PAR-5.2); nil leaves
	// /api/traffic returning 503 (used by tests).
	Traffic *traffic.Tracker

	// Create is the .torrent creation service (PAR-5.4); nil leaves the
	// /api/torrents/create routes returning 503 (used by tests).
	Create *mktorrent.Service

	// IPFilter is the blocklist service (PAR-5.6); nil leaves /api/ipfilter
	// reporting disabled (used by tests).
	IPFilter *ipfilter.Service

	// Health supplies live connection info for /api/health.
	Health func() HealthInfo

	// Build is the binary identity stamped at link time (POL-8.7
	// /api/version). Zero values degrade to dev/none/unknown.
	Build BuildInfo
}

// WatchNotice is a server-initiated watch-directory event pushed to every
// WebSocket client so open consoles can toast it (PAR-3.1). The fields mirror
// the watchdir package's Event without importing it here.
type WatchNotice struct {
	WatchDir string `json:"watchDir"`
	File     string `json:"file"`
	Kind     string `json:"kind"` // loaded | duplicate | malformed | load_error | watch_error
	Hash     string `json:"hash,omitempty"`
	Message  string `json:"message,omitempty"`
}

// HealthInfo is the /api/health payload.
type HealthInfo struct {
	Connection string `json:"connection"` // connected | disconnected
	LastError  string `json:"last_error,omitempty"`
	Stale      bool   `json:"stale"`
	Torrents   int    `json:"torrents"`
	// CoalescedTicks counts poller ticks merged into an already-dirty
	// (slow) WebSocket client instead of being queued separately (PERF-6.2).
	// Omitted when zero so older payloads are unchanged.
	CoalescedTicks int64 `json:"coalescedTicks,omitempty"`
}

// Server is the root HTTP handler plus shutdown hooks.
type Server struct {
	opts     Options
	hub      *hub
	upgrader websocket.Upgrader
	handler  http.Handler
	unsub    func()
	history  *history.Log
	meta     *metaStore
	auth     *Auth
	moveMu   sync.Mutex
	moves    map[string]*moveJob
	moveSeq  uint64
	// portMu guards the last user-initiated reachability verdict (PAR-5.5).
	portMu        sync.Mutex
	lastPortCheck *portcheck.Result
}

// New builds the API server. The caller should invoke Close on shutdown to
// drain WebSocket clients.
func New(opts Options, auth *Auth) *Server {
	if opts.History == nil {
		opts.History = history.New(history.Options{})
	}
	s := &Server{
		opts:    opts,
		history: opts.History,
		meta: newMetaStore(func() string {
			if opts.Store == nil {
				return ""
			}
			return opts.Store.Get().Directories.Session
		}),
		auth: auth,
		hub:  &hub{clients: map[*wsClient]bool{}},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// permessage-deflate (PERF-6.2): negotiated per client; browsers
			// accept it, shrinking steady-state ticks on top of v2 patches.
			EnableCompression: true,
			// Auth gates the upgrade, but Basic credentials are ambient:
			// a hostile page can open a socket to this server and have the
			// browser attach them. The handshake is a GET, so the
			// middleware's state-changing gate does not cover it — check
			// the origin here. The Vite dev server still works: the browser
			// sees /ws as same-origin and says so in Sec-Fetch-Site.
			CheckOrigin: func(r *http.Request) bool {
				if auth == nil {
					return sameHostOrigin(r)
				}
				return auth.SameOrigin(r)
			},
		},
		moves: map[string]*moveJob{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthzHandler)
	for _, r := range apiRoutes(s) {
		mux.HandleFunc(r.pattern, r.handler)
		// POL-8.8: every REST route is versioned under /api/v1. The
		// unprefixed /api/* forms keep working as the legacy spelling
		// (SHIP-1.4: deprecated surface needs a migration path before
		// removal); new clients use /api/v1.
		mux.HandleFunc(v1Pattern(r.pattern), r.handler)
	}
	mux.HandleFunc("GET /ws", s.wsHandler)
	mux.Handle("/", frontendHandler(opts.Store))

	var h http.Handler = mux
	if auth != nil {
		h = auth.Middleware(h) // covers every API route and the WS upgrade
	}
	s.handler = h

	// Fan poller deltas out to WebSocket clients; unsubscribed on Close.
	if opts.Poller != nil {
		s.unsub = opts.Poller.Subscribe(s.hub.broadcastDelta)
	}
	return s
}

// apiRoute is one REST route: the legacy /api/* pattern plus its handler.
// apiRoutes is the single route table (POL-8.8); New() serves every entry
// under both /api/* (legacy) and /api/v1/* (versioned).
type apiRoute struct {
	pattern string
	handler http.HandlerFunc
}

// v1Pattern maps "GET /api/health" to "GET /api/v1/health".
func v1Pattern(pattern string) string {
	return strings.Replace(pattern, "/api/", "/api/v1/", 1)
}

func apiRoutes(s *Server) []apiRoute {
	return []apiRoute{
		{"GET /api/health", s.healthHandler},
		{"GET /api/version", s.versionHandler},
		{"GET /api/session", s.sessionHandler},
		{"GET /api/stats", s.statsHandler},
		{"GET /api/traffic", s.trafficHandler},
		{"GET /api/host", s.hostHandler},
		{"GET /api/history", s.historyHandler},
		{"GET /api/history/flight", s.flightHandler},
		{"GET /api/attention", s.attentionHandler},
		{"GET /api/preservation", s.preservationHandler},
		{"POST /api/preservation", s.preservationUpdateHandler},
		{"POST /api/storage/forecast", s.storageHandler},
		{"POST /api/attention", s.attentionUpdateHandler},
		{"GET /api/torrents/{hash}", s.detailHandler},
		{"GET /api/torrent-file/{hash}", s.torrentFileHandler},
		{"POST /api/torrents/action", s.actionHandler},
		{"GET /api/directories", s.directoryHandler},
		{"POST /api/directories", s.directoryCreateHandler},
		{"POST /api/torrents/move", s.moveStartHandler},
		{"GET /api/torrents/move/{id}", s.moveStatusHandler},
		{"POST /api/torrents/move/{id}/cancel", s.moveCancelHandler},
		{"POST /api/torrents/add", s.addHandler},
		{"POST /api/torrents/create", s.createStartHandler},
		{"GET /api/torrents/create/{id}", s.createStatusHandler},
		{"POST /api/torrents/create/{id}/cancel", s.createCancelHandler},
		{"GET /api/torrents/create/{id}/download", s.createDownloadHandler},
		{"POST /api/torrents/create/{id}/add", s.createAddHandler},
		{"GET /api/settings", s.settingsGetHandler},
		{"POST /api/settings", s.settingsSaveHandler},
		{"POST /api/automation/dry-run", s.automationDryRunHandler},
		{"GET /api/rss", s.rssViewHandler},
		{"POST /api/rss/add", s.rssAddHandler},
		{"POST /api/rss/read", s.rssReadHandler},
		{"GET /api/unpack", s.unpackStatusHandler},
		{"GET /api/throttles", s.throttlesHandler},
		{"GET /api/seeding", s.seedingHandler},
		{"POST /api/seeding/dry-run", s.seedingDryRunHandler},
		{"GET /api/schedule", s.scheduleStatusHandler},
		{"POST /api/schedule/override", s.scheduleOverrideHandler},
		{"DELETE /api/schedule/override", s.scheduleOverrideClearHandler},
		{"GET /api/bandwidth", s.bandwidthHandler},
		{"POST /api/bandwidth", s.bandwidthSetHandler},
		{"GET /api/port-check", s.portCheckStatusHandler},
		{"POST /api/port-check", s.portCheckRunHandler},
		{"GET /api/ipfilter", s.ipfilterStatusHandler},
		{"POST /api/ipfilter/reload", s.ipfilterReloadHandler},
		{"POST /api/settings/execute", s.settingsExecuteHandler},
		{"GET /api/themes", s.themesListHandler},
		{"POST /api/themes/import", s.themesImportHandler},
		{"DELETE /api/themes/{name}", s.themeDeleteHandler},
		{"GET /api/custom-css", s.customCSSHandler},
	}
}

// portCheckConfig reads the live probe configuration (Settings saves apply
// without a restart).
func (s *Server) portCheckConfig() config.PortCheck {
	if s.opts.Store == nil {
		return config.PortCheck{}
	}
	return s.opts.Store.Get().PortCheck
}

// HasVisibleClients reports whether at least one non-hidden browser tab is
// connected. The poller stretches toward poll.max_interval while this is
// false and snaps back on the first visible client (PERF-6.3).
func (s *Server) HasVisibleClients() bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for c := range s.hub.clients {
		c.mu.Lock()
		hidden := c.hidden
		c.mu.Unlock()
		if !hidden {
			return true
		}
	}
	return false
}

// healthz is a lightweight process probe for container orchestrators. It is
// intentionally separate from /api/health, which exposes rTorrent state and
// remains protected by the configured Basic auth middleware.
func (s *Server) healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.opts.Health == nil {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	h := s.opts.Health()
	h.CoalescedTicks = s.hub.Coalesced()
	writeJSON(w, http.StatusOK, h)
}

// Handler returns the root handler (auth-wrapped).
func (s *Server) Handler() http.Handler { return s.handler }

// BroadcastWatch pushes a watch-directory event to every WebSocket client
// (PAR-3.1). The watcher service calls this for each load/malformed/error
// outcome; it is safe to call before any client connects.
func (s *Server) BroadcastWatch(n WatchNotice) { s.hub.broadcastWatch(n) }

// AutomationNotice is a completion-rule outcome (PAR-3.2) pushed to every
// WebSocket client so open consoles can toast failures.
type AutomationNotice struct {
	Hash    string `json:"hash"`
	Torrent string `json:"torrent,omitempty"`
	Rule    string `json:"rule"`
	Kind    string `json:"kind"` // completed | failed
	Message string `json:"message,omitempty"`
}

// BroadcastAutomation pushes a completion-rule outcome to every WebSocket
// client (PAR-3.2); safe to call before any client connects.
func (s *Server) BroadcastAutomation(n AutomationNotice) { s.hub.broadcastAutomation(n) }

// Notice is a user-facing event (POL-8.3) pushed to every WebSocket client
// for toasts and the notification centre: torrent completions and RSS loads.
// Kinds: "completed" (a d.complete 0→1 transition, with the torrent hash and
// name) and "rss-loaded" (a filter match auto-loaded into the session, with
// the infohash, item title, and feed name in Message).
type Notice struct {
	Kind    string `json:"kind"`
	Hash    string `json:"hash"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

// BroadcastNotice pushes a user-facing event to every WebSocket client
// (POL-8.3); safe to call before any client connects.
func (s *Server) BroadcastNotice(n Notice) { s.hub.broadcastNotice(n) }

// MoveForAutomation moves one torrent's data with the PAR-2.2 move engine on
// behalf of a completion rule (PAR-3.2): stop/restart running torrents,
// cross-device copy + verify, and configured-root boundary enforcement.
func (s *Server) MoveForAutomation(ctx context.Context, hash, destination string) error {
	return s.moveTorrent(ctx, hash, destination, moveFiles)
}

// Close drains WebSocket clients and releases the poller subscription.
func (s *Server) Close() {
	if s.unsub != nil {
		s.unsub()
	}
	s.hub.closeAll()
}

// frontendHandler serves the embedded Vite build with pre-compressed
// variants (see assets.go): hashed assets under /assets/ are immutable,
// index.html (and any other path, for SPA fallback) is no-cache.
// The operator-default theme (ui.theme) is injected into index.html's
// no-flash boot script per request via a hook closure over the live
// config store; a nil store keeps the precompressed path. The shared
// singleton is never mutated: each server gets a shallow copy carrying
// its own hook.
func frontendHandler(store ConfigStore) http.Handler {
	base := embeddedAssets()
	if store == nil {
		return base.handler()
	}
	s := &assetServer{
		files:    base.files,
		fallback: base.fallback,
		dist:     base.dist,
		ThemeDefault: func() string {
			return store.Get().UI.Theme
		},
		AccentDefault: func() string {
			return store.Get().UI.Accent
		},
	}
	return s.handler()
}
