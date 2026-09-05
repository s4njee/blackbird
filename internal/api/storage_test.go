package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/storage"
)

type forecastStore struct {
	roots []string
	cfg   config.Config
}

func (s *forecastStore) Get() config.Config               { return s.cfg }
func (s *forecastStore) SaveSettings(config.Config) error { return nil }
func (s *forecastStore) ConfigPath() string               { return "forecast.yml" }
func (s *forecastStore) DownloadDirs() []string           { return s.roots }
func TestStorageAPIUploadedMetadataPreallocationUnknownIntakeAndRules(t *testing.T) {
	stack := newTestStack(t, "", fakertorrent.Options{})
	waitForConnected(t, stack.p)
	root := t.TempDir()
	store := &forecastStore{roots: []string{root}, cfg: config.Config{Directories: config.Directories{Default: root}}}
	store.cfg.Automation.Unpack.Rules = []config.UnpackRule{{Name: "possible", Destination: root}}
	srv := New(Options{Poller: stack.p, Store: store}, NewAuth("", "", nil))
	defer srv.Close()
	query := func(fields map[string]string, data []byte) (int, storage.Plan) {
		body := new(bytes.Buffer)
		form := multipart.NewWriter(body)
		form.WriteField("kind", "add")
		form.WriteField("destination", root)
		for key, value := range fields {
			form.WriteField(key, value)
		}
		if data != nil {
			f, _ := form.CreateFormFile("files", "test.torrent")
			f.Write(data)
		}
		form.Close()
		req := httptest.NewRequest("POST", "/api/v1/storage/forecast", body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing cache policy")
		}
		var p storage.Plan
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, p
	}
	data := bytes.Repeat([]byte{42}, 8192)
	if err := os.WriteFile(filepath.Join(root, "test"), data, 0600); err != nil {
		t.Fatal(err)
	}
	code, p := query(map[string]string{"extraction_bytes": "4096"}, []byte("d4:infod6:lengthi8192e4:name4:test12:piece lengthi1024eee"))
	if code != 200 {
		t.Fatalf("forecast: %d", code)
	}
	var downloaded, extraction bool
	for _, op := range p.Operations {
		if strings.HasPrefix(op.Description, "Batch download") {
			downloaded = true
			if op.Allocated != 8192 || op.Upper == nil || *op.Upper != 0 {
				t.Fatalf("preallocation counted twice: %+v", op)
			}
		}
		if strings.Contains(op.Description, "extraction") {
			extraction = true
			if op.Upper == nil || *op.Upper != 4096 {
				t.Fatal("extraction assumption omitted")
			}
		}
	}
	if !downloaded || !extraction {
		t.Fatal("missing intake/extraction operations")
	}
	code, p = query(map[string]string{"magnets": "magnet:?xt=urn:btih:abc"}, nil)
	if code != 200 {
		t.Fatal(code)
	}
	for _, op := range p.Operations {
		if op.Description == "Unknown intake sizes" && op.Upper != nil {
			t.Fatal("magnet given invented size")
		}
	}
	if p.ExpiresAt.Sub(p.GeneratedAt).Seconds() != 30 || p.Signature == "" {
		t.Fatal("missing expiry/review signature")
	}
	for _, field := range []map[string]string{{"reserve_bytes": "-1", "magnets": "a"}, {"unknown_bytes": "9007199254740992", "magnets": "a"}} {
		if code, _ := query(field, nil); code != 400 {
			t.Fatal("invalid byte bound accepted")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "test")); err != nil {
		t.Fatal("preview mutated data")
	}
}
func TestStorageAPIAuthenticationAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		auth *Auth
		want int
	}{{NewAuth("", "", nil), 503}, {NewAuth("op", "hash", nil), 401}} {
		srv := New(Options{}, tc.auth)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/storage/forecast", nil))
		srv.Close()
		if w.Code != tc.want {
			t.Fatalf("status %d want %d", w.Code, tc.want)
		}
	}
}

func TestStoragePrioritizesSelectedMovesInLargeSessions(t *testing.T) {
	stack := newTestStack(t, "", fakertorrent.Options{SessionSize: 300})
	waitForConnected(t, stack.p)
	rows := stack.p.Snapshot().Torrents
	hash := rows[len(rows)-1].Hash
	root := t.TempDir()
	srv := New(Options{Poller: stack.p, Store: &forecastStore{roots: []string{root}}}, NewAuth("", "", nil))
	defer srv.Close()
	body := new(bytes.Buffer)
	form := multipart.NewWriter(body)
	for key, value := range map[string]string{"kind": "move", "mode": "set_directory", "hashes": hash, "destination": root} {
		form.WriteField(key, value)
	}
	form.Close()
	req := httptest.NewRequest("POST", "/api/v1/storage/forecast", body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Set directory only · "+hash) {
		t.Fatalf("selected target lost in background budget: %d", w.Code)
	}
}
