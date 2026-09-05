package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIThemeDefaultsToDark(t *testing.T) {
	if got := defaults().UI.Theme; got != "dark" {
		t.Fatalf("defaults().UI.Theme = %q, want %q", got, "dark")
	}
	// A config without a ui section keeps the dark default.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	minimal := "rtorrent:\n  scgi: \"unix:///tmp/rt.sock\"\n"
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("minimal config rejected: %v", err)
	}
	if cfg.UI.Theme != "dark" {
		t.Fatalf("UI.Theme = %q, want %q", cfg.UI.Theme, "dark")
	}
}

func TestUIThemeValidation(t *testing.T) {
	for _, valid := range []string{"dark", "light", "midnight", "contrast", "classic", "system", ""} {
		cfg := defaults()
		cfg.UI.Theme = valid
		if verrs := Validate(cfg); len(verrs) != 0 {
			t.Errorf("Theme %q rejected: %v", valid, verrs)
		}
	}

	cfg := defaults()
	cfg.UI.Theme = "neon"
	verrs := Validate(cfg)
	if len(verrs) == 0 {
		t.Fatal("garbage theme accepted")
	}
	found := false
	for _, ve := range verrs {
		if strings.Join(ve.Path, ".") == "ui.theme" {
			found = true
			for _, want := range []string{"dark", "light", "midnight", "contrast", "classic", "system"} {
				if !strings.Contains(ve.Msg, want) {
					t.Errorf("ui.theme message missing %q: %q", want, ve.Msg)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no ui.theme path in %v", verrs)
	}

	// Garbage via YAML file surfaces the ui.theme path in the Load error.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	bad := "rtorrent:\n  scgi: \"unix:///tmp/rt.sock\"\nui:\n  theme: \"neon\"\n"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected Load error for garbage theme")
	} else if !strings.Contains(err.Error(), "ui.theme") {
		t.Fatalf("Load error missing ui.theme path: %v", err)
	}
}
