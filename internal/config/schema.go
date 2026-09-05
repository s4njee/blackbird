// Package config defines the YAML configuration schema, load/validate/save
// logic, and the annotated example written on first run.
//
// Tuning fields use pointers so that "key absent from YAML" (nil — leave the
// daemon untouched) is distinguishable from an explicit zero value. JSON tags
// match the YAML key names so the Settings API round-trips the same names.
package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of the YAML schema.
type Config struct {
	Server      Server      `yaml:"server" json:"server"`
	Log         Log         `yaml:"log" json:"log"`
	Auth        Auth        `yaml:"auth" json:"auth"`
	RTorrent    RTorrent    `yaml:"rtorrent" json:"rtorrent"`
	Poll        Poll        `yaml:"poll" json:"poll"`
	Trackers    Trackers    `yaml:"trackers" json:"trackers"`
	History     History     `yaml:"history" json:"history"`
	Stats       Stats       `yaml:"stats" json:"stats"`
	PortCheck   PortCheck   `yaml:"portcheck" json:"portcheck"`
	Network     Network     `yaml:"network" json:"network"`
	Directories Directories `yaml:"directories" json:"directories"`
	Automation  Automation  `yaml:"automation" json:"automation"`
	Seeding     Seeding     `yaml:"seeding" json:"seeding"`
	Schedule    Schedule    `yaml:"schedule" json:"schedule"`
	Labels      []Label     `yaml:"labels" json:"labels"`
	Volumes     []string    `yaml:"volumes" json:"volumes"`
	UI          UI          `yaml:"ui" json:"ui"`
	Tuning      Tuning      `yaml:"tuning" json:"tuning"`

	// Warnings carries non-fatal notices from the last Load, e.g. deprecated
	// key migrations. It is never serialized to YAML.
	Warnings []string `yaml:"-" json:"-"`
}

// DefaultTrafficDays bounds persisted transfer history.
const DefaultTrafficDays = 90

// DefaultPortCheckTimeout bounds one external probe round trip.
const DefaultPortCheckTimeout = 10 * time.Second

// DefaultIPFilterRefresh is the re-fetch/reload cadence for a URL blocklist
// when refresh_interval is unset.
const DefaultIPFilterRefresh = 24 * time.Hour

// Network holds daemon-network policy that Blackbird manages itself
// (PAR-5.6). The blocklist file is loaded into rTorrent's ipv4_filter table;
// the path is read by the daemon, so it must be visible to rTorrent as well
// as to Blackbird (which counts the rules for Settings).
type Network struct {
	// IPFilter is the blocklist source. Empty (no path and no URL) disables
	// the feature; nothing is loaded and no refresh runs.
	IPFilter IPFilter `yaml:"ipfilter" json:"ipfilter"`
}

// IPFilter points at a PeerGuardian P2P / eMule DAT blocklist: either a
// local file (path) or a remote list (url) downloaded to a local cache and
// then loaded. Exactly one of Path/URL may be set.
type IPFilter struct {
	// Path is a local blocklist file, e.g. "/data/filters/ipfilter.dat".
	// It must be absolute and readable by both Blackbird and rTorrent.
	Path string `yaml:"path" json:"path"`
	// URL is an http(s) address the list is fetched from (PeerGuardian,
	// eMule DAT, or gzipped either). Mutually exclusive with Path.
	URL string `yaml:"url" json:"url"`
	// RefreshInterval re-fetches a URL list and re-loads the daemon table on
	// this cadence (connect-time loads and manual reloads always apply).
	// Unset (0) means the 24h default for URL sources and no periodic
	// refresh for file sources.
	RefreshInterval time.Duration `yaml:"refresh_interval" json:"refresh_interval"`
}

// Enabled reports whether a blocklist source is configured.
func (f IPFilter) Enabled() bool {
	return strings.TrimSpace(f.Path) != "" || strings.TrimSpace(f.URL) != ""
}

// EffectiveRefresh returns the re-fetch/reload cadence: the configured
// interval, the 24h default for URL sources when unset, or 0 (no periodic
// refresh) for file sources without an explicit interval.
func (f IPFilter) EffectiveRefresh() time.Duration {
	if f.RefreshInterval > 0 {
		return f.RefreshInterval
	}
	if strings.TrimSpace(f.URL) != "" {
		return DefaultIPFilterRefresh
	}
	return 0
}

