package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"net"
	neturl "net/url"

	"gopkg.in/yaml.v3"
)

//go:embed example.yml
var exampleYAML []byte

// Example returns the annotated example configuration document (also written
// to disk on first run when the config file is missing).
func Example() []byte { return exampleYAML }

// defaults returns the schema populated with sane defaults. A minimal config
// file needs only the rtorrent endpoint and auth credentials.
func defaults() *Config {
	return &Config{
		Server: Server{Listen: ":8222", BaseURL: "/"},
		Log:    Log{Level: "info"},
		RTorrent: RTorrent{
			SCGI:    "unix:///tmp/rtorrent.sock",
			Timeout: 10 * time.Second,
		},
		Poll: Poll{
			Interval:       2 * time.Second,
			DetailInterval: time.Second,
			VolumeInterval: 30 * time.Second,
		},
		History: History{
			ActionLogEntries:   200,
			ActionLogRetention: 24 * time.Hour,
			MessageEntries:     200,
		},
		UI: UI{Accent: "", Theme: "dark", Sort: Sort{Column: "added", Dir: "desc"}, DateFormat: "local", RateFormat: "binary", PollInterval: "2s"},
	}
}

// Load reads and validates the YAML file at path. A missing file is not an
// error: defaults are used and the annotated example is written to path.
// An invalid file fails with line-numbered errors naming the offending keys.
func Load(path string) (*Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if werr := WriteExample(path); werr != nil {
			return cfg, nil // defaults still usable; surface write issue via log upstream
		}
		return cfg, nil
	}

	if err := decodeStrict(data, cfg); err != nil {
		return nil, fmt.Errorf("invalid config %s:\n%s", path, err)
	}
	cfg.UI.MigrateColumns()
	cfg.Warnings = append(cfg.Warnings, cfg.Directories.MigrateWatch()...)
	// ui.poll_interval is deprecated and ignored (POL-8.4): the list poll is
	// server-driven (poll.interval). Kept as a read-compatible alias until v2
	// so old configs keep loading; the Settings editor no longer offers it.
	if cfg.UI.PollInterval != "" {
		cfg.Warnings = append(cfg.Warnings, "ui.poll_interval is deprecated and ignored: the torrent list poll is server-driven (see poll.interval); remove the key")
	}
	if verrs := Validate(cfg); len(verrs) > 0 {
		lines := decorateWithLines(data, verrs)
		return nil, fmt.Errorf("invalid config %s:\n%s", path, strings.Join(lines, "\n"))
	}
	return cfg, nil
}

// decodeStrict decodes YAML into cfg, rejecting unknown fields. Errors keep
// their "line N:" prefixes from the parser.
func decodeStrict(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			return errors.New(strings.Join(te.Errors, "\n"))
		}
		return err
	}
	return nil
}

// ValidationError is a semantic constraint violation for a config key path.
type ValidationError struct {
	Path []string // e.g. ["tuning", "dht_mode"]
	Msg  string
}

func (v ValidationError) String() string {
	return strings.Join(v.Path, ".") + ": " + v.Msg
}

