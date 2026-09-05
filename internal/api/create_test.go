package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/history"
	"blackbird/internal/mktorrent"
	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

// stubCreateDaemon captures session adds for creation tests.
type stubCreateDaemon struct {
	adds []rtorrent.AddOptions
	data [][]byte
}

func (d *stubCreateDaemon) AddTorrentFile(_ context.Context, data []byte, opts rtorrent.AddOptions) error {
	d.adds = append(d.adds, opts)
	d.data = append(d.data, append([]byte(nil), data...))
	return nil
}

type createStack struct {
	ts     *httptest.Server
	srv    *Server
	svc    *mktorrent.Service
	daemon *stubCreateDaemon
	log    *history.Log
	root   string
}

func newCreateStack(t *testing.T) *createStack {
	t.Helper()
	// Resolve symlinks (macOS temp dirs hide behind /var → /private/var)
	// so assertions on daemon directory ties compare equal strings.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	daemon := &stubCreateDaemon{}
	log := history.New(history.Options{})
	svc := mktorrent.New(mktorrent.Options{
		Daemon:  daemon,
		History: log,
		Roots:   func() []string { return []string{root} },
	})
	srv := New(Options{Store: &dirTestStore{roots: []string{root}}, History: log, Create: svc}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return &createStack{ts: ts, srv: srv, svc: svc, daemon: daemon, log: log, root: root}
}

func postCreate(t *testing.T, url string, body map[string]any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func waitCreate(t *testing.T, ts *httptest.Server, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(ts.URL + "/api/torrents/create/" + id)
		if err != nil {
			t.Fatal(err)
		}
		var job map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if job["status"] != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("creation job never finished")
	return nil
}

func TestCreateValidation(t *testing.T) {
	st := newCreateStack(t)
	outside := t.TempDir()

	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"empty source", map[string]any{}, "bad_request"},
		{"relative source", map[string]any{"source": "rel/path"}, "bad_request"},
		{"missing source", map[string]any{"source": filepath.Join(st.root, "nope")}, "bad_request"},
		{"outside roots", map[string]any{"source": outside}, "path_outside_download_dirs"},
		{"bad tracker", map[string]any{"source": st.root, "trackers": []string{"ftp://x/y"}}, "bad_request"},
		{"bad piece length", map[string]any{"source": st.root, "piece_length": 1000}, "bad_request"},
		{"bad name", map[string]any{"source": st.root, "name": "a/b"}, "bad_request"},
	}
	for _, tc := range cases {
		status, body := postCreate(t, st.ts.URL+"/api/torrents/create", tc.body)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status = %d", tc.name, status)
			continue
		}
		errObj, _ := body["error"].(map[string]any)
		if code, _ := errObj["code"].(string); code != tc.code {
			t.Errorf("%s: code = %q, want %q (body %+v)", tc.name, code, tc.code, body)
		}
	}
}

