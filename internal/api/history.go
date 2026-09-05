package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"blackbird/internal/history"
	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

// torrentMeta is the .torrent-originated metadata captured when a torrent is
// added through Blackbird. Live fields (name, path, dates) are not stored here;
// the General tab merges this with the current session row.
type torrentMeta struct {
	Comment   string     `json:"comment"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt *time.Time `json:"creationDate"`
}

// metaStore remembers .torrent metadata by infohash for the General tab. It is
// populated two ways: eagerly when a torrent is added through Blackbird, and
// lazily by scanning the shared rTorrent session directory (mounted into the
// Blackbird container) for the session's `<infohash>.torrent` files.
type metaStore struct {
	mu      sync.Mutex
	m       map[string]torrentMeta
	missed  map[string]bool // hashes already looked up in the session dir
	session func() string   // session dir ("" disables the session scan)
}

func newMetaStore(sessionDir func() string) *metaStore {
	if sessionDir == nil {
		sessionDir = func() string { return "" }
	}
	return &metaStore{m: map[string]torrentMeta{}, missed: map[string]bool{}, session: sessionDir}
}

func (ms *metaStore) put(hash string, meta torrentMeta) {
	if hash == "" {
		return
	}
	ms.mu.Lock()
	ms.m[hash] = meta
	ms.mu.Unlock()
}

// get returns cached metadata for a hash. When nothing is cached and a session
// directory is configured, it scans for `<hash>.torrent` (lower- or
// uppercase, as rTorrent writes) and parses it, caching the result so each
// session file is only read once. ok is false when no metadata is known.
func (ms *metaStore) get(hash string) (torrentMeta, bool) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if meta, ok := ms.m[hash]; ok {
		return meta, true
	}
	if ms.session() == "" || ms.missed[hash] {
		return torrentMeta{}, false
	}
	ms.missed[hash] = true // avoid repeated directory probes for unknown hashes
	meta, found := ms.scanLocked(hash)
	if found {
		ms.m[hash] = meta
		return meta, true
	}
	return torrentMeta{}, false
}

// scanLocked looks for the torrent's session file under the configured session
// dir. rTorrent names session metafiles `<infohash>.torrent` with the hash in
// the case the client used; try both spellings.
func (ms *metaStore) scanLocked(hash string) (torrentMeta, bool) {
	for _, name := range []string{hash + ".torrent", strings.ToUpper(hash) + ".torrent"} {
		data, err := os.ReadFile(filepath.Join(ms.session(), name))
		if err != nil {
			continue
		}
		parsed, err := torrentfile.Parse(data)
		if err != nil || !strings.EqualFold(parsed.Infohash, hash) {
			continue
		}
		var created *time.Time
		if parsed.CreationDate != nil {
			created = parsed.CreationDate
		}
		return torrentMeta{Comment: parsed.Comment, CreatedBy: parsed.CreatedBy, CreatedAt: created}, true
	}
	return torrentMeta{}, false
}

// metaFromBytes parses .torrent bytes into the stored metadata shape.
func metaFromBytes(data []byte) (string, torrentMeta) {
	parsed, err := torrentfile.Parse(data)
	if err != nil {
		return "", torrentMeta{}
	}
	var created *time.Time
	if parsed.CreationDate != nil {
		created = parsed.CreationDate
	}
	return parsed.Infohash, torrentMeta{Comment: parsed.Comment, CreatedBy: parsed.CreatedBy, CreatedAt: created}
}

// IngestTorrentMeta records .torrent metadata keyed by infohash (PAR-3.1:
// the watch-directory loader parses files before loading them, and hands the
// metadata here so the General tab has comment/created-by).
func (s *Server) IngestTorrentMeta(hash string, parsed torrentfile.Meta) {
	var created *time.Time
	if parsed.CreationDate != nil {
		created = parsed.CreationDate
	}
	s.meta.put(hash, torrentMeta{Comment: parsed.Comment, CreatedBy: parsed.CreatedBy, CreatedAt: created})
}

// magnetHash extracts the btih infohash from a magnet URI.
func magnetHash(uri string) string {
	const marker = "btih:"
	rest := uri
	if i := strings.Index(uri, marker); i >= 0 {
		rest = uri[i+len(marker):]
	} else {
		return ""
	}
	// The infohash runs to the next '&' or end.
	if i := strings.IndexAny(rest, "&"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.ToLower(rest)
	if torrentfile.HasInfohash(rest) {
		return rest
	}
	return ""
}

// actorFromRequest returns who performed a request: the authenticated
// username when auth is on, otherwise "local" (auth disabled / dev).
func actorFromRequest(r *http.Request, auth *Auth) string {
	if auth == nil || auth.PasswordHash == "" {
		return "local"
	}
	user, _, ok := r.BasicAuth()
	if ok && user == auth.Username {
		return user
	}
	return auth.Username
}

// torrentName resolves a hash to its current session name for history
// entries ("" when the poller is unwired or the hash is gone).
func (s *Server) torrentName(hash string) string {
	if s.opts.Poller == nil {
		return ""
	}
	for _, t := range s.opts.Poller.Snapshot().Torrents {
		if t.Hash == hash {
			return t.Name
		}
	}
	return ""
}

// ---- GET /api/history (PAR-5.3 global event log) ----

// historyQuery is the JSON shape of one History page: the same events the
// Logger tab reads, newest first, with a sequence cursor for older pages.
type historyQuery struct {
	Events []history.Event `json:"events"`
	// NextBeforeSeq pages older: pass it as before_seq for the next page.
	// Zero when HasMore is false.
	NextBeforeSeq uint64 `json:"nextBeforeSeq"`
	HasMore       bool   `json:"hasMore"`
}

// historyHandler serves the global event log with newest-first pagination:
//
//	GET /api/history?limit=50&before_seq=123&kind=action,add&actor=admin&hash=…&q=…
//
// kind accepts a comma-separated list; q is a case-insensitive substring
// over name, hash, action, and message. limit defaults to 50 (max 200) and
// before_seq defaults to the latest event.
func (s *Server) historyHandler(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "history log not wired")
		return
	}
	q := r.URL.Query()
	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "bad_request", "limit must be an integer")
			return
		}
		limit = n
	}
	var beforeSeq uint64
	if raw := strings.TrimSpace(q.Get("before_seq")); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "bad_request", "before_seq must be an integer")
			return
		}
		beforeSeq = n
	}
	var kinds []history.Kind
	if raw := strings.TrimSpace(q.Get("kind")); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			switch history.Kind(k) {
			case history.KindAction, history.KindMessage, history.KindAdd, history.KindMove, history.KindComplete:
				kinds = append(kinds, history.Kind(k))
			default:
				writeAPIError(w, http.StatusBadRequest, "bad_request", "unknown kind "+k)
				return
			}
		}
	}
	res := s.history.Query(history.Filter{
		Kinds:  kinds,
		Actor:  strings.TrimSpace(q.Get("actor")),
		Hash:   strings.TrimSpace(q.Get("hash")),
		Search: strings.TrimSpace(q.Get("q")),
	}, limit, beforeSeq)
	events := res.Events
	if events == nil {
		events = []history.Event{}
	}
	writeJSON(w, http.StatusOK, historyQuery{Events: events, NextBeforeSeq: res.NextBeforeSeq, HasMore: res.HasMore})
}

// ---- GET /api/torrents/{hash}/logger ----

type loggerResponse struct {
	Hash    string          `json:"hash"`
	Entries []history.Entry `json:"entries"`
}

func (s *Server) loggerHandler(w http.ResponseWriter, r *http.Request, hash string) {
	if s.history == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "history log not wired")
		return
	}
	entries := s.history.ForHash(hash)
	writeJSON(w, http.StatusOK, loggerResponse{Hash: hash, Entries: entries})
}

// ---- GET /api/torrents/{hash}/speed ----

type speedResponse struct {
	Hash    string         `json:"hash"`
	Samples []pollerSample `json:"samples"`
}

// pollerSample is the JSON shape of one rate sample (matches poller.Sample).
type pollerSample struct {
	At       time.Time `json:"at"`
	DownRate int64     `json:"downRate"`
	UpRate   int64     `json:"upRate"`
}

func (s *Server) speedHandler(w http.ResponseWriter, r *http.Request, hash string) {
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "poller not wired")
		return
	}
	raw := s.opts.Poller.SpeedHistory(hash)
	samples := make([]pollerSample, len(raw))
	for i, smp := range raw {
		samples[i] = pollerSample{At: smp.At, DownRate: smp.DownRate, UpRate: smp.UpRate}
	}
	writeJSON(w, http.StatusOK, speedResponse{Hash: hash, Samples: samples})
}

// ---- GET /api/torrents/{hash}/general ----

type generalFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Copy  bool   `json:"copy,omitempty"` // whether a copy button applies
}

type generalResponse struct {
	Hash  string        `json:"hash"`
	Facts []generalFact `json:"facts"`
}

func (s *Server) generalHandler(w http.ResponseWriter, r *http.Request, hash string) {
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "poller not wired")
		return
	}
	var torrent *rtorrent.Torrent
	snap := s.opts.Poller.Snapshot()
	for i := range snap.Torrents {
		if snap.Torrents[i].Hash == hash {
			torrent = &snap.Torrents[i]
			break
		}
	}
	if torrent == nil {
		writeAPIError(w, http.StatusNotFound, "torrent_not_found", "torrent not found")
		return
	}
	t := *torrent
	meta, _ := s.meta.get(hash)

	sessionPath := ""
	if s.opts.Store != nil {
		sessionPath = s.opts.Store.Get().Directories.Session
	}
	created := ""
	if meta.CreatedAt != nil {
		created = meta.CreatedAt.Format(time.RFC3339)
	} else if !t.CreationDate.IsZero() {
		created = t.CreationDate.Format(time.RFC3339)
	}
	facts := []generalFact{
		{Label: "Name", Value: t.Name, Copy: true},
		{Label: "Hash", Value: t.Hash, Copy: true},
		{Label: "Comment", Value: meta.Comment},
		{Label: "Created by", Value: meta.CreatedBy},
		{Label: "Creation date", Value: created},
		{Label: "Tied file", Value: t.TiedToFile, Copy: true},
		{Label: "Session path", Value: sessionPath, Copy: true},
		{Label: "Message", Value: t.Message},
		{Label: "Downloaded", Value: formatBytes(t.DownloadedBytes)},
		{Label: "Uploaded", Value: formatBytes(t.UploadedBytes)},
		{Label: "Ratio", Value: trimFloat(t.Ratio)},
		{Label: "Tracker", Value: t.TrackerHost},
		{Label: "Tracker status", Value: t.TrackerStatus},
		{Label: "Priority", Value: itoa(t.Priority)},
		{Label: "Private", Value: boolYesNo(t.IsPrivate)},
	}
	writeJSON(w, http.StatusOK, generalResponse{Hash: hash, Facts: facts})
}

func boolYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