// PortCheck configures the user-initiated reachability probe (PAR-5.5): an
// external service asked whether the rTorrent listening port is reachable.
// Empty URL disables the check; nothing is ever probed automatically.
type PortCheck struct {
	// URL is the probe template with a {port} placeholder, e.g.
	// "https://probe.example/check?port={port}". Empty disables.
	URL string `yaml:"url" json:"url"`
	// Timeout bounds one probe round trip; 0 means the default.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

// EffectiveTimeout returns the probe timeout (0 = default).
func (p PortCheck) EffectiveTimeout() time.Duration {
	if p.Timeout <= 0 {
		return DefaultPortCheckTimeout
	}
	return p.Timeout
}

// Stats configures the statistics surfaces (PAR-5.2).
type Stats struct {
	// TrafficDays bounds persisted per-day/hourly transfer totals; older
	// buckets are dropped. Nil (absent) means the default; 0 disables
	// persistence (the tracker still counts the live session in memory).
	TrafficDays *int `yaml:"traffic_days" json:"traffic_days"`
}

// EffectiveTrafficDays returns the retention window in days (0 = disabled).
func (s Stats) EffectiveTrafficDays() int {
	if s.TrafficDays == nil {
		return DefaultTrafficDays
	}
	if *s.TrafficDays < 0 {
		return 0
	}
	return *s.TrafficDays
}

// DefaultSeedingSlot holds ratio-group assignment (custom1 is the label).
const DefaultSeedingSlot = "custom2"

// Seeding holds ratio-group and seeding-limit policy (PAR-4.2). Enforcement
// runs in Blackbird's poller against the list data (see the design note in
// deploy/README.md), not in rTorrent's group.seeding schedules.
type Seeding struct {
	// CustomSlot names the custom field holding group assignment
	// (custom2-custom5; custom1 is the label). Default "custom2".
	CustomSlot string `yaml:"custom_slot" json:"custom_slot"`
	// Groups are the ratio/seeding-limit policies, evaluated in order.
	Groups []SeedingGroup `yaml:"groups" json:"groups"`
}

// EffectiveSlot returns the configured slot, defaulting to custom2.
func (s Seeding) EffectiveSlot() string {
	switch s.CustomSlot {
	case "custom3", "custom4", "custom5":
		return s.CustomSlot
	default:
		return DefaultSeedingSlot
	}
}

// Seeding actions.
const (
	SeedingStop            = "stop"
	SeedingStopAndSetLabel = "stop_and_set_label"
	SeedingErase           = "erase"
	SeedingEraseWithData   = "erase_with_data"
)

// SeedingGroup is one ratio/seeding-limit policy: every positive condition
// is a trigger, and the first met condition fires the group's action once
// per torrent (see internal/seeding).
type SeedingGroup struct {
	// Name identifies the group for assignment and history. Required, unique.
	Name string `yaml:"name" json:"name"`
	// MinRatio seeds until at least this ratio (0 = unset).
	MinRatio float64 `yaml:"min_ratio" json:"min_ratio"`
	// MaxRatio is a hard ratio ceiling (0 = unset).
	MaxRatio float64 `yaml:"max_ratio" json:"max_ratio"`
	// MinUploadBytes seeds until at least this many uploaded bytes (0 = unset).
	MinUploadBytes int64 `yaml:"min_upload_bytes" json:"min_upload_bytes"`
	// MaxSeedingTime stops seeding after this long since finished (0 = unset).
	MaxSeedingTime time.Duration `yaml:"max_seeding_time" json:"max_seeding_time"`
	// Action runs on trigger: stop | stop_and_set_label | erase | erase_with_data.
	Action string `yaml:"action" json:"action"`
	// Label is applied by stop_and_set_label via d.custom1.set.
	Label string `yaml:"label" json:"label"`
}

// Automation holds event-driven rules (PAR-3.2), RSS intake (PAR-3.3), and
// unpack-on-completion (PAR-3.4).
type Automation struct {
	// OnComplete rules run in order; the first rule whose match conditions
	// all pass handles the torrent (first-match-wins, like firewall rules).
	OnComplete []CompletionRule `yaml:"on_complete" json:"on_complete"`
	// Rss feeds and filters auto-load torrents from RSS/Atom feeds.
	Rss RSSConfig `yaml:"rss" json:"rss"`
	// Unpack extracts archives from completed torrents.
	Unpack UnpackConfig `yaml:"unpack" json:"unpack"`
}

// CompletionRule is one automation rule: every non-empty match condition must
// pass, then the set actions run in a fixed order (set_label, add_tracker,
// move_to, webhook).
type CompletionRule struct {
	// Name identifies the rule in logs, the history, and toasts. Required.
	Name string `yaml:"name" json:"name"`

	// --- Match conditions. Empty/zero = condition ignored. ---

	// Label matches d.custom1 exactly, case-insensitively.
	Label string `yaml:"label" json:"label"`
	// Tracker matches the torrent's tracker host case-insensitively as a
	// substring, so "tracker.example" matches "tracker.example.org".
	Tracker string `yaml:"tracker" json:"tracker"`
	// NameRegex is a Go regular expression matched against the torrent name.
	NameRegex string `yaml:"name_regex" json:"name_regex"`
	// MinSize / MaxSize bound the torrent size in bytes; 0 = unbounded.
	MinSize int64 `yaml:"min_size" json:"min_size"`
	MaxSize int64 `yaml:"max_size" json:"max_size"`
	// Private filters on d.is_private; nil = any.
	Private *bool `yaml:"private" json:"private"`

	// --- Actions (at least one required). ---

	// SetLabel applies d.custom1.set.
	SetLabel string `yaml:"set_label" json:"set_label"`
	// AddTracker adds the announce URL in group 0 (public torrents only; the
	// move/label actions are skipped for private torrents, and adding a
	// tracker to a private torrent is refused daemon-side).
	AddTracker string `yaml:"add_tracker" json:"add_tracker"`
	// MoveTo moves the torrent's data via the PAR-2.2 move engine. The
	// destination must be inside the configured download roots.
	MoveTo string `yaml:"move_to" json:"move_to"`
	// Webhook POSTs a JSON completion payload to the URL.
	Webhook string `yaml:"webhook" json:"webhook"`
}

// HasActions reports whether the rule would do anything.
func (r CompletionRule) HasActions() bool {
	return r.SetLabel != "" || r.AddTracker != "" || r.MoveTo != "" || r.Webhook != ""
}

// DefaultRSSPollInterval is the feed poll cadence when a feed sets no
// poll_interval.
const DefaultRSSPollInterval = 15 * time.Minute

// RSSConfig holds RSS/Atom intake (PAR-3.3): feeds to poll and filters that
// auto-load matching items.
type RSSConfig struct {
	Feeds   []RSSFeed   `yaml:"feeds" json:"feeds"`
	Filters []RSSFilter `yaml:"filters" json:"filters"`
}

// RSSFeed is one polled RSS/Atom feed.
type RSSFeed struct {
	// Name identifies the feed in filters, logs, and the UI. Required, unique.
	Name string `yaml:"name" json:"name"`
	// URL is the feed's http(s) address. Required.
	URL string `yaml:"url" json:"url"`
	// PollInterval overrides the feed poll cadence; default 15m.
	PollInterval time.Duration `yaml:"poll_interval" json:"poll_interval"`
	// Label is applied via d.custom1.set on auto-loaded items.
	Label string `yaml:"label" json:"label"`
	// Destination is applied via d.directory.set on auto-loaded items.
	Destination string `yaml:"destination" json:"destination"`
	// Cookies is the Cookie header sent for feed fetches and enclosure
	// downloads (private trackers). Secret: redacted in logs and the API.
	Cookies string `yaml:"cookies" json:"cookies"`
	// Headers are extra request headers for feed fetches and enclosure
	// downloads (e.g. Authorization). Secret: redacted in logs and the API.
	Headers map[string]string `yaml:"headers" json:"headers"`
}

// EffectivePollInterval returns the feed's poll override or the default.
func (f RSSFeed) EffectivePollInterval() time.Duration {
	if f.PollInterval <= 0 {
		return DefaultRSSPollInterval
	}
	return f.PollInterval
}

// RSSFilter auto-loads feed items whose conditions all pass. Filters are
// evaluated in order per item; the first matching filter loads the item.
type RSSFilter struct {
	// Name identifies the filter in the match history and UI. Required.
	Name string `yaml:"name" json:"name"`
	// Feed restricts the filter to one feed by name; empty = all feeds.
	Feed string `yaml:"feed" json:"feed"`
	// TitleRegex is a Go regular expression matched against the item title.
	TitleRegex string `yaml:"title_regex" json:"title_regex"`
	// Category matches an item category case-insensitively as a substring.
	Category string `yaml:"category" json:"category"`
	// MinSize / MaxSize bound the enclosure length in bytes; 0 = unbounded.
	// Items with an unknown length never match when either bound is set.
	MinSize int64 `yaml:"min_size" json:"min_size"`
	MaxSize int64 `yaml:"max_size" json:"max_size"`
	// Label overrides the feed default label on load.
	Label string `yaml:"label" json:"label"`
	// Destination overrides the feed default destination on load.
	Destination string `yaml:"destination" json:"destination"`
	// Start starts the loaded torrent immediately; default true.
	Start *bool `yaml:"start" json:"start"`
}

// Starts reports whether the filter should start loaded torrents.
func (f RSSFilter) Starts() bool { return f.Start == nil || *f.Start }

// DefaultUnpackWorkers bounds concurrent extractions; DefaultUnpackTimeout
// caps one torrent's total extraction time.
const (
	DefaultUnpackWorkers = 2
	DefaultUnpackTimeout = 30 * time.Minute
)

// UnpackConfig holds unpack-on-completion rules (PAR-3.4).
type UnpackConfig struct {
	// Workers bounds concurrent extractions (low-priority background work).
	// Default 2.
	Workers int `yaml:"workers" json:"workers"`
	// Timeout caps one torrent's total extraction time. Default 30m.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
	// Rules selects which completed torrents are extracted. First match wins.
	Rules []UnpackRule `yaml:"rules" json:"rules"`
}

// EffectiveWorkers returns the worker count, defaulting and clamping to
// a sane range.
func (c UnpackConfig) EffectiveWorkers() int {
	if c.Workers <= 0 {
		return DefaultUnpackWorkers
	}
	if c.Workers > 8 {
		return 8
	}
	return c.Workers
}

// EffectiveTimeout returns the per-torrent extraction cap.
func (c UnpackConfig) EffectiveTimeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultUnpackTimeout
	}
	return c.Timeout
}

