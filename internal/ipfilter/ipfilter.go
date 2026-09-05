// Package ipfilter manages the rTorrent IPv4 blocklist (PAR-5.6): a
// PeerGuardian P2P / eMule DAT list from a local file or a URL is loaded
// into the daemon's ipv4_filter table on connect and on a refresh cadence,
// and the rule count, last load time, and errors are reported to Settings.
//
// The daemon reads the file itself (ipv4_filter.load takes a path), so a
// local path must be visible to rTorrent as well as to Blackbird (which
// counts the rules). URL lists are fetched by Blackbird to a local cache
// file first, then loaded from there.
package ipfilter

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"blackbird/internal/config"
)

// fetchTimeout bounds one blocklist download; maxListBytes caps it.
const (
	fetchTimeout = 60 * time.Second
	maxListBytes = 64 << 20
)

// Daemon loads a local blocklist file into the daemon filter table.
type Daemon interface {
	LoadIPFilter(ctx context.Context, path string) error
}

// Options configures the Service.
type Options struct {
	// Log is the slog logger; default slog.Default().
	Log *slog.Logger
	// Daemon receives ipv4_filter.load calls.
	Daemon Daemon
	// Config returns the live blocklist configuration (read under the
	// caller's config lock); Settings saves and SIGHUP reloads apply
	// through it without a restart.
	Config func() config.IPFilter
	// CachePath is where a URL list is stored before loading. Required
	// when a URL source is used; derived from the config path in main
	// (<config>-ipfilter.dat).
	CachePath string
	// ReconcileCadence overrides the refresh-check tick (tests).
	ReconcileCadence time.Duration
	// Now is the clock, overridable for tests.
	Now func() time.Time
}

