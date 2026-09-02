// Package api wires the HTTP server: REST handlers, WebSocket hub, auth
// middleware, and the embedded frontend.
package api

import (
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
	"blackbird/web"
)

// Options configures the API server.
type Options struct {
	// Poller, RTorrent and Store power the REST/WS surface; nil leaves the
	// related routes returning 503 (used by tests and during early setup).
	Poller   *poller.Poller
	RTorrent *rtorrent.Client
	Store    ConfigStore

	// Health supplies live connection info for /api/health.
	Health func() HealthInfo
}

// HealthInfo is the /api/health payload.
type HealthInfo struct {
	Connection string `json:"connection"` // connected | disconnected
	LastError  string `json:"last_error,omitempty"`
	Stale      bool   `json:"stale"`
	Torrents   int    `json:"torrents"`
}

// Server is the root HTTP handler plus shutdown hooks.
type Server struct {
	opts     Options
	hub      *hub
	upgrader websocket.Upgrader
	handler  http.Handler
	unsub    func()
	moveMu   sync.Mutex
	moves    map[string]*moveJob
	moveSeq  uint64
}

// New builds the API server. The caller should invoke Close on shutdown to
// drain WebSocket clients.
func New(opts Options, auth *Auth) *Server {
	s := &Server{
		opts: opts,
		hub:  &hub{clients: map[*wsClient]bool{}},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// Auth gates the upgrade; cross-origin origins are accepted so
			// the Vite dev server (proxied /ws) works during development.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		moves: map[string]*moveJob{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthzHandler)
	mux.HandleFunc("GET /api/health", s.healthHandler)
	mux.HandleFunc("GET /api/session", s.sessionHandler)
	mux.HandleFunc("GET /api/stats", s.statsHandler)
	mux.HandleFunc("GET /api/torrents/{hash}", s.detailHandler)
	mux.HandleFunc("POST /api/torrents/action", s.actionHandler)
	mux.HandleFunc("GET /api/directories", s.directoryHandler)
	mux.HandleFunc("POST /api/torrents/move", s.moveStartHandler)
	mux.HandleFunc("GET /api/torrents/move/{id}", s.moveStatusHandler)
	mux.HandleFunc("POST /api/torrents/move/{id}/cancel", s.moveCancelHandler)
	mux.HandleFunc("POST /api/torrents/add", s.addHandler)
	mux.HandleFunc("GET /api/settings", s.settingsGetHandler)
	mux.HandleFunc("POST /api/settings", s.settingsSaveHandler)
	mux.HandleFunc("POST /api/settings/execute", s.settingsExecuteHandler)
	mux.HandleFunc("GET /ws", s.wsHandler)
	mux.Handle("/", frontendHandler())

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
	writeJSON(w, http.StatusOK, s.opts.Health())
}

// Handler returns the root handler (auth-wrapped).
func (s *Server) Handler() http.Handler { return s.handler }

// Close drains WebSocket clients and releases the poller subscription.
func (s *Server) Close() {
	if s.unsub != nil {
		s.unsub()
	}
	s.hub.closeAll()
}

// frontendHandler serves the embedded Vite build. Hashed assets under /assets/
// are immutable; index.html (and any other path, for SPA fallback) is no-cache.
func frontendHandler() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic("web/dist missing from embed: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")

		if p == "" || p == "index.html" {
			serveIndex(w, r, dist)
			return
		}

		if f, err := dist.Open(p); err == nil {
			f.Close()
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: unknown paths render the app shell.
		serveIndex(w, r, dist)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "frontend not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
