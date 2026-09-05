package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blackbird/internal/config"
	"blackbird/internal/themefile"
)

// themeTestStore is a ConfigStore stub whose config file lives in a temp dir,
// so theme/CSS handlers operate on an isolated <dir>/themes + custom.css.
type themeTestStore struct {
	cfg  config.Config
	path string
}

func (s *themeTestStore) Get() config.Config                 { return s.cfg }
func (s *themeTestStore) SaveSettings(c config.Config) error { s.cfg = c; return nil }
func (s *themeTestStore) ConfigPath() string                 { return s.path }
func (s *themeTestStore) DownloadDirs() []string             { return []string{"/mnt/data"} }

type themeTestStack struct {
	ts    *httptest.Server
	srv   *Server
	store *themeTestStore
	dir   string
}

func newThemeTestStack(t *testing.T) *themeTestStack {
	t.Helper()
	dir := t.TempDir()
	store := &themeTestStore{path: filepath.Join(dir, "blackbird.yml")}
	srv := New(Options{Store: store}, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return &themeTestStack{ts: ts, srv: srv, store: store, dir: dir}
}

const themeTestValidYAML = "version: 1\nname: Ocean\ndescription: deep blue\nextends: dark\n"

func themeNames(t *testing.T, url string) (int, []string, []string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Themes []themefile.Theme `json:"themes"`
		Errors []string          `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(body.Themes))
	for _, th := range body.Themes {
		names = append(names, th.Name)
	}
	return resp.StatusCode, names, body.Errors
}

func postThemeImport(t *testing.T, url, name, content string) (*http.Response, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"name": name, "content": content})
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	return resp, out
}

func TestThemesListValidAndInvalid(t *testing.T) {
	st := newThemeTestStack(t)
	themesDir := filepath.Join(st.dir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themesDir, "good.yml"), []byte(themeTestValidYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	// Invalid: missing version (line 1) with an empty palette and no extends.
	if err := os.WriteFile(filepath.Join(themesDir, "bad.yml"), []byte("name: Broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, names, errs := themeNames(t, st.ts.URL+"/api/themes")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if len(names) != 1 || names[0] != "Ocean" {
		t.Fatalf("names = %v", names)
	}
	if len(errs) == 0 {
		t.Fatal("expected error strings for bad.yml")
	}
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e, "bad.yml:") && strings.Contains(e, ":1:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bad.yml:1 error missing: %v", errs)
	}

	// The /api/v1 variant is served automatically from the route table.
	if status, _, _ := themeNames(t, st.ts.URL+"/api/v1/themes"); status != http.StatusOK {
		t.Fatalf("v1 status = %d", status)
	}
}

func TestThemesImportRoundTrip(t *testing.T) {
	st := newThemeTestStack(t)

	resp, out := postThemeImport(t, st.ts.URL+"/api/themes/import", "", themeTestValidYAML)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d: %v", resp.StatusCode, out)
	}
	theme := out["theme"].(map[string]any)
	if theme["name"] != "Ocean" {
		t.Fatalf("theme = %v", theme)
	}

	_, names, _ := themeNames(t, st.ts.URL+"/api/themes")
	if len(names) != 1 || names[0] != "Ocean" {
		t.Fatalf("names after import = %v", names)
	}

	// Invalid YAML → 400 whose message carries the source line.
	bad := "version: 1\nname: Broken\ndensity: cozy\n"
	resp, out = postThemeImport(t, st.ts.URL+"/api/themes/import", "", bad)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad import status = %d: %v", resp.StatusCode, out)
	}
	msg := out["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, ":3:") {
		t.Fatalf("message missing :3:: %q", msg)
	}

	// Unsanitizable requested name → 400.
	resp, out = postThemeImport(t, st.ts.URL+"/api/themes/import", "!!!", themeTestValidYAML)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad name status = %d: %v", resp.StatusCode, out)
	}
}

func TestThemeImportOverwriteAsUpdate(t *testing.T) {
	st := newThemeTestStack(t)
	if resp, _ := postThemeImport(t, st.ts.URL+"/api/themes/import", "", themeTestValidYAML); resp.StatusCode != http.StatusOK {
		t.Fatalf("first import status = %d", resp.StatusCode)
	}
	updated := "version: 1\nname: Ocean\ndescription: deeper blue\nextends: midnight\n"
	if resp, _ := postThemeImport(t, st.ts.URL+"/api/themes/import", "", updated); resp.StatusCode != http.StatusOK {
		t.Fatalf("second import status = %d", resp.StatusCode)
	}
	resp, err := http.Get(st.ts.URL + "/api/themes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Themes []themefile.Theme `json:"themes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Themes) != 1 {
		t.Fatalf("themes = %+v, want exactly 1 (overwrite = update)", body.Themes)
	}
	if body.Themes[0].Description != "deeper blue" || body.Themes[0].Extends != "midnight" {
		t.Fatalf("theme not updated: %+v", body.Themes[0])
	}
}

func TestThemeDelete(t *testing.T) {
	st := newThemeTestStack(t)
	if resp, _ := postThemeImport(t, st.ts.URL+"/api/themes/import", "", themeTestValidYAML); resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, st.ts.URL+"/api/themes/ocean", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("delete = %d %v", resp.StatusCode, out)
	}
	if _, names, _ := themeNames(t, st.ts.URL+"/api/themes"); len(names) != 0 {
		t.Fatalf("names after delete = %v", names)
	}

	// Deleting again → 404.
	req, _ = http.NewRequest(http.MethodDelete, st.ts.URL+"/api/themes/ocean", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", resp.StatusCode)
	}
}

func TestThemeDeleteTraversalAndEmpty(t *testing.T) {
	st := newThemeTestStack(t)
	for _, raw := range []string{"../x", "", "a/b", "..", "  "} {
		req := httptest.NewRequest(http.MethodDelete, "/api/themes/x", nil)
		req.SetPathValue("name", raw)
		rec := httptest.NewRecorder()
		st.srv.themeDeleteHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: status = %d, want 400", raw, rec.Code)
		}
	}
}

func TestCustomCSS(t *testing.T) {
	st := newThemeTestStack(t)

	// Absent → 200 with an empty body (absence is normal; an empty 200 is
	// unambiguous for fetch clients).
	resp, err := http.Get(st.ts.URL + "/api/custom-css")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("absent status = %d, want 200", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("absent body = %q, want empty", body)
	}

	// Present → text/css body.
	css := "body { background: #000; }"
	if err := os.WriteFile(filepath.Join(st.dir, "custom.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(st.ts.URL + "/api/custom-css")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("present status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("content-type = %q", ct)
	}
	if buf := string(body); buf != css {
		t.Fatalf("body = %q", buf)
	}

	// Too big → 413.
	big := make([]byte, themefile.MaxCustomCSSBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(st.dir, "custom.css"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(st.ts.URL + "/api/custom-css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("too-big status = %d, want 413", resp.StatusCode)
	}
}

func TestThemesNilStore503(t *testing.T) {
	srv := New(Options{}, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()

	resp, err := http.Get(ts.URL + "/api/themes")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("list status = %d, want 503", resp.StatusCode)
	}

	data, _ := json.Marshal(map[string]string{"content": themeTestValidYAML})
	resp, err = http.Post(ts.URL+"/api/themes/import", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("import status = %d, want 503", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/themes/ocean", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("delete status = %d, want 503", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/custom-css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("css status = %d, want 503", resp.StatusCode)
	}
}
