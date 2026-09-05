package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

// WebSocket protocol (v1 and v2):
//
//	server → client: {"v":1|2,"type":"snapshot"|"delta"|"detail"|"bitfield"|"watch"|"automation"|"pong","data":…,"hash":…}
//	client → server: {"type":"hello","version":2}
//	                 | {"type":"focus","hash":"…"} | {"type":"unfocus"}
//	                 | {"type":"hidden","value":true|false} | {"type":"ping"}
//
// On connect the server sends a full snapshot, then a delta per poll cycle.
// Connection status transitions arrive embedded in deltas ("status" field).
// While a tab is hidden (client sends hidden=true) detail subscriptions are
// paused and the connection downgrades to a slow keepalive; on visible=false
// a fresh snapshot is pushed before detail resumes. "bitfield" carries the
// focused torrent's piece map (hex), sent only when it changes (PAR-2.6).
// "watch" carries a watch-directory event (PAR-3.1) for the toast surface.
//
// v1 (frozen, byte-compatible): deltas carry whole changed torrent objects,
// global stats on every successful poll, and full aggregates.
// v2 (PERF-6.2): deltas carry {hash, fields} patches for changed torrents,
// global stats only when changed since the client's last flush, and
// aggregate patches (updated/removed keys). Added/removed rows, snapshots,
// detail, bitfield, watch, and automation shapes are identical in both.
//
// Negotiation and compatibility rules (SHIP-1.4):
//   - A client announces v2 with {"type":"hello","version":2} after open.
//     Unknown inbound types are ignored, so a v2 client against a v1 server
//     simply never receives v2 deltas, and a v1 client (no hello) against
//     this server keeps receiving byte-identical v1 deltas. No flag day.
//   - The envelope "v" echoes the negotiated version for that client.
//   - Clients apply both delta shapes (presence-based: "changed" wholes vs
//     "changedPatches", "aggregates" vs "aggregatesPatch"), so mixed-version
//     fleets converge.
//   - v1 service is retained indefinitely; removing it would follow the
//     REL-8.1 deprecation policy (documented migration + warning first).
//   - Frames use permessage-deflate when the client negotiates it (all
//     browsers do); sizes are recorded in the PERF-6.2 report in backlogv2.md.
//
// Slow clients never lose data: poller cycles merge into per-client pending
// state (latest-wins per hash) instead of being dropped, and a hub counter
// (exposed on /api/health as coalescedTicks) counts merged ticks.
const (
	// wsVersion is the newest protocol served; wsVersionMin is the oldest.
	// Clients hello with the version they speak; anything above wsVersion
	// clamps down, anything below stays at the minimum.
	wsVersion    = 2
	wsVersionMin = 1

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
	Type    string `json:"type"`
	Hash    string `json:"hash"`
	Value   bool   `json:"value"`
	Version int    `json:"version"` // hello: the protocol version the client speaks
}

// hub fans poller deltas out to every connected client.
type hub struct {
	mu      sync.Mutex
	clients map[*wsClient]bool

	// coalesced counts poller ticks merged into an already-dirty (slow)
	// client instead of being queued separately (PERF-6.2).
	coalesced atomic.Int64
}

// Coalesced returns the merged-tick count for /api/health.
func (h *hub) Coalesced() int64 { return h.coalesced.Load() }

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

// clientPending is one client's unsent delta state. New poller cycles merge
// into it (latest-wins per hash) instead of queueing behind a slow reader,
// so slow clients converge without loss (PERF-6.2). The write pump flushes
// and clears it; a snapshot send clears it too (the snapshot supersedes).
type clientPending struct {
	added     map[string]rtorrent.Torrent
	changed   map[string]rtorrent.Torrent // whole rows (v1 wire)
	patches   map[string]map[string]any   // merged field patches (v2 wire)
	removed   map[string]struct{}
	global    *rtorrent.GlobalStats // latest; set on every successful cycle
	aggs      *poller.Aggregates    // latest
	status    poller.ConnStatus
	hasStatus bool
	// globalChanged ORs cycles for the v1 wire, which historically flagged
	// any global change per forwarded delta.
	globalChanged bool
	at            time.Time
}

