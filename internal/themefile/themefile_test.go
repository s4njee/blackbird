package themefile

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const validFullTheme = `version: 1
name: Ocean
description: A deep blue theme
extends: dark
dark: true
accents:
  - "#ff0000"
  - "#00ff00"
  - "#0000ff"
  - "#ffff00"
  - "#ff00ff"
palette:
  bg-app: "#0b1220"
  text-main: "#e6edf3"
preview:
  bg: "#0b1220"
  panel: "#111a2b"
  text: "#e6edf3"
  accent: "#2f9dff"
  progress: "#3fb950"
accent: "#2f9dff"
density: comfortable
`

func TestValidateFullFile(t *testing.T) {
	theme, errs := ValidateContent("ocean.yml", []byte(validFullTheme))
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if theme.Name != "Ocean" || theme.Description != "A deep blue theme" || theme.Extends != "dark" {
		t.Fatalf("identity = %+v", theme)
	}
	if !theme.Dark {
		t.Fatalf("dark = false, want true")
	}
	if len(theme.Accents) != 5 || theme.Accents[0] != "#ff0000" {
		t.Fatalf("accents = %+v", theme.Accents)
	}
	if theme.Palette["bg-app"] != "#0b1220" || theme.Palette["text-main"] != "#e6edf3" {
		t.Fatalf("palette = %+v", theme.Palette)
	}
	if theme.Preview["bg"] != "#0b1220" || theme.Preview["progress"] != "#3fb950" || len(theme.Preview) != 5 {
		t.Fatalf("preview = %+v", theme.Preview)
	}
	if theme.Accent != "#2f9dff" || theme.Density != "comfortable" {
		t.Fatalf("accent/density = %+v", theme)
	}
}

func TestValidateExtendsSubset(t *testing.T) {
	content := "version: 1\nname: Tweak\n" +
		"extends: light\n" +
		"palette:\n" +
		"  bg-app: \"#ffffff\"\n"
	theme, errs := ValidateContent("tweak.yml", []byte(content))
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if theme.Dark {
		t.Fatalf("dark = true for extends light, want false")
	}
	if theme.Palette["bg-app"] != "#ffffff" {
		t.Fatalf("palette = %+v", theme.Palette)
	}
}

func TestValidateInvalidCases(t *testing.T) {
	cases := []struct {
		name    string
		content string
		line    int
		substr  string
	}{
		{
			name:    "missing version",
			content: "name: NoVer\nextends: dark\n",
			line:    1,
			substr:  "version",
		},
		{
			name:    "version not 1",
			content: "version: 2\nname: V2\nextends: dark\n",
			line:    1,
			substr:  "version must be 1",
		},
		{
			name:    "missing name",
			content: "version: 1\nextends: dark\n",
			line:    1,
			substr:  "name is required",
		},
		{
			name:    "empty name",
			content: "version: 1\nname: \"\"\nextends: dark\n",
			line:    2,
			substr:  "name is required",
		},
		{
			name:    "unknown key",
			content: "version: 1\nname: Typo\nextends: dark\nbogus: 1\n",
			line:    4,
			substr:  `unknown key "bogus"`,
		},
		{
			name:    "bad extends",
			content: "version: 1\nname: Neon\nextends: neon\npalette:\n  bg-app: \"#000000\"\n",
			line:    3,
			substr:  "extends must be one of",
		},
		{
			name:    "custom chain extends",
			content: "version: 1\nname: Chain\nextends: mytheme\npalette:\n  bg-app: \"#000000\"\n",
			line:    3,
			substr:  "extends must be one of",
		},
		{
			name:    "custom prefix extends",
			content: "version: 1\nname: Chain\nextends: custom:other\npalette:\n  bg-app: \"#000000\"\n",
			line:    3,
			substr:  "extends must be one of",
		},
		{
			name:    "empty palette no extends",
			content: "version: 1\nname: Bare\n",
			line:    1,
			substr:  "extending nothing with an empty palette",
		},
		{
			name:    "palette bad key",
			content: "version: 1\nname: Bad\npalette:\n  Bad_Key: \"#ffffff\"\n",
			line:    4,
			substr:  "palette key",
		},
		{
			name:    "palette bad value",
			content: "version: 1\nname: Bad\npalette:\n  bg-app: red\n",
			line:    4,
			substr:  "#rrggbb",
		},
		{
			name: "accents wrong length",
			content: "version: 1\nname: Acc\nextends: dark\naccents:\n" +
				"  - \"#ff0000\"\n  - \"#00ff00\"\n  - \"#0000ff\"\n",
			line:   5,
			substr: "exactly 5",
		},
		{
			name: "accents bad color",
			content: "version: 1\nname: Acc\nextends: dark\naccents:\n" +
				"  - \"#ff0000\"\n  - \"#00ff00\"\n  - \"#0000ff\"\n  - \"#ffff00\"\n  - \"nope\"\n",
			line:   9,
			substr: "#rrggbb",
		},
		{
			name:    "preview bad key",
			content: "version: 1\nname: Prev\nextends: dark\npreview:\n  sidebar: \"#000000\"\n",
			line:    5,
			substr:  "preview key",
		},
		{
			name:    "preview bad value",
			content: "version: 1\nname: Prev\nextends: dark\npreview:\n  bg: blue\n",
			line:    5,
			substr:  "#rrggbb",
		},
		{
			name:    "accent bad",
			content: "version: 1\nname: Acc\nextends: dark\naccent: blue\n",
			line:    4,
			substr:  "accent must be",
		},
		{
			name:    "density bad",
			content: "version: 1\nname: Dense\nextends: dark\ndensity: cozy\n",
			line:    4,
			substr:  "density must be",
		},
		{
			name:    "dark non-bool",
			content: "version: 1\nname: Dk\nextends: dark\ndark: maybe\n",
			line:    4,
			substr:  "dark must be a boolean",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := ValidateContent("case.yml", []byte(tc.content))
			if len(errs) == 0 {
				t.Fatalf("expected errors, got none")
			}
			joined := ""
			for _, e := range errs {
				joined += e.Error() + "; "
			}
			want := "case.yml:" + strconv.Itoa(tc.line) + ":"
			if !strings.Contains(joined, want) {
				t.Fatalf("errors %q do not contain %q", joined, want)
			}
			if !strings.Contains(joined, tc.substr) {
				t.Fatalf("errors %q do not contain %q", joined, tc.substr)
			}
		})
	}
}

