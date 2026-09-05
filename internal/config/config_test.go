package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsWhenMissingAndWritesExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != ":8222" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Poll.Interval != 2*time.Second {
		t.Fatalf("poll interval = %v", cfg.Poll.Interval)
	}
	// First run writes the annotated example to the configured path.
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("example not written: %v", err)
	}
	if !strings.Contains(string(written), "annotated configuration reference") {
		t.Fatalf("written file is not the annotated example: %q", string(written)[:80])
	}
}

func TestLoadMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	minimal := `
rtorrent:
  scgi: "tcp://10.0.0.5:5001"
auth:
  username: op
  password_hash: "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
`
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RTorrent.SCGI != "tcp://10.0.0.5:5001" {
		t.Fatalf("scgi = %q", cfg.RTorrent.SCGI)
	}
	if cfg.Auth.Username != "op" || cfg.Auth.PasswordHash == "" {
		t.Fatalf("auth = %+v", cfg.Auth)
	}
	if cfg.Poll.Interval != 2*time.Second {
		t.Fatalf("default poll interval lost: %v", cfg.Poll.Interval)
	}
}

// TestUIPollIntervalDeprecatedWarns proves the POL-8.4 migration: a set
// ui.poll_interval loads fine (read-compatible) but warns that it is ignored.
func TestUIPollIntervalDeprecatedWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("ui:\n  poll_interval: 5s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.PollInterval != "5s" {
		t.Fatalf("poll_interval was not preserved: %q", cfg.UI.PollInterval)
	}
	found := false
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "ui.poll_interval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ui.poll_interval warning in %+v", cfg.Warnings)
	}
}

func TestVisibleColumnsMigratesToLayout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	legacy := "ui:\n  visible_columns: [name, status]\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.UI.Columns) != 2 || cfg.UI.Columns[0].Key != "name" || !cfg.UI.Columns[1].Visible {
		t.Fatalf("columns migration = %+v", cfg.UI.Columns)
	}
	if cfg.UI.VisibleColumns != nil {
		t.Fatalf("legacy visible_columns was not cleared: %+v", cfg.UI.VisibleColumns)
	}
}

func TestLoadFullSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	full := `
server:
  listen: "127.0.0.1:9000"
  base_url: "/console/"
log:
  level: debug
rtorrent:
  scgi: "unix:///tmp/rt.sock"
  timeout: 5s
poll:
  interval: 3s
  detail_interval: 2s
  volume_interval: 60s
directories:
  default: /mnt/data/downloads
  per_label:
    iso: /mnt/data/iso
labels:
  - name: iso
    color: "#f59e0b"
  - name: kernel
    color: "#22d3ee"
volumes:
  - /mnt/data
ui:
  accent: "#2f9dff"
  visible_columns: [name, size, done, status]
  saved_filters:
    - name: Linux ISOs
      query: "path:linux size>1gb"
      status: downloading
  sort:
    column: name
    dir: asc
    keys:
      - column: name
        dir: asc
      - column: hash
        dir: desc
tuning:
  port_range: "51413-51420"
  port_random: true
  encryption: "require,require_RC4"
  dht_mode: "auto"
  dht_port: 6881
  use_udp: true
  pex: false
  local_address: "10.0.0.2"
  bind_address: "10.0.0.2"
  http_max_open: 64
  max_open_sockets: 2048
  max_open_files: 2048
  min_peers_normal: 40
  max_peers_normal: 250
  min_peers_seeded: 5
  max_peers_seeded: 80
  max_uploads: 16
  max_uploads_global: 100
  max_downloads_global: 40
  global_down_rate_kb: 0
  global_up_rate_kb: 20480
`
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:9000" || cfg.Server.BaseURL != "/console/" {
		t.Fatalf("server = %+v", cfg.Server)
	}
	if cfg.Tuning.PortRange == nil || *cfg.Tuning.PortRange != "51413-51420" {
		t.Fatalf("port_range = %+v", cfg.Tuning.PortRange)
	}
	if cfg.Tuning.PEX == nil || *cfg.Tuning.PEX {
		t.Fatalf("pex = %+v", cfg.Tuning.PEX)
	}
	if cfg.Tuning.GlobalUpRateKB == nil || *cfg.Tuning.GlobalUpRateKB != 20480 {
		t.Fatalf("global_up_rate_kb = %+v", cfg.Tuning.GlobalUpRateKB)
	}
	if cfg.Tuning.DHTMode == nil || *cfg.Tuning.DHTMode != "auto" {
		t.Fatalf("dht_mode = %+v", cfg.Tuning.DHTMode)
	}
	// Absent keys stay nil — the daemon must not be touched for them.
	if cfg.Tuning.MaxOpenFiles == nil || *cfg.Tuning.MaxOpenFiles != 2048 {
		t.Fatalf("max_open_files = %+v", cfg.Tuning.MaxOpenFiles)
	}
	if cfg.UI.Sort.Column != "name" || cfg.UI.Sort.Dir != "asc" {
		t.Fatalf("sort = %+v", cfg.UI.Sort)
	}
	if len(cfg.UI.Sort.Keys) != 2 || cfg.UI.Sort.Keys[1].Column != "hash" || cfg.UI.Sort.Keys[1].Dir != "desc" {
		t.Fatalf("sort keys = %+v", cfg.UI.Sort.Keys)
	}
	if len(cfg.UI.SavedFilters) != 1 || cfg.UI.SavedFilters[0].Name != "Linux ISOs" || cfg.UI.SavedFilters[0].Status != "downloading" {
		t.Fatalf("saved filters = %+v", cfg.UI.SavedFilters)
	}
}

