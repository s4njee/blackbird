package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"blackbird/internal/fakertorrent"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

// statusOf extracts the connection status carried by a delta envelope.
func statusOf(env wsEnvelope) (string, bool) {
	data, err := json.Marshal(env.Data)
	if err != nil {
		return "", false
	}
	var d struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return "", false
	}
	return d.Status, d.Status != ""
}

// waitForWSStatus reads envelopes until a delta carrying the wanted
// connection status arrives (or the deadline passes).
func waitForWSStatus(t *testing.T, ws *websocket.Conn, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		env := readMsg(t, ws)
		if env.Type == "delta" {
			if s, ok := statusOf(env); ok && s == want {
				return
			}
		}
	}
	t.Fatalf("no delta with status %q received", want)
}

// dialWS opens a WebSocket connection with optional basic auth.
func dialWS(t *testing.T, url, user, pass string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	if user != "" {
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http")+"/ws", header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// readMsg reads one server envelope with a timeout.
func readMsg(t *testing.T, ws *websocket.Conn) wsEnvelope {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var env wsEnvelope
	if err := ws.ReadJSON(&env); err != nil {
		t.Fatalf("read message: %v", err)
	}
	return env
}

// readUntilType consumes envelopes until one of the wanted types arrives.
// It tolerates interleaved deltas racing the connect snapshot.
func readUntilType(t *testing.T, ws *websocket.Conn, timeout time.Duration, want ...string) wsEnvelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		env := readMsg(t, ws)
		for _, w := range want {
			if env.Type == w {
				return env
			}
		}
	}
	t.Fatalf("no %v message within %s", want, timeout)
	return wsEnvelope{}
}

func TestWebSocketSnapshotAndDelta(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	ws := dialWS(t, ts.URL, "", "")

	snap := readUntilType(t, ws, 3*time.Second, "snapshot")
	data, _ := json.Marshal(snap.Data)
	var sd struct {
		Status   string `json:"status"`
		Torrents []struct {
			Hash string `json:"hash"`
		} `json:"torrents"`
		Aggregates map[string]any   `json:"aggregates"`
		Volumes    []map[string]any `json:"volumes"`
		Global     struct {
			Version string `json:"version"`
		} `json:"global"`
	}
	if err := json.Unmarshal(data, &sd); err != nil {
		t.Fatal(err)
	}
	// Default clients (no hello) stay on v1: byte-compatible envelopes.
	if snap.V != wsVersionMin {
		t.Fatalf("snapshot version = %d, want v1", snap.V)
	}
	if sd.Status != "connected" || len(sd.Torrents) != 3 || sd.Global.Version != "0.15.4" {
		t.Fatalf("snapshot = %s", data)
	}

	// Deltas arrive each poll cycle and carry the server's category counts.
	deadline := time.Now().Add(3 * time.Second)
	sawDelta := false
	for time.Now().Before(deadline) && !sawDelta {
		env := readMsg(t, ws)
		if env.Type == "delta" {
			data, _ := json.Marshal(env.Data)
			var delta struct {
				Aggregates struct {
					Status map[string]int `json:"status"`
				} `json:"aggregates"`
			}
			if err := json.Unmarshal(data, &delta); err != nil {
				t.Fatal(err)
			}
			if delta.Aggregates.Status["all"] != 3 {
				t.Fatalf("delta aggregates = %s", data)
			}
			sawDelta = true
		}
	}
	if !sawDelta {
		t.Fatal("no delta received")
	}
}