// Validate checks semantic constraints beyond YAML typing.
func Validate(cfg *Config) []ValidationError {
	var errs []ValidationError
	add := func(path []string, msg string) { errs = append(errs, ValidationError{path, msg}) }

	if cfg.Server.Listen == "" {
		add([]string{"server", "listen"}, "must not be empty")
	}
	if cfg.Server.BaseURL == "" {
		add([]string{"server", "base_url"}, "must not be empty")
	}
	// Trusted origins are compared against the Origin header, which is
	// always scheme://host[:port] with no path — reject anything else now
	// rather than silently never matching at runtime.
	for i, origin := range cfg.Server.TrustedOrigins {
		u, err := neturl.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" {
			add([]string{"server", "trusted_origins", strconv.Itoa(i)},
				fmt.Sprintf("must be a scheme://host[:port] origin with no path (got %q)", origin))
		}
	}
	for i, entry := range cfg.Server.TrustedProxies {
		if _, _, err := net.ParseCIDR(entry); err == nil {
			continue
		}
		if net.ParseIP(entry) != nil {
			continue
		}
		add([]string{"server", "trusted_proxies", strconv.Itoa(i)},
			fmt.Sprintf("must be an IP or CIDR (got %q)", entry))
	}
	if !contains([]string{"debug", "info", "warn", "error"}, cfg.LogLevelRaw()) {
		add([]string{"log", "level"}, fmt.Sprintf("must be one of debug, info, warn, error (got %q)", cfg.LogLevelRaw()))
	}
	if cfg.RTorrent.SCGI == "" {
		add([]string{"rtorrent", "scgi"}, "must not be empty")
	} else if !strings.HasPrefix(cfg.RTorrent.SCGI, "unix://") && !strings.HasPrefix(cfg.RTorrent.SCGI, "tcp://") {
		add([]string{"rtorrent", "scgi"}, "must be unix:///path/to/socket or tcp://host:port")
	}
	if cfg.RTorrent.Timeout <= 0 {
		add([]string{"rtorrent", "timeout"}, "must be positive (e.g. 10s)")
	}
	if cfg.RTorrent.MaxResponseBytes < 0 {
		add([]string{"rtorrent", "max_response_bytes"}, "must be >= 0 (0 = 64MB default)")
	}
	if cfg.Poll.Interval <= 0 {
		add([]string{"poll", "interval"}, "must be positive (e.g. 2s)")
	}
	if cfg.Poll.DetailInterval <= 0 {
		add([]string{"poll", "detail_interval"}, "must be positive (e.g. 1s)")
	}
	if cfg.Poll.VolumeInterval <= 0 {
		add([]string{"poll", "volume_interval"}, "must be positive (e.g. 30s)")
	}
	if cfg.Poll.MaxInterval < 0 {
		add([]string{"poll", "max_interval"}, "must not be negative (e.g. 30s)")
	} else if cfg.Poll.MaxInterval > 0 && cfg.Poll.MaxInterval < cfg.Poll.Interval {
		add([]string{"poll", "max_interval"}, "must be >= poll.interval (it caps the idle stretch)")
	}
	if cfg.Trackers.RampBatch < 0 {
		add([]string{"trackers", "ramp_batch"}, "must be >= 0 (0 = default)")
	}
	if cfg.Trackers.RampInterval < 0 {
		add([]string{"trackers", "ramp_interval"}, "must not be negative (e.g. 2s)")
	}
	if cfg.History.RecorderBytes != 0 && (cfg.History.RecorderBytes < 1<<20 || cfg.History.RecorderBytes > 128<<20) {
		add([]string{"history", "recorder_bytes"}, "must be 0 (16 MiB default) or between 1 MiB and 128 MiB; restart required")
	}
	if cfg.History.ActionLogEntries < 0 {
		add([]string{"history", "action_log_entries"}, "must be >= 0 (0 disables the action log)")
	}
	if cfg.History.MessageEntries < 0 {
		add([]string{"history", "message_entries"}, "must be >= 0 (0 disables the message history)")
	}
	if cfg.History.GlobalEntries < 0 {
		add([]string{"history", "global_entries"}, "must be >= 0 (0 = default)")
	}
	if cfg.History.ActionLogRetention < 0 {
		add([]string{"history", "action_log_retention"}, "must not be negative (e.g. 24h)")
	}
	if cfg.Stats.TrafficDays != nil && *cfg.Stats.TrafficDays < 0 {
		add([]string{"stats", "traffic_days"}, "must be >= 0 (0 disables persistence)")
	}
	if strings.TrimSpace(cfg.PortCheck.URL) != "" {
		u, err := neturl.Parse(strings.ReplaceAll(cfg.PortCheck.URL, "{port}", "51413"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add([]string{"portcheck", "url"}, "must be an absolute http(s) URL with a {port} placeholder (empty disables)")
		} else if !strings.Contains(cfg.PortCheck.URL, "{port}") {
			add([]string{"portcheck", "url"}, "must contain a {port} placeholder (empty disables)")
		}
	}
	if cfg.PortCheck.Timeout < 0 {
		add([]string{"portcheck", "timeout"}, "must not be negative (e.g. 10s)")
	}
	if f := cfg.Network.IPFilter; f.Enabled() {
		if strings.TrimSpace(f.Path) != "" && strings.TrimSpace(f.URL) != "" {
			add([]string{"network", "ipfilter"}, "set exactly one of path or url")
		} else if strings.TrimSpace(f.Path) != "" {
			if !filepath.IsAbs(f.Path) {
				add([]string{"network", "ipfilter", "path"}, "must be an absolute path")
			}
		} else {
			u, err := neturl.Parse(strings.TrimSpace(f.URL))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				add([]string{"network", "ipfilter", "url"}, "must be an absolute http(s) URL")
			}
		}
		if f.RefreshInterval < 0 {
			add([]string{"network", "ipfilter", "refresh_interval"}, "must not be negative (e.g. 24h)")
		}
	}
	if h := cfg.Auth.PasswordHash; h != "" && !bcryptRe.MatchString(h) {
		add([]string{"auth", "password_hash"}, "must be a bcrypt hash ($2a$/$2b$/$2y$ followed by 53 characters)")
	}

	seenWatch := map[string]bool{}
	for i, w := range cfg.Directories.Watch {
		path := []string{"directories", "watch", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(w.Path) == "" {
			add(append(path, "path"), "must not be empty")
		} else if !filepath.IsAbs(w.Path) {
			add(append(path, "path"), "must be an absolute path")
		}
		if seenWatch[w.Path] {
			add(append(path, "path"), "duplicate watch directory "+w.Path)
		}
		seenWatch[w.Path] = true
		if w.PollInterval < 0 {
			add(append(path, "poll_interval"), "must not be negative (e.g. 5s)")
		}
	}
	seenRules := map[string]bool{}
	for i, r := range cfg.Automation.OnComplete {
		path := []string{"automation", "on_complete", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(r.Name) == "" {
			add(append(path, "name"), "must not be empty")
		} else if seenRules[r.Name] {
			add(append(path, "name"), "duplicate rule name "+r.Name)
		}
		seenRules[r.Name] = true
		if r.NameRegex != "" {
			if _, err := regexp.Compile(r.NameRegex); err != nil {
				add(append(path, "name_regex"), "invalid regular expression: "+err.Error())
			}
		}
		if r.MinSize < 0 || r.MaxSize < 0 {
			add(append(path, "min_size"), "must not be negative")
		}
		if r.MinSize > 0 && r.MaxSize > 0 && r.MinSize > r.MaxSize {
			add(append(path, "max_size"), "must be >= min_size")
		}
		if !r.HasActions() {
			add(path, "must define at least one action (set_label, add_tracker, move_to, or webhook)")
		}
		if r.MoveTo != "" && !filepath.IsAbs(r.MoveTo) {
			add(append(path, "move_to"), "must be an absolute path")
		}
		for field, url := range map[string]string{"add_tracker": r.AddTracker, "webhook": r.Webhook} {
			if url == "" {
				continue
			}
			u, err := neturl.Parse(url)
			if err != nil || u.Host == "" {
				add(append(path, field), "must be an absolute URL")
			}
		}
	}
	seenFeeds := map[string]bool{}
	for i, f := range cfg.Automation.Rss.Feeds {
		path := []string{"automation", "rss", "feeds", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(f.Name) == "" {
			add(append(path, "name"), "must not be empty")
		} else if seenFeeds[f.Name] {
			add(append(path, "name"), "duplicate feed name "+f.Name)
		}
		seenFeeds[f.Name] = true
		if strings.TrimSpace(f.URL) == "" {
			add(append(path, "url"), "must not be empty")
		} else {
			u, err := neturl.Parse(f.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				add(append(path, "url"), "must be an absolute http(s) URL")
			}
		}
		if f.PollInterval < 0 {
			add(append(path, "poll_interval"), "must not be negative (e.g. 15m)")
		}
	}
	for i, fl := range cfg.Automation.Rss.Filters {
		path := []string{"automation", "rss", "filters", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(fl.Name) == "" {
			add(append(path, "name"), "must not be empty")
		}
		if fl.Feed != "" && !seenFeeds[fl.Feed] {
			add(append(path, "feed"), "unknown feed "+strconv.Quote(fl.Feed))
		}
		if fl.TitleRegex != "" {
			if _, err := regexp.Compile(fl.TitleRegex); err != nil {
				add(append(path, "title_regex"), "invalid regular expression: "+err.Error())
			}
		}
		if fl.MinSize < 0 || fl.MaxSize < 0 {
			add(append(path, "min_size"), "must not be negative")
		}
		if fl.MinSize > 0 && fl.MaxSize > 0 && fl.MinSize > fl.MaxSize {
			add(append(path, "max_size"), "must be >= min_size")
		}
	}
	if cfg.Automation.Unpack.Workers < 0 || cfg.Automation.Unpack.Workers > 8 {
		add([]string{"automation", "unpack", "workers"}, "must be between 0 (default 2) and 8")
	}
	if cfg.Automation.Unpack.Timeout < 0 {
		add([]string{"automation", "unpack", "timeout"}, "must not be negative (e.g. 30m)")
	}
	seenUnpack := map[string]bool{}
	for i, r := range cfg.Automation.Unpack.Rules {
		path := []string{"automation", "unpack", "rules", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(r.Name) == "" {
			add(append(path, "name"), "must not be empty")
		} else if seenUnpack[r.Name] {
			add(append(path, "name"), "duplicate rule name "+r.Name)
		}
		seenUnpack[r.Name] = true
		if r.Destination != "" && !filepath.IsAbs(r.Destination) {
			add(append(path, "destination"), "must be an absolute path")
		}
	}
	seen := map[string]bool{}
	for i, l := range cfg.Labels {
		path := []string{"labels", fmt.Sprintf("[%d]", i)}
		if l.Name == "" {
			add(path, "name must not be empty")
		}
		if seen[l.Name] {
			add(append(path, "name"), "duplicate label name "+l.Name)
		}
		seen[l.Name] = true
		if !hexColorRe.MatchString(l.Color) {
			add(append(path, "color"), "must be a #rrggbb hex color")
		}
	}

	if cfg.UI.Accent != "" && !hexColorRe.MatchString(cfg.UI.Accent) {
		add([]string{"ui", "accent"}, "must be a #rrggbb hex color")
	}
	if cfg.UI.Theme != "" && !contains([]string{"dark", "light", "midnight", "contrast", "classic", "system"}, cfg.UI.Theme) {
		add([]string{"ui", "theme"}, `must be one of dark, light, midnight, contrast, classic, system (got "`+cfg.UI.Theme+`")`)
	}
	if cfg.UI.Sort.Dir != "" && !contains([]string{"asc", "desc"}, cfg.UI.Sort.Dir) {
		add([]string{"ui", "sort", "dir"}, "must be asc or desc")
	}
	if len(cfg.UI.Sort.Keys) > 2 {
		add([]string{"ui", "sort", "keys"}, "supports at most a primary and secondary key")
	}
	seenSortKeys := map[string]bool{}
	for i, key := range cfg.UI.Sort.Keys {
		path := []string{"ui", "sort", "keys", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(key.Column) == "" {
			add(append(path, "column"), "must not be empty")
		}
		if key.Dir != "asc" && key.Dir != "desc" {
			add(append(path, "dir"), "must be asc or desc")
		}
		if seenSortKeys[key.Column] {
			add(append(path, "column"), "duplicate sort column "+key.Column)
		}
		seenSortKeys[key.Column] = true
	}
	seenFilters := map[string]bool{}
	for i, filter := range cfg.UI.SavedFilters {
		path := []string{"ui", "saved_filters", fmt.Sprintf("[%d]", i), "name"}
		if strings.TrimSpace(filter.Name) == "" {
			add(path, "must not be empty")
			continue
		}
		if seenFilters[filter.Name] {
			add(path, "duplicate saved filter name "+filter.Name)
		}
		seenFilters[filter.Name] = true
	}
	if cfg.UI.DateFormat != "" && !contains([]string{"local", "iso"}, cfg.UI.DateFormat) {
		add([]string{"ui", "date_format"}, "must be local or iso")
	}
	if cfg.UI.RateFormat != "" && !contains([]string{"binary", "decimal"}, cfg.UI.RateFormat) {
		add([]string{"ui", "rate_format"}, "must be binary or decimal")
	}

	// Tuning constraints (present = non-nil pointer).
	t := cfg.Tuning
	strp := []string{"tuning"}
	if t.Encryption != nil && !validEncryption(*t.Encryption) {
		add(append(strp, "encryption"), "must be comma-separated tokens from: allow_incoming, try_outgoing, require, require_RC4, enable_retry, prefer_plaintext (or 'none')")
	}
	if t.DHTMode != nil && !contains([]string{"auto", "on", "off", "disable"}, *t.DHTMode) {
		add(append(strp, "dht_mode"), "must be one of auto, on, off, disable")
	}
	if t.PortRange != nil && !portRangeRe.MatchString(*t.PortRange) {
		add(append(strp, "port_range"), "must be a port or range like 51413 or 51413-51420")
	}
	if t.DHTPort != nil && (*t.DHTPort < 1 || *t.DHTPort > 65535) {
		add(append(strp, "dht_port"), "must be 1-65535")
	}
	for _, f := range []struct {
		name string
		v    *int
	}{
		{"http_max_open", t.HTTPMaxOpen},
		{"max_open_sockets", t.MaxOpenSockets},
		{"max_open_files", t.MaxOpenFiles},
		{"min_peers_normal", t.MinPeersNormal},
		{"max_peers_normal", t.MaxPeersNormal},
		{"min_peers_seeded", t.MinPeersSeeded},
		{"max_peers_seeded", t.MaxPeersSeeded},
		{"max_uploads", t.MaxUploads},
		{"max_uploads_global", t.MaxUploadsGlobal},
		{"max_downloads_global", t.MaxDownloadsGlobal},
	} {
		if f.v != nil && *f.v < 0 {
			add(append(strp, f.name), "must be >= 0")
		}
	}
	for _, f := range []struct {
		name string
		v    *int64
	}{
		{"global_down_rate_kb", t.GlobalDownRateKB},
		{"global_up_rate_kb", t.GlobalUpRateKB},
	} {
		if f.v != nil && *f.v < 0 {
			add(append(strp, f.name), "must be >= 0 (0 = unlimited)")
		}
	}
	seenThrottles := map[string]bool{}
	for i, ch := range t.Throttles {
		path := []string{"tuning", "throttles", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(ch.Name) == "" {
			add(append(path, "name"), "must not be empty")
		} else if ch.Name == "NULL" {
			add(append(path, "name"), "must not be NULL (reserved by rTorrent)")
		} else if seenThrottles[ch.Name] {
			add(append(path, "name"), "duplicate channel name "+ch.Name)
		}
		seenThrottles[ch.Name] = true
		if ch.UpKB < 0 || ch.DownKB < 0 {
			add(path, "up_kb and down_kb must be >= 0 (0 = unlimited)")
		}
	}
	switch cfg.Seeding.CustomSlot {
	case "", "custom2", "custom3", "custom4", "custom5":
	default:
		add([]string{"seeding", "custom_slot"}, "must be one of custom2, custom3, custom4, custom5 (custom1 is the label)")
	}
	seenGroups := map[string]bool{}
	for i, g := range cfg.Seeding.Groups {
		path := []string{"seeding", "groups", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(g.Name) == "" {
			add(append(path, "name"), "must not be empty")
		} else if seenGroups[g.Name] {
			add(append(path, "name"), "duplicate group name "+g.Name)
		}
		seenGroups[g.Name] = true
		if g.MinRatio < 0 || g.MaxRatio < 0 {
			add(path, "min_ratio and max_ratio must be >= 0")
		}
		if g.MinRatio > 0 && g.MaxRatio > 0 && g.MinRatio > g.MaxRatio {
			add(append(path, "max_ratio"), "must be >= min_ratio")
		}
		if g.MinUploadBytes < 0 {
			add(append(path, "min_upload_bytes"), "must be >= 0")
		}
		if g.MaxSeedingTime < 0 {
			add(append(path, "max_seeding_time"), "must not be negative (e.g. 72h)")
		}
		switch g.Action {
		case SeedingStop, SeedingStopAndSetLabel, SeedingErase, SeedingEraseWithData:
		default:
			add(append(path, "action"), "must be one of stop, stop_and_set_label, erase, erase_with_data")
		}
		if g.Action == SeedingStopAndSetLabel && strings.TrimSpace(g.Label) == "" {
			add(append(path, "label"), "is required for stop_and_set_label")
		}
	}
	if tz := cfg.Schedule.Timezone; tz != "" && tz != "Local" && tz != "local" {
		if _, err := time.LoadLocation(tz); err != nil {
			add([]string{"schedule", "timezone"}, "unknown time zone "+strconv.Quote(tz))
		}
	}
	seenProfiles := map[string]bool{}
	for i, p := range cfg.Schedule.Bandwidth.Profiles {
		path := []string{"schedule", "bandwidth", "profiles", fmt.Sprintf("[%d]", i)}
		if strings.TrimSpace(p.Name) == "" {
			add(append(path, "name"), "must not be empty")
		} else if seenProfiles[p.Name] {
			add(append(path, "name"), "duplicate profile name "+p.Name)
		}
		seenProfiles[p.Name] = true
		if p.Color != "" && !hexColorRe.MatchString(p.Color) {
			add(append(path, "color"), "must be a #rrggbb hex color")
		}
		if p.DownKB < 0 || p.UpKB < 0 {
			add(path, "down_kb and up_kb must be >= 0 (0 = unlimited)")
		}
		seenChannels := map[string]bool{}
		for j, ch := range p.Throttles {
			cpath := append(path, "throttles", fmt.Sprintf("[%d]", j))
			if strings.TrimSpace(ch.Name) == "" {
				add(append(cpath, "name"), "must not be empty")
			} else if ch.Name == "NULL" {
				add(append(cpath, "name"), "must not be NULL (reserved by rTorrent)")
			} else if seenChannels[ch.Name] {
				add(append(cpath, "name"), "duplicate channel name "+ch.Name)
			}
			seenChannels[ch.Name] = true
			if ch.UpKB < 0 || ch.DownKB < 0 {
				add(cpath, "up_kb and down_kb must be >= 0 (0 = unlimited)")
			}
		}
	}
	for _, day := range ScheduleWeekdays {
		cells, ok := cfg.Schedule.Bandwidth.Grid[day]
		if !ok || len(cells) == 0 {
			continue
		}
		if len(cells) != 24 {
			add([]string{"schedule", "bandwidth", "grid", day}, "must have exactly 24 hourly entries")
			continue
		}
		for h, name := range cells {
			if name != "" && !seenProfiles[name] {
				add([]string{"schedule", "bandwidth", "grid", day, fmt.Sprintf("[%d]", h)}, "unknown profile "+strconv.Quote(name))
			}
		}
	}
	return errs
}

// Save writes the config atomically (temp file + rename). Comments in the
// source file are NOT preserved — YAML is re-marshalled from the schema.
func Save(path string, cfg *Config) error {
	cfg.UI.MigrateColumns()
	_ = cfg.Directories.MigrateWatch() // clear legacy keys on save; warnings were shown at load
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// WriteExample writes the annotated example config to path.
func WriteExample(path string) error {
	return os.WriteFile(path, exampleYAML, 0o600)
}

// decorateWithLines re-parses the document and annotates each validation
// error with its key's line number.
func decorateWithLines(data []byte, verrs []ValidationError) []string {
	var root yaml.Node
	_ = yaml.Unmarshal(data, &root)
	out := make([]string, len(verrs))
	for i, ve := range verrs {
		line := findLine(&root, ve.Path)
		if line > 0 {
			out[i] = fmt.Sprintf("  line %d: %s: %s", line, strings.Join(ve.Path, "."), ve.Msg)
		} else {
			out[i] = "  " + ve.String()
		}
	}
	return out
}

// findLine walks mapping nodes following path, returning the line of the
// final key (0 if not found).
func findLine(n *yaml.Node, path []string) int {
	if n == nil || len(path) == 0 {
		if n != nil {
			return n.Line
		}
		return 0
	}
	if n.Kind != yaml.DocumentNode {
		return 0
	}
	for _, child := range n.Content {
		if l := findMappingLine(child, path); l > 0 {
			return l
		}
	}
	return 0
}

func findMappingLine(n *yaml.Node, path []string) int {
	if n == nil || (n.Kind != yaml.MappingNode) {
		return 0
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if key.Value == path[0] {
			if len(path) == 1 {
				return key.Line
			}
			if l := findMappingLine(val, path[1:]); l > 0 {
				return l
			}
			return key.Line
		}
	}
	return 0
}

var (
	bcryptRe    = regexp.MustCompile(`^\$2[aby]\$\d{2}\$.{53}$`)
	hexColorRe  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	portRangeRe = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?$`)
)

var encryptionTokens = []string{"allow_incoming", "try_outgoing", "require", "require_RC4", "enable_retry", "prefer_plaintext"}

// validEncryption accepts "none" or a comma-separated token list.
func validEncryption(s string) bool {
	if strings.TrimSpace(s) == "none" {
		return true
	}
	for _, tok := range strings.Split(s, ",") {
		if !contains(encryptionTokens, strings.TrimSpace(tok)) {
			return false
		}
	}
	return len(strings.Split(s, ",")) > 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
