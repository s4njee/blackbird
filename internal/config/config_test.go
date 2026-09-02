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
  sort:
    column: name
    dir: asc
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