func TestWebSocketFocusDetailAndHidden(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	ws := dialWS(t, ts.URL, "", "")

	// First message: full versioned snapshot.
	readMsg(t, ws)

	// Focus a hash: the poller fetches detail lazily and pushes it.
	if err := ws.WriteJSON(map[string]any{"type": "focus", "hash": "aaaa1111aaaa1111"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var detail wsEnvelope
	for time.Now().Before(deadline) {
		env := readMsg(t, ws)
		if env.Type == "detail" {
			detail = env
			break
		}
	}
	if detail.Hash != "aaaa1111aaaa1111" {
		t.Fatalf("detail hash = %q", detail.Hash)
	}
	data, _ := json.Marshal(detail.Data)
	var d rtorrent.Detail
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Files) == 0 {
		t.Fatalf("detail has no files: %s", data)
	}

	// hidden=true pauses detail pushes. Deltas still flow each poll cycle, so
	// reads stay live; we simply require no detail in the window. The first
	// message is drained to discount a detail enqueued just before the hidden
	// signal was processed (a broken pause still surfaces its next push
	// ~500ms later within the window).
	if err := ws.WriteJSON(map[string]any{"type": "hidden", "value": true}); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	var drained wsEnvelope
	if err := ws.ReadJSON(&drained); err != nil {
		t.Fatalf("read while hidden: %v", err)
	}
	windowEnd := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(windowEnd) {
		_ = ws.SetReadDeadline(time.Now().Add(time.Second))
		var env wsEnvelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatalf("read while hidden: %v", err)
		}
		if env.Type == "detail" {
			t.Fatal("detail pushed while hidden")
		}
	}

	// hidden=false resumes with a fresh snapshot.
	if err := ws.WriteJSON(map[string]any{"type": "hidden", "value": false}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		env := readMsg(t, ws)
		if env.Type == "snapshot" {
			return
		}
	}
	t.Fatal("no snapshot on visibility resume")
}

func TestWebSocketBitfieldDiffed(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	ws := dialWS(t, ts.URL, "", "")
	readMsg(t, ws) // snapshot

	// Focus: a bitfield envelope should arrive (hex of the piece map).
	if err := ws.WriteJSON(map[string]any{"type": "focus", "hash": "aaaa1111aaaa1111"}); err != nil {
		t.Fatal(err)
	}
	var first wsEnvelope
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		env := readMsg(t, ws)
		if env.Type == "bitfield" {
			first = env
			break
		}
	}
	if first.Hash != "aaaa1111aaaa1111" {
		t.Fatalf("bitfield hash = %q", first.Hash)
	}
	data, _ := json.Marshal(first.Data)
	var bf struct {
		Hex string `json:"hex"`
	}
	if err := json.Unmarshal(data, &bf); err != nil {
		t.Fatal(err)
	}
	if len(bf.Hex) == 0 || len(bf.Hex)%2 != 0 {
		t.Fatalf("bitfield hex = %q", bf.Hex)
	}

	// The fake daemon's bitfield is static, so no further bitfield envelope
	// should arrive while still focused (deltas continue to flow).
	windowEnd := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(windowEnd) {
		env := readMsg(t, ws)
		if env.Type == "bitfield" {
			t.Fatal("unchanged bitfield was re-sent")
		}
	}
}

func TestWebSocketPingPong(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	ws := dialWS(t, ts.URL, "", "")

	if err := ws.WriteJSON(map[string]any{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	env := readUntilType(t, ws, 3*time.Second, "pong")
	if env.V != wsVersionMin {
		t.Fatalf("pong version = %d, want v1", env.V)
	}
}

func TestWebSocketAuthRequired(t *testing.T) {
	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))

	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err == nil {
		t.Fatal("unauthenticated upgrade accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("upgrade response = %+v, err = %v", resp, err)
	}

	ws := dialWS(t, ts.URL, "op", "hunter2") // valid credentials upgrade fine
	readMsg(t, ws)
}

// TestWebSocketUnfocusStopsDetail verifies the unsubscribe path: after
// unfocus, no further detail envelopes are pushed for the hash (deltas still
// flow, so the connection itself stays live).
func TestWebSocketUnfocusStopsDetail(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	ws := dialWS(t, ts.URL, "", "")
	readMsg(t, ws) // snapshot

	if err := ws.WriteJSON(map[string]any{"type": "focus", "hash": "aaaa1111aaaa1111"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env := readMsg(t, ws); env.Type == "detail" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no detail after focus")
		}
	}

	if err := ws.WriteJSON(map[string]any{"type": "unfocus"}); err != nil {
		t.Fatal(err)
	}
	// Drain one message (a detail enqueued just before the unfocus was
	// processed), then require no detail within the window.
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	var drained wsEnvelope
	if err := ws.ReadJSON(&drained); err != nil {
		t.Fatalf("read after unfocus: %v", err)
	}
	windowEnd := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(windowEnd) {
		_ = ws.SetReadDeadline(time.Now().Add(time.Second))
		var env wsEnvelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatalf("read after unfocus: %v", err)
		}
		if env.Type == "detail" {
			t.Fatal("detail pushed after unfocus")
		}
	}
}

