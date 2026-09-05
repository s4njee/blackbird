package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/history"
	"blackbird/internal/rss"
	"blackbird/internal/rtorrent"
)

const rssTestTorrent = "d4:infod4:name1:x6:lengthi1eee"

// stubDaemon records RSS loads for the API tests.
type stubDaemon struct {
	mu    sync.Mutex
	files int
}

func (d *stubDaemon) SetLabel(_ context.Context, _, _ string) error { return nil }

func (d *stubDaemon) AddTorrentFile(_ context.Context, data []byte, _ rtorrent.AddOptions) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files++
	return nil
}

func (d *stubDaemon) AddMagnet(_ context.Context, _ string, _ rtorrent.AddOptions) error {
	return nil
}

func (d *stubDaemon) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.files
}

// rssTestFeed serves a two-item feed plus a valid enclosure.
func rssTestFeed(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Show.Name.S02E04.1080p</title><guid>api-item-1</guid>
<enclosure url="` + server.URL + `/dl/a.torrent" length="100" type="application/x-bittorrent" />
<category>TV</category></item>
<item><title>Unrelated</title><guid>api-item-2</guid>
<enclosure url="` + server.URL + `/dl/b.torrent" length="200" type="application/x-bittorrent" />
<category>Other</category></item>
</channel></rss>`))
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssTestTorrent))
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func newRSSTestServer(t *testing.T, feedURL string) (*httptest.Server, *rss.Service, *stubDaemon) {
	t.Helper()
	daemon := &stubDaemon{}
	svc := rss.New(rss.Options{
		Daemon:  daemon,
		History: history.New(history.Options{}),
		Feeds: func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "tv", URL: feedURL, Label: "tv", PollInterval: 50 * time.Millisecond}}
		},
		Filters: func() []config.RSSFilter {
			return []config.RSSFilter{{Name: "shows", Feed: "tv", TitleRegex: `S\d\dE\d\d`}}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)
	srv := New(Options{RSS: svc, History: history.New(history.Options{})}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return ts, svc, daemon
}

func waitForRSS(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func getRSSView(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/rss status = %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRSSViewEndpoint(t *testing.T) {
	feed := rssTestFeed(t)
	ts, svc, _ := newRSSTestServer(t, feed.URL+"/feed.xml")

	waitForRSS(t, "items stored", func() bool { return len(svc.Snapshot().Items) == 2 })
	body := getRSSView(t, ts.URL+"/api/rss")

	feeds, _ := body["feeds"].([]any)
	if len(feeds) != 1 {
		t.Fatalf("feeds = %+v", feeds)
	}
	feed0 := feeds[0].(map[string]any)
	if feed0["name"] != "tv" || int(feed0["items"].(float64)) != 2 {
		t.Fatalf("feed = %+v", feed0)
	}
	// The "shows" filter auto-loaded the matching item.
	if int(feed0["unread"].(float64)) != 1 {
		t.Fatalf("unread = %v, want 1", feed0["unread"])
	}

	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}

	filters, _ := body["filters"].([]any)
	if len(filters) != 1 {
		t.Fatalf("filters = %+v", filters)
	}
	filter0 := filters[0].(map[string]any)
	if filter0["name"] != "shows" || int(filter0["loaded"].(float64)) != 1 {
		t.Fatalf("filter = %+v", filter0)
	}
	history0, _ := filter0["history"].([]any)
	if len(history0) != 1 {
		t.Fatalf("filter history = %+v", history0)
	}
}

func TestRSSManualAddAndRead(t *testing.T) {
	feed := rssTestFeed(t)
	ts, svc, daemon := newRSSTestServer(t, feed.URL+"/feed.xml")

	waitForRSS(t, "items stored", func() bool { return len(svc.Snapshot().Items) == 2 })
	// The filter's auto-load runs on the feed-poll goroutine. Wait for it to
	// land before sampling the baseline: otherwise it can arrive between the
	// sample and the assertion below, and the manual add looks like it
	// loaded twice (or not at all).
	waitForRSS(t, "auto-load settled", func() bool { return daemon.count() == 1 })
	before := daemon.count()

	// The non-matching item loads manually with the feed defaults.
	resp, body := postJSON(t, ts.URL+"/api/rss/add", map[string]any{"feed": "tv", "id": "api-item-2"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d body = %+v", resp.StatusCode, body)
	}
	if body["hash"] == "" {
		t.Fatalf("add response = %+v", body)
	}
	if daemon.count() != before+1 {
		t.Fatal("manual add did not load")
	}

	// Unknown item / missing fields are clean 400s.
	resp, _ = postJSON(t, ts.URL+"/api/rss/add", map[string]any{"feed": "tv", "id": "nope"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown item status = %d", resp.StatusCode)
	}

	// Mark one read, then everything.
	resp, body = postJSON(t, ts.URL+"/api/rss/read", map[string]any{"feed": "tv", "ids": []string{"api-item-1"}})
	if resp.StatusCode != http.StatusOK || int(body["marked"].(float64)) != 1 {
		t.Fatalf("read = %d %+v", resp.StatusCode, body)
	}
	resp, body = postJSON(t, ts.URL+"/api/rss/read", map[string]any{"all": true})
	if resp.StatusCode != http.StatusOK || int(body["marked"].(float64)) != 1 {
		t.Fatalf("mark-all = %d %+v (one item already read)", resp.StatusCode, body)
	}
}

func TestRSSEndpointsUnavailableWithoutService(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	for _, target := range []string{"/api/rss"} {
		resp, err := http.Get(ts.URL + target)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET %s status = %d, want 503", target, resp.StatusCode)
		}
	}
	resp, _ := postJSON(t, ts.URL+"/api/rss/add", map[string]any{"feed": "tv", "id": "x"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST add status = %d, want 503", resp.StatusCode)
	}
}

// TestSettingsRedactsAndPreservesSecrets asserts feed credentials are masked
// in GET responses and preserved across saves that resubmit the mask.
func TestSettingsRedactsAndPreservesSecrets(t *testing.T) {
	st := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, st.p)

	seed := map[string]any{
		"tuning": map[string]any{},
		"automation": map[string]any{"rss": map[string]any{"feeds": []map[string]any{{
			"name": "secret-feed", "url": "https://tracker.example/rss?passkey=ABC",
			"cookies": "uid=7; pass=DEF",
			"headers": map[string]string{"Authorization": "Bearer TOKEN"},
		}}}},
	}
	resp, body := postJSON(t, st.ts.URL+"/api/settings", seed)
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("seed save = %d %+v", resp.StatusCode, body)
	}

	// GET masks the secrets.
	resp, err := http.Get(st.ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	feeds := settings["automation"].(map[string]any)["rss"].(map[string]any)["feeds"].([]any)
	feed0 := feeds[0].(map[string]any)
	if feed0["cookies"] != "***" {
		t.Fatalf("cookies not masked: %+v", feed0)
	}
	headers := feed0["headers"].(map[string]any)
	if headers["Authorization"] != "***" {
		t.Fatalf("headers not masked: %+v", headers)
	}

	// Resubmitting the masked form preserves the stored secrets.
	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{},
		"automation": map[string]any{"rss": map[string]any{"feeds": []map[string]any{{
			"name": "secret-feed", "url": "https://tracker.example/rss?passkey=ABC",
			"cookies": "***", "headers": map[string]string{"Authorization": "***"},
		}}}},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("mask save = %d %+v", resp.StatusCode, body)
	}
	stored := st.store.Get().Automation.Rss.Feeds[0]
	if stored.Cookies != "uid=7; pass=DEF" || stored.Headers["Authorization"] != "Bearer TOKEN" {
		t.Fatalf("secrets not preserved: %+v", stored)
	}

	// New values replace, empty values clear.
	resp, body = postJSON(t, st.ts.URL+"/api/settings", map[string]any{
		"tuning": map[string]any{},
		"automation": map[string]any{"rss": map[string]any{"feeds": []map[string]any{{
			"name": "secret-feed", "url": "https://tracker.example/rss?passkey=ABC",
			"cookies": "uid=9", "headers": map[string]string{},
		}}}},
	})
	if resp.StatusCode != http.StatusOK || body["saved"] != true {
		t.Fatalf("replace save = %d %+v", resp.StatusCode, body)
	}
	stored = st.store.Get().Automation.Rss.Feeds[0]
	if stored.Cookies != "uid=9" || len(stored.Headers) != 0 {
		t.Fatalf("secrets not replaced: %+v", stored)
	}

	// The stored passkey URL must stay intact through all of this.
	if !strings.Contains(stored.URL, "passkey=ABC") {
		t.Fatalf("feed URL mangled: %q", stored.URL)
	}
}
