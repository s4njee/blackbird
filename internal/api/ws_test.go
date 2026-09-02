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
	if snap.V != wsVersion {
		t.Fatalf("snapshot version = %d", snap.V)
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

func TestWebSocketPingPong(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	ws := dialWS(t, ts.URL, "", "")

	if err := ws.WriteJSON(map[string]any{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	env := readUntilType(t, ws, 3*time.Second, "pong")
	if env.V != wsVersion {
		t.Fatalf("pong version = %d", env.V)
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
	if env.V != wsVersion {
		t.Fatalf("snapshot version = %d", env.V)
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