// TestWebSocketConnectionStatusTransitions verifies the delta stream carries
// rtorrent lost/recovered transitions as events (Epic 4.2).
func TestWebSocketConnectionStatusTransitions(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	ws := dialWS(t, st.ts.URL, "", "")
	env := readUntilType(t, ws, 3*time.Second, "snapshot")
	if env.V != wsVersionMin {
		t.Fatalf("snapshot version = %d, want v1", env.V)
	}
	data, _ := json.Marshal(env.Data)
	var snap struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Status != "connected" {
		t.Fatalf("snapshot status = %q", snap.Status)
	}

	// Kill the daemon: the poller flips to disconnected and pushes a delta.
	st.daemon.Stop()
	waitForWSStatus(t, ws, "disconnected")

	// Restart on the same socket: recovery pushes a connected delta, and the
	// cache keeps serving the last-good snapshot underneath.
	d2, err := fakertorrent.StartOpts(st.sock, fakertorrent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Stop()
	waitForWSStatus(t, ws, "connected")
}

// TestWebSocketServerCloseDrainsClient exercises the shutdown path: closeAll
// closes every client while detail delivery may still be in flight. The
// client must observe a clean close and the server must not panic or hang
// (regression for sending on a closed channel).
func TestWebSocketServerCloseDrainsClient(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	ws := dialWS(t, st.ts.URL, "", "")
	readMsg(t, ws) // snapshot

	// Keep detail flowing so c.close() races detailLoop's sends.
	if err := ws.WriteJSON(map[string]any{"type": "focus", "hash": "aaaa1111aaaa1111"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env := readMsg(t, ws); env.Type == "detail" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no detail after focus")
		}
	}

	st.srv.Close() // drains the hub: closeAll on the live client
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return // clean close / network error — no hang, no server panic
		}
	}
}

