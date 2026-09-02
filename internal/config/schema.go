// Package config defines the YAML configuration schema, load/validate/save
// logic, and the annotated example written on first run.
//
// Tuning fields use pointers so that "key absent from YAML" (nil — leave the
// daemon untouched) is distinguishable from an explicit zero value. JSON tags
// match the YAML key names so the Settings API round-trips the same names.
package config

import (
	"log/slog"
	"time"
)

// Config is the root of the YAML schema.
type Config struct {
	Server      Server      `yaml:"server" json:"server"`
	Log         Log         `yaml:"log" json:"log"`
	Auth        Auth        `yaml:"auth" json:"auth"`
	RTorrent    RTorrent    `yaml:"rtorrent" json:"rtorrent"`
	Poll        Poll        `yaml:"poll" json:"poll"`
	Directories Directories `yaml:"directories" json:"directories"`
	Labels      []Label     `yaml:"labels" json:"labels"`
	Volumes     []string    `yaml:"volumes" json:"volumes"`
	UI          UI          `yaml:"ui" json:"ui"`
	Tuning      Tuning      `yaml:"tuning" json:"tuning"`
}

type Server struct {
	// Listen is the address the HTTP server binds, e.g. ":8222".
	Listen string `yaml:"listen" json:"listen"`
	// BaseURL is the URL path prefix the app is served under ("/" default).
	BaseURL string `yaml:"base_url" json:"base_url"`
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
}

type Poll struct {
	// Interval is the full torrent-list poll period.
	Interval time.Duration `yaml:"interval" json:"interval"`
	// DetailInterval refreshes focused torrents' files/peers/trackers.
	DetailInterval time.Duration `yaml:"detail_interval" json:"detail_interval"`
	// VolumeInterval refreshes statfs data for configured volumes.
	VolumeInterval time.Duration `yaml:"volume_interval" json:"volume_interval"`
}

type Directories struct {
	// Default is the default download destination (directory.default.set is
	// NOT touched by this — it is a local default for adding torrents).
	Default string `yaml:"default" json:"default"`
	// PerLabel maps label name → destination, applied when a label is
	// picked in the add-torrent flow.
	PerLabel map[string]string `yaml:"per_label" json:"per_label"`
	// Watch is an optional directory for a future auto-load watcher. It is
	// persisted here so the Settings UI has one YAML source of truth.
	Watch      string `yaml:"watch" json:"watch"`
	WatchLabel string `yaml:"watch_label" json:"watch_label"`
	// Session is informational in the current daemon integration; rTorrent's
	// session location is normally fixed at daemon startup.
	Session string `yaml:"session" json:"session"`
}

type Label struct {
	Name  string `yaml:"name" json:"name"`
	Color string `yaml:"color" json:"color"` // #rrggbb, drives sidebar squares and chips
}

type UI struct {
	// Accent: #f59e0b default; alternates #e5484d, #2f9dff, #3fb950 or any
	// #rrggbb.
	Accent string `yaml:"accent" json:"accent"`
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

	// Queue: active download/upload slots globally
	MaxDownloadsGlobal *int `yaml:"max_downloads_global" json:"max_downloads_global"` // throttle.max_downloads.global
	MaxUploadsGlobal   *int `yaml:"max_uploads_global" json:"max_uploads_global"`     // throttle.max_uploads.global
}