// UnpackRule extracts archives from a completed torrent.
type UnpackRule struct {
	// Name identifies the rule in logs and history. Required, unique.
	Name string `yaml:"name" json:"name"`
	// Label matches the torrent's (post-action) label exactly,
	// case-insensitively; empty matches every completed torrent.
	Label string `yaml:"label" json:"label"`
	// Destination selects where files land: empty extracts in place (next
	// to each archive), otherwise it is an extract root and files land in
	// <root>/<torrent name>. Must be absolute and inside the download roots.
	Destination string `yaml:"destination" json:"destination"`
	// DeleteArchives removes extracted archives (including multi-part
	// siblings) after a successful extraction.
	DeleteArchives bool `yaml:"delete_archives" json:"delete_archives"`
}

// Matches reports whether the rule applies to a torrent label.
func (r UnpackRule) Matches(label string) bool {
	return r.Label == "" || strings.EqualFold(r.Label, label)
}

// Weekdays in grid order, Monday first.
var ScheduleWeekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Schedule holds the bandwidth scheduler (PAR-4.3): named limit profiles
// painted onto a 7×24 grid of profile names, evaluated in an explicit time
// zone.
type Schedule struct {
	// Timezone is an IANA name (e.g. "America/Chicago"); empty or "Local"
	// means the server's local zone.
	Timezone string `yaml:"timezone" json:"timezone"`
	// Bandwidth profiles and the weekly grid selecting them.
	Bandwidth BandwidthSchedule `yaml:"bandwidth" json:"bandwidth"`
}

