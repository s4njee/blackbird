// Package themefile loads and validates operator-supplied custom themes
// (THM-9.4). Themes live as YAML files under <configDir>/themes/ and an
// optional <configDir>/custom.css carries operator CSS overrides. Every
// validation error carries the source line so the API can surface it.
package themefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxCustomCSSBytes caps the operator custom.css served by /api/custom-css.
const MaxCustomCSSBytes = 256 * 1024

// ErrTooLarge is returned by ReadCustomCSS when custom.css exceeds
// MaxCustomCSSBytes (the API maps it to 413).
var ErrTooLarge = errors.New("custom.css exceeds 256KB limit")

// Theme is one validated custom theme. Palette keys are palette names
// WITHOUT the `pal-` prefix (e.g. "bg-app"); preview keys are
// bg/panel/text/accent/progress.
type Theme struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Extends     string            `json:"extends"`
	Dark        bool              `json:"dark"`
	Accents     []string          `json:"accents"`
	Palette     map[string]string `json:"palette"`
	Preview     map[string]string `json:"preview"`
	Accent      string            `json:"accent"`
	Density     string            `json:"density"`
}

// FileError is one validation failure anchored to a source line.
type FileError struct {
	File    string
	Line    int
	Message string
}

// Error renders "file.yml:LINE: message".
func (e FileError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message)
}

var (
	paletteKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hexColorRe   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	yamlLineRe   = regexp.MustCompile(`line (\d+)`)
)

func isHexColor(s string) bool { return hexColorRe.MatchString(s) }

// validExtends are the only legal `extends` values: built-in theme ids.
// Custom-to-custom chains and `custom:` values are rejected.
func validExtends(s string) bool {
	switch s {
	case "dark", "light", "midnight", "contrast", "classic":
		return true
	default:
		return false
	}
}

// extendsIsDark reports the darkness of a built-in parent theme.
func extendsIsDark(extends string) bool {
	switch extends {
	case "dark", "midnight", "contrast":
		return true
	default:
		return false
	}
}

func validPreviewKey(k string) bool {
	switch k {
	case "bg", "panel", "text", "accent", "progress":
		return true
	default:
		return false
	}
}

// yamlErrLine extracts the source line from a yaml.v3 parse error,
// defaulting to 1 when the message carries none.
func yamlErrLine(err error) int {
	if m := yamlLineRe.FindStringSubmatch(err.Error()); len(m) == 2 {
		var n int
		if _, serr := fmt.Sscanf(m[1], "%d", &n); serr == nil && n > 0 {
			return n
		}
	}
	return 1
}

