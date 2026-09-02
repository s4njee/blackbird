package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"blackbird/internal/poller"
)

// WebSocket protocol (v1):
//
//	server → client: {"v":1,"type":"snapshot"|"delta"|"detail"|"pong","data":…,"hash":…}
//	client → server: {"type":"focus","hash":"…"} | {"type":"unfocus"}
//	                 | {"type":"hidden","value":true|false} | {"type":"ping"}
//
// On connect the server sends a full snapshot, then a delta per poll cycle.
// Connection status transitions arrive embedded in deltas ("status" field).
// While a tab is hidden (client sends hidden=true) detail subscriptions are
// paused and the connection downgrades to a slow keepalive; on visible=false
// a fresh snapshot is pushed before detail resumes.
const (
	wsVersion         = 1
	wsWriteWait       = 10 * time.Second
	wsPongWait        = 60 * time.Second
	wsPingPeriod      = 30 * time.Second
	wsHiddenKeepalive = 30 * time.Second
	sendBuffer        = 64
)

type wsEnvelope struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	Hash string `json:"hash,omitempty"`
	Data any    `json:"data,omitempty"`
}

type wsInbound struct {
	Type  string `json:"type"`
	Hash  string `json:"hash"`
	Value bool   `json:"value"`
}

// hub fans poller deltas out to every connected client.
type hub struct {
	mu      sync.Mutex
	clients map[*wsClient]bool
	sub     func() // unsubscribe from poller deltas
}

func (h *hub) register(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *hub) unregister(c *wsClient) {
	h.mu.Lock()
	if h.clients[c] {
		delete(h.clients, c)
	}
	h.mu.Unlock()
}

// broadcastDelta marshals once and delivers to every client. Slow clients
// (full send buffer) skip the delta; their next snapshot reconnect fixes it.
func (h *hub) broadcastDelta(d poller.Delta) {
	data, err := json.Marshal(wsEnvelope{V: wsVersion, Type: "delta", Data: d})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
		}
	}
}

func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.close()
	}
	h.clients = map[*wsClient]bool{}
}

// wsClient is one browser connection.
type wsClient struct {
	hub     *hub
	poller  *poller.Poller
	conn    *websocket.Conn
	send    chan []byte
	closeCh chan struct{}
	closed  bool

	mu      sync.Mutex
	focused string
	hidden  bool
}

func (c *wsClient) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	focused := c.focused
	c.mu.Unlock()
	// Only closeCh signals termination. c.send is never closed: sendEnvelope,
	// detailLoop and the hub can select on it concurrently, and sending on a
	// closed channel panics. writePump exits on closeCh; straggling sends
	// after close land in the bounded buffer and are dropped.
	close(c.closeCh)
	_ = c.conn.Close()
	if focused != "" {
		c.poller.Unfocus(focused)
	}
}

func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{
		hub:     s.hub,
		poller:  s.opts.Poller,
		conn:    conn,
		send:    make(chan []byte, sendBuffer),
		closeCh: make(chan struct{}),
	}
	s.hub.register(c)
	defer s.hub.unregister(c)
	defer c.close()

	// Full snapshot on connect. Snapshot() already copies the slices, so no
	// aliasing with the poller's internal state.
	snap := s.opts.Poller.Snapshot()
	if data, err := json.Marshal(wsEnvelope{V: wsVersion, Type: "snapshot", Data: snap}); err == nil {
		select {
		case c.send <- data:
		default:
		}
	}
	// Focus this connection's hash lazily and keep detail flowing.
	go c.detailLoop()

	go c.writePump()
	c.readPump()
}

// readPump processes client messages and enforces heartbeat liveness.
func (c *wsClient) readPump() {
	defer c.close()
	c.conn.SetReadLimit(1 << 16)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	for {
		var msg wsInbound
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "ping":
			c.sendEnvelope(wsEnvelope{V: wsVersion, Type: "pong"})
		case "focus":
			c.mu.Lock()
			old := c.focused
			c.focused = msg.Hash
			c.mu.Unlock()
			if old != "" && old != msg.Hash {
				c.poller.Unfocus(old)
			}
			if msg.Hash != "" && msg.Hash != old {
				c.poller.Focus(msg.Hash)
			}
		case "unfocus":
			c.mu.Lock()
			old := c.focused
			c.focused = ""
			c.mu.Unlock()
			if old != "" {
				c.poller.Unfocus(old)
			}
		case "hidden":
			c.mu.Lock()
			wasHidden := c.hidden
			c.hidden = msg.Value
			c.mu.Unlock()
			// Resuming from a hidden tab: push a fresh snapshot before
			// detail subscriptions resume.
			if wasHidden && !msg.Value {
				snap := c.poller.Snapshot()
				c.sendEnvelope(wsEnvelope{V: wsVersion, Type: "snapshot", Data: snap})
			}
		case "close":
			return
		}
	}
}

// writePump drains the send channel and sends protocol-level pings; the
// read side enforces pong liveness (heartbeat ping/pong).
func (c *wsClient) writePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		case data, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *wsClient) sendEnvelope(e wsEnvelope) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-c.closeCh:
	default:
		// Buffer full — drop; the client recovers via snapshot on reconnect.
	}
}

// detailLoop pushes focused-torrent detail on the poller's detail interval.
// While the tab is hidden the loop skips detail (slow keepalive only).
func (c *wsClient) detailLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		hash, hidden := c.focused, c.hidden
		c.mu.Unlock()

		if hash == "" || hidden {
			continue
		}
		if d, ok := c.poller.Detail(hash); ok {
			c.sendEnvelope(wsEnvelope{V: wsVersion, Type: "detail", Hash: hash, Data: d})
		}
	}
}