// BandwidthSchedule paints profiles onto the week. Each day maps to 24
// profile names (hour 0-23); an empty cell leaves the daemon limits alone.
type BandwidthSchedule struct {
	Profiles []BandwidthProfile  `yaml:"profiles" json:"profiles"`
	Grid     map[string][]string `yaml:"grid" json:"grid"`
}

// BandwidthProfile is one named set of global and channel limits in KB/s
// (0 = unlimited), applied when its grid cells are active.
type BandwidthProfile struct {
	// Name identifies the profile in the grid. Required, unique.
	Name string `yaml:"name" json:"name"`
	// Color drives the scheduler grid swatch (#rrggbb).
	Color string `yaml:"color" json:"color"`
	// DownKB caps global download in KB/s; 0 = unlimited.
	DownKB int64 `yaml:"down_kb" json:"down_kb"`
	// UpKB caps global upload in KB/s; 0 = unlimited.
	UpKB int64 `yaml:"up_kb" json:"up_kb"`
	// Throttles caps named channels (created on the daemon if missing).
	Throttles []ThrottleChannel `yaml:"throttles" json:"throttles"`
}

type Server struct {
	// Listen is the address the HTTP server binds, e.g. ":8222".
	Listen string `yaml:"listen" json:"listen"`
	// BaseURL is the URL path prefix the app is served under ("/" default).
	BaseURL string `yaml:"base_url" json:"base_url"`
	// TrustedOrigins are extra browser origins allowed to make
	// state-changing requests and open the WebSocket, e.g.
	// "http://localhost:5173" for the Vite dev server. Same-origin is
	// always allowed; this list is only for genuinely separate origins.
	TrustedOrigins []string `yaml:"trusted_origins" json:"trusted_origins"`
	// TrustedProxies are CIDRs (or bare IPs) whose X-Forwarded-For header
	// is believed when attributing a request to a client address. Empty
	// (the default) means the header is ignored entirely: a client that is
	// not a known proxy must never be able to choose its own identity.
	TrustedProxies []string `yaml:"trusted_proxies" json:"trusted_proxies"`
	// AllowExecute enables POST /api/settings/execute, the Advanced tab's
	// raw XML-RPC escape hatch. Off by default: it reaches the daemon's
	// full command surface, so it is opt-in per deployment.
	AllowExecute bool `yaml:"allow_execute" json:"allow_execute"`
}

