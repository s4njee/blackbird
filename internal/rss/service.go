package rss

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

const (
	// itemsPerFeed caps the stored item list per feed (PAR-3.3 "last N").
	itemsPerFeed = 200
	// seenCap bounds the GUID/enclosure dedup sets per feed.
	seenCap = 2000
	// evalHistoryCap bounds the per-filter match history.
	evalHistoryCap = 50
	// fetchTimeout bounds a feed fetch; enclosureTimeout bounds a download.
	fetchTimeout     = 30 * time.Second
	enclosureTimeout = 60 * time.Second
	// maxTorrentBytes bounds an enclosure download, like the Add API.
	maxTorrentBytes = 16 << 20
	// reconcileInterval re-reads the feed list and polls due feeds.
	reconcileInterval = 30 * time.Second
	// backoffBase / backoffMax bound fetch-failure backoff.
	backoffBase = time.Minute
	backoffMax  = time.Hour
	// userAgent identifies feed fetches and enclosure downloads.
	userAgent = "Blackbird RSS (+https://github.com/anomalyco/opencode)"
)

// Daemon loads torrents discovered by filters or manual adds.
type Daemon interface {
	SetLabel(ctx context.Context, hash, label string) error
	AddTorrentFile(ctx context.Context, data []byte, opts rtorrent.AddOptions) error
	AddMagnet(ctx context.Context, uri string, opts rtorrent.AddOptions) error
}

// Options configures the Service.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon loads matched enclosures into rTorrent.
	Daemon Daemon
	// History records loads and failures on the torrent's Logger tab.
	History *history.Log
	// OnMeta receives parsed .torrent metadata for the General tab.
	OnMeta func(hash string, meta torrentfile.Meta)
	// OnLoaded receives successful filter auto-loads (feed name, item
	// title, infohash) for user-facing notices (POL-8.3). Magnets report an
	// empty hash. Failures stay in the filter match history only.
	OnLoaded func(feed, title, hash string)
	// Snapshot returns the live session rows for duplicate detection.
	Snapshot func() []rtorrent.Torrent
	// Feeds returns the live feed list (read under the caller's config lock).
	Feeds func() []config.RSSFeed
	// Filters returns the live filter list.
	Filters func() []config.RSSFilter
	// Now is the clock, overridable for tests.
	Now func() time.Time
	// PollCadence overrides the supervisor tick (tests).
	PollCadence time.Duration
}

// StoredItem is one feed entry plus Blackbird-side state.
type StoredItem struct {
	Item
	Read       bool
	Loaded     bool
	LoadedHash string
	LoadedBy   string // filter name or "manual"
}

// FilterEval is one filter/item evaluation recorded in the match history.
type FilterEval struct {
	At      time.Time
	Feed    string
	ItemID  string
	Title   string
	Outcome string // loaded | already-in-session | no-enclosure | download-failed | load-failed
	Reason  string
}

// feedState is one feed's runtime state.
type feedState struct {
	cfg               config.RSSFeed
	items             []*StoredItem
	byID              map[string]*StoredItem
	seenGUID          *boundedSet
	seenEnclosure     *boundedSet
	fetching          bool
	consecutiveErrors int
	lastError         string
	lastFetch         time.Time
	lastOK            time.Time
	nextRetry         time.Time
	nextPoll          time.Time
}

// filterState is one filter's runtime counters and match history.
type filterState struct {
	name      string
	evaluated int
	matched   int
	loaded    int
	history   []FilterEval // newest first, capped
}

// Service polls RSS/Atom feeds and auto-loads matching items. It runs on its
// own goroutines and never touches the torrent poller except for a
// read-only snapshot used in duplicate detection.
type Service struct {
	opts Options
	http *http.Client

	mu      sync.Mutex
	feeds   map[string]*feedState
	filters map[string]*filterState
}

// New builds a Service. Run starts supervision; methods are safe before that.
func New(opts Options) *Service {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PollCadence <= 0 {
		opts.PollCadence = reconcileInterval
	}
	return &Service{
		opts:    opts,
		http:    &http.Client{Timeout: fetchTimeout},
		feeds:   map[string]*feedState{},
		filters: map[string]*filterState{},
	}
}

