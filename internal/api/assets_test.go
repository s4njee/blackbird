package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNegotiateEncoding(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"gzip", "gzip"},
		{"gzip, deflate, br", "br"},
		{"gzip, deflate, br, zstd", "br"},
		{"br;q=0", ""},
		{"br;q=0, gzip", "gzip"},
		{"gzip;q=0.0", ""},
		{"GZip, BR", "br"},
		{"identity", ""},
		{"*;q=0.5", ""},
	}
	for _, c := range cases {
		if got := negotiateEncoding(c.in); got != c.want {
			t.Errorf("negotiate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// stubAssets builds an assetServer over an in-memory FS so tests do not
// depend on the checked-in frontend build.
func stubAssets() *assetServer {
	raw := []byte(strings.Repeat("console.log(\"blackbird\");\n", 100))
	dist := fstest.MapFS{
		"index.html":                {Data: []byte("<html>" + strings.Repeat("<p>shell</p>", 50) + "</html>")},
		"assets/app.js":             {Data: raw},
		"icon.jpg":                  {Data: []byte{0xff, 0xd8, 0xff, 0x00}},
		"fonts/ibm-plex-sans.woff2": {Data: []byte{0x77, 0x4f, 0x46, 0x32}},
	}
	s := &assetServer{files: map[string]*assetBody{}, fallback: http.FileServer(http.FS(dist)), dist: dist}
	types := map[string]string{"index.html": "text/html; charset=utf-8", "assets/app.js": "text/javascript; charset=utf-8"}
	for _, p := range []string{"index.html", "assets/app.js"} {
		data, _ := fs.ReadFile(dist, p)
		body := &assetBody{contentType: types[p], cacheControl: cacheControlFor(p), raw: data}
		if gz := gzipBytes(data); len(gz) < len(data) {
			body.gzip = gz
		}
		if br := brotliBytes(data); len(br) < len(body.smallest()) {
			body.br = br
		}
		s.files[p] = body
	}
	return s
}

func getAsset(t *testing.T, s *assetServer, target, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	return rec
}

func TestAssetEncodingNegotiation(t *testing.T) {
	s := stubAssets()

	br := getAsset(t, s, "/assets/app.js", "gzip, deflate, br")
	if br.Code != http.StatusOK || br.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("br: code=%d encoding=%q", br.Code, br.Header().Get("Content-Encoding"))
	}
	gz := getAsset(t, s, "/assets/app.js", "gzip")
	if gz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: encoding=%q", gz.Header().Get("Content-Encoding"))
	}
	if len(br.Body.Bytes()) >= len(gz.Body.Bytes()) {
		t.Fatalf("br %d not smaller than gzip %d", len(br.Body.Bytes()), len(gz.Body.Bytes()))
	}
	identity := getAsset(t, s, "/assets/app.js", "")
	if identity.Header().Get("Content-Encoding") != "" {
		t.Fatalf("identity: encoding=%q", identity.Header().Get("Content-Encoding"))
	}
	excluded := getAsset(t, s, "/assets/app.js", "br;q=0, gzip;q=0")
	if excluded.Header().Get("Content-Encoding") != "" {
		t.Fatalf("excluded: encoding=%q", excluded.Header().Get("Content-Encoding"))
	}
	for _, rec := range []*httptest.ResponseRecorder{br, gz, identity} {
		if rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("missing Vary header: %+v", rec.Header())
		}
		if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("cache = %q", rec.Header().Get("Cache-Control"))
		}
	}
}

func TestAssetShellAndBinary(t *testing.T) {
	s := stubAssets()

	// index.html stays no-cache even when compressed.
	idx := getAsset(t, s, "/", "br")
	if idx.Code != http.StatusOK || idx.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index: code=%d cache=%q", idx.Code, idx.Header().Get("Cache-Control"))
	}
	if idx.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("index not compressed: %+v", idx.Header())
	}

	// Unknown paths fall back to the app shell (compressed).
	spa := getAsset(t, s, "/stats", "gzip")
	if spa.Code != http.StatusOK || spa.Body.String() == "" {
		t.Fatalf("spa fallback: code=%d", spa.Code)
	}
	if ct := spa.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("spa content-type = %q", ct)
	}

	// Binary formats stream raw with legacy cache headers.
	img := getAsset(t, s, "/icon.jpg", "gzip, br")
	if img.Code != http.StatusOK || img.Header().Get("Content-Encoding") != "" {
		t.Fatalf("image: code=%d encoding=%q", img.Code, img.Header().Get("Content-Encoding"))
	}
	if cc := img.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Fatalf("image cache = %q", cc)
	}
	font := getAsset(t, s, "/fonts/ibm-plex-sans.woff2", "br")
	if font.Header().Get("Content-Encoding") != "" {
		t.Fatalf("font compressed: %q", font.Header().Get("Content-Encoding"))
	}
}

// TestEmbeddedAssetsCompressed proves the real embedded build serves its
// shell and scripts pre-compressed (PERF-6.5 acceptance on the artifact,
// not just the harness).
func TestEmbeddedAssetsCompressed(t *testing.T) {
	s := embeddedAssets()
	if _, ok := s.files["index.html"]; !ok {
		t.Fatal("embedded dist has no index.html")
	}
	foundJS := false
	for p, body := range s.files {
		if !strings.HasSuffix(p, ".js") {
			continue
		}
		foundJS = true
		if body.br == nil || body.gzip == nil {
			t.Fatalf("%s missing a compressed variant", p)
		}
	}
	if !foundJS {
		t.Fatal("embedded dist has no scripts")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("shell: code=%d encoding=%q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("shell cache = %q", rec.Header().Get("Cache-Control"))
	}
}
