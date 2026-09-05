package rss

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

// realTorrentFile is a minimal, parseable .torrent for a single file "x".
const realTorrentFile = "d4:infod4:name1:x6:lengthi1eee"

// recordingDaemon is a Daemon stub that records the loads it receives.
type recordingDaemon struct {
	mu      sync.Mutex
	files   []loadCall
	magnets []loadCall
	labels  map[string]string
	fail    map[string]string // enclosure key → load error message
}

type loadCall struct {
	data []byte
	uri  string
	opts rtorrent.AddOptions
}

type daemonErr struct{ msg string }

func (e *daemonErr) Error() string { return e.msg }

func (r *recordingDaemon) SetLabel(_ context.Context, hash, label string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.labels == nil {
		r.labels = map[string]string{}
	}
	r.labels[hash] = label
	return nil
}

func (r *recordingDaemon) AddTorrentFile(_ context.Context, data []byte, opts rtorrent.AddOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg, ok := r.fail[string(data)]; ok {
		return &daemonErr{msg}
	}
	r.files = append(r.files, loadCall{data: append([]byte(nil), data...), opts: opts})
	return nil
}

func (r *recordingDaemon) AddMagnet(_ context.Context, uri string, opts rtorrent.AddOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.magnets = append(r.magnets, loadCall{uri: uri, opts: opts})
	return nil
}

func (r *recordingDaemon) fileCalls() []loadCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]loadCall(nil), r.files...)
}

func (r *recordingDaemon) magnetCalls() []loadCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]loadCall(nil), r.magnets...)
}

// feedHarness serves a feed document and enclosures from httptest.
type feedHarness struct {
	mu       sync.Mutex
	feedBody string
	feedCode int
	feedHits int
	dlHits   int
	server   *httptest.Server
}

func newFeedHarness(t *testing.T, feedBody string) *feedHarness {
	t.Helper()
	h := &feedHarness{feedBody: feedBody, feedCode: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.feedHits++
		code, body := h.feedCode, h.feedBody
		h.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.dlHits++
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write([]byte(realTorrentFile))
	})
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func (h *feedHarness) hits() (feed, dl int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.feedHits, h.dlHits
}

func (h *feedHarness) setFeed(code int, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.feedCode, h.feedBody = code, body
}