func newClientPending() *clientPending {
	return &clientPending{
		added:   map[string]rtorrent.Torrent{},
		changed: map[string]rtorrent.Torrent{},
		patches: map[string]map[string]any{},
		removed: map[string]struct{}{},
	}
}

// merge folds one poller cycle into the pending state. Removal wins over
// queued adds/changes; a re-add after removal starts over; a change to a
// queued add folds the newer whole row into the add.
func (p *clientPending) merge(d poller.Delta) {
	patchesByHash := make(map[string]map[string]any, len(d.ChangedPatches))
	for _, patch := range d.ChangedPatches {
		patchesByHash[patch.Hash] = patch.Fields
	}
	for _, t := range d.Added {
		p.added[t.Hash] = t
		delete(p.changed, t.Hash)
		delete(p.patches, t.Hash)
		delete(p.removed, t.Hash)
	}
	for _, t := range d.Changed {
		if _, ok := p.added[t.Hash]; ok {
			p.added[t.Hash] = t
			continue
		}
		p.changed[t.Hash] = t
		if fields, ok := patchesByHash[t.Hash]; ok {
			merged := p.patches[t.Hash]
			if merged == nil {
				merged = map[string]any{}
				p.patches[t.Hash] = merged
			}
			for k, v := range fields {
				merged[k] = v
			}
		}
	}
	for _, h := range d.Removed {
		delete(p.added, h)
		delete(p.changed, h)
		delete(p.patches, h)
		p.removed[h] = struct{}{}
	}
	if d.Global != nil {
		p.global = d.Global
	}
	if d.Aggregates != nil {
		p.aggs = d.Aggregates
	}
	if d.Status != "" {
		p.status = d.Status
		p.hasStatus = true
	}
	p.globalChanged = p.globalChanged || d.GlobalChanged
	p.at = d.At
}

// v1Delta is the frozen v1 delta wire shape: whole changed rows, global on
// every successful cycle, full aggregates. Byte-compatible with the old
// broadcast path.
type v1Delta struct {
	Added         []rtorrent.Torrent    `json:"added,omitempty"`
	Changed       []rtorrent.Torrent    `json:"changed,omitempty"`
	Removed       []string              `json:"removed,omitempty"`
	GlobalChanged bool                  `json:"globalChanged,omitempty"`
	Global        *rtorrent.GlobalStats `json:"global,omitempty"`
	Status        poller.ConnStatus     `json:"status,omitempty"`
	Aggregates    *poller.Aggregates    `json:"aggregates,omitempty"`
	At            time.Time             `json:"at"`
}

// v2Delta is the PERF-6.2 wire shape: field patches for changed rows,
// global only when present in the flush (the hub already filtered it
// against the client's last sent value), and aggregate patches.
type v2Delta struct {
	Added           []rtorrent.Torrent      `json:"added,omitempty"`
	ChangedPatches  []poller.TorrentPatch   `json:"changedPatches,omitempty"`
	Removed         []string                `json:"removed,omitempty"`
	GlobalChanged   bool                    `json:"globalChanged,omitempty"`
	Global          *rtorrent.GlobalStats   `json:"global,omitempty"`
	Status          poller.ConnStatus       `json:"status,omitempty"`
	AggregatesPatch *poller.AggregatesPatch `json:"aggregatesPatch,omitempty"`
	At              time.Time               `json:"at"`
}