type Log struct {
	// Level is one of debug, info, warn, error.
	Level string `yaml:"level" json:"level"`
}

// LogLevelRaw returns the configured level string as-is.
func (c *Config) LogLevelRaw() string { return c.Log.Level }

// LogLevel parses the level for slog.
func (c *Config) LogLevel() slog.Level {
	switch c.Log.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type Auth struct {
	Username     string `yaml:"username" json:"username"`
	PasswordHash string `yaml:"password_hash" json:"-"` // bcrypt; never serialized to clients
}

type RTorrent struct {
	// SCGI endpoint: unix:///path/to/socket or tcp://host:port.
	SCGI string `yaml:"scgi" json:"scgi"`
	// Timeout for a single SCGI/XML-RPC call.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
	// MaxResponseBytes caps one SCGI response (headers + payload); 0 means
	// the 64MB default. Exceeding it aborts the read with a typed error and
	// a reconnect instead of growing memory without bound (PERF-6.3).
	MaxResponseBytes int64 `yaml:"max_response_bytes" json:"max_response_bytes"`
}

// DefaultPollMaxInterval caps the adaptive idle poll stretch (PERF-6.3).
const DefaultPollMaxInterval = 30 * time.Second

type Poll struct {
	// Interval is the full torrent-list poll period.
	Interval time.Duration `yaml:"interval" json:"interval"`
	// DetailInterval refreshes focused torrents' files/peers/trackers.
	DetailInterval time.Duration `yaml:"detail_interval" json:"detail_interval"`
	// VolumeInterval refreshes statfs data for configured volumes.
	VolumeInterval time.Duration `yaml:"volume_interval" json:"volume_interval"`
	// MaxInterval caps the adaptive stretch applied when no visible client
	// is connected: idle cycles back off toward this. 0 means the 30s
	// default; the first active client snaps back to Interval (PERF-6.3).
	MaxInterval time.Duration `yaml:"max_interval" json:"max_interval"`
}

// EffectiveMaxInterval returns the idle stretch cap (0 = default).
func (p Poll) EffectiveMaxInterval() time.Duration {
	if p.MaxInterval <= 0 {
		return DefaultPollMaxInterval
	}
	return p.MaxInterval
}

// Tracker ramp defaults. rTorrent enables trackers per torrent, so a large
// session coming back after a daemon restart would otherwise announce every
// tracker at once. Each in-flight announce holds a file descriptor, and
// rTorrent raises a fatal resource_error rather than degrading when it runs
// out, so the appliance spreads the announces over time. 25 trackers every
// 2s ramps a 2,600-tracker session up over roughly three and a half minutes
// while keeping concurrent announces far below any sane nofile limit.
const (
	DefaultTrackerRampBatch    = 25
	DefaultTrackerRampInterval = 2 * time.Second
)

// Trackers configures the connect-time tracker ramp. rTorrent's own config
// disables every tracker as the session loads (torrents may still be
// checking), and Blackbird re-enables them gradually once it connects.
type Trackers struct {
	// EnableOnConnect ramps trackers back up after each successful daemon
	// connection. Nil (unset) means enabled; set it to false to keep the
	// session's announces off, which leaves the daemon in the state
	// rtorrent.rc booted it into.
	EnableOnConnect *bool `yaml:"enable_on_connect" json:"enableOnConnect"`
	// RampBatch is how many trackers are enabled per step; 0 = default.
	RampBatch int `yaml:"ramp_batch" json:"rampBatch"`
	// RampInterval is the pause between steps; 0 = default.
	RampInterval time.Duration `yaml:"ramp_interval" json:"rampInterval"`
}

// RampEnabled reports whether the connect-time ramp runs (default true).
func (t Trackers) RampEnabled() bool {
	return t.EnableOnConnect == nil || *t.EnableOnConnect
}

// EffectiveRampBatch returns the per-step tracker count (0 = default).
func (t Trackers) EffectiveRampBatch() int {
	if t.RampBatch > 0 {
		return t.RampBatch
	}
	return DefaultTrackerRampBatch
}

// EffectiveRampInterval returns the pause between steps (0 = default).
func (t Trackers) EffectiveRampInterval() time.Duration {
	if t.RampInterval > 0 {
		return t.RampInterval
	}
	return DefaultTrackerRampInterval
}

// History bounds the per-torrent Logger/action-log retention (PAR-2.5) and
// the global History view ring (PAR-5.3).
type History struct {
	// RecorderBytes bounds the durable flight recorder; 0 = 16 MiB.
	// Byte-bound changes take effect on restart; retention uses ActionLogRetention.
	RecorderBytes int64 `yaml:"recorder_bytes" json:"recorder_bytes"`
	// ActionLogEntries caps the per-torrent Blackbird action-log entries kept.
	ActionLogEntries int `yaml:"action_log_entries" json:"action_log_entries"`
	// ActionLogRetention ages out action-log entries older than this window.
	ActionLogRetention time.Duration `yaml:"action_log_retention" json:"action_log_retention"`
	// MessageEntries caps the per-torrent d.message history entries kept.
	MessageEntries int `yaml:"message_entries" json:"message_entries"`
	// GlobalEntries caps the global History-view recency ring; 0 (absent)
	// means the default.
	GlobalEntries int `yaml:"global_entries" json:"global_entries"`
}

// DefaultGlobalEntries caps the History-view ring when unconfigured.
const DefaultGlobalEntries = 5000

// EffectiveGlobalEntries returns the global ring cap.
func (h History) EffectiveGlobalEntries() int {
	if h.GlobalEntries <= 0 {
		return DefaultGlobalEntries
	}
	return h.GlobalEntries
}

type Directories struct {
	// Default is the default download destination (directory.default.set is
	// NOT touched by this — it is a local default for adding torrents).
	Default string `yaml:"default" json:"default"`
	// PerLabel maps label name → destination, applied when a label is
	// picked in the add-torrent flow.
	PerLabel map[string]string `yaml:"per_label" json:"per_label"`
	// Watch is the list of watched directories (PAR-3.1): .torrent files
	// dropped into each path are loaded into the session with the entry's
	// label/destination/start options. WatchLabel is retained for one release
	// as a read-compatible alias: Load migrates it into the first list entry
	// with a deprecation warning, and Save clears it. The deprecated scalar
	// directories.watch form ("/path") is still accepted and migrates to a
	// single entry.
	Watch      WatchList `yaml:"watch" json:"watch"`
	WatchLabel string    `yaml:"watch_label" json:"watch_label"`
	// Session is the rTorrent session directory. When readable, Blackbird
	// reads <infohash>.torrent files there to backfill comment/created-by
	// metadata on the General tab (PAR-2.5). In Compose it is the shared
	// /data/session volume; rTorrent's own location is fixed at daemon start.
	Session string `yaml:"session" json:"session"`
	// OpenURLTemplate, when set, turns "Open directory" into a link: the
	// torrent's base path is URL-escaped and substituted for {path} (or
	// appended when no placeholder is present), then opened in a new tab.
	OpenURLTemplate string `yaml:"open_url_template" json:"open_url_template"`
}

// WatchDir is one auto-load directory (PAR-3.1): every .torrent dropped into
// Path is loaded into rTorrent, then renamed to .loaded or deleted per
// DeleteAfterLoad.
type WatchDir struct {
	// Path is the watched directory. Required and must be an absolute path.
	Path string `yaml:"path" json:"path"`
	// Label, when non-empty, is applied via d.custom1.set on load.
	Label string `yaml:"label" json:"label"`
	// Destination, when non-empty, is applied via d.directory.set on load.
	Destination string `yaml:"destination" json:"destination"`
	// Start starts the loaded torrent immediately (load.*_start); default true.
	Start *bool `yaml:"start" json:"start"`
	// DeleteAfterLoad removes the .torrent file instead of renaming it to
	// <name>.loaded; default false.
	DeleteAfterLoad bool `yaml:"delete_after_load" json:"delete_after_load"`
	// PollInterval overrides the watch poll cadence (network filesystems that
	// cannot deliver fsnotify events fall back to polling); default 5s.
	PollInterval time.Duration `yaml:"poll_interval" json:"poll_interval"`

	// scalarLegacy marks an entry decoded from the deprecated scalar
	// directories.watch form so Load can emit the migration warning. It is
	// never serialized.
	scalarLegacy bool `yaml:"-" json:"-"`
}

// Starts reports whether the entry should start loaded torrents.
func (w WatchDir) Starts() bool { return w.Start == nil || *w.Start }

// EffectivePollInterval returns the entry's poll override or the default.
func (w WatchDir) EffectivePollInterval(def time.Duration) time.Duration {
	if w.PollInterval <= 0 {
		return def
	}
	return w.PollInterval
}

// WatchList is the directories.watch value. It accepts both the PAR-3.1 list
// shape and the deprecated scalar form (watch: "/path"), which decodes to a
// single entry so pre-migration config files still load.
type WatchList []WatchDir

// UnmarshalYAML implements yaml.Unmarshaler.
func (w *WatchList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var path string
		if err := node.Decode(&path); err != nil {
			return err
		}
		if path == "" {
			*w = nil
			return nil
		}
		*w = WatchList{{Path: path, scalarLegacy: true}}
		return nil
	}
	var list []WatchDir
	if err := node.Decode(&list); err != nil {
		return err
	}
	*w = WatchList(list)
	return nil
}