func TestDarkDefaults(t *testing.T) {
	cases := []struct {
		extends string
		dark    bool
	}{
		{"dark", true},
		{"midnight", true},
		{"contrast", true},
		{"light", false},
		{"classic", false},
		{"", true},
	}
	for _, tc := range cases {
		content := "version: 1\nname: Dd\n"
		if tc.extends != "" {
			content += "extends: " + tc.extends + "\n"
		} else {
			content += "palette:\n  bg-app: \"#000000\"\n"
		}
		theme, errs := ValidateContent("d.yml", []byte(content))
		if len(errs) != 0 {
			t.Fatalf("extends %q: errors = %v", tc.extends, errs)
		}
		if theme.Dark != tc.dark {
			t.Fatalf("extends %q: dark = %v, want %v", tc.extends, theme.Dark, tc.dark)
		}
	}
}

func TestLoadDirMissing(t *testing.T) {
	themes, errs := LoadDir(filepath.Join(t.TempDir(), "nope"))
	if len(themes) != 0 || len(errs) != 0 {
		t.Fatalf("themes = %v, errs = %v", themes, errs)
	}
}

func TestLoadDirMixed(t *testing.T) {
	dir := t.TempDir()
	themesDir := filepath.Join(dir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "b-ocean.yml"), []byte(validFullTheme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "a-min.yml"), []byte("version: 1\nname: Mini\nextends: midnight\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "bad.yaml"), []byte("name: Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "skip.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	themes, errs := LoadDir(dir)
	if len(themes) != 2 {
		t.Fatalf("themes = %+v (errs %v)", themes, errs)
	}
	// Sorted by filename: a-min first.
	if themes[0].Name != "Mini" || themes[1].Name != "Ocean" {
		t.Fatalf("order/names = %+v", themes)
	}
	if len(errs) == 0 {
		t.Fatal("expected errors for bad.yaml")
	}
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e.Error(), "bad.yaml:") && strings.Contains(e.Error(), ":1:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bad.yaml error missing: %v", errs)
	}
}

func TestFileErrorString(t *testing.T) {
	e := FileError{File: "x.yml", Line: 7, Message: "boom"}
	if e.Error() != "x.yml:7: boom" {
		t.Fatalf("Error() = %q", e.Error())
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in   string
		base string
		ok   bool
	}{
		{"Ocean", "ocean", true},
		{"my-theme_2", "my-theme_2", true},
		{"My Theme!", "mytheme", true},
		{"", "", false},
		{"!!!", "", false},
		{"-lead", "", false},
		{"_lead", "", false},
		{"9lives", "9lives", true},
		{"../x", "x", true}, // traversal bits stripped; API still rejects separators separately
	}
	for _, tc := range cases {
		base, ok := SanitizeName(tc.in)
		if base != tc.base || ok != tc.ok {
			t.Errorf("SanitizeName(%q) = (%q, %v), want (%q, %v)", tc.in, base, ok, tc.base, tc.ok)
		}
	}
}

func TestReadCustomCSS(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadCustomCSS(dir); !os.IsNotExist(err) {
		t.Fatalf("missing err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom.css"), []byte("body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := ReadCustomCSS(dir)
	if err != nil || string(data) != "body{color:red}" {
		t.Fatalf("data = %q, err = %v", data, err)
	}
	big := make([]byte, MaxCustomCSSBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "custom.css"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCustomCSS(dir); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too-large err = %v", err)
	}
}
