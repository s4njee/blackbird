package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"blackbird/internal/fakertorrent"
)

func waitForThrottle(t *testing.T, st *testStack, hash, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, row := range st.p.Snapshot().Torrents {
			if row.Hash == hash && row.Throttle == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("throttle of %s never became %q", hash, want)
}

func hasDaemonCall(st *testStack, method string) bool {
	for _, m := range st.daemon.CallMethods() {
		if m == method {
			return true
		}
	}
	return false
}

// TestThrottleChannelLifecycle covers the PAR-4.1 backend contract end to
// end: channel creation via settings save, assignment with the stop/set/start
// cycle, live listing, and refusal of in-use removal with a count.
func TestThrottleChannelLifecycle(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	// 1. Create channels through the settings save.
	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"throttles": []map[string]any{
			{"name": "slow", "up_kb": 100, "down_kb": 500},
		}},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	if !hasDaemonCall(st, "throttle.up") || !hasDaemonCall(st, "throttle.down") {
		t.Fatalf("daemon never received channel creation: %v", st.daemon.CallMethods())
	}
	found := false
	if results, ok := body["results"].([]any); ok {
		for _, r := range results {
			if m, ok := r.(map[string]any); ok && m["key"] == "throttle.slow" && m["error"] == nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("channel result missing: %+v", body)
	}

	// 2. List channels: YAML caps plus live daemon rates.
	listResp, err := http.Get(st.ts.URL + "/api/throttles")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var list struct {
		Channels []struct {
			Name        string `json:"name"`
			UpKB        int64  `json:"upKB"`
			DownKB      int64  `json:"downKB"`
			UpRateBps   int64  `json:"upRateBps"`
			DownRateBps int64  `json:"downRateBps"`
			InUse       int    `json:"inUse"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Channels) != 1 || list.Channels[0].UpKB != 100 || list.Channels[0].DownKB != 500 {
		t.Fatalf("channels = %+v", list.Channels)
	}

	// 3. Assign the downloading torrent (exercises stop/set/start).
	resp, body = postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{
		"action": "set_throttle", "hashes": []string{"aaaa1111aaaa1111"}, "throttle": "slow",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign = %d %+v", resp.StatusCode, body)
	}
	if results, ok := body["results"].([]any); ok {
		if m, ok := results[0].(map[string]any); !ok || m["ok"] != true {
			t.Fatalf("assign result = %+v", body)
		}
	}
	for _, want := range []string{"d.stop", "d.throttle_name.set", "d.open", "d.start"} {
		if !hasDaemonCall(st, want) {
			t.Fatalf("stop/set/start cycle missing %s: %v", want, st.daemon.CallMethods())
		}
	}
	waitForThrottle(t, st, "aaaa1111aaaa1111", "slow")

	// History recorded the assignment.
	entries := st.srv.history.ForHash("aaaa1111aaaa1111")
	seen := false
	for _, e := range entries {
		if e.Action == "set_throttle" && e.Result == "ok" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("history = %+v", entries)
	}

	// 4. Aggregates and listing reflect usage.
	if got := st.p.Snapshot().Aggregates.Throttles["slow"]; got != 1 {
		t.Fatalf("aggregate throttles = %+v", st.p.Snapshot().Aggregates.Throttles)
	}

	// 5. Removing the channel while assigned is refused with a count.
	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"throttles": []any{}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("removal status = %d %+v", resp.StatusCode, body)
	}
	if msg := errorMessage(body); msg == "" || !strings.Contains(msg, `"slow"`) || !strings.Contains(msg, "1 torrent") {
		t.Fatalf("refusal message = %+v", body)
	}
	if len(st.store.Get().Tuning.Throttles) != 1 {
		t.Fatal("refused removal still persisted")
	}

	// 6. Unassign, then removal succeeds and neutralizes the daemon channel.
	resp, body = postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{
		"action": "set_throttle", "hashes": []string{"aaaa1111aaaa1111"}, "throttle": "",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unassign = %d %+v", resp.StatusCode, body)
	}
	waitForThrottle(t, st, "aaaa1111aaaa1111", "")
	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"throttles": []any{}},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("removal = %d %+v", resp.StatusCode, body)
	}
	if len(st.store.Get().Tuning.Throttles) != 0 {
		t.Fatal("removal did not persist")
	}
}

// TestThrottleSettingsValidation asserts bad channel definitions are
// rejected before touching the daemon.
func TestThrottleSettingsValidation(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	resp, body := postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"throttles": []map[string]any{
			{"name": "NULL", "up_kb": 10},
		}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d %+v", resp.StatusCode, body)
	}

	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"throttles": []map[string]any{
			{"name": "dup", "up_kb": 10},
			{"name": "dup", "up_kb": 20},
		}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d %+v", resp.StatusCode, body)
	}
}

func errorMessage(body map[string]any) string {
	if errObj, ok := body["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
	}
	return ""
}
