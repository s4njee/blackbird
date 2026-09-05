package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"blackbird/internal/config"
	"blackbird/internal/history"
)

// dirTestStore serves configurable download roots for the directory browser
// tests (PAR-5.1).
type dirTestStore struct {
	roots []string
}

func (s *dirTestStore) Get() config.Config                 { return config.Config{} }
func (s *dirTestStore) SaveSettings(_ config.Config) error { return nil }
func (s *dirTestStore) ConfigPath() string                 { return "test.yml" }
func (s *dirTestStore) DownloadDirs() []string             { return append([]string(nil), s.roots...) }

func newDirTestServer(t *testing.T, roots ...string) *httptest.Server {
	t.Helper()
	srv := New(Options{Store: &dirTestStore{roots: roots}, History: history.New(history.Options{})}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return ts
}

type dirView struct {
	Roots []struct {
		Path       string `json:"path"`
		FreeBytes  uint64 `json:"freeBytes"`
		TotalBytes uint64 `json:"totalBytes"`
	} `json:"roots"`
	Path    string `json:"path"`
	Parent  string `json:"parent"`
	Entries []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"entries"`
}

func getDir(t *testing.T, url string) (int, dirView, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var view dirView
	var raw map[string]any
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(data, &view); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, view, raw
}

func writeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseRootsAndFreeSpace(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeDir(t, filepath.Join(rootA, "movies"))
	writeDir(t, filepath.Join(rootA, "tv"))
	if err := os.WriteFile(filepath.Join(rootA, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newDirTestServer(t, rootA, rootB)

	status, view, _ := getDir(t, ts.URL+"/api/directories")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if len(view.Roots) != 2 {
		t.Fatalf("roots = %+v", view.Roots)
	}
	for _, root := range view.Roots {
		if root.TotalBytes == 0 || root.FreeBytes == 0 || root.FreeBytes > root.TotalBytes {
			t.Fatalf("root space = %+v", root)
		}
	}
	if view.Path != rootA {
		t.Fatalf("default path = %q, want first root", view.Path)
	}
	// Only directories surface, sorted case-insensitively; files are hidden.
	if len(view.Entries) != 2 || view.Entries[0].Name != "movies" || view.Entries[1].Name != "tv" {
		t.Fatalf("entries = %+v", view.Entries)
	}
	// Sibling roots are unreachable from each other: rootA's parent chain
	// stays inside rootA, never offering rootB.
	status, view, _ = getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(filepath.Join(rootA, "movies")))
	if status != http.StatusOK || view.Path != filepath.Join(rootA, "movies") {
		t.Fatalf("status = %d view = %+v", status, view)
	}
	if view.Parent != rootA {
		t.Fatalf("parent = %q", view.Parent)
	}
}

func TestBrowseTraversalRefused(t *testing.T) {
	root := t.TempDir()
	writeDir(t, filepath.Join(root, "sub"))
	ts := newDirTestServer(t, root)

	for _, target := range []string{
		"/etc",
		filepath.Join(root, ".."),
		filepath.Join(root, "sub", "..", ".."),
		filepath.Dir(root),
		"/",
	} {
		status, _, raw := getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(target))
		if status != http.StatusBadRequest {
			t.Fatalf("target %q: status = %d", target, status)
		}
		if code, _ := raw["error"].(map[string]any)["code"].(string); code != "path_outside_download_dirs" {
			t.Fatalf("target %q: error = %+v", target, raw)
		}
	}
	// A cleaned in-roots path with dot segments still works.
	status, view, _ := getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(filepath.Join(root, "sub", "..", "sub")))
	if status != http.StatusOK || view.Path != filepath.Join(root, "sub") {
		t.Fatalf("status = %d view = %+v", status, view)
	}
}

func TestBrowseSymlinkEscapeRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeDir(t, filepath.Join(outside, "secret"))
	ts := newDirTestServer(t, root)

	// A symlink inside the root pointing outside is refused as a browse
	// target and never listed as a child entry.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	status, _, raw := getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(link))
	if status != http.StatusBadRequest {
		t.Fatalf("symlink target status = %d", status)
	}
	if code, _ := raw["error"].(map[string]any)["code"].(string); code != "path_outside_download_dirs" {
		t.Fatalf("symlink error = %+v", raw)
	}
	status, view, _ := getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(root))
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	for _, entry := range view.Entries {
		if entry.Name == "escape" {
			t.Fatal("symlink escape listed as an entry")
		}
	}
}

func TestBrowseUnreadableAndMissing(t *testing.T) {
	root := t.TempDir()
	ts := newDirTestServer(t, root)

	// Missing directory: clean directory_unavailable, not a 500.
	status, _, raw := getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(filepath.Join(root, "nope")))
	if status != http.StatusBadRequest {
		t.Fatalf("missing status = %d", status)
	}
	if code, _ := raw["error"].(map[string]any)["code"].(string); code != "directory_unavailable" {
		t.Fatalf("missing error = %+v", raw)
	}

	// A regular file is not browsable either.
	file := filepath.Join(root, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _, _ = getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(file))
	if status != http.StatusBadRequest {
		t.Fatalf("file status = %d", status)
	}

	// Unreadable directory (skipped for root, where chmod is a no-op).
	if os.Geteuid() != 0 {
		locked := filepath.Join(root, "locked")
		writeDir(t, locked)
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		status, _, raw = getDir(t, ts.URL+"/api/directories?path="+urlQueryEscape(locked))
		if status != http.StatusBadRequest {
			t.Fatalf("locked status = %d", status)
		}
		if code, _ := raw["error"].(map[string]any)["code"].(string); code != "directory_unavailable" {
			t.Fatalf("locked error = %+v", raw)
		}
	}
}

func TestBrowseNoRoots(t *testing.T) {
	ts := newDirTestServer(t)
	status, _, raw := getDir(t, ts.URL+"/api/directories")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	if code, _ := raw["error"].(map[string]any)["code"].(string); code != "no_download_roots" {
		t.Fatalf("error = %+v", raw)
	}
}

func postDir(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func TestCreateDirectory(t *testing.T) {
	root := t.TempDir()
	ts := newDirTestServer(t, root)

	status, out := postDir(t, ts.URL+"/api/directories", map[string]any{"path": root, "name": "newshow"})
	if status != http.StatusCreated {
		t.Fatalf("status = %d out = %+v", status, out)
	}
	if out["path"] != filepath.Join(root, "newshow") || out["created"] != true {
		t.Fatalf("out = %+v", out)
	}
	if info, err := os.Stat(filepath.Join(root, "newshow")); err != nil || !info.IsDir() {
		t.Fatal("directory was not created")
	}

	// Idempotent re-create reports created=false.
	status, out = postDir(t, ts.URL+"/api/directories", map[string]any{"path": root, "name": "newshow"})
	if status != http.StatusOK || out["created"] != false {
		t.Fatalf("re-create = %d %+v", status, out)
	}

	// Existing file blocks with 409.
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _ = postDir(t, ts.URL+"/api/directories", map[string]any{"path": root, "name": "f.txt"})
	if status != http.StatusConflict {
		t.Fatalf("file clash status = %d", status)
	}

	// Invalid names are rejected.
	for _, name := range []string{"", "a/b", `a\b`, "..", "."} {
		status, _ = postDir(t, ts.URL+"/api/directories", map[string]any{"path": root, "name": name})
		if status != http.StatusBadRequest {
			t.Fatalf("name %q status = %d", name, status)
		}
	}
}

func TestCreateDirectoryOutsideRoots(t *testing.T) {
	root := t.TempDir()
	ts := newDirTestServer(t, root)

	for _, body := range []any{
		map[string]any{"path": "/etc", "name": "evil"},
		map[string]any{"path": filepath.Join(root, ".."), "name": "evil"},
	} {
		status, out := postDir(t, ts.URL+"/api/directories", body)
		if status != http.StatusBadRequest {
			t.Fatalf("body %+v: status = %d", body, status)
		}
		if code, _ := out["error"].(map[string]any)["code"].(string); code != "path_outside_download_dirs" {
			t.Fatalf("body %+v: error = %+v", body, out)
		}
	}
	if _, err := os.Stat("/etc/evil"); !os.IsNotExist(err) {
		t.Fatal("outside write escaped!")
	}
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}