// MigrateWatch upgrades the deprecated scalar directories.watch / watch_label
// keys to list entries (PAR-3.1). It returns human-readable deprecation
// warnings for the caller to surface. The legacy keys are cleared so the next
// save removes them from YAML.
func (d *Directories) MigrateWatch() []string {
	var warnings []string
	if d.WatchLabel != "" {
		migrated := false
		for i := range d.Watch {
			if d.Watch[i].Label == "" {
				d.Watch[i].Label = d.WatchLabel
				migrated = true
				break
			}
		}
		if migrated {
			warnings = append(warnings, "directories.watch_label is deprecated: folded into the first directories.watch entry's label")
		} else if len(d.Watch) > 0 {
			warnings = append(warnings, "directories.watch_label is deprecated and had no directories.watch entry without a label; it was dropped")
		} else {
			warnings = append(warnings, "directories.watch_label is deprecated and had no directories.watch entry to apply to; it was dropped")
		}
		d.WatchLabel = ""
	}
	for i := range d.Watch {
		if d.Watch[i].scalarLegacy {
			d.Watch[i].scalarLegacy = false
			warnings = append(warnings, fmt.Sprintf("directories.watch (scalar %q) is deprecated: use a list of entries with path/label/destination/start/delete_after_load/poll_interval", d.Watch[i].Path))
		}
	}
	return warnings
}