func TestLoadInvalidUnknownKeyHasLineNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := "server:\n  listen: \":8222\"\n  listen_addr: \":8223\"\n"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "line 3") {
		t.Fatalf("error missing line number: %v", msg)
	}
	if !strings.Contains(msg, "listen_addr") {
		t.Fatalf("error missing bad key name: %v", msg)
	}
}

func TestLoadInvalidTypeHasLineNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := "poll:\n  interval: soon\n"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error missing line number: %v", err)
	}
}

func TestValidateSemanticErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := `
rtorrent:
  scgi: "http://nope"
labels:
  - name: iso
    color: "orange"
  - name: iso
    color: "#ff0000"
tuning:
  dht_mode: "maybe"
  dht_port: 99999
  global_up_rate_kb: -5
  encryption: "require_bogus"
ui:
  accent: "amber"
`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"rtorrent.scgi", "labels.[0].color", "labels.[1].name",
		"tuning.dht_mode", "tuning.dht_port", "tuning.global_up_rate_kb",
		"tuning.encryption", "ui.accent",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestValidateAutomationRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
automation:
  on_complete:
    - name: "no-actions"
      label: "iso"
    - name: "bad-regex"
      name_regex: "(unclosed"
      set_label: "x"
    - name: "bad-sizes"
      min_size: 100
      max_size: 50
      webhook: "not-a-url"
    - name: "relative-move"
      move_to: "relative/path"
      set_label: "x"
    - label: "missing-name"
      set_label: "x"
`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"automation.on_complete.[0]", "must define at least one action",
		"automation.on_complete.[1].name_regex",
		"automation.on_complete.[2].max_size",
		"automation.on_complete.[2].webhook",
		"automation.on_complete.[3].move_to",
		"automation.on_complete.[4].name",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}

	// A valid rule list loads cleanly and round-trips through Save.
	good := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
automation:
  on_complete:
    - name: "tv"
      label: "tv"
      name_regex: "^Show\\.S\\d\\d"
      private: true
      min_size: 1
      max_size: 20000000000
      set_label: "tv-done"
      move_to: "/mnt/data/tv"
      add_tracker: "udp://tracker.example:1337/announce"
      webhook: "https://hooks.example/blackbird"
`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	if len(cfg.Automation.OnComplete) != 1 {
		t.Fatalf("rules = %+v", cfg.Automation.OnComplete)
	}
	rule := cfg.Automation.OnComplete[0]
	if rule.Name != "tv" || !rule.HasActions() || rule.Private == nil || !*rule.Private {
		t.Fatalf("rule = %+v", rule)
	}
}

func TestValidateRSSConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
automation:
  rss:
    feeds:
      - name: ""
        url: "not-a-url"
        poll_interval: -5s
      - name: "dup"
        url: "https://feeds.example/rss"
      - name: "dup"
        url: "ftp://feeds.example/rss"
    filters:
      - name: ""
        feed: "missing"
        title_regex: "("
        min_size: 100
        max_size: 50