// readDeltaWhere consumes envelopes until a delta satisfies pred.
func readDeltaWhere(t *testing.T, ws *websocket.Conn, timeout time.Duration, pred func(map[string]any) bool) (wsEnvelope, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		env := readMsg(t, ws)
		if env.Type != "delta" {
			continue
		}
		data, _ := json.Marshal(env.Data)
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if pred(m) {
			return env, m
		}
	}
	t.Fatal("no matching delta received")
	return wsEnvelope{}, nil
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// assignThrottle drives one single-field change through the fake daemon: the
// throttle column flips "" → name on the next list poll.
func assignThrottle(t *testing.T, url, hash, channel string) {
	t.Helper()
	resp, _ := postJSON(t, url+"/api/torrents/action", map[string]any{
		"action": "set_throttle", "hashes": []string{hash}, "throttle": channel,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set_throttle status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestWebSocketV2HelloPatches proves the v2 wire: after hello, deltas carry
// changedPatches (not wholes), omit unchanged globals, and send aggregate
// patches. Steady-state ticks shrink to roughly the timestamp alone.
func TestWebSocketV2HelloPatches(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	ws := dialWS(t, st.ts.URL, "", "")
	snap := readUntilType(t, ws, 3*time.Second, "snapshot")
	if snap.V != wsVersionMin {
		t.Fatalf("pre-hello snapshot version = %d, want v1", snap.V)
	}
	if err := ws.WriteJSON(map[string]any{"type": "hello", "version": 2}); err != nil {
		t.Fatal(err)
	}

	// Steady state against the static fake daemon: v2 deltas omit the
	// unchanged global (the first post-hello flush may still carry it if it
	// raced the hello, so loop until a filtered tick arrives).
	env, _ := readDeltaWhere(t, ws, 5*time.Second, func(m map[string]any) bool {
		return !hasKey(m, "global") && !hasKey(m, "aggregates")
	})
	if env.V != wsVersion {
		t.Fatalf("v2 delta version = %d", env.V)
	}

	assignThrottle(t, st.ts.URL, "aaaa1111aaaa1111", "bulk")

	env, m := readDeltaWhere(t, ws, 5*time.Second, func(m map[string]any) bool {
		patches, ok := m["changedPatches"].([]any)
		if !ok {
			return false
		}
		for _, p := range patches {
			pm, _ := p.(map[string]any)
			if pm["hash"] == "aaaa1111aaaa1111" {
				return true
			}
		}
		return false
	})
	if env.V != wsVersion {
		t.Fatalf("v2 delta version = %d", env.V)
	}
	if hasKey(m, "changed") {
		t.Fatalf("v2 delta carries whole changed rows: %v", m)
	}
	if hasKey(m, "aggregates") {
		t.Fatalf("v2 delta carries full aggregates: %v", m)
	}
	patches := m["changedPatches"].([]any)
	if len(patches) != 1 {
		t.Fatalf("changedPatches = %v", patches)
	}
	pm := patches[0].(map[string]any)
	fields, _ := pm["fields"].(map[string]any)
	if len(fields) != 1 || fields["throttle"] != "bulk" {
		t.Fatalf("patch fields = %v, want exactly {throttle:bulk}", fields)
	}
	ap, ok := m["aggregatesPatch"].(map[string]any)
	if !ok {
		t.Fatalf("aggregatesPatch missing: %v", m)
	}
	throttles, _ := ap["throttles"].(map[string]any)
	updated, _ := throttles["updated"].(map[string]any)
	if updated["bulk"] != float64(1) {
		t.Fatalf("aggregatesPatch = %v", ap)
	}
}

// TestWebSocketV1Compat proves clients without hello keep receiving
// byte-compatible v1 deltas: whole changed rows, global on every tick, full
// aggregates, and no v2-only keys.
func TestWebSocketV1Compat(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	ws := dialWS(t, st.ts.URL, "", "")
	readUntilType(t, ws, 3*time.Second, "snapshot")

	assignThrottle(t, st.ts.URL, "aaaa1111aaaa1111", "bulk")

	env, m := readDeltaWhere(t, ws, 5*time.Second, func(m map[string]any) bool {
		changed, ok := m["changed"].([]any)
		if !ok {
			return false
		}
		for _, c := range changed {
			cm, _ := c.(map[string]any)
			if cm["hash"] == "aaaa1111aaaa1111" && cm["throttle"] == "bulk" {
				return true
			}
		}
		return false
	})
	if env.V != wsVersionMin {
		t.Fatalf("v1 delta version = %d", env.V)
	}
	if hasKey(m, "changedPatches") || hasKey(m, "aggregatesPatch") {
		t.Fatalf("v1 delta carries v2 keys: %v", m)
	}
	if !hasKey(m, "global") || !hasKey(m, "aggregates") {
		t.Fatalf("v1 delta dropped always-on keys: %v", m)
	}
}

// TestHubBroadcastNoticeDelivers proves the POL-8.3 notice envelope reaches
// connected clients with its kind intact (no network round trip).
func TestHubBroadcastNoticeDelivers(t *testing.T) {
	h := &hub{clients: map[*wsClient]bool{}}
	c := &wsClient{send: make(chan []byte, sendBuffer), deltaPing: make(chan struct{}, 1), version: wsVersion}
	h.clients[c] = true
	h.broadcastNotice(Notice{Kind: "completed", Hash: "abc", Title: "x.iso"})
	select {
	case raw := <-c.send:
		var env wsEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "notice" || env.V != wsVersion {
			t.Fatalf("envelope = %+v", env)
		}
		data, _ := json.Marshal(env.Data)
		var got Notice
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Kind != "completed" || got.Hash != "abc" || got.Title != "x.iso" {
			t.Fatalf("notice = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no notice delivered")
	}
}

// TestHubCoalescingMergesLatestWins proves slow-client convergence without
// network: ticks merged into dirty pending state collapse latest-wins, the
// metric counts them, removal wins over queued adds, and both wire
// encodings render from the same merged state.
func TestHubCoalescingMergesLatestWins(t *testing.T) {
	h := &hub{clients: map[*wsClient]bool{}}
	c := &wsClient{send: make(chan []byte, sendBuffer), deltaPing: make(chan struct{}, 1), version: wsVersionMin}
	h.clients[c] = true
	now := time.Now()

	g1 := rtorrent.GlobalStats{DownRate: 1}
	h.broadcastDelta(poller.Delta{
		Added:  []rtorrent.Torrent{{Hash: "a", Name: "A"}, {Hash: "b", Name: "B"}},
		Global: &g1, At: now,
	})
	if h.Coalesced() != 0 {
		t.Fatalf("coalesced = %d, want 0 (first tick arms the flush)", h.Coalesced())
	}
	select {
	case <-c.deltaPing:
	default:
		t.Fatal("no flush signaled for the first tick")
	}
	// Second tick before any flush: change a, remove b (wins over the queued
	// add), change the global.
	g2 := rtorrent.GlobalStats{DownRate: 2}
	h.broadcastDelta(poller.Delta{
		Changed:        []rtorrent.Torrent{{Hash: "a", Name: "A2"}},
		ChangedPatches: []poller.TorrentPatch{{Hash: "a", Fields: map[string]any{"name": "A2"}}},
		Removed:        []string{"b"},
		Global:         &g2,
		At:             now.Add(time.Second),
	})
	if h.Coalesced() != 1 {
		t.Fatalf("coalesced = %d, want 1", h.Coalesced())
	}
	// No second signal: one flush covers both ticks.
	select {
	case <-c.deltaPing:
		t.Fatal("second flush signaled despite coalescing")
	default:
	}

	v1 := c.encodeV1(c.pending)
	if len(v1.Added) != 1 || v1.Added[0].Name != "A2" {
		t.Fatalf("v1 added = %+v, want the folded newer whole", v1.Added)
	}
	if len(v1.Changed) != 0 {
		t.Fatalf("v1 changed = %+v, want empty (folded into the add)", v1.Changed)
	}
	if len(v1.Removed) != 1 || v1.Removed[0] != "b" {
		t.Fatalf("v1 removed = %+v", v1.Removed)
	}
	if v1.Global == nil || v1.Global.DownRate != 2 {
		t.Fatalf("v1 global = %+v", v1.Global)
	}

	v2 := c.encodeV2(c.pending, &g2, poller.DiffAggregates(nil, &poller.Aggregates{
		Status: map[rtorrent.State]int{rtorrent.StateDownloading: 1},
	}))
	if len(v2.Added) != 1 || len(v2.ChangedPatches) != 0 || len(v2.Removed) != 1 {
		t.Fatalf("v2 = %+v", v2)
	}
	if v2.Global == nil || v2.Global.DownRate != 2 {
		t.Fatalf("v2 global = %+v", v2.Global)
	}
}

// TestWebSocketCompressionNegotiated proves the server offers
// permessage-deflate (PERF-6.2) to clients that ask for it.
func TestWebSocketCompressionNegotiated(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	d := websocket.Dialer{EnableCompression: true}
	ws, resp, err := d.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.Close()
	if resp == nil || !strings.Contains(resp.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate") {
		t.Fatalf("extensions = %+v", resp)
	}
}

// TestHasVisibleClients proves the adaptive-polling signal (PERF-6.3): a
// connected tab counts, a hidden tab does not, and disconnect clears it.
func TestHasVisibleClients(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	ws := dialWS(t, st.ts.URL, "", "")
	readUntilType(t, ws, 3*time.Second, "snapshot")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st.srv.HasVisibleClients() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !st.srv.HasVisibleClients() {
		t.Fatal("connected tab not counted as visible")
	}

	if err := ws.WriteJSON(map[string]any{"type": "hidden", "value": true}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !st.srv.HasVisibleClients() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.srv.HasVisibleClients() {
		t.Fatal("hidden tab still counted as visible")
	}
	ws.Close()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !st.srv.HasVisibleClients() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("closed tab still counted")
}

// TestSendDetailIfChanged proves per-client detail sends are change-driven
// (PERF-6.3): an identical payload hash queues nothing, a new one queues
// exactly one envelope.
func TestSendDetailIfChanged(t *testing.T) {
	c := &wsClient{
		send:    make(chan []byte, 8),
		closeCh: make(chan struct{}),
		version: wsVersionMin,
	}
	d := rtorrent.Detail{Hash: "a"}
	c.sendDetailIfChanged("a", d, 123)
	if len(c.send) != 1 {
		t.Fatalf("queued = %d, want 1", len(c.send))
	}
	c.sendDetailIfChanged("a", d, 123)
	if len(c.send) != 1 {
		t.Fatalf("queued = %d, want still 1 (unchanged payload)", len(c.send))
	}
	c.sendDetailIfChanged("a", d, 456)
	if len(c.send) != 2 {
		t.Fatalf("queued = %d, want 2", len(c.send))
	}
	var env wsEnvelope
	if err := json.Unmarshal(<-c.send, &env); err != nil {
		t.Fatal(err)
	}
	if env.V != wsVersionMin || env.Type != "detail" || env.Hash != "a" {
		t.Fatalf("envelope = %+v", env)
	}
}