type Label struct {
	Name  string `yaml:"name" json:"name"`
	Color string `yaml:"color" json:"color"` // #rrggbb, drives sidebar squares and chips
}

type UI struct {
	// Accent overrides the active theme's default accent (#35418f on
	// Blackbird Dark; alternates #f59e0b, #e5484d, #2f9dff, #3fb950 or any
	// #rrggbb). Empty means the theme default.
	Accent string `yaml:"accent" json:"accent"`
	// Theme is the operator default theme id
	// (dark|light|midnight|contrast|classic|system); empty means dark;
	// browsers with a saved choice ignore it.
	Theme string `yaml:"theme" json:"theme"`
	// Columns is the operator's default table layout. Browser-local layouts take
	// precedence, while this value is used when no local layout exists.
	Columns []Column `yaml:"columns" json:"columns"`
	// VisibleColumns is retained for one release as a read-compatible alias.
	// Load migrates it into Columns and clears it on the next save.
	VisibleColumns []string `yaml:"visible_columns" json:"visible_columns"`
	Sort           Sort     `yaml:"sort" json:"sort"`
	DateFormat     string   `yaml:"date_format" json:"date_format"`
	RateFormat     string   `yaml:"rate_format" json:"rate_format"`
	PollInterval   string   `yaml:"poll_interval" json:"poll_interval"`
	// SavedFilters seeds browser-local sidebar filters when no local filters
	// have been saved yet. Browser changes never overwrite this operator default.
	SavedFilters []SavedFilter `yaml:"saved_filters" json:"saved_filters"`
}

// SavedFilter is an operator-provided default for a named, pinned search.
type SavedFilter struct {
	Name    string `yaml:"name" json:"name"`
	Query   string `yaml:"query" json:"query"`
	Status  string `yaml:"status" json:"status"`
	Label   string `yaml:"label" json:"label"`
	Tracker string `yaml:"tracker" json:"tracker"`
}

// Column describes one table column in the persisted operator layout.
type Column struct {
	Key     string `yaml:"key" json:"key"`
	Visible bool   `yaml:"visible" json:"visible"`
	Width   int    `yaml:"width" json:"width"`
}