`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"automation.rss.feeds.[0].name",
		"automation.rss.feeds.[0].url",
		"automation.rss.feeds.[0].poll_interval",
		"automation.rss.feeds.[2].name",
		"automation.rss.feeds.[2].url",
		"automation.rss.filters.[0].name",
		"automation.rss.filters.[0].feed",
		"automation.rss.filters.[0].title_regex",
		"automation.rss.filters.[0].max_size",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}

	good := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
automation:
  rss:
    feeds:
      - name: "tv"
        url: "https://tracker.example/rss?passkey=SECRET"
        poll_interval: 10m
        label: "tv"
        destination: "/mnt/data/tv"
        cookies: "uid=7"
        headers:
          Authorization: "Bearer TOKEN"
    filters:
      - name: "shows"
        feed: "tv"
        title_regex: "^Show\\\\.S\\\\d\\\\d"
        category: "TV"
        min_size: 1
        max_size: 20000000000
        label: "tv-done"
`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("valid rss config rejected: %v", err)
	}
	if len(cfg.Automation.Rss.Feeds) != 1 || len(cfg.Automation.Rss.Filters) != 1 {
		t.Fatalf("rss = %+v", cfg.Automation.Rss)
	}
	feed := cfg.Automation.Rss.Feeds[0]
	if feed.EffectivePollInterval() != 10*time.Minute || feed.Cookies != "uid=7" || feed.Headers["Authorization"] != "Bearer TOKEN" {
		t.Fatalf("feed = %+v", feed)
	}
	if cfg.Automation.Rss.Feeds[0].PollInterval != 10*time.Minute {
		t.Fatalf("poll interval not parsed as duration: %v", cfg.Automation.Rss.Feeds[0].PollInterval)
	}
}

func TestValidateUnpackConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
automation:
  unpack:
    workers: 99
    timeout: -5m
    rules:
      - name: ""
        destination: "relative/path"
      - name: "dup"
      - name: "dup"
`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"automation.unpack.workers",
		"automation.unpack.timeout",
		"automation.unpack.rules.[0].name",
		"automation.unpack.rules.[0].destination",
		"automation.unpack.rules.[2].name",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}

	good := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
automation:
  unpack:
    workers: 3
    timeout: 1h
    rules:
      - name: "tv"
        label: "tv"
        destination: "/mnt/data/tv"
        delete_archives: true
`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("valid unpack config rejected: %v", err)
	}
	unpack := cfg.Automation.Unpack
	if unpack.EffectiveWorkers() != 3 || unpack.EffectiveTimeout() != time.Hour {
		t.Fatalf("unpack = %+v", unpack)
	}
	if !unpack.Rules[0].Matches("TV") || unpack.Rules[0].Matches("movies") {
		t.Fatalf("rule = %+v", unpack.Rules[0])
	}
	if def := (UnpackConfig{}); def.EffectiveWorkers() != 2 || def.EffectiveTimeout() != 30*time.Minute {
		t.Fatalf("defaults = %d, %v", def.EffectiveWorkers(), def.EffectiveTimeout())
	}
}

func TestValidateThrottleChannels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
tuning:
  throttles:
    - name: ""
      up_kb: -5
    - name: "NULL"
      up_kb: 10
    - name: "slow"
      up_kb: 100
      down_kb: 500
    - name: "slow"
      up_kb: 1
`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"tuning.throttles.[0].name",
		"tuning.throttles.[0]",
		"tuning.throttles.[1].name",
		"tuning.throttles.[3].name",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}

	good := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
tuning:
  global_up_rate_kb: 0
  throttles:
    - name: "slow"
      up_kb: 100
      down_kb: 500
    - name: "seed"
`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("valid throttles rejected: %v", err)
	}
	if len(cfg.Tuning.Throttles) != 2 || cfg.Tuning.Throttles[0].UpKB != 100 || cfg.Tuning.Throttles[1].DownKB != 0 {
		t.Fatalf("throttles = %+v", cfg.Tuning.Throttles)
	}
}