// Status is the Settings-facing blocklist state: rule count, last load
// time, and errors (PAR-5.6 acceptance).
type Status struct {
	Enabled   bool       `json:"enabled"`
	Source    string     `json:"source"` // file | url | ""
	Path      string     `json:"path,omitempty"`
	Rules     int        `json:"rules"`
	LastLoad  *time.Time `json:"lastLoad,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

// Service keeps the daemon filter table in sync with the configured source.
type Service struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	status  Status
	applied string    // source fingerprint of the last successful apply
	lastTry time.Time // last ApplyNow attempt (success or failure)
}

// New builds the Service. A nil Config disables it (status reports
// disabled; ApplyNow is a no-op).
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{opts: opts, log: log}
}

// cfg returns the live configuration (zero value when unconfigured).
func (s *Service) cfg() config.IPFilter {
	if s.opts.Config == nil {
		return config.IPFilter{}
	}
	return s.opts.Config()
}

func (s *Service) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// fingerprint identifies the configured source for change detection.
func fingerprint(c config.IPFilter) string {
	return strings.TrimSpace(c.Path) + "\x00" + strings.TrimSpace(c.URL)
}

// CountRules counts the IPv4 ranges in a P2P/DAT-format blocklist: blank
// lines and #-comments are skipped, and each remaining line contributes one
// rule when it parses as a single IP, a CIDR, a start-end range, an eMule
// DAT entry ("start - end , level , name"), or a PeerGuardian P2P entry
// ("name:start-end"). Unparseable lines are ignored (they are still passed
// to the daemon verbatim inside the loaded file).
func CountRules(r io.Reader) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	count := 0
	for sc.Scan() {
		if parseRuleLine(sc.Text()) {
			count++
		}
	}
	return count, sc.Err()
}

// parseRuleLine reports whether one blocklist line is a usable IPv4 rule.
func parseRuleLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	// eMule DAT carries ", level , name" after the range; the range is the
	// part before the first comma.
	if i := strings.Index(line, ","); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if parseRange(strings.TrimSpace(line)) {
		return true
	}
	// PeerGuardian P2P prefixes the range with "name:". The range itself
	// never contains a colon (IPv4 only), so the text after the last colon
	// is the candidate.
	if i := strings.LastIndex(line, ":"); i >= 0 {
		return parseRange(strings.TrimSpace(line[i+1:]))
	}
	return false
}

// parseRange accepts a single IPv4 address, a CIDR, or a start-end range.
// Octets may be zero-padded (eMule DAT files write 001.002.003.004), which
// netip rejects, so addresses are normalized before parsing.
func parseRange(s string) bool {
	if s == "" {
		return false
	}
	if addr, prefix, ok := strings.Cut(s, "/"); ok {
		bits := strings.TrimSpace(prefix)
		norm, ok := normalizeV4(strings.TrimSpace(addr))
		if !ok {
			return false
		}
		p, err := netip.ParsePrefix(norm + "/" + bits)
		return err == nil && p.Addr().Is4()
	}
	if start, end, ok := strings.Cut(s, "-"); ok {
		lo, ok1 := normalizeV4(strings.TrimSpace(start))
		hi, ok2 := normalizeV4(strings.TrimSpace(end))
		if !ok1 || !ok2 {
			return false
		}
		loAddr, err1 := netip.ParseAddr(lo)
		hiAddr, err2 := netip.ParseAddr(hi)
		return err1 == nil && err2 == nil && loAddr.Compare(hiAddr) <= 0
	}
	norm, ok := normalizeV4(s)
	if !ok {
		return false
	}
	addr, err := netip.ParseAddr(norm)
	return err == nil && addr.Is4()
}

// normalizeV4 rewrites a dotted-quad with optional zero-padded octets into
// canonical form ("005" → "5"). It reports false for anything that is not
// four decimal octets in 0-255.
func normalizeV4(s string) (string, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return "", false
	}
	out := make([]string, 4)
	for i, p := range parts {
		if p == "" || len(p) > 3 {
			return "", false
		}
		n := 0
		for j := 0; j < len(p); j++ {
			c := p[j]
			if c < '0' || c > '9' {
				return "", false
			}
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return "", false
		}
		out[i] = strings.TrimLeft(p, "0")
		if out[i] == "" {
			out[i] = "0"
		}
	}
	return strings.Join(out, "."), true
}

// countFile counts the rules in the file at path.
func countFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return CountRules(f)
}

// ApplyNow fetches (URL sources), counts, and loads the blocklist into the
// daemon immediately. It records the rule count, load time, or error in the
// status. With no source configured it is a no-op returning nil.
func (s *Service) ApplyNow(ctx context.Context) error {
	c := s.cfg()
	if !c.Enabled() {
		s.mu.Lock()
		s.status = Status{}
		s.applied = ""
		s.mu.Unlock()
		return nil
	}
	path, rules, err := s.resolve(ctx, c)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.lastTry = now
	if err != nil {
		s.status.Enabled = true
		s.status.LastError = err.Error()
		s.log.Warn("ipfilter: refresh failed", "err", err)
		return err
	}
	if err := s.opts.Daemon.LoadIPFilter(ctx, path); err != nil {
		s.status.Enabled = true
		s.status.LastError = fmt.Sprintf("ipv4_filter.load %s: %v", path, err)
		s.log.Warn("ipfilter: daemon load failed", "path", path, "err", err)
		return err
	}
	loaded := now
	source := "file"
	if strings.TrimSpace(c.URL) != "" {
		source = "url"
	}
	s.status = Status{Enabled: true, Source: source, Path: path, Rules: rules, LastLoad: &loaded}
	s.applied = fingerprint(c)
	s.log.Info("ipfilter: blocklist loaded", "source", source, "path", path, "rules", rules)
	return nil
}

// resolve returns the local file the daemon should load plus its rule
// count, fetching a URL source into the cache first.
func (s *Service) resolve(ctx context.Context, c config.IPFilter) (string, int, error) {
	if path := strings.TrimSpace(c.Path); path != "" {
		rules, err := countFile(path)
		if err != nil {
			return "", 0, fmt.Errorf("read blocklist %s: %w", path, err)
		}
		return path, rules, nil
	}
	rawURL := strings.TrimSpace(c.URL)
	if s.opts.CachePath == "" {
		return "", 0, fmt.Errorf("no cache file configured for URL blocklist")
	}
	if err := fetchToCache(ctx, rawURL, s.opts.CachePath); err != nil {
		return "", 0, err
	}
	rules, err := countFile(s.opts.CachePath)
	if err != nil {
		return "", 0, fmt.Errorf("read cached blocklist: %w", err)
	}
	return s.opts.CachePath, rules, nil
}

// fetchToCache downloads the URL list (plain or gzipped) to a temp file and
// renames it over the cache path atomically.
func fetchToCache(ctx context.Context, rawURL, cachePath string) error {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("blocklist request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream, text/plain, */*")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", "blackbird-ipfilter")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch blocklist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch blocklist: HTTP %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, maxListBytes+1)
	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read blocklist: %w", err)
	}
	if int64(len(raw)) > maxListBytes {
		return fmt.Errorf("blocklist exceeds %d bytes", maxListBytes)
	}
	raw, err = maybeGunzip(rawURL, resp.Header.Get("Content-Encoding"), raw)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(cachePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("blocklist cache dir: %w", err)
		}
	}
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write blocklist cache: %w", err)
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("install blocklist cache: %w", err)
	}
	return nil
}

// maybeGunzip decompresses gzip-encoded or .gz-suffixed payloads (many
// blocklist mirrors serve ipfilter.dat.gz).
func maybeGunzip(rawURL, encoding string, raw []byte) ([]byte, error) {
	gzipped := strings.Contains(strings.ToLower(encoding), "gzip") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".gz")
	if !gzipped && len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gzipped = true
	}
	if !gzipped {
		return raw, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decompress blocklist: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxListBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decompress blocklist: %w", err)
	}
	if int64(len(out)) > maxListBytes {
		return nil, fmt.Errorf("blocklist exceeds %d bytes", maxListBytes)
	}
	return out, nil
}

// Status returns the last known blocklist state for Settings.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Run watches the live configuration: a changed source reloads immediately,
// and a URL source re-fetches on its refresh cadence. File sources reload
// when the source changes (connect-time and manual reloads cover the rest;
// the daemon holds the table between loads).
func (s *Service) Run(ctx context.Context) {
	cadence := s.opts.ReconcileCadence
	if cadence <= 0 {
		cadence = time.Minute
	}
	t := time.NewTicker(cadence)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcile(ctx)
		}
	}
}

func (s *Service) reconcile(ctx context.Context) {
	c := s.cfg()
	if !c.Enabled() {
		return
	}
	s.mu.Lock()
	changed := fingerprint(c) != s.applied
	refresh := c.EffectiveRefresh()
	due := refresh > 0 && s.now().Sub(s.lastTry) >= refresh
	s.mu.Unlock()
	if changed || due {
		_ = s.ApplyNow(ctx)
	}
}