// SetOnMeta installs the metadata ingester (wired after the API server is
// constructed in main.go).
func (s *Service) SetOnMeta(fn func(hash string, meta torrentfile.Meta)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.OnMeta = fn
}

// SetOnLoaded installs the auto-load notice hook (POL-8.3).
func (s *Service) SetOnLoaded(fn func(feed, title, hash string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.OnLoaded = fn
}

// Run supervises feeds until ctx is cancelled: the feed list re-reads from
// live config and due feeds are polled (each in its own goroutine so a slow
// feed never stalls the others).
func (s *Service) Run(ctx context.Context) {
	s.reconcile()
	s.pollDue(ctx)
	ticker := time.NewTicker(s.opts.PollCadence)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcile()
			s.pollDue(ctx)
		}
	}
}

// reconcile syncs feed/filter runtime state with the live config.
func (s *Service) reconcile() {
	feeds := []config.RSSFeed{}
	if s.opts.Feeds != nil {
		feeds = s.opts.Feeds()
	}
	filters := []config.RSSFilter{}
	if s.opts.Filters != nil {
		filters = s.opts.Filters()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{}
	for _, f := range feeds {
		keep[f.Name] = true
		st, ok := s.feeds[f.Name]
		if !ok {
			st = &feedState{
				byID:          map[string]*StoredItem{},
				seenGUID:      newBoundedSet(seenCap),
				seenEnclosure: newBoundedSet(seenCap),
			}
			s.feeds[f.Name] = st
		}
		st.cfg = f
	}
	for name := range s.feeds {
		if !keep[name] {
			delete(s.feeds, name)
			s.opts.Log.Info("rss: feed removed", "feed", name)
		}
	}
	keepFilters := map[string]bool{}
	for _, f := range filters {
		keepFilters[f.Name] = true
		if _, ok := s.filters[f.Name]; !ok {
			s.filters[f.Name] = &filterState{name: f.Name}
		}
	}
	for name := range s.filters {
		if !keepFilters[name] {
			delete(s.filters, name)
		}
	}
}

// pollDue starts a fetch for every due, idle feed.
func (s *Service) pollDue(ctx context.Context) {
	now := s.opts.Now()
	s.mu.Lock()
	var due []*feedState
	for _, st := range s.feeds {
		if st.fetching || now.Before(st.nextPoll) || now.Before(st.nextRetry) {
			continue
		}
		st.fetching = true
		due = append(due, st)
	}
	s.mu.Unlock()
	for _, st := range due {
		go s.pollFeed(ctx, st)
	}
}

// pollFeed fetches, parses, stores, and evaluates one feed.
func (s *Service) pollFeed(ctx context.Context, st *feedState) {
	s.mu.Lock()
	cfg := st.cfg
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		st.fetching = false
		st.nextPoll = s.opts.Now().Add(cfg.EffectivePollInterval())
		s.mu.Unlock()
	}()

	now := s.opts.Now()
	data, err := s.fetch(ctx, cfg, cfg.URL, fetchTimeout)
	s.mu.Lock()
	st.lastFetch = now
	if err != nil {
		st.consecutiveErrors++
		st.lastError = sanitizeError(cfg.URL, err)
		st.nextRetry = now.Add(backoffFor(st.consecutiveErrors))
		s.opts.Log.Warn("rss: feed fetch failed", "feed", cfg.Name, "consecutive", st.consecutiveErrors, "retryIn", time.Until(st.nextRetry).Round(time.Second), "err", st.lastError)
		s.mu.Unlock()
		return
	}
	st.consecutiveErrors = 0
	st.lastError = ""
	st.lastOK = now
	st.nextRetry = time.Time{}
	s.mu.Unlock()

	items, err := ParseFeed(data)
	if err != nil {
		s.mu.Lock()
		st.lastError = err.Error()
		s.mu.Unlock()
		s.opts.Log.Warn("rss: feed parse failed", "feed", cfg.Name, "err", err)
		return
	}
	s.opts.Log.Info("rss: feed polled", "feed", cfg.Name, "items", len(items))
	s.ingest(cfg, items)
}