func TestValidateSchedule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
schedule:
  timezone: "Mars/Olympus"
  bandwidth:
    profiles:
      - name: ""
        color: "red"
        down_kb: -5
        throttles:
          - name: "NULL"
          - name: "slow"
          - name: "slow"
      - name: "day"
        color: "#f59e0b"
    grid:
      mon: ["day"]
      frobnicate: ["day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day", "day"]
      tue: ["ghost", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", ""]
`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"schedule.timezone",
		"schedule.bandwidth.profiles.[0].name",
		"schedule.bandwidth.profiles.[0].color",
		"schedule.bandwidth.profiles.[0]",
		"schedule.bandwidth.profiles.[0].throttles.[0].name",
		"schedule.bandwidth.profiles.[0].throttles.[2].name",
		"schedule.bandwidth.grid.mon",
		"schedule.bandwidth.grid.tue.[0]",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
	// Note: unknown day keys ("frobnicate") are ignored, not errors.

	good := `
rtorrent:
  scgi: "unix:///tmp/rt.sock"
schedule:
  timezone: "America/Chicago"
  bandwidth:
    profiles:
      - name: "day"
        color: "#f59e0b"
        down_kb: 1000
        up_kb: 500
        throttles:
          - name: slow
            up_kb: 100
            down_kb: 200
      - name: "night"
        down_kb: 0
        up_kb: 0
    grid:
      mon: ["night", "night", "night", "night", "night", "night", "night", "night", "night", "day", "day", "day", "day", "day", "day", "day", "day", "night", "night", "night", "night", "night", "night", "night"]
`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
	if len(cfg.Schedule.Bandwidth.Profiles) != 2 || len(cfg.Schedule.Bandwidth.Grid["mon"]) != 24 {
		t.Fatalf("schedule = %+v", cfg.Schedule.Bandwidth)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	full := defaults()
	full.Server.Listen = "127.0.0.1:9001"
	full.Log.Level = "debug"
	full.RTorrent.SCGI = "tcp://10.0.0.5:5001"
	full.RTorrent.Timeout = 7 * time.Second
	full.Directories.Default = "/mnt/data"
	full.Directories.PerLabel = map[string]string{"iso": "/mnt/data/iso"}
	full.Labels = []Label{{Name: "iso", Color: "#f59e0b"}}
	full.Volumes = []string{"/mnt/data"}
	full.UI.Accent = "#3fb950"
	full.UI.VisibleColumns = []string{"name", "size"}
	full.UI.Sort = Sort{Column: "name", Dir: "asc"}
	on := true
	n := 200
	kb := int64(10240)
	full.Tuning = Tuning{
		PortRandom:         &on,
		DHTMode:            strPtr("auto"),
		DHTPort:            &n,
		MaxPeersNormal:     &n,
		MaxUploads:         ptr(16),
		MaxUploadsGlobal:   ptr(64),
		MaxDownloadsGlobal: ptr(32),
		GlobalDownRateKB:   &kb,
		GlobalUpRateKB:     &kb,
	}

	if err := Save(path, full); err != nil {
		t.Fatal(err)
	}
	// Atomic write leaves no temp file behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Compare the fields we set (pointer targets must round-trip).
	if loaded.Server.Listen != full.Server.Listen || loaded.Log.Level != full.Log.Level {
		t.Fatalf("scalar round-trip failed: %+v vs %+v", loaded.Server, full.Server)
	}
	if loaded.Tuning.PortRandom == nil || !*loaded.Tuning.PortRandom {
		t.Fatal("port_random round-trip failed")
	}
	if loaded.Tuning.DHTPort == nil || *loaded.Tuning.DHTPort != 200 {
		t.Fatal("dht_port round-trip failed")
	}
	if loaded.Tuning.GlobalUpRateKB == nil || *loaded.Tuning.GlobalUpRateKB != 10240 {
		t.Fatal("global_up_rate_kb round-trip failed")
	}
	if loaded.Tuning.DHTMode == nil || *loaded.Tuning.DHTMode != "auto" {
		t.Fatal("dht_mode round-trip failed")
	}
	if len(loaded.Labels) != 1 || loaded.Labels[0].Name != "iso" || loaded.Labels[0].Color != "#f59e0b" {
		t.Fatalf("labels round-trip failed: %+v", loaded.Labels)
	}
	if loaded.Directories.PerLabel["iso"] != "/mnt/data/iso" {
		t.Fatalf("per_label round-trip failed: %+v", loaded.Directories.PerLabel)
	}
}

func TestExampleIsValid(t *testing.T) {
	// The embedded example (written on first run) must itself pass
	// validation — it is the documented reference.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := WriteExample(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("embedded example invalid: %v", err)
	}
}

func strPtr(s string) *string { return &s }
func ptr(n int) *int          { return &n }

func TestValidatePollAndResponseBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	write := func(body string) error {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		return err
	}
	bad := `
poll:
  interval: 2s
  max_interval: 1s
rtorrent:
  scgi: "unix:///tmp/rt.sock"
  max_response_bytes: -1
`
	if err := write(bad); err == nil {
		t.Fatal("expected error for max_interval < interval and negative max_response_bytes")
	} else {
		msg := err.Error()
		for _, want := range []string{"poll.max_interval", "rtorrent.max_response_bytes"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error missing %q:\n%s", want, msg)
			}
		}
	}
	good := `
poll:
  interval: 2s
  max_interval: 30s
rtorrent:
  scgi: "unix:///tmp/rt.sock"
  max_response_bytes: 134217728
`
	if err := write(good); err != nil {
		t.Fatalf("valid bounds rejected: %v", err)
	}
	// Defaults apply when unset: 30s idle cap.
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.EffectiveMaxInterval() != 30*time.Second {
		t.Fatalf("effective max interval = %v", cfg.Poll.EffectiveMaxInterval())
	}
}
