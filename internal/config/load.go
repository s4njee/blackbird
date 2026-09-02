package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

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
		UI: UI{Accent: "#35418f", Sort: Sort{Column: "added", Dir: "desc"}, DateFormat: "local", RateFormat: "binary", PollInterval: "2s"},
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
	if cfg.Poll.Interval <= 0 {
		add([]string{"poll", "interval"}, "must be positive (e.g. 2s)")
	}
	if cfg.Poll.DetailInterval <= 0 {
		add([]string{"poll", "detail_interval"}, "must be positive (e.g. 1s)")
	}
	if cfg.Poll.VolumeInterval <= 0 {
		add([]string{"poll", "volume_interval"}, "must be positive (e.g. 30s)")
	}
	if h := cfg.Auth.PasswordHash; h != "" && !bcryptRe.MatchString(h) {
		add([]string{"auth", "password_hash"}, "must be a bcrypt hash ($2a$/$2b$/$2y$ followed by 53 characters)")
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
	return errs
}

// Save writes the config atomically (temp file + rename). Comments in the
// source file are NOT preserved — YAML is re-marshalled from the schema.
func Save(path string, cfg *Config) error {
	cfg.UI.MigrateColumns()
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
