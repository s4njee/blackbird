package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBootstrapCreatesCredentialsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first, err := Bootstrap(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Password == "" || first.Username != "admin" {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if first.ConfigPath != filepath.Join(dir, "config.yml") || first.EnvPath != filepath.Join(dir, ".env") {
		t.Fatalf("unexpected paths: %+v", first)
	}
	data, err := os.ReadFile(first.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tcp://rtorrent:5000") || !strings.Contains(string(data), "password_hash:") {
		t.Fatalf("bootstrap config missing Compose settings: %s", data)
	}
	env, err := os.ReadFile(first.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "BLACKBIRD_PASSWORD_HASH=$$2") {
		t.Fatalf("bootstrap env missing Compose-escaped bcrypt hash: %s", env)
	}
	envLine := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(env), "\n", 2)[1], "BLACKBIRD_PASSWORD_HASH="))
	if strings.ReplaceAll(envLine, "$$", "$") != string(firstHashFromConfig(t, first.ConfigPath)) {
		t.Fatalf("bootstrap env hash does not decode to config hash")
	}

	second, err := Bootstrap(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Password != "" {
		t.Fatalf("second run replaced credentials: %+v", second)
	}

	var cfg Config
	loaded, err := Load(first.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg = *loaded
	if err := bcrypt.CompareHashAndPassword([]byte(cfg.Auth.PasswordHash), []byte(first.Password)); err != nil {
		t.Fatalf("generated password does not match config hash: %v", err)
	}
}

func firstHashFromConfig(t *testing.T, path string) []byte {
	t.Helper()
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(loaded.Auth.PasswordHash)
}

func TestBootstrapRotateReplacesPassword(t *testing.T) {
	dir := t.TempDir()
	first, err := Bootstrap(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := Bootstrap(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !rotated.Created || rotated.Password == first.Password {
		t.Fatalf("rotate did not create a new password: first=%q rotated=%q", first.Password, rotated.Password)
	}
	loaded, err := Load(rotated.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(loaded.Auth.PasswordHash), []byte(rotated.Password)); err != nil {
		t.Fatalf("rotated password does not match config hash: %v", err)
	}
}
