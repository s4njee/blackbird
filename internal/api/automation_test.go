package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
)

// TestAutomationDryRun asserts the PAR-3.2 dry-run endpoint evaluates unsaved
// draft rules against the live snapshot with first-match-wins semantics.
func TestAutomationDryRun(t *testing.T) {
	ts, p := newTestAPI(t, "")
	waitForConnected(t, p)

	// Fixture torrents: aaaa = ubuntu iso (label "iso", 6.4 GB),
	// bbbb = linux tar.xz (label "kernel", 150 GB, complete),
	// cccc = broken torrent (no label, 1 MB).
	resp, body := postJSON(t, ts.URL+"/api/automation/dry-run", map[string]any{
		"rules": []map[string]any{
			{"name": "iso-rule", "label": "iso", "set_label": "done"},
			{"name": "huge", "min_size": 100000000000, "move_to": "/mnt/data/huge"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry-run status = %d body = %+v", resp.StatusCode, body)
	}
	matches, _ := body["matches"].([]any)
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want 2", matches)
	}
	first := matches[0].(map[string]any)
	if first["hash"] != "aaaa1111aaaa1111" || first["rule"] != "iso-rule" {
		t.Fatalf("first match = %+v", first)
	}
	second := matches[1].(map[string]any)
	if second["hash"] != "bbbb2222bbbb2222" || second["rule"] != "huge" {
		t.Fatalf("second match = %+v", second)
	}
	if int(body["unmatched"].(float64)) != 1 {
		t.Fatalf("unmatched = %v, want 1", body["unmatched"])
	}

	// No rules: every torrent is unmatched.
	resp, body = postJSON(t, ts.URL+"/api/automation/dry-run", map[string]any{"rules": []any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty dry-run status = %d", resp.StatusCode)
	}
	if got, _ := body["matches"].([]any); len(got) != 0 {
		t.Fatalf("matches with no rules = %+v", got)
	}
	if int(body["unmatched"].(float64)) != 3 {
		t.Fatalf("unmatched with no rules = %v, want 3", body["unmatched"])
	}
}

// TestSettingsSaveAutomationRoundTrip asserts the Automation section
// round-trips through the settings API with validation.
func TestSettingsSaveAutomationRoundTrip(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	rule := map[string]any{
		"name":        "tv",
		"label":       "tv",
		"name_regex":  `^Show\.S\d\d`,
		"min_size":    1,
		"max_size":    20000000000,
		"private":     false,
		"set_label":   "tv-done",
		"move_to":     "/mnt/data/tv",
		"add_tracker": "udp://tracker.example:1337/announce",
	}
	payload := map[string]any{
		"tuning":     map[string]any{},
		"automation": map[string]any{"on_complete": []map[string]any{rule}},
	}
	resp, body := postJSON(t, st.ts.URL+"/api/settings", payload)
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("save = %d %+v", resp.StatusCode, body)
	}
	got := st.store.Get()
	if len(got.Automation.OnComplete) != 1 {
		t.Fatalf("automation rules = %+v", got.Automation.OnComplete)
	}
	saved := got.Automation.OnComplete[0]
	if saved.Name != "tv" || saved.SetLabel != "tv-done" || saved.MoveTo != "/mnt/data/tv" || saved.AddTracker != "udp://tracker.example:1337/announce" {
		t.Fatalf("saved rule = %+v", saved)
	}
	if saved.Private == nil || *saved.Private != false || saved.MinSize != 1 || saved.MaxSize != 20000000000 {
		t.Fatalf("saved rule conditions = %+v", saved)
	}

	// GET advertises the section back.
	getResp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	var settings struct {
		Automation config.Automation `json:"automation"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Automation.OnComplete) != 1 || settings.Automation.OnComplete[0].Name != "tv" {
		t.Fatalf("GET automation = %+v", settings.Automation)
	}

	// Validation: no action, bad regex, min > max.
	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{},
		"automation": map[string]any{"on_complete": []map[string]any{
			{"name": "bad", "name_regex": "(", "min_size": 10, "max_size": 5},
		}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid rules status = %d body = %+v", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(map[string]any)["message"].(string); msg == "" {
		t.Fatalf("missing validation message: %+v", body)
	}
}