// ingest stores fresh items and evaluates new ones against the filters.
func (s *Service) ingest(feed config.RSSFeed, items []Item) {
	filters := []config.RSSFilter{}
	if s.opts.Filters != nil {
		filters = s.opts.Filters()
	}
	session := map[string]bool{}
	if s.opts.Snapshot != nil {
		for _, t := range s.opts.Snapshot() {
			session[t.Hash] = true
		}
	}

	s.mu.Lock()
	st := s.feeds[feed.Name]
	if st == nil {
		// The feed was removed from config while this fetch was in flight;
		// drop the batch, but never leave s.mu held on the way out.
		s.mu.Unlock()
		return
	}
	// Merge: fresh items first (feed order), then previously known items not
	// in this fetch, truncated to the cap. Read/loaded state survives.
	merged := make([]*StoredItem, 0, itemsPerFeed)
	seen := map[string]bool{}
	var fresh []*StoredItem
	for _, item := range items {
		id := item.ID
		if id == "" {
			continue
		}
		existing := st.byID[id]
		if existing == nil {
			existing = &StoredItem{Item: item}
			st.byID[id] = existing
		} else {
			existing.Item = item
		}
		if !seen[id] {
			seen[id] = true
			merged = append(merged, existing)
			if !st.seenGUID.has(guidKey(item)) && !st.seenEnclosure.has(enclosureKey(item)) {
				fresh = append(fresh, existing)
			}
		}
	}
	for _, old := range st.items {
		if len(merged) >= itemsPerFeed {
			break
		}
		if !seen[old.ID] {
			seen[old.ID] = true
			merged = append(merged, old)
		}
	}
	st.items = merged
	// Evict dropped IDs from the lookup so un-bounded growth stops here; the
	// dedup sets still suppress re-processing.
	for id := range st.byID {
		if !seen[id] {
			delete(st.byID, id)
		}
	}
	s.mu.Unlock()

	loadedBatch := map[string]bool{}
	for _, stored := range fresh {
		s.evaluate(feed, stored, filters, session, loadedBatch)
	}
}

// evaluate runs one new item through the ordered filters (first match wins).
// loadedBatch tracks enclosure URLs already loaded during this ingest so two
// items sharing one enclosure (reposts) load exactly once.
func (s *Service) evaluate(feed config.RSSFeed, stored *StoredItem, filters []config.RSSFilter, session map[string]bool, loadedBatch map[string]bool) {
	s.mu.Lock()
	st := s.feeds[feed.Name]
	if st != nil {
		st.seenGUID.add(guidKey(stored.Item))
		st.seenEnclosure.add(enclosureKey(stored.Item))
	}
	s.mu.Unlock()

	for i := range filters {
		f := filters[i]
		ok, err := MatchFilter(f, feed.Name, stored.Item)
		if err != nil || !ok {
			continue
		}
		if key := enclosureKey(stored.Item); key != "" && loadedBatch[key] {
			s.recordEval(f.Name, feed.Name, stored, "duplicate", "enclosure already loaded from this feed")
			return
		}
		s.mu.Lock()
		fs := s.filters[f.Name]
		if fs == nil {
			fs = &filterState{name: f.Name}
			s.filters[f.Name] = fs
		}
		fs.evaluated++
		fs.matched++
		s.mu.Unlock()
		outcome, reason, hash := s.loadItem(feed, stored, f.Label, f.Destination, f.Starts(), session)
		if outcome == "loaded" {
			if key := enclosureKey(stored.Item); key != "" {
				loadedBatch[key] = true
			}
			if hash != "" {
				session[hash] = true
			}
		}
		s.recordEval(f.Name, feed.Name, stored, outcome, reason)
		return
	}
}

// recordEval appends one entry to a filter's match history (newest first).
func (s *Service) recordEval(filterName, feedName string, stored *StoredItem, outcome, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fs := s.filters[filterName]
	if fs == nil {
		fs = &filterState{name: filterName}
		s.filters[filterName] = fs
	}
	if outcome == "loaded" {
		fs.loaded++
	}
	fs.history = append([]FilterEval{{
		At: s.opts.Now(), Feed: feedName, ItemID: stored.ID,
		Title: stored.Title, Outcome: outcome, Reason: reason,
	}}, fs.history...)
	if len(fs.history) > evalHistoryCap {
		fs.history = fs.history[:evalHistoryCap]
	}
}

