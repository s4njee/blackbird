package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/fakertorrent"
)

func postJSON(t *testing.T, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return resp, out
}

func TestSessionEndpoint(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	resp, err := http.Get(ts.URL + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var snap struct {
		Status   string `json:"status"`
		Torrents []struct {
			Hash        string  `json:"hash"`
			Name        string  `json:"name"`
			State       string  `json:"state"`
			Percent     float64 `json:"percent"`
			TrackerHost string  `json:"trackerHost"`
			Label       string  `json:"label"`
		} `json:"torrents"`
		Aggregates struct {
			Status map[string]int `json:"status"`
		} `json:"aggregates"`
		Global struct {
			Version string `json:"version"`
		} `json:"global"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Status != "connected" || len(snap.Torrents) != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Torrents[0].State != "downloading" || snap.Torrents[0].TrackerHost != "torrent.ubuntu.com" {
		t.Fatalf("first torrent = %+v", snap.Torrents[0])
	}
	if snap.Aggregates.Status["downloading"] != 1 || snap.Aggregates.Status["seeding"] != 1 || snap.Aggregates.Status["error"] != 1 {
		t.Fatalf("aggregates = %+v", snap.Aggregates)
	}
	if snap.Global.Version != "0.15.4" {
		t.Fatalf("version = %q", snap.Global.Version)
	}
}

func TestActionBatchPerHashResults(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	// The fake daemon acks everything; per-hash results all OK.
	resp, body := postJSON(t, ts.URL+"/api/torrents/action", map[string]any{
		"action": "start",
		"hashes": []string{"h1", "h2", "h3"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	results := body["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	for i, r := range results {
		got := r.(map[string]any)
		if got["ok"] != true {
			t.Errorf("result %d = %+v", i, got)
		}
	}

	// Unknown action → 400 structured error.
	resp, body = postJSON(t, ts.URL+"/api/torrents/action", map[string]any{
		"action": "detonate",
		"hashes": []string{"h1"},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "bad_request" || errObj["message"] == "" {
		t.Fatalf("error envelope = %+v", errObj)
	}

	// Empty hashes → 400.
	resp, _ = postJSON(t, ts.URL+"/api/torrents/action", map[string]any{"action": "start"})
	if resp.StatusCode != 400 {
		t.Fatalf("empty hashes status = %d", resp.StatusCode)
	}
}

func TestDetailActions(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	for _, body := range []map[string]any{
		{"action": "file_priority", "hashes": []string{"aaaa1111aaaa1111"}, "fileIndex": 0, "priority": 2},
		{"action": "tracker_add", "hashes": []string{"aaaa1111aaaa1111"}, "trackerUrl": "https://tracker.example/announce"},
		{"action": "tracker_enable", "hashes": []string{"aaaa1111aaaa1111"}, "trackerIndex": 0, "enabled": false},
		{"action": "reannounce", "hashes": []string{"aaaa1111aaaa1111"}},
		{"action": "peer_ban", "hashes": []string{"aaaa1111aaaa1111"}, "peerId": "peer1xyz"},
		{"action": "peer_snub", "hashes": []string{"aaaa1111aaaa1111"}, "peerId": "peer1xyz"},
		{"action": "peer_unsnub", "hashes": []string{"aaaa1111aaaa1111"}, "peerId": "peer1xyz"},
		{"action": "peer_disconnect", "hashes": []string{"aaaa1111aaaa1111"}, "peerId": "peer1xyz"},
	} {
		resp, out := postJSON(t, ts.URL+"/api/torrents/action", body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("action %q status = %d: %+v", body["action"], resp.StatusCode, out)
		}
		if results, ok := out["results"].([]any); !ok || len(results) != 1 || !results[0].(map[string]any)["ok"].(bool) {
			t.Fatalf("action %q results = %+v", body["action"], out)
		}
	}

	resp, _ := postJSON(t, ts.URL+"/api/torrents/action", map[string]any{
		"action": "file_priority", "hashes": []string{"aaaa1111aaaa1111"}, "priority": 2,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("incomplete file_priority status = %d, want 400", resp.StatusCode)
	}

	// Peer moderation requires a peerId.
	resp, out := postJSON(t, ts.URL+"/api/torrents/action", map[string]any{
		"action": "peer_ban", "hashes": []string{"aaaa1111aaaa1111"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing peerId status = %d: %+v", resp.StatusCode, out)
	}
}

func TestRemoveWithDataRefusesOutsideDirs(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	// Fake daemon reports base_path /etc — outside /mnt/data.
	resp, body := postJSON(t, ts.URL+"/api/torrents/action", map[string]any{
		"action": "remove_with_data",
		"hashes": []string{"cccc3333cccc3333"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	results := body["results"].([]any)
	r := results[0].(map[string]any)
	if r["ok"] != false {
		t.Fatalf("expected refusal: %+v", r)
	}
	if !strings.Contains(r["error"].(string), "outside the configured download directories") {
		t.Fatalf("error = %+v", r["error"])
	}
}

func TestAddTorrentEndpoint(t *testing.T) {
	ts, _ := newTestAPI(t, "")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("magnets", "magnet:?xt=urn:btih:abc123\nnot-a-magnet\nhttps://example.com/x.torrent\nhttps://example.com/page.html")
	fw, _ := mw.CreateFormFile("files", "test.torrent")
	fw.Write([]byte("d4:infod4:name4:testee"))
	fw2, _ := mw.CreateFormFile("files", "wrong.txt")
	fw2.Write([]byte("nope"))
	mw.WriteField("destination", "/mnt/data/iso")
	mw.WriteField("label", "iso")
	mw.WriteField("start", "true")
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/torrents/add", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			Source string `json:"source"`
			OK     bool   `json:"ok"`
			Error  string `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 6 {
		t.Fatalf("results = %+v", body.Results)
	}
	// Order: 4 magnet lines then 2 files.
	if !body.Results[0].OK {
		t.Errorf("valid magnet rejected: %+v", body.Results[0])
	}
	if body.Results[1].OK || !strings.Contains(body.Results[1].Error, "magnet") {
		t.Errorf("invalid magnet accepted: %+v", body.Results[1])
	}
	if !body.Results[2].OK {
		t.Errorf("torrent URL rejected: %+v", body.Results[2])
	}
	if body.Results[3].OK || !strings.Contains(body.Results[3].Error, ".torrent") {
		t.Errorf("non-torrent URL accepted: %+v", body.Results[3])
	}
	if !body.Results[4].OK {
		t.Errorf("torrent file rejected: %+v", body.Results[4])
	}
	if body.Results[5].OK || !strings.Contains(body.Results[5].Error, ".torrent") {
		t.Errorf("non-torrent file accepted: %+v", body.Results[5])
	}
}

func TestDetailEndpoint(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	resp, err := http.Get(ts.URL + "/api/torrents/aaaa1111aaaa1111")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d struct {
		Hash  string `json:"hash"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		Peers []struct {
			Address string `json:"address"`
		} `json:"peers"`
		Trackers []struct {
			URL string `json:"url"`
		} `json:"trackers"`
		Transfer struct {
			DownloadedBytes int64 `json:"downloadedBytes"`
		} `json:"transfer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Hash != "aaaa1111aaaa1111" || len(d.Files) == 0 {
		t.Fatalf("detail = %+v", d)
	}
}

func TestLoggerViewRecordsActions(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	// Perform an action, then read the logger view for that hash.
	postJSON(t, ts.URL+"/api/torrents/action", map[string]any{
		"action": "start", "hashes": []string{"aaaa1111aaaa1111"},
	})
	resp, err := http.Get(ts.URL + "/api/torrents/aaaa1111aaaa1111?view=logger")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var lr struct {
		Hash    string `json:"hash"`
		Entries []struct {
			Kind   string `json:"kind"`
			Actor  string `json:"actor"`
			Action string `json:"action"`
			Result string `json:"result"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	if lr.Hash != "aaaa1111aaaa1111" {
		t.Fatalf("hash = %q", lr.Hash)
	}
	found := false
	for _, e := range lr.Entries {
		if e.Kind == "action" && e.Action == "start" && e.Actor == "local" && e.Result == "ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("start action not logged: %+v", lr.Entries)
	}
}

func TestSpeedViewReturnsSamples(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	st.p.Focus("aaaa1111aaaa1111")

	// Wait for a few poll cycles to sample the focused ring.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(st.ts.URL + "/api/torrents/aaaa1111aaaa1111?view=speed")
		if err != nil {
			t.Fatal(err)
		}
		var sr struct {
			Hash    string `json:"hash"`
			Samples []struct {
				At       string `json:"at"`
				DownRate int64  `json:"downRate"`
				UpRate   int64  `json:"upRate"`
			} `json:"samples"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&sr)
		resp.Body.Close()
		if decErr != nil {
			t.Fatal(decErr)
		}
		if len(sr.Samples) >= 2 {
			if sr.Hash != "aaaa1111aaaa1111" || sr.Samples[len(sr.Samples)-1].At == "" {
				t.Fatalf("speed = %+v", sr)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("speed samples never accumulated for focused hash")
}

func TestGeneralViewIncludesMetaFromAdd(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	ts := st.ts

	// Add a .torrent with a comment + created-by; metadata is captured by hash.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "commented.torrent")
	// Bencode: d 8:comment 12:Hello world 10:created by 13:SomeTool 1.0 4:info d4:name1:x 6:length i1e e e
	fw.Write([]byte("d8:comment12:Hello world10:created by13:SomeTool 1.04:infod4:name1:x6:lengthi1eee"))
	mw.Close()
	postResp, err := http.Post(ts.URL+"/api/torrents/add", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

	// General view for a focused torrent: comment metadata should surface if
	// the add correlated to a hash; use the fixture hash for the session row.
	resp, err := http.Get(ts.URL + "/api/torrents/aaaa1111aaaa1111?view=general")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var gr struct {
		Facts []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		} `json:"facts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		t.Fatal(err)
	}
	if len(gr.Facts) == 0 {
		t.Fatal("general facts empty")
	}
}

func TestSettingsGetAndSave(t *testing.T) {
	ts, _ := newTestAPI(t, "")

	// GET includes tuning + live daemon values.
	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var get struct {
		Tuning map[string]any    `json:"tuning"`
		Daemon map[string]string `json:"daemon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&get); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if get.Daemon["dht.mode"] != "auto" || get.Daemon["network.port"] == "" && get.Daemon["dht.port"] == "" {
		t.Fatalf("daemon values = %+v", get.Daemon)
	}

	// SAVE applies changed keys and persists.
	upRate := float64(10240)
	saveBody := map[string]any{
		"tuning": map[string]any{
			"global_up_rate_kb": upRate,
		},
	}
	resp, body := postJSON(t, ts.URL+"/api/settings", saveBody)
	if resp.StatusCode != 200 {
		t.Fatalf("save status = %d body=%v", resp.StatusCode, body)
	}
	if body["saved"] != true {
		t.Fatalf("save response = %+v", body)
	}

	// Invalid tuning → 400 validation error naming the key.
	resp, body = postJSON(t, ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"dht_mode": "sometimes"},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("invalid tuning status = %d", resp.StatusCode)
	}
	if !strings.Contains(body["error"].(map[string]any)["message"].(string), "dht_mode") {
		t.Fatalf("validation error = %+v", body)
	}
}

func TestStatsEndpoint(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)
	time.Sleep(30 * time.Millisecond) // let a couple of samples accumulate

	resp, err := http.Get(ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		Cards struct {
			Download struct {
				Value string `json:"value"`
			} `json:"download"`
			Torrents struct {
				Value  string `json:"value"`
				Detail string `json:"detail"`
			} `json:"torrents"`
		} `json:"cards"`
		Volumes []struct {
			Path string `json:"path"`
		} `json:"volumes"`
		LabelUsage []struct {
			Label     string `json:"label"`
			SizeBytes int64  `json:"sizeBytes"`
		} `json:"labelUsage"`
		History []struct {
			DownRate int64 `json:"downRate"`
		} `json:"history"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.Cards.Torrents.Value != "3" {
		t.Fatalf("torrents card = %+v", stats.Cards.Torrents)
	}
	if len(stats.Volumes) == 0 || len(stats.History) == 0 {
		t.Fatalf("volumes/history missing: %+v", stats)
	}
	// Space by label: kernel (150 GB) should be the largest.
	if len(stats.LabelUsage) == 0 || stats.LabelUsage[0].Label != "kernel" {
		t.Fatalf("labelUsage = %+v", stats.LabelUsage)
	}
	_ = io.Discard
}

// TestActionBatchEveryAction drives every transport/management action through
// the batch endpoint and asserts per-hash results, plus the input validation
// guardrails for priority and move_data.
func TestActionBatchEveryAction(t *testing.T) {
	ts, _ := newTestAPI(t, "")

	cases := []map[string]any{
		{"action": "start"},
		{"action": "force_start"},
		{"action": "pause"},
		{"action": "stop"},
		{"action": "recheck"},
		{"action": "remove"},
		{"action": "set_label", "label": "iso"},
		{"action": "priority", "priority": 3},
		{"action": "superseed", "enabled": true},
		{"action": "sequential", "enabled": true},
		{"action": "save_session"},
		{"action": "set_custom", "customField": "custom3", "customValue": "release"},
	}
	for _, c := range cases {
		body := map[string]any{"hashes": []string{"h1", "h2"}}
		for k, v := range c {
			body[k] = v
		}
		resp, out := postJSON(t, ts.URL+"/api/torrents/action", body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%v: status = %d body=%v", c, resp.StatusCode, out)
		}
		results := out["results"].([]any)
		if len(results) != 2 {
			t.Fatalf("%v: results = %+v", c, results)
		}
		for i, r := range results {
			got := r.(map[string]any)
			if got["ok"] != true {
				t.Errorf("%v result %d = %+v", c, i, got)
			}
		}
	}

	// priority requires 0-3.
	for _, pr := range []any{nil, 5, -1} {
		b := map[string]any{"action": "priority", "hashes": []string{"h1"}}
		if pr != nil {
			b["priority"] = pr
		}
		resp, _ := postJSON(t, ts.URL+"/api/torrents/action", b)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("priority=%v status = %d, want 400", pr, resp.StatusCode)
		}
	}

	for _, action := range []string{"superseed", "sequential"} {
		resp, _ := postJSON(t, ts.URL+"/api/torrents/action", map[string]any{"action": action, "hashes": []string{"h1"}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s without enabled status = %d, want 400", action, resp.StatusCode)
		}
	}
	resp, _ := postJSON(t, ts.URL+"/api/torrents/action", map[string]any{"action": "set_custom", "hashes": []string{"h1"}, "customField": "custom1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("set_custom custom1 status = %d, want 400", resp.StatusCode)
	}

	// move_data requires a destination.
	resp, _ = postJSON(t, ts.URL+"/api/torrents/action", map[string]any{"action": "move_data", "hashes": []string{"h1"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("move_data without destination status = %d, want 400", resp.StatusCode)
	}
}

func TestExtendedActionsReachRTorrent(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	for _, body := range []map[string]any{
		{"action": "force_start", "hashes": []string{"h1"}},
		{"action": "superseed", "hashes": []string{"h1"}, "enabled": true},
		{"action": "sequential", "hashes": []string{"h1"}, "enabled": true},
		{"action": "save_session", "hashes": []string{"h1"}},
		{"action": "set_custom", "hashes": []string{"h1"}, "customField": "custom5", "customValue": "note"},
	} {
		resp, out := postJSON(t, st.ts.URL+"/api/torrents/action", body)
		if resp.StatusCode != http.StatusOK || out["results"].([]any)[0].(map[string]any)["ok"] != true {
			t.Fatalf("action %v failed: status=%d body=%v", body["action"], resp.StatusCode, out)
		}
	}
	seen := map[string]bool{}
	for _, method := range st.daemon.CallMethods() {
		seen[method] = true
	}
	for _, method := range []string{"d.open", "d.start", "d.resume", "d.connection_seed.set", "d.sequential.set", "d.save_full_session", "d.custom5.set"} {
		if !seen[method] {
			t.Errorf("fakertorrent did not receive %q; calls=%v", method, st.daemon.CallMethods())
		}
	}
}

// TestActionBatchPartialFaultSurfaces scripts the fake daemon to fault one
// hash in a batch and asserts the per-hash failure is reported with the
// rtorrent fault message while the others succeed.
func TestActionBatchPartialFaultSurfaces(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{Fail: &fakertorrent.Failure{
		Method:  "d.start",
		Hashes:  []string{"h2"},
		Message: "no such torrent",
	}})
	waitForConnected(t, st.p)

	resp, body := postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{
		"action": "start",
		"hashes": []string{"h1", "h2", "h3"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%v", resp.StatusCode, body)
	}
	results := body["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].(map[string]any)["ok"] != true {
		t.Errorf("h1 should succeed: %+v", results[0])
	}
	h2 := results[1].(map[string]any)
	if h2["ok"] != false || !strings.Contains(h2["error"].(string), "no such torrent") {
		t.Errorf("h2 should surface the fault: %+v", h2)
	}
	if results[2].(map[string]any)["ok"] != true {
		t.Errorf("h3 should succeed: %+v", results[2])
	}
}

// TestMoveDataGuardrails checks the destination safety boundary. PAR-2.2
// stops and restarts active torrents automatically, so callers no longer need
// to stop them before requesting a move.
func TestMoveDataGuardrails(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{IncludeStopped: true})
	waitForConnected(t, st.p)

	do := func(hash, dest string) map[string]any {
		t.Helper()
		resp, body := postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{
			"action":      "move_data",
			"hashes":      []string{hash},
			"destination": dest,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s -> %s: status = %d body=%v", hash, dest, resp.StatusCode, body)
		}
		return body["results"].([]any)[0].(map[string]any)
	}

	// A destination outside configured dirs is refused before filesystem work.
	if r := do("dddd4444dddd4444", "/etc"); r["ok"] != false ||
		!strings.Contains(r["error"].(string), "outside the configured download directories") {
		t.Errorf("outside-dir move = %+v", r)
	}
}

// TestAddEndpointNonMultipartAndOptions covers the request-shape guardrails
// and the option fields (start, hash-check, sequential, destination, label).
func TestAddEndpointNonMultipartAndOptions(t *testing.T) {
	ts, _ := newTestAPI(t, "")

	// A non-multipart body is a structured 400.
	resp, err := http.Post(ts.URL+"/api/torrents/add", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-multipart status = %d", resp.StatusCode)
	}
	if errObj := out["error"].(map[string]any); errObj["code"] != "bad_request" || errObj["message"] == "" {
		t.Fatalf("error envelope = %+v", errObj)
	}

	// Options + an uppercase .TORRENT extension are all accepted.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("magnets", "magnet:?xt=urn:btih:abcdef12")
	mw.WriteField("destination", "/mnt/data/linux")
	mw.WriteField("label", "linux")
	mw.WriteField("start", "false")
	mw.WriteField("skip_hash_check", "true")
	mw.WriteField("sequential", "true")
	fw, _ := mw.CreateFormFile("files", "debian.TORRENT")
	fw.Write([]byte("d4:infod4:name6:debianee"))
	mw.Close()

	resp, err = http.Post(ts.URL+"/api/torrents/add", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []addItemResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 2 {
		t.Fatalf("results = %+v", body.Results)
	}
	for i, r := range body.Results {
		if !r.OK {
			t.Errorf("item %d should succeed: %+v", i, r)
		}
	}
}

// TestSettingsSavePerKeyResults verifies save applies only changed keys and
// reports per-key outcomes (here: faults from the daemon, passed through with
// their message for the Settings UI).
func TestSettingsSavePerKeyResults(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{Fail: &fakertorrent.Failure{
		Method:  "throttle.global_up.max_rate.set",
		Message: "configured setter fault",
	}})
	waitForConnected(t, st.p)
	ts := st.ts
	up := float64(20480)
	payload := map[string]any{"tuning": map[string]any{"global_up_rate_kb": up}}

	resp, body := postJSON(t, ts.URL+"/api/settings", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d body=%v", resp.StatusCode, body)
	}
	if body["saved"] != true {
		t.Fatalf("save response = %+v", body)
	}
	results := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 changed key, got %+v", results)
	}
	first := results[0].(map[string]any)
	if first["key"] != "throttle.global_up.max_rate" {
		t.Fatalf("result = %+v", first)
	}
	// The daemon's per-key setter fault must reach the UI.
	errEntry, ok := first["error"].(string)
	if !ok {
		t.Fatalf("missing per-key error: %+v", first)
	}
	if !strings.Contains(errEntry, "configured setter fault") {
		t.Fatalf("fault message not passed through: %+v", errEntry)
	}

	// Saving the identical value changes nothing: zero keys applied.
	resp, body = postJSON(t, ts.URL+"/api/settings", payload)
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("resave status = %d body=%v", resp.StatusCode, body)
	}
	// No changed keys → results is empty (nil slice serializes as null).
	var applied int
	if results, ok := body["results"].([]any); ok {
		applied = len(results)
	}
	if applied != 0 {
		t.Fatalf("no-op save applied keys: %+v", body)
	}
}

func TestSettingsSaveYAMLBackedSectionsAndExecute(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	payload := map[string]any{
		"tuning":      map[string]any{},
		"directories": map[string]any{"default": "/mnt/data/downloads", "per_label": map[string]string{"iso": "/mnt/data/iso"}, "watch": []map[string]any{{"path": "/mnt/watch", "label": "iso", "delete_after_load": true}}, "session": "/mnt/session"},
		"labels":      []map[string]string{{"name": "iso", "color": "#f59e0b"}},
		"ui":          map[string]any{"accent": "#2f9dff", "visible_columns": []string{"name", "status"}, "sort": map[string]string{"column": "name", "dir": "asc"}},
	}
	resp, body := postJSON(t, st.ts.URL+"/api/settings", payload)
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save YAML settings = %d %+v", resp.StatusCode, body)
	}
	got := st.store.Get()
	watch := got.Directories.Watch
	if len(watch) != 1 || watch[0].Path != "/mnt/watch" || watch[0].Label != "iso" || !watch[0].DeleteAfterLoad || !watch[0].Starts() ||
		got.Directories.PerLabel["iso"] != "/mnt/data/iso" || got.UI.Accent != "#2f9dff" || len(got.Labels) != 1 {
		t.Fatalf("settings were not persisted: %+v", got)
	}
	// Raw XML-RPC is opt-in: refused until the deployment enables it.
	resp, _ = postJSON(t, st.ts.URL+"/api/settings/execute", map[string]any{"method": "d.tracker_announce"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("execute before opt-in = %d, want 403", resp.StatusCode)
	}
	st.store.cfg.Server.AllowExecute = true

	resp, body = postJSON(t, st.ts.URL+"/api/settings/execute", map[string]any{"method": "d.tracker_announce", "params": []string{"aaaa1111aaaa1111"}})
	if resp.StatusCode != http.StatusOK || body["ok"] != true {
		t.Fatalf("execute = %d %+v", resp.StatusCode, body)
	}
	resp, _ = postJSON(t, st.ts.URL+"/api/settings/execute", map[string]any{"method": "bad method"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid execute status = %d, want 400", resp.StatusCode)
	}
}

// TestExecuteDeniesCommandRunningMethods covers the families that stay
// blocked even with server.allow_execute on. The escape hatch exists to
// reach daemon settings; execute2 is a shell. Keeping these out means a
// future gap in the auth or origin checks cannot escalate to running
// commands on the daemon host.
func TestExecuteDeniesCommandRunningMethods(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	st.store.cfg.Server.AllowExecute = true

	for _, method := range []string{
		"execute", "execute2", "execute.throw", "execute.capture",
		"system.method.set", "system.method.insert",
		"import", "try_import", "schedule2",
		"EXECUTE2", // the check is case-insensitive
	} {
		resp, body := postJSON(t, st.ts.URL+"/api/settings/execute", map[string]any{
			"method": method, "params": []string{"", "rm -rf /"},
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403 (body %+v)", method, resp.StatusCode, body)
		}
	}

	// An ordinary daemon method still works.
	resp, body := postJSON(t, st.ts.URL+"/api/settings/execute", map[string]any{
		"method": "d.tracker_announce", "params": []string{"aaaa1111aaaa1111"},
	})
	if resp.StatusCode != http.StatusOK || body["ok"] != true {
		t.Fatalf("ordinary method refused: %d %+v", resp.StatusCode, body)
	}
}

// TestStatsLabelUsageBuckets asserts space-by-label aggregation: labels
// summed, sorted descending, and the unlabeled bucket included.
func TestStatsLabelUsageBuckets(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	resp, err := http.Get(ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		LabelUsage []struct {
			Label     string `json:"label"`
			SizeBytes int64  `json:"sizeBytes"`
			Count     int    `json:"count"`
		} `json:"labelUsage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.LabelUsage) != 3 {
		t.Fatalf("labelUsage = %+v", stats.LabelUsage)
	}
	if stats.LabelUsage[0].Label != "kernel" { // 150 GB, largest
		t.Fatalf("largest bucket = %+v", stats.LabelUsage[0])
	}
	for i := 1; i < len(stats.LabelUsage); i++ {
		if stats.LabelUsage[i].SizeBytes > stats.LabelUsage[i-1].SizeBytes {
			t.Fatalf("not sorted desc: %+v", stats.LabelUsage)
		}
	}
	// The broken torrent has no label → unlabeled bucket with count 1.
	if last := stats.LabelUsage[len(stats.LabelUsage)-1]; last.Label != "unlabeled" || last.Count != 1 {
		t.Fatalf("unlabeled bucket = %+v", last)
	}
}

func TestRenameActionAndCapabilities(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	// The fake daemon claims rename support and acks d.name.set.
	resp, body := postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{
		"action": "rename", "hashes": []string{"aaaa1111aaaa1111"}, "name": "Renamed ISO",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename status = %d: %+v", resp.StatusCode, body)
	}
	results := body["results"].([]any)
	if !results[0].(map[string]any)["ok"].(bool) {
		t.Fatalf("rename result = %+v", results[0])
	}

	// Missing name → 400.
	resp, _ = postJSON(t, st.ts.URL+"/api/torrents/action", map[string]any{
		"action": "rename", "hashes": []string{"aaaa1111aaaa1111"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("rename without name status = %d, want 400", resp.StatusCode)
	}

	// Capabilities surface rename availability in the settings payload.
	resp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var settings struct {
		Capabilities struct {
			Rename bool `json:"rename"`
		} `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if !settings.Capabilities.Rename {
		t.Fatal("capabilities.rename should be true against the fake daemon")
	}
}

// TestTorrentFileRejectsPathTraversal covers the session-directory escape:
// ServeMux unescapes the {hash} wildcard, so "%2f" reaches the handler as a
// real separator. Only hex infohashes may reach filepath.Join.
func TestTorrentFileRejectsPathTraversal(t *testing.T) {
	base := t.TempDir()
	sessionDir := filepath.Join(base, "session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := []byte("TOP-SECRET-NOT-A-TORRENT")
	if err := os.WriteFile(filepath.Join(base, "secret.torrent"), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	st.store.cfg.Directories.Session = sessionDir

	for _, escape := range []string{"..%2fsecret", "%2e%2e%2fsecret", "..%2F..%2Fsecret"} {
		resp, err := http.Get(st.ts.URL + "/api/torrent-file/" + escape)
		if err != nil {
			t.Fatalf("%s: %v", escape, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("%s: served %d, expected refusal", escape, resp.StatusCode)
		}
		if strings.Contains(string(body), "TOP-SECRET") {
			t.Fatalf("%s: leaked a file outside the session directory", escape)
		}
	}
}

func TestTorrentFileDownload(t *testing.T) {
	// Point the store's session dir at a temp dir with a fixture .torrent.
	dir := t.TempDir()
	hash := "aaaa1111aaaa1111"
	content := []byte("d4:infod4:name1:xe")
	if err := os.WriteFile(filepath.Join(dir, strings.ToUpper(hash)+".torrent"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	st.store.cfg.Directories.Session = dir

	resp, err := http.Get(st.ts.URL + "/api/torrent-file/" + hash)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-bittorrent" {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("content-disposition = %q", cd)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("body = %q, want %q", got, content)
	}

	// Unknown hash → clean 404 without exposing the session dir.
	resp2, err := http.Get(st.ts.URL + "/api/torrent-file/deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("missing torrent status = %d, want 404", resp2.StatusCode)
	}
}

func TestOpenDirectoryURL(t *testing.T) {
	base := "/mnt/data/iso/My File.iso"
	// Template with {path} placeholder.
	got := openDirectoryURL("https://files.example.com/browse?path={path}", base)
	want := "https://files.example.com/browse?path=" + url.QueryEscape(base)
	if got != want {
		t.Fatalf("template URL = %q, want %q", got, want)
	}
	// No template → "" (copy path instead).
	if openDirectoryURL("", base) != "" {
		t.Fatal("empty template should return empty URL")
	}
	// Template without placeholder appends the escaped path.
	got = openDirectoryURL("https://files.example.com/open/", base)
	if got != "https://files.example.com/open/"+url.QueryEscape(base) {
		t.Fatalf("append URL = %q", got)
	}
}

// TestSettingsSaveWithoutTuningKeepsTuning covers a partial settings body.
// "tuning" is optional like every other section, so omitting it must leave
// the persisted tuning keys alone. It used to be a value type assigned
// unconditionally, so any partial save zeroed every tuning key on disk —
// and because the resulting diff was empty, the daemon kept running the old
// values and the loss stayed invisible until the next restart.
func TestSettingsSaveWithoutTuningKeepsTuning(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)
	ts := st.ts

	up := float64(20480)
	resp, body := postJSON(t, ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{"global_up_rate_kb": up},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("seed save: status=%d body=%v", resp.StatusCode, body)
	}

	// A body that omits "tuning" entirely.
	resp, body = postJSON(t, ts.URL+"/api/settings", map[string]any{
		"ui": map[string]any{"theme": "light"},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("partial save: status=%d body=%v", resp.StatusCode, body)
	}

	getResp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var get struct {
		Tuning map[string]any `json:"tuning"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&get); err != nil {
		t.Fatal(err)
	}
	getResp.Body.Close()
	if got := get.Tuning["global_up_rate_kb"]; got != up {
		t.Fatalf("partial save wiped tuning: global_up_rate_kb = %v, want %v", got, up)
	}
}