// isNull reports a YAML null node (`key:` with no value).
func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// ValidateContent parses and validates one theme file's content, walking the
// yaml.Node tree so every error carries its source line number.
func ValidateContent(filename string, content []byte) (Theme, []FileError) {
	var theme Theme
	var errs []FileError
	fail := func(line int, msg string) {
		if line < 1 {
			line = 1
		}
		errs = append(errs, FileError{File: filename, Line: line, Message: msg})
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		fail(yamlErrLine(err), strings.TrimSpace(err.Error()))
		return theme, errs
	}
	var root *yaml.Node
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			root = nil
		} else {
			root = doc.Content[0]
		}
	} else {
		root = &doc
	}
	if root == nil {
		fail(1, "version is required (must be 1)")
		fail(1, "name is required (must be a non-empty string)")
		fail(1, "extending nothing with an empty palette: set extends or define a palette")
		return theme, errs
	}
	if root.Kind != yaml.MappingNode {
		fail(root.Line, "theme must be a mapping")
		return theme, errs
	}

	allowed := map[string]bool{
		"version": true, "name": true, "description": true,
		"extends": true, "dark": true, "accents": true,
		"palette": true, "preview": true, "accent": true, "density": true,
	}
	fields := map[string]*yaml.Node{}
	keyLines := map[string]int{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if !allowed[k.Value] {
			fail(k.Line, fmt.Sprintf("unknown key %q", k.Value))
			continue
		}
		if _, dup := fields[k.Value]; !dup {
			fields[k.Value] = v
			keyLines[k.Value] = k.Line
		} else {
			fields[k.Value] = v
		}
	}

	// version: required, must be 1.
	if v, ok := fields["version"]; !ok || isNull(v) {
		fail(1, "version is required (must be 1)")
	} else if v.Kind != yaml.ScalarNode || v.Tag != "!!int" || v.Value != "1" {
		fail(v.Line, fmt.Sprintf("version must be 1 (got %q)", v.Value))
	}

	// name: required non-empty string.
	if v, ok := fields["name"]; !ok || isNull(v) {
		fail(1, "name is required (must be a non-empty string)")
	} else if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || strings.TrimSpace(v.Value) == "" {
		fail(v.Line, "name is required (must be a non-empty string)")
	} else {
		theme.Name = v.Value
	}

	// description: optional string.
	if v, ok := fields["description"]; ok && !isNull(v) {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			fail(v.Line, "description must be a string")
		} else {
			theme.Description = v.Value
		}
	}

	// extends: optional built-in id.
	extends := ""
	if v, ok := fields["extends"]; ok && !isNull(v) {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			fail(v.Line, "extends must be one of dark|light|midnight|contrast|classic")
		} else if strings.TrimSpace(v.Value) == "" {
			extends = ""
		} else if !validExtends(v.Value) {
			fail(v.Line, fmt.Sprintf("extends must be one of dark|light|midnight|contrast|classic (got %q)", v.Value))
			extends = v.Value
		} else {
			extends = v.Value
		}
	}
	theme.Extends = extends

	// dark: optional bool, defaulted from the parent's darkness (true with
	// no extends).
	if v, ok := fields["dark"]; ok && !isNull(v) {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!bool" {
			fail(v.Line, "dark must be a boolean")
			theme.Dark = extends == "" || extendsIsDark(extends)
		} else {
			theme.Dark = v.Value == "true"
		}
	} else if extends == "" {
		theme.Dark = true
	} else {
		theme.Dark = extendsIsDark(extends)
	}

	// palette: optional map of name → #rrggbb.
	palette := map[string]string{}
	if v, ok := fields["palette"]; ok && !isNull(v) {
		if v.Kind != yaml.MappingNode {
			fail(v.Line, "palette must be a mapping of name to #rrggbb color")
		} else {
			for i := 0; i+1 < len(v.Content); i += 2 {
				k, val := v.Content[i], v.Content[i+1]
				if k.Kind != yaml.ScalarNode || !paletteKeyRe.MatchString(k.Value) {
					fail(k.Line, fmt.Sprintf("palette key %q must match ^[a-z0-9][a-z0-9-]*$", k.Value))
					continue
				}
				if val.Kind != yaml.ScalarNode || val.Tag != "!!str" || !isHexColor(val.Value) {
					fail(val.Line, fmt.Sprintf("palette color for %q must be #rrggbb (got %q)", k.Value, val.Value))
					continue
				}
				palette[k.Value] = val.Value
			}
		}
	}
	if len(palette) > 0 {
		theme.Palette = palette
	}
	if extends == "" && len(palette) == 0 {
		fail(1, "extending nothing with an empty palette: set extends or define a palette")
	}

	// accents: optional list of exactly 5 #rrggbb.
	if v, ok := fields["accents"]; ok && !isNull(v) {
		if v.Kind != yaml.SequenceNode {
			fail(v.Line, "accents must be a list of exactly 5 #rrggbb colors")
		} else if len(v.Content) != 5 {
			fail(v.Line, fmt.Sprintf("accents must be a list of exactly 5 #rrggbb colors (got %d)", len(v.Content)))
		} else {
			accents := make([]string, 0, 5)
			for _, item := range v.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || !isHexColor(item.Value) {
					fail(item.Line, fmt.Sprintf("accent color must be #rrggbb (got %q)", item.Value))
				} else {
					accents = append(accents, item.Value)
				}
			}
			if len(accents) == 5 {
				theme.Accents = accents
			}
		}
	}

	// preview: optional map restricted to bg|panel|text|accent|progress.
	if v, ok := fields["preview"]; ok && !isNull(v) {
		if v.Kind != yaml.MappingNode {
			fail(v.Line, "preview must be a mapping of bg|panel|text|accent|progress to #rrggbb")
		} else {
			preview := map[string]string{}
			for i := 0; i+1 < len(v.Content); i += 2 {
				k, val := v.Content[i], v.Content[i+1]
				if !validPreviewKey(k.Value) {
					fail(k.Line, fmt.Sprintf("preview key must be one of bg|panel|text|accent|progress (got %q)", k.Value))
					continue
				}
				if val.Kind != yaml.ScalarNode || val.Tag != "!!str" || !isHexColor(val.Value) {
					fail(val.Line, fmt.Sprintf("preview color for %q must be #rrggbb (got %q)", k.Value, val.Value))
					continue
				}
				preview[k.Value] = val.Value
			}
			if len(preview) > 0 {
				theme.Preview = preview
			}
		}
	}

	// accent: optional empty or #rrggbb.
	if v, ok := fields["accent"]; ok && !isNull(v) {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			fail(v.Line, "accent must be a #rrggbb color")
		} else if strings.TrimSpace(v.Value) == "" {
			theme.Accent = ""
		} else if !isHexColor(v.Value) {
			fail(v.Line, fmt.Sprintf("accent must be a #rrggbb color (got %q)", v.Value))
		} else {
			theme.Accent = v.Value
		}
	}

	// density: optional empty|dense|comfortable.
	if v, ok := fields["density"]; ok && !isNull(v) {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			fail(v.Line, "density must be empty, dense, or comfortable")
		} else if v.Value == "" {
			theme.Density = ""
		} else if v.Value != "dense" && v.Value != "comfortable" {
			fail(v.Line, fmt.Sprintf("density must be empty, dense, or comfortable (got %q)", v.Value))
		} else {
			theme.Density = v.Value
		}
	}

	return theme, errs
}