// broadcastDelta merges one poller cycle into every client's pending state
// and wakes writers that have nothing outstanding. A merge into an already
// dirty client is a coalesced tick: the cycles collapse latest-wins instead
// of queueing, and the hub metric counts it.
func (h *hub) broadcastDelta(d poller.Delta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		dirty := c.pending != nil
		if !dirty {
			c.pending = newClientPending()
		}
		c.pending.merge(d)
		if dirty {
			h.coalesced.Add(1)
		} else {
			c.signalLocked()
		}
		c.mu.Unlock()
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

// broadcastWatch fans a watch-directory event (PAR-3.1) out to every client
// as a "watch" envelope so open consoles can toast it. The shape is version
// independent; only the envelope tag follows the negotiated version.
func (h *hub) broadcastWatch(n WatchNotice) {
	h.deliverVersioned("watch", "", n)
}

// broadcastAutomation fans a completion-rule outcome (PAR-3.2) out to every
// client as an "automation" envelope so open consoles can toast failures.
func (h *hub) broadcastAutomation(n AutomationNotice) {
	h.deliverVersioned("automation", "", n)
}

// broadcastNotice fans a user-facing event (POL-8.3) out to every client as
// a "notice" envelope for toasts and the notification centre.
func (h *hub) broadcastNotice(n Notice) {
	h.deliverVersioned("notice", "", n)
}

// deliverVersioned marshals one envelope per protocol version in play (at
// most two) and sends each client its own. Shapes shared across versions
// marshal once per version, not once per client.
func (h *hub) deliverVersioned(envType, hash string, data any) {
	v1, err := json.Marshal(wsEnvelope{V: wsVersionMin, Type: envType, Hash: hash, Data: data})
	if err != nil {
		return
	}
	v2, err := json.Marshal(wsEnvelope{V: wsVersion, Type: envType, Hash: hash, Data: data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		out := v1
		if c.version >= wsVersion {
			out = v2
		}
		c.mu.Unlock()
		select {
		case c.send <- out:
		default:
		}
	}
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
	// lastBitfield tracks the last bitfield hex pushed for the focused hash so
	// unchanged piece maps are not re-sent every detail tick (PAR-2.6).
	lastBitfield string
	// lastDetailHash fingerprints the last detail payload sent for the
	// focused hash: detail envelopes go out only when the content changed,
	// not on the fixed tick (PERF-6.3).
	lastDetailHash uint64
	// version is the negotiated protocol version (default v1 until hello).
	version int
	// deltaPing wakes the write pump for a pending delta flush (cap 1,
	// signaled tracking below keeps it exact).
	deltaPing chan struct{}
	signaled  bool
	// pending is the unsent merged delta; nil when clean.
	pending *clientPending
	// lastGlobal/lastAggs are the v2 client's last flushed values, so
	// unchanged globals/aggregates are omitted on the next flush.
	lastGlobal rtorrent.GlobalStats
	hasGlobal  bool
	lastAggs   *poller.Aggregates
	// snapEpoch invalidates a flush taken before a snapshot send: the
	// snapshot supersedes anything queued earlier.
	snapEpoch int
	// snapPending holds snapshot bytes that must precede any delta flush.
	snapPending []byte
}

// signalLocked wakes the write pump for pending work. Caller holds c.mu.
func (c *wsClient) signalLocked() {
	if c.signaled {
		return
	}
	c.signaled = true
	select {
	case c.deltaPing <- struct{}{}:
	default:
	}
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
	// Compression is negotiated on the upgrader (see server.go); frames are
	// permessage-deflate when the client accepts it.
	c := &wsClient{
		hub:       s.hub,
		poller:    s.opts.Poller,
		conn:      conn,
		send:      make(chan []byte, sendBuffer),
		deltaPing: make(chan struct{}, 1),
		closeCh:   make(chan struct{}),
		version:   wsVersionMin,
	}
	s.hub.register(c)
	defer s.hub.unregister(c)
	defer c.close()

	// Full snapshot on connect, tagged v1: the client has not hello'd yet,
	// and the snapshot shape is identical in both versions.
	snap := s.opts.Poller.Snapshot()
	if data, err := json.Marshal(wsEnvelope{V: wsVersionMin, Type: "snapshot", Data: snap}); err == nil {
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
			c.sendEnvelope(wsEnvelope{Type: "pong"})
		case "hello":
			// v2 negotiation (PERF-6.2): clamp to the served range and seed
			// the v2 send state from the live snapshot so the first v2
			// delta omits already-known globals/aggregates. Unknown
			// versions stay at v1; old servers ignore hello entirely.
			c.mu.Lock()
			if msg.Version > wsVersionMin {
				c.version = min(msg.Version, wsVersion)
			}
			snap := c.poller.Snapshot()
			c.lastGlobal, c.hasGlobal = snap.Global, true
			c.lastAggs = &snap.Aggregates
			c.mu.Unlock()
		case "focus":
			c.mu.Lock()
			old := c.focused
			c.focused = msg.Hash
			if msg.Hash != old {
				c.lastBitfield = ""  // a different torrent needs a fresh bitfield push
				c.lastDetailHash = 0 // and a fresh detail push even if hashes collide
			}
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
			c.lastBitfield = ""
			c.lastDetailHash = 0
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
			// detail subscriptions resume. The snapshot travels through the
			// flush path (not c.send) so queued deltas cannot overtake it,
			// and pending state clears — the snapshot supersedes it.
			if wasHidden && !msg.Value {
				c.queueSnapshot()
			}
		case "close":
			return
		}
	}
}

// queueSnapshot marshals the live snapshot and routes it through the flush
// path ahead of any delta: pending state clears (superseded) and the epoch
// invalidates flushes taken earlier, so a client can never apply stale
// deltas on top of a fresh snapshot.
func (c *wsClient) queueSnapshot() {
	c.mu.Lock()
	snap := c.poller.Snapshot()
	version := c.version
	data, err := json.Marshal(wsEnvelope{V: version, Type: "snapshot", Data: snap})
	if err != nil {
		c.mu.Unlock()
		return
	}
	c.pending = nil
	c.snapEpoch++
	c.snapPending = data
	c.lastGlobal, c.hasGlobal = snap.Global, true
	c.lastAggs = &snap.Aggregates
	c.signalLocked()
	c.mu.Unlock()
}

// writePump drains the send channel and flushes merged deltas; the read
// side enforces pong liveness (heartbeat ping/pong).
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
		case <-c.deltaPing:
			c.flush()
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

// flush writes one queued snapshot (first, when present) followed by the
// merged pending delta in the client's negotiated version.
func (c *wsClient) flush() {
	c.mu.Lock()
	c.signaled = false
	snap := c.snapPending
	c.snapPending = nil
	pending, epoch := c.pending, c.snapEpoch
	c.pending = nil
	version := c.version
	// v2 send decisions are made here, atomically with the take: globals go
	// out only when changed since the last flush, aggregates as patches.
	var global *rtorrent.GlobalStats
	var aggPatch *poller.AggregatesPatch
	if version >= wsVersion && pending != nil {
		if pending.global != nil && (!c.hasGlobal || *pending.global != c.lastGlobal) {
			global = pending.global
			c.lastGlobal, c.hasGlobal = *pending.global, true
		}
		if patch := poller.DiffAggregates(c.lastAggs, pending.aggs); patch != nil {
			aggPatch = patch
			c.lastAggs = pending.aggs
		}
	}
	c.mu.Unlock()

	if snap != nil {
		if !c.writeBytes(snap) {
			return
		}
	}
	if pending == nil {
		return
	}
	// A snapshot queued after this flush was taken supersedes it.
	c.mu.Lock()
	stale := epoch != c.snapEpoch
	c.mu.Unlock()
	if stale {
		return
	}
	var data []byte
	var err error
	if version >= wsVersion {
		data, err = json.Marshal(wsEnvelope{V: version, Type: "delta", Data: c.encodeV2(pending, global, aggPatch)})
	} else {
		data, err = json.Marshal(wsEnvelope{V: version, Type: "delta", Data: c.encodeV1(pending)})
	}
	if err != nil {
		return
	}
	c.writeBytes(data)
}

// encodeV1 renders pending state in the frozen v1 shape.
func (c *wsClient) encodeV1(p *clientPending) v1Delta {
	out := v1Delta{
		GlobalChanged: p.globalChanged,
		Global:        p.global,
		Status:        p.status,
		Aggregates:    p.aggs,
		At:            p.at,
	}
	// hasStatus distinguishes an explicit transition from the zero value:
	// v1 omits status unless a transition was merged.
	if !p.hasStatus {
		out.Status = ""
	}
	for _, h := range sortedKeys(p.added) {
		out.Added = append(out.Added, p.added[h])
	}
	for _, h := range sortedKeys(p.changed) {
		out.Changed = append(out.Changed, p.changed[h])
	}
	out.Removed = append(out.Removed, sortedKeys(p.removed)...)
	return out
}

// encodeV2 renders pending state as field patches with filtered globals.
func (c *wsClient) encodeV2(p *clientPending, global *rtorrent.GlobalStats, aggPatch *poller.AggregatesPatch) v2Delta {
	out := v2Delta{
		GlobalChanged:   p.globalChanged,
		Global:          global,
		Status:          p.status,
		AggregatesPatch: aggPatch,
		At:              p.at,
	}
	if !p.hasStatus {
		out.Status = ""
	}
	for _, h := range sortedKeys(p.added) {
		out.Added = append(out.Added, p.added[h])
	}
	for _, h := range sortedKeys(p.patches) {
		out.ChangedPatches = append(out.ChangedPatches, poller.TorrentPatch{Hash: h, Fields: p.patches[h]})
	}
	out.Removed = append(out.Removed, sortedKeys(p.removed)...)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for h := range m {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// writeBytes writes one frame under deadline; false means the connection is
// dead and the pump should stop.
func (c *wsClient) writeBytes(data []byte) bool {
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		c.close()
		return false
	}
	return true
}

func (c *wsClient) sendEnvelope(e wsEnvelope) {
	c.mu.Lock()
	e.V = c.version
	c.mu.Unlock()
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
// While the tab is hidden the loop skips detail (slow keepalive only). The
// torrent's piece bitfield is pushed in its own envelope, and only when it
// actually changed since the last tick (PAR-2.6).
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
		if d, h, ok := c.poller.DetailHash(hash); ok {
			c.sendDetailIfChanged(hash, d, h)
			c.pushBitfieldIfChanged(hash, d.BitfieldHex)
		}
	}
}

// sendDetailIfChanged pushes a detail envelope only when the payload hash
// differs from the last one sent for this client/hash (PERF-6.3): the loop
// still ticks on its cadence, but identical payloads cost nothing on the
// wire. The hash comes from the poller, which fingerprints every fetch, so
// no client-side marshal is needed for the comparison.
func (c *wsClient) sendDetailIfChanged(hash string, d rtorrent.Detail, h uint64) {
	c.mu.Lock()
	if h == c.lastDetailHash {
		c.mu.Unlock()
		return
	}
	c.lastDetailHash = h
	version := c.version
	c.mu.Unlock()
	data, err := json.Marshal(wsEnvelope{V: version, Type: "detail", Hash: hash, Data: d})
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-c.closeCh:
	default:
	}
}

// pushBitfieldIfChanged emits a bitfield envelope only when the hex string
// differs from the last one sent for this client/hash.
func (c *wsClient) pushBitfieldIfChanged(hash, hex string) {
	if hex == "" {
		return // no bitfield data (e.g. a magnet still resolving) — nothing to draw
	}
	c.mu.Lock()
	changed := hex != c.lastBitfield
	if changed {
		c.lastBitfield = hex
	}
	c.mu.Unlock()
	if !changed {
		return
	}
	c.sendEnvelope(wsEnvelope{Type: "bitfield", Hash: hash, Data: map[string]string{"hex": hex}})
}