// loadItem downloads the enclosure (when needed) and loads it into rTorrent.
// Returns outcome/reason for the match history, mirroring the UI vocabulary.
func (s *Service) loadItem(feed config.RSSFeed, stored *StoredItem, label, destination string, start bool, session map[string]bool) (outcome, reason, hash string) {
	if label == "" {
		label = feed.Label
	}
	if destination == "" {
		destination = feed.Destination
	}
	enclosureURL := stored.Enclosure.URL
	if enclosureURL == "" {
		enclosureURL = guessTorrentURL(stored.Link)
	}
	if enclosureURL == "" {
		return "no-enclosure", "item has no enclosure or torrent link to download", ""
	}
	opts := rtorrent.AddOptions{Start: start}
	if destination != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.directory.set="+destination)
	}
	if label != "" {
		opts.ExtraCommands = append(opts.ExtraCommands, "d.custom1.set="+label)
	}
	ctx := context.Background()

	if strings.HasPrefix(strings.ToLower(enclosureURL), "magnet:") {
		if err := s.opts.Daemon.AddMagnet(ctx, enclosureURL, opts); err != nil {
			s.recordLoad(stored, "", "rss", "load", "failed", "magnet load failed: "+err.Error())
			return "load-failed", err.Error(), ""
		}
		s.finishLoad(feed, stored, nil, "")
		return "loaded", "magnet added to session", ""
	}

	data, err := s.fetch(ctx, feed, enclosureURL, enclosureTimeout)
	if err != nil {
		s.recordLoad(stored, "", "rss", "load", "failed", "enclosure download failed: "+sanitizeError(feed.URL, err))
		return "download-failed", sanitizeError(feed.URL, err), ""
	}
	if int64(len(data)) >= maxTorrentBytes {
		// readLimited caps the body; a full cap means the file was truncated.
		s.recordLoad(stored, "", "rss", "load", "failed", "enclosure exceeds the 16 MiB limit")
		return "download-failed", "enclosure exceeds the 16 MiB limit", ""
	}
	parsed, err := torrentfile.Parse(data)
	if err != nil {
		s.recordLoad(stored, "", "rss", "load", "failed", "not a valid .torrent: "+err.Error())
		return "load-failed", "not a valid .torrent: " + err.Error(), ""
	}
	if session[parsed.Infohash] {
		s.recordLoad(stored, parsed.Infohash, "rss", "load", "failed", "already in session")
		return "already-in-session", "torrent is already in the session", parsed.Infohash
	}
	if err := s.opts.Daemon.AddTorrentFile(ctx, data, opts); err != nil {
		s.recordLoad(stored, parsed.Infohash, "rss", "load", "failed", "load failed: "+err.Error())
		return "load-failed", err.Error(), ""
	}
	s.finishLoad(feed, stored, parsed, parsed.Infohash)
	return "loaded", "added to session", parsed.Infohash
}

// finishLoad records a successful load in the history and item state. The
// history entry is keyed by infohash so the torrent's Logger tab shows it.
func (s *Service) finishLoad(feed config.RSSFeed, stored *StoredItem, parsed *torrentfile.Meta, hash string) {
	s.mu.Lock()
	stored.Loaded = true
	stored.LoadedHash = hash
	onMeta := s.opts.OnMeta
	onLoaded := s.opts.OnLoaded
	s.mu.Unlock()
	if onMeta != nil && parsed != nil && hash != "" {
		onMeta(hash, *parsed)
	}
	if onLoaded != nil {
		onLoaded(feed.Name, stored.Title, hash)
	}
	s.recordLoad(stored, hash, "rss", "add", "ok", "rss feed: "+feed.Name)
}

// recordLoad writes one history entry for an RSS load outcome. Outcomes
// without a known infohash (download failures, magnets) are keyed
// "rss:<item id>".
func (s *Service) recordLoad(stored *StoredItem, hash, actor, action, result, message string) {
	if s.opts.History == nil {
		return
	}
	if hash == "" {
		hash = "rss:" + stored.ID
	}
	s.opts.History.Add(hash, history.Entry{
		Kind: history.KindAdd, Actor: actor, Action: action, Result: result, Message: message,
		Name: stored.Title,
	})
}