func testFeedXML(serverURL string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Test</title>
<item><title>Show.Name.S02E04.1080p.WEB.h264-EXAMPLE</title>
<link>` + serverURL + `/t/1</link><guid>item-1</guid>
<enclosure url="` + serverURL + `/dl/show.torrent" length="73400320" type="application/x-bittorrent" />
<category>TV</category><pubDate>Tue, 02 Sep 2026 14:12:00 +0000</pubDate></item>
<item><title>Some.Other.Movie.2026.1080p.BluRay-EXAMPLE</title>
<link>` + serverURL + `/t/2</link><guid>item-2</guid>
<enclosure url="` + serverURL + `/dl/movie.torrent" length="8589934592" type="application/x-bittorrent" />
<category>Movies</category><pubDate>Tue, 02 Sep 2026 13:00:00 +0000</pubDate></item>
</channel></rss>`
}

func startService(t *testing.T, daemon *recordingDaemon, hist *history.Log, feeds func() []config.RSSFeed, filters func() []config.RSSFilter, snapshot func() []rtorrent.Torrent) *Service {
	t.Helper()
	if hist == nil {
		hist = history.New(history.Options{})
	}
	svc := New(Options{
		Log:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Daemon:   daemon,
		History:  hist,
		Snapshot: snapshot,
		Feeds:    feeds,
		Filters:  filters,
	})
	svc.reconcile()
	return svc
}

func waitFor(t *testing.T, what string, cond func() bool) {
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

// TestOnLoadedHookFiresOnAutoLoad proves the POL-8.3 notice hook: a filter
// match that loads reports feed, title, and infohash exactly once.
func TestOnLoadedHookFiresOnAutoLoad(t *testing.T) {
	h := newFeedHarness(t, "")
	h.setFeed(http.StatusOK, testFeedXML(h.server.URL))
	daemon := &recordingDaemon{}
	hist := history.New(history.Options{})
	start := false
	svc := startService(t, daemon, hist,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "tv", URL: h.server.URL + "/feed.xml", PollInterval: 50 * time.Millisecond}}
		},
		func() []config.RSSFilter {
			return []config.RSSFilter{{
				Name: "shows", Feed: "tv", TitleRegex: `\.S\d\dE\d\d\.`,
				Label: "tv", Destination: "/data/tv", Start: &start,
			}}
		},
		func() []rtorrent.Torrent { return nil },
	)
	type loaded struct{ feed, title, hash string }
	// The hook fires on the feed-poll goroutine while the assertions below
	// read from the test goroutine, so the recorded calls need a lock.
	var mu sync.Mutex
	var calls []loaded
	svc.SetOnLoaded(func(feed, title, hash string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, loaded{feed, title, hash})
	})

	svc.PollNow(context.Background())
	waitFor(t, "onLoaded hook", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if calls[0].feed != "tv" || calls[0].title == "" || calls[0].hash == "" {
		t.Fatalf("onLoaded = %+v", calls[0])
	}
}

// TestIngestReleasesLockWhenFeedRemoved covers a feed deleted from the config
// while its fetch was still in flight: ingest must drop the batch without
// leaking s.mu, or every later RSS operation blocks on it forever.
func TestIngestReleasesLockWhenFeedRemoved(t *testing.T) {
	svc := New(Options{
		Log:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Daemon:   &recordingDaemon{},
		Feeds:    func() []config.RSSFeed { return nil },
		Filters:  func() []config.RSSFilter { return nil },
		Snapshot: func() []rtorrent.Torrent { return nil },
	})
	svc.reconcile()

	// "tv" is absent from s.feeds, exactly as it would be had reconcile
	// dropped it between the fetch starting and ingest running.
	svc.ingest(config.RSSFeed{Name: "tv"}, []Item{{ID: "a", Title: "a"}})

	done := make(chan struct{})
	go func() { svc.Snapshot(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ingest returned holding s.mu: the RSS service is deadlocked")
	}
}

// TestFilterMatchLoadsIntoSession is the PAR-3.3 integration proof: a feed
// item matching a filter ends as a torrent load with the filter's
// label/destination/start options, recorded in history and match history.
func TestFilterMatchLoadsIntoSession(t *testing.T) {
	h := newFeedHarness(t, "")
	h.setFeed(http.StatusOK, testFeedXML(h.server.URL))
	daemon := &recordingDaemon{}
	hist := history.New(history.Options{})
	start := false
	svc := startService(t, daemon, hist,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "tv", URL: h.server.URL + "/feed.xml", PollInterval: 50 * time.Millisecond}}
		},
		func() []config.RSSFilter {
			return []config.RSSFilter{{
				Name: "shows", Feed: "tv", TitleRegex: `\.S\d\dE\d\d\.`,
				Label: "tv", Destination: "/data/tv", Start: &start,
			}}
		},
		func() []rtorrent.Torrent { return nil },
	)

	svc.PollNow(context.Background())
	waitFor(t, "matched enclosure loaded", func() bool { return len(daemon.fileCalls()) == 1 })

	calls := daemon.fileCalls()
	if string(calls[0].data) != realTorrentFile {
		t.Fatalf("loaded bytes = %q", calls[0].data)
	}
	cmds := calls[0].opts.ExtraCommands
	if len(cmds) != 2 || cmds[0] != "d.directory.set=/data/tv" || cmds[1] != "d.custom1.set=tv" {
		t.Fatalf("trailing commands = %+v", cmds)
	}
	if calls[0].opts.Start {
		t.Fatal("start=false was not honored")
	}

	// The load is recorded under the torrent's infohash with the rss actor,
	// so the Logger tab shows it.
	infohash := fixtureInfohash(t)
	logged := hist.ForHash(infohash)
	if len(logged) != 1 || logged[0].Actor != "rss" || logged[0].Result != "ok" {
		t.Fatalf("history = %+v", logged)
	}

	// The non-matching movie item was stored but never loaded.
	view := svc.Snapshot()
	if len(view.Items) != 2 {
		t.Fatalf("stored items = %d, want 2", len(view.Items))
	}
	var movie ItemView
	for _, item := range view.Items {
		if strings.Contains(item.Title, "Movie") {
			movie = item
		}
	}
	if movie.Loaded {
		t.Fatal("non-matching item was loaded")
	}

	// History and match history recorded the load.
	found := false
	for _, f := range svc.Snapshot().Filters {
		if f.Name == "shows" {
			if f.Evaluated != 1 || f.Matched != 1 || f.Loaded != 1 {
				t.Fatalf("filter counters = %+v", f)
			}
			if len(f.History) != 1 || f.History[0].Outcome != "loaded" {
				t.Fatalf("match history = %+v", f.History)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("filter state missing")
	}

	// A second poll re-fetches but never re-downloads or re-loads.
	_, dlBefore := h.hits()
	svc.PollNow(context.Background())
	time.Sleep(300 * time.Millisecond)
	if _, dlAfter := h.hits(); dlAfter != dlBefore {
		t.Fatalf("enclosure re-downloaded: %d → %d", dlBefore, dlAfter)
	}
	if len(daemon.fileCalls()) != 1 {
		t.Fatalf("item loaded %d times", len(daemon.fileCalls()))
	}
}

func TestDedupeByEnclosureAcrossGUIDs(t *testing.T) {
	h := newFeedHarness(t, "")
	body := `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Same File</title><guid>guid-a</guid>
<enclosure url="` + h.server.URL + `/dl/same.torrent" length="100" type="application/x-bittorrent" /></item>
<item><title>Same File Reposted</title><guid>guid-b</guid>
<enclosure url="` + h.server.URL + `/dl/same.torrent" length="100" type="application/x-bittorrent" /></item>
</channel></rss>`
	h.setFeed(http.StatusOK, body)
	daemon := &recordingDaemon{}
	svc := startService(t, daemon, nil,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "dup", URL: h.server.URL + "/feed.xml", PollInterval: 50 * time.Millisecond}}
		},
		func() []config.RSSFilter { return []config.RSSFilter{{Name: "all"}} },
		func() []rtorrent.Torrent { return nil },
	)

	svc.PollNow(context.Background())
	waitFor(t, "first load", func() bool { return len(daemon.fileCalls()) == 1 })
	time.Sleep(300 * time.Millisecond)
	if len(daemon.fileCalls()) != 1 {
		t.Fatalf("same enclosure loaded %d times", len(daemon.fileCalls()))
	}
}

func TestMagnetEnclosureLoads(t *testing.T) {
	h := newFeedHarness(t, "")
	h.setFeed(http.StatusOK, `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Magnet Item</title><guid>mag-1</guid>
<enclosure url="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" type="application/x-bittorrent" /></item>
</channel></rss>`)
	daemon := &recordingDaemon{}
	svc := startService(t, daemon, nil,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "mag", URL: h.server.URL + "/feed.xml", Label: "mag-default", PollInterval: 50 * time.Millisecond}}
		},
		func() []config.RSSFilter { return []config.RSSFilter{{Name: "all"}} },
		func() []rtorrent.Torrent { return nil },
	)

	svc.PollNow(context.Background())
	waitFor(t, "magnet loaded", func() bool { return len(daemon.magnetCalls()) == 1 })
	call := daemon.magnetCalls()[0]
	if !strings.HasPrefix(call.uri, "magnet:") {
		t.Fatalf("uri = %q", call.uri)
	}
	// No filter label: the feed default applies.
	found := false
	for _, cmd := range call.opts.ExtraCommands {
		if cmd == "d.custom1.set=mag-default" {
			found = true
		}
	}
	if !found {
		t.Fatalf("feed default label missing: %+v", call.opts.ExtraCommands)
	}
	if !call.opts.Start {
		t.Fatal("manual/auto default start should be true")
	}
}

func TestAlreadyInSessionSkipsLoad(t *testing.T) {
	h := newFeedHarness(t, "")
	h.setFeed(http.StatusOK, testFeedXML(h.server.URL))
	daemon := &recordingDaemon{}
	// The fixture torrent's infohash is already in the session.
	infohash := fixtureInfohash(t)
	svc := startService(t, daemon, nil,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "tv", URL: h.server.URL + "/feed.xml", PollInterval: 50 * time.Millisecond}}
		},
		func() []config.RSSFilter { return []config.RSSFilter{{Name: "all"}} },
		func() []rtorrent.Torrent {
			return []rtorrent.Torrent{{Hash: infohash}}
		},
	)
	svc.PollNow(context.Background())
	time.Sleep(400 * time.Millisecond)
	if len(daemon.fileCalls()) != 0 {
		t.Fatalf("already-seeded torrent loaded %d times", len(daemon.fileCalls()))
	}
	for _, f := range svc.Snapshot().Filters {
		if f.Name == "all" {
			if len(f.History) == 0 || f.History[0].Outcome != "already-in-session" {
				t.Fatalf("match history = %+v", f.History)
			}
		}
	}
}

func fixtureInfohash(t *testing.T) string {
	t.Helper()
	parsed, err := torrentfile.Parse([]byte(realTorrentFile))
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Infohash
}

func TestFetchFailureBacksOff(t *testing.T) {
	h := newFeedHarness(t, "")
	h.setFeed(http.StatusInternalServerError, "boom")
	daemon := &recordingDaemon{}
	svc := startService(t, daemon, nil,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "bad", URL: h.server.URL + "/feed.xml", PollInterval: time.Millisecond}}
		},
		func() []config.RSSFilter { return []config.RSSFilter{{Name: "all"}} },
		func() []rtorrent.Torrent { return nil },
	)

	svc.PollNow(context.Background())
	waitFor(t, "error recorded", func() bool {
		for _, f := range svc.Snapshot().Feeds {
			if f.Name == "bad" && f.LastError != "" {
				return true
			}
		}
		return false
	})
	view := svc.Snapshot()
	if !strings.Contains(view.Feeds[0].LastError, "500") {
		t.Fatalf("lastError = %q", view.Feeds[0].LastError)
	}
	if view.Feeds[0].RetryIn <= 0 {
		t.Fatal("no backoff scheduled")
	}

	// Immediate re-polls must not hammer the failing feed.
	feedHits, _ := h.hits()
	svc.PollNow(context.Background())
	svc.PollNow(context.Background())
	time.Sleep(200 * time.Millisecond)
	if feedHits2, _ := h.hits(); feedHits2 != feedHits {
		t.Fatalf("backoff ignored: %d → %d hits", feedHits, feedHits2)
	}
	if len(daemon.fileCalls()) != 0 {
		t.Fatal("failed feed loaded something")
	}
}

func TestManualAddAndMarkRead(t *testing.T) {
	h := newFeedHarness(t, "")
	h.setFeed(http.StatusOK, testFeedXML(h.server.URL))
	daemon := &recordingDaemon{}
	svc := startService(t, daemon, nil,
		func() []config.RSSFeed {
			return []config.RSSFeed{{Name: "tv", URL: h.server.URL + "/feed.xml", Label: "manual-default", PollInterval: 50 * time.Millisecond}}
		},
		func() []config.RSSFilter { return nil },
		func() []rtorrent.Torrent { return nil },
	)

	// No filters: nothing auto-loads, but items are stored.
	svc.PollNow(context.Background())
	waitFor(t, "items stored", func() bool { return len(svc.Snapshot().Items) == 2 })

	if _, err := svc.AddItem("nope", "item-1"); err == nil {
		t.Fatal("expected unknown-feed error")
	}
	if _, err := svc.AddItem("tv", "nope"); err == nil {
		t.Fatal("expected unknown-item error")
	}
	hash, err := svc.AddItem("tv", "item-1")
	if err != nil {
		t.Fatalf("manual add = %v", err)
	}
	if hash == "" {
		t.Fatal("manual add returned no hash")
	}
	if len(daemon.fileCalls()) != 1 {
		t.Fatal("manual add did not load")
	}
	if _, err := svc.AddItem("tv", "item-1"); err == nil {
		t.Fatal("expected already-loaded error")
	}

	if n := svc.MarkRead("tv", []string{"item-2"}, false); n != 1 {
		t.Fatalf("marked = %d", n)
	}
	if n := svc.MarkRead("", nil, true); n != 1 {
		t.Fatalf("mark-all marked = %d, want 1 (item-1 unread, item-2 already read)", n)
	}
	for _, item := range svc.Snapshot().Items {
		if !item.Read {
			t.Fatalf("item %q not read", item.ID)
		}
	}
}

func TestMatchFilterConditions(t *testing.T) {
	item := Item{
		Title:      "Show.Name.S02E04.1080p",
		Categories: []string{"TV", "HD"},
		Enclosure:  Enclosure{URL: "https://x/y.torrent", Length: 1000},
	}
	cases := []struct {
		what   string
		filter config.RSSFilter
		feed   string
		want   bool
	}{
		{"empty matches everything", config.RSSFilter{}, "any", true},
		{"feed restriction passes", config.RSSFilter{Feed: "tv"}, "tv", true},
		{"feed restriction blocks", config.RSSFilter{Feed: "tv"}, "movies", false},
		{"title regex", config.RSSFilter{TitleRegex: `S\d\dE\d\d`}, "tv", true},
		{"title regex mismatch", config.RSSFilter{TitleRegex: `^Movie`}, "tv", false},
		{"category substring", config.RSSFilter{Category: "tv"}, "tv", true},
		{"category mismatch", config.RSSFilter{Category: "music"}, "tv", false},
		{"size range passes", config.RSSFilter{MinSize: 999, MaxSize: 1001}, "tv", true},
		{"size range fails", config.RSSFilter{MinSize: 1001}, "tv", false},
	}
	for _, tc := range cases {
		got, err := MatchFilter(tc.filter, tc.feed, item)
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.what, got, tc.want)
		}
	}
	// Unknown length never matches a bounded filter.
	unknown := item
	unknown.Enclosure.Length = -1
	if ok, _ := MatchFilter(config.RSSFilter{MinSize: 1}, "tv", unknown); ok {
		t.Error("unknown length matched a bounded filter")
	}
	if _, err := MatchFilter(config.RSSFilter{TitleRegex: "("}, "tv", item); err == nil {
		t.Error("invalid regex should error")
	}
}

func TestBackoffSchedule(t *testing.T) {
	if backoffFor(1) != time.Minute || backoffFor(2) != 2*time.Minute {
		t.Fatalf("backoff = %v, %v", backoffFor(1), backoffFor(2))
	}
	if backoffFor(20) != time.Hour {
		t.Fatalf("backoff cap = %v", backoffFor(20))
	}
}

func TestSanitizeErrorRedactsSecrets(t *testing.T) {
	feedURL := "https://tracker.example/feed?passkey=SECRET123&uid=7"
	err := fmt.Errorf(`Get "%s": dial tcp: refused`, feedURL)
	msg := sanitizeError(feedURL, err)
	if strings.Contains(msg, "SECRET123") {
		t.Fatalf("secret leaked: %q", msg)
	}
	if !strings.Contains(msg, "tracker.example/feed") {
		t.Fatalf("over-redacted: %q", msg)
	}
}