// MigrateColumns upgrades the pre-PAR-1.2 visible_columns setting to the
// richer layout shape. The legacy field is cleared so the next save removes
// the inert key from YAML.
func (u *UI) MigrateColumns() {
	if len(u.Columns) == 0 && len(u.VisibleColumns) > 0 {
		u.Columns = make([]Column, 0, len(u.VisibleColumns))
		for _, key := range u.VisibleColumns {
			u.Columns = append(u.Columns, Column{Key: key, Visible: true})
		}
	}
	u.VisibleColumns = nil
}

type Sort struct {
	Column string `yaml:"column" json:"column"` // e.g. "added"
	Dir    string `yaml:"dir" json:"dir"`       // asc | desc
	// Keys supports a primary and optional secondary sort. Column/Dir remain
	// read-compatible with the original single-key configuration.
	Keys []SortKey `yaml:"keys" json:"keys"`
}

type SortKey struct {
	Column string `yaml:"column" json:"column"`
	Dir    string `yaml:"dir" json:"dir"`
}

// Tuning declares rTorrent runtime settings applied on (re)connect via their
// *.set methods. Nil fields are left untouched on the daemon. Setter methods
// and validation live in internal/tuning; the rtorrent key for each field is
// documented in internal/config/example.yml and tuning.Entries.
type Tuning struct {
	// Connection & network
	PortRange      *string `yaml:"port_range" json:"port_range"`             // network.port_range
	PortRandom     *bool   `yaml:"port_random" json:"port_random"`           // network.port_random
	Encryption     *string `yaml:"encryption" json:"encryption"`             // protocol.encryption
	DHTMode        *string `yaml:"dht_mode" json:"dht_mode"`                 // dht.mode
	DHTPort        *int    `yaml:"dht_port" json:"dht_port"`                 // dht.port
	UseUDP         *bool   `yaml:"use_udp" json:"use_udp"`                   // trackers.use_udp
	PEX            *bool   `yaml:"pex" json:"pex"`                           // protocol.pex
	LocalAddress   *string `yaml:"local_address" json:"local_address"`       // network.local_address
	BindAddress    *string `yaml:"bind_address" json:"bind_address"`         // network.bind_address
	HTTPMaxOpen    *int    `yaml:"http_max_open" json:"http_max_open"`       // network.http.max_open
	MaxOpenSockets *int    `yaml:"max_open_sockets" json:"max_open_sockets"` // network.max_open_sockets
	MaxOpenFiles   *int    `yaml:"max_open_files" json:"max_open_files"`     // network.max_open_files

	// Peer limits
	MinPeersNormal *int `yaml:"min_peers_normal" json:"min_peers_normal"` // throttle.min_peers.normal
	MaxPeersNormal *int `yaml:"max_peers_normal" json:"max_peers_normal"` // throttle.max_peers.normal
	MinPeersSeeded *int `yaml:"min_peers_seeded" json:"min_peers_seeded"` // throttle.min_peers.seeded
	MaxPeersSeeded *int `yaml:"max_peers_seeded" json:"max_peers_seeded"` // throttle.max_peers.seeded
	MaxUploads     *int `yaml:"max_uploads" json:"max_uploads"`           // throttle.max_uploads (per torrent)

	// Bandwidth (KB/s; 0 = unlimited)
	GlobalDownRateKB *int64 `yaml:"global_down_rate_kb" json:"global_down_rate_kb"` // throttle.global_down.max_rate
	GlobalUpRateKB   *int64 `yaml:"global_up_rate_kb" json:"global_up_rate_kb"`     // throttle.global_up.max_rate

	// Named throttle channels (PAR-4.1): each entry creates `throttle.up`
	// and `throttle.down` limits applied to torrents via d.throttle_name.
	// A nil slice leaves existing daemon channels untouched; a non-nil
	// (even empty) slice replaces the channel list.
	Throttles []ThrottleChannel `yaml:"throttles" json:"throttles"`

	// Queue: active download/upload slots globally
	MaxDownloadsGlobal *int `yaml:"max_downloads_global" json:"max_downloads_global"` // throttle.max_downloads.global
	MaxUploadsGlobal   *int `yaml:"max_uploads_global" json:"max_uploads_global"`     // throttle.max_uploads.global
}

// ThrottleChannel is one named throttle group: up/down caps in KB/s
// (0 = unlimited) assignable per torrent.
type ThrottleChannel struct {
	// Name identifies the channel; required, unique, never "NULL".
	Name string `yaml:"name" json:"name"`
	// UpKB caps upload in KB/s; 0 = unlimited.
	UpKB int64 `yaml:"up_kb" json:"up_kb"`
	// DownKB caps download in KB/s; 0 = unlimited.
	DownKB int64 `yaml:"down_kb" json:"down_kb"`
}