// fetch GETs url with the feed's cookies/headers. Secrets never reach logs;
// callers sanitize errors that may embed the URL.
func (s *Service) fetch(ctx context.Context, feed config.RSSFeed, url string, timeout time.Duration) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	if feed.Cookies != "" {
		req.Header.Set("Cookie", feed.Cookies)
	}
	for k, v := range feed.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	client := *s.http
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	limit := int64(maxFeedBytes)
	if timeout == enclosureTimeout {
		limit = maxTorrentBytes
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// MatchFilter reports whether every non-empty condition of the filter passes
// for an item from the named feed.
func MatchFilter(filter config.RSSFilter, feedName string, item Item) (bool, error) {
	if filter.Feed != "" && filter.Feed != feedName {
		return false, nil
	}
	if filter.TitleRegex != "" {
		re, err := regexp.Compile(filter.TitleRegex)
		if err != nil {
			return false, fmt.Errorf("filter %q: %w", filter.Name, err)
		}
		if !re.MatchString(item.Title) {
			return false, nil
		}
	}
	if filter.Category != "" {
		needle := strings.ToLower(filter.Category)
		matched := false
		for _, c := range item.Categories {
			if strings.Contains(strings.ToLower(c), needle) {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	if filter.MinSize > 0 || filter.MaxSize > 0 {
		if item.Enclosure.Length < 0 {
			return false, nil
		}
		if filter.MinSize > 0 && item.Enclosure.Length < filter.MinSize {
			return false, nil
		}
		if filter.MaxSize > 0 && item.Enclosure.Length > filter.MaxSize {
			return false, nil
		}
	}
	return true, nil
}

// guessTorrentURL treats a plain .torrent link as a downloadable enclosure
// so feeds without <enclosure> elements still work.
func guessTorrentURL(link string) string {
	trimmed := strings.TrimSpace(link)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".torrent") {
		return trimmed
	}
	return ""
}

// guidKey / enclosureKey are the two dedup dimensions (PAR-3.3): items are
// new only when both their GUID and their enclosure URL are unseen.
func guidKey(item Item) string {
	if item.ID == "" {
		return ""
	}
	return "guid:" + item.ID
}

func enclosureKey(item Item) string {
	if item.Enclosure.URL == "" {
		return ""
	}
	sum := sha1.Sum([]byte(item.Enclosure.URL))
	return "enclosure:" + hex.EncodeToString(sum[:])
}

// backoffFor returns the retry delay after n consecutive failures.
func backoffFor(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := backoffBase
	for i := 1; i < failures && d < backoffMax; i++ {
		d *= 2
	}
	if d > backoffMax {
		d = backoffMax
	}
	return d
}

// sanitizeError rewrites an error that may embed the feed URL (private feeds
// carry passkeys in the query string) so it is safe for logs and the API.
func sanitizeError(feedURL string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if feedURL == "" || !strings.Contains(msg, feedURL) {
		// Also redact any http(s) URL query strings found in the message.
		return redactQueries(msg)
	}
	u, uerr := url.Parse(feedURL)
	if uerr != nil {
		return redactQueries(msg)
	}
	u.RawQuery = ""
	u.Fragment = ""
	return strings.ReplaceAll(msg, feedURL, u.String())
}

func redactQueries(msg string) string {
	// Best-effort: strip query strings from embedded URLs.
	words := strings.Fields(msg)
	for i, w := range words {
		if (strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://")) && strings.Contains(w, "?") {
			if u, err := url.Parse(w); err == nil {
				u.RawQuery = ""
				u.Fragment = ""
				words[i] = u.String()
			}
		}
	}
	return strings.Join(words, " ")
}

// boundedSet is a small insertion-ordered string set with a cap; the oldest
// entries are evicted past it.
type boundedSet struct {
	mu   sync.Mutex
	cap  int
	seen map[string]bool
	fifo []string
}

func newBoundedSet(capacity int) *boundedSet {
	if capacity < 1 {
		capacity = 1
	}
	return &boundedSet{cap: capacity, seen: map[string]bool{}}
}

func (b *boundedSet) add(key string) {
	if key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen[key] {
		return
	}
	b.seen[key] = true
	b.fifo = append(b.fifo, key)
	for len(b.fifo) > b.cap {
		delete(b.seen, b.fifo[0])
		b.fifo = b.fifo[1:]
	}
}

func (b *boundedSet) has(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen[key]
}