func TestCreateSingleFileFlow(t *testing.T) {
	st := newCreateStack(t)
	data := []byte("hello torrent world, this is test data for hashing pieces")
	if err := os.WriteFile(filepath.Join(st.root, "movie.mkv"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	status, body := postCreate(t, st.ts.URL+"/api/torrents/create", map[string]any{
		"source":   filepath.Join(st.root, "movie.mkv"),
		"trackers": []string{"https://tracker.example/announce"},
		"comment":  "smoke",
	})
	if status != http.StatusAccepted {
		t.Fatalf("submit = %d %+v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("no job id: %+v", body)
	}
	job := waitCreate(t, st.ts, id)
	if job["status"] != "completed" {
		t.Fatalf("job = %+v", job)
	}
	infohash, _ := job["infohash"].(string)
	if len(infohash) != 40 {
		t.Fatalf("infohash = %+v", job)
	}

	// Download streams the finished bytes as an attachment.
	resp, err := http.Get(st.ts.URL + "/api/torrents/create/" + id + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-bittorrent" {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "movie.mkv.torrent") {
		t.Fatalf("content-disposition = %q", cd)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := torrentfile.Parse(raw)
	if err != nil {
		t.Fatalf("downloaded bytes do not parse: %v", err)
	}
	if meta.Infohash != infohash || meta.Name != "movie.mkv" || meta.Comment != "smoke" {
		t.Fatalf("meta = %+v", meta)
	}

	// Post-hoc add ties the session directory to the source's parent.
	status, added := postCreate(t, st.ts.URL+"/api/torrents/create/"+id+"/add", map[string]any{"start": true})
	if status != http.StatusOK {
		t.Fatalf("add = %d %+v", status, added)
	}
	if added["hash"] != infohash || added["started"] != true {
		t.Fatalf("added = %+v", added)
	}
	if len(st.daemon.adds) != 1 {
		t.Fatalf("daemon adds = %+v", st.daemon.adds)
	}
	joined := strings.Join(st.daemon.adds[0].ExtraCommands, " ")
	if !strings.Contains(joined, "d.directory.set="+st.root) {
		t.Fatalf("directory tie missing: %+v", st.daemon.adds[0])
	}
	// Both the creation and the add are history.
	names := map[string]bool{}
	for _, ev := range st.log.Query(history.Filter{Hash: infohash}, 10, 0).Events {
		names[ev.Action] = true
	}
	if !names["create_torrent"] || !names["add"] {
		t.Fatalf("history actions = %+v", st.log.Query(history.Filter{Hash: infohash}, 10, 0).Events)
	}
}

func TestCreateMultiFilePrivate(t *testing.T) {
	st := newCreateStack(t)
	pack := filepath.Join(st.root, "pack")
	if err := os.Mkdir(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "b.bin"), []byte(strings.Repeat("b", 100000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "a.bin"), []byte(strings.Repeat("a", 50000)), 0o644); err != nil {
		t.Fatal(err)
	}

	status, body := postCreate(t, st.ts.URL+"/api/torrents/create", map[string]any{
		"source":         pack,
		"trackers":       []string{"https://one.example/a", "udp://two.example:1337/a"},
		"piece_length":   131072,
		"private":        true,
		"source_tag":     "TEST",
		"add_to_session": true,
		"start":          false,
		"label":          "iso",
	})
	if status != http.StatusAccepted {
		t.Fatalf("submit = %d %+v", status, body)
	}
	job := waitCreate(t, st.ts, body["id"].(string))
	if job["status"] != "completed" {
		t.Fatalf("job = %+v", job)
	}
	if job["fileCount"] != float64(2) || job["pieceLength"] != float64(131072) {
		t.Fatalf("job = %+v", job)
	}
	if added, _ := job["added"].(bool); !added {
		t.Fatalf("at-creation add missing: %+v", job)
	}
	if len(st.daemon.adds) != 1 || st.daemon.adds[0].Start {
		t.Fatalf("daemon adds = %+v", st.daemon.adds)
	}
	joined := strings.Join(st.daemon.adds[0].ExtraCommands, " ")
	if !strings.Contains(joined, "d.directory.set="+st.root) || !strings.Contains(joined, "d.custom1.set=iso") {
		t.Fatalf("directory tie/label missing: %+v", st.daemon.adds[0])
	}

	resp, err := http.Get(st.ts.URL + "/api/torrents/create/" + body["id"].(string) + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := torrentfile.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Private || meta.Name != "pack" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestCreateJobErrors(t *testing.T) {
	st := newCreateStack(t)

	for url, want := range map[string]int{
		"/api/torrents/create/nope":          http.StatusNotFound,
		"/api/torrents/create/nope/cancel":   http.StatusNotFound,
		"/api/torrents/create/nope/download": http.StatusNotFound,
		"/api/torrents/create/nope/add":      http.StatusNotFound,
	} {
		var resp *http.Response
		var err error
		if strings.HasSuffix(url, "/cancel") || strings.HasSuffix(url, "/add") {
			resp, err = http.Post(st.ts.URL+url, "application/json", strings.NewReader("{}"))
		} else {
			resp, err = http.Get(st.ts.URL + url)
		}
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET/POST %s = %d, want %d", url, resp.StatusCode, want)
		}
	}

	// Cancelling a finished job reports it unchanged.
	if err := os.WriteFile(filepath.Join(st.root, "f.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, body := postCreate(t, st.ts.URL+"/api/torrents/create", map[string]any{"source": filepath.Join(st.root, "f.bin")})
	if status != http.StatusAccepted {
		t.Fatalf("submit = %d", status)
	}
	job := waitCreate(t, st.ts, body["id"].(string))
	resp, err := http.Post(st.ts.URL+"/api/torrents/create/"+job["id"].(string)+"/cancel", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cancelled map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled["status"] != "completed" {
		t.Fatalf("cancel of finished job = %+v", cancelled)
	}
}

func TestCreateUnwired(t *testing.T) {
	srv := New(Options{Store: &dirTestStore{}}, NewAuth("", "", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	resp, err := http.Post(ts.URL+"/api/torrents/create", "application/json", strings.NewReader(`{"source":"/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