// LoadDir reads <dir>/themes/*.yml|*.yaml (sorted by filename) and validates
// each file. A missing themes directory yields empty results with no errors;
// a bad file contributes FileErrors but never fails the whole load.
func LoadDir(dir string) ([]Theme, []FileError) {
	themesDir := filepath.Join(dir, "themes")
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return nil, nil
	}
	var themes []Theme
	var errs []FileError
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(themesDir, name))
		if err != nil {
			errs = append(errs, FileError{File: name, Line: 1, Message: err.Error()})
			continue
		}
		theme, ferrs := ValidateContent(name, data)
		if len(ferrs) > 0 {
			errs = append(errs, ferrs...)
			continue
		}
		themes = append(themes, theme)
	}
	return themes, errs
}

// ReadCustomCSS reads <dir>/custom.css. A missing file returns an
// os.ErrNotExist-matching error (the API maps it to 404); a file larger than
// MaxCustomCSSBytes returns an ErrTooLarge-matching error (mapped to 413).
func ReadCustomCSS(dir string) ([]byte, error) {
	path := filepath.Join(dir, "custom.css")
	if st, err := os.Stat(path); err != nil {
		return nil, err
	} else if st.Size() > MaxCustomCSSBytes {
		return nil, fmt.Errorf("custom.css is %d bytes (limit %d): %w", st.Size(), MaxCustomCSSBytes, ErrTooLarge)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxCustomCSSBytes {
		return nil, fmt.Errorf("custom.css is %d bytes (limit %d): %w", len(data), MaxCustomCSSBytes, ErrTooLarge)
	}
	return data, nil
}

// SanitizeName maps a theme name to a safe filename base (no extension):
// lowercase, keeping [a-z0-9-_], which must start alphanumerically and be
// non-empty. ok=false rejects empty/traversal-prone names.
func SanitizeName(name string) (string, bool) {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			sb.WriteRune(r)
		}
	}
	base := sb.String()
	if base == "" {
		return "", false
	}
	if c := base[0]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
		return "", false
	}
	return base, true
}
