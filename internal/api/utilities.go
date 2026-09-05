package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ---- GET /api/torrents/{hash}/torrent (Save .torrent) ----

// torrentFileHandler streams the torrent's session .torrent file from the
// configured session directory. The response is an attachment download, so
// the session directory path is never exposed to the client.
func (s *Server) torrentFileHandler(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	// The hash is joined into a filesystem path below. ServeMux unescapes
	// the wildcard, so "%2f" arrives here as a real separator and would
	// otherwise escape the session directory; only a bare infohash is safe.
	if !validInfohash(hash) {
		writeAPIError(w, http.StatusBadRequest, "invalid_hash", "hash must be a hex infohash")
		return
	}
	if s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "settings store not wired")
		return
	}
	sessionDir := s.opts.Store.Get().Directories.Session
	if sessionDir == "" {
		writeAPIError(w, http.StatusNotFound, "torrent_file_unavailable", "no session directory is configured")
		return
	}

	// Try both spellings: rTorrent stores session metafiles as <hash>.torrent
	// using the case of the hash as printed by the daemon (usually uppercase).
	var data []byte
	var name string
	for _, candidate := range []string{hash + ".torrent", strings.ToUpper(hash) + ".torrent"} {
		if b, err := os.ReadFile(filepath.Join(sessionDir, candidate)); err == nil {
			data = b
			name = candidate
			break
		}
	}
	if data == nil {
		writeAPIError(w, http.StatusNotFound, "torrent_file_unavailable", "session .torrent file not found (is the session directory mounted?)")
		return
	}

	// Prefer a descriptive download name from the live session row when
	// available; fall back to the file's own name.
	downloadName := name
	if s.opts.Poller != nil {
		snap := s.opts.Poller.Snapshot()
		for _, t := range snap.Torrents {
			if t.Hash == hash && t.Name != "" {
				downloadName = t.Name + ".torrent"
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeFilename(downloadName)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// sanitizeFilename strips path separators and control characters so the
// Content-Disposition header cannot be abused.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	return name
}

// ---- torrent capabilities (rename availability etc.) ----

// capabilities is the daemon-capability snapshot delivered in /api/settings
// so the UI can show daemon-dependent controls (PAR-2.7 rename, …).
type capabilities struct {
	Rename bool `json:"rename"` // daemon exposes d.name.set
}

// probeCapabilities checks daemon methods the UI gates on. The probe hits the
// daemon once per successful check; callers should cache briefly.
func (s *Server) probeCapabilities(r *http.Request) capabilities {
	caps := capabilities{}
	if s.opts.RTorrent == nil {
		return caps
	}
	if ok, err := s.opts.RTorrent.SupportsMethod(r.Context(), "d.name.set"); err == nil && ok {
		caps.Rename = true
	}
	return caps
}

// openDirectoryURL builds the "Open directory" link from the configured URL
// template, or returns "" when none is configured (the UI copies instead).
func openDirectoryURL(template, basePath string) string {
	if template == "" {
		return ""
	}
	escaped := url.QueryEscape(basePath)
	if strings.Contains(template, "{path}") {
		return strings.ReplaceAll(template, "{path}", escaped)
	}
	return template + escaped
}

// validInfohash reports whether s is a bare infohash: hex digits only, at
// most as long as a v2 (SHA-256) hash. Length is left loose so short
// fixture hashes still work; rejecting every non-hex byte is what keeps a
// separator, a dot, or an escaped one out of the filesystem path.
func validInfohash(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
