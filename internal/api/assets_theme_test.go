package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/andybalholm/brotli"
)

// stubThemeAssets mirrors stubAssets but its index.html body carries the
// THM-9.2 placeholder (web/index.html does not contain it yet, and web/
// must not be touched by this story).
func stubThemeAssets(hook func() string) *assetServer {
	indexRaw := []byte("<html><head><script>window.__THEME__=" + ThemePlaceholder + ";</script></head><body>" + strings.Repeat("<p>shell</p>", 50) + "</body></html>")
	appRaw := []byte(strings.Repeat("console.log(\"blackbird\");\n", 100))
	dist := fstest.MapFS{
		"index.html":    {Data: indexRaw},
		"assets/app.js": {Data: appRaw},
	}
	s := &assetServer{
		files:        map[string]*assetBody{},
		fallback:     http.FileServer(http.FS(dist)),
		dist:         dist,
		ThemeDefault: hook,
	}
	types := map[string]string{"index.html": "text/html; charset=utf-8", "assets/app.js": "text/javascript; charset=utf-8"}
	for _, p := range []string{"index.html", "assets/app.js"} {
		data := dist[p].Data
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

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	data := rec.Body.Bytes()
	switch rec.Header().Get("Content-Encoding") {
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	case "br":
		return string(brotliBytesDecode(t, data))
	default:
		return rec.Body.String()
	}
}

func brotliBytesDecode(t *testing.T, data []byte) []byte {
	t.Helper()
	r := brotli.NewReader(bytes.NewReader(data))
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIndexThemeInjection(t *testing.T) {
	s := stubThemeAssets(func() string { return "midnight" })

	for _, target := range []string{"/", "/stats"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		rec := httptest.NewRecorder()
		s.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: code=%d", target, rec.Code)
		}
		// Injected shell is always identity.
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("%s: Content-Encoding=%q, want identity", target, enc)
		}
		body := rec.Body.String()
		if strings.Contains(body, ThemePlaceholder) {
			t.Fatalf("%s: placeholder not replaced", target)
		}
		if !strings.Contains(body, "midnight") {
			t.Fatalf("%s: theme not injected: %.120q", target, body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: Content-Type=%q", target, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: Cache-Control=%q", target, cc)
		}
		if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
			t.Fatalf("%s: Vary=%q", target, v)
		}
		if cl := rec.Header().Get("Content-Length"); cl != itoa(len(rec.Body.Bytes())) {
			t.Fatalf("%s: Content-Length=%q, body=%d", target, cl, len(rec.Body.Bytes()))
		}
	}

	// Direct /index.html requests are injected too.
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), ThemePlaceholder) || !strings.Contains(rec.Body.String(), "midnight") {
		t.Fatalf("direct index.html not injected: %.120q", rec.Body.String())
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("direct index.html encoding=%q, want identity", enc)
	}
}

func TestIndexThemeInvalidFallsBackToDark(t *testing.T) {
	for _, hookVal := range []string{"neon", ""} {
		s := stubThemeAssets(func() string { return hookVal })
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "br")
		rec := httptest.NewRecorder()
		s.handler().ServeHTTP(rec, req)
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("hook %q: encoding=%q, want identity", hookVal, enc)
		}
		if strings.Contains(rec.Body.String(), ThemePlaceholder) {
			t.Fatalf("hook %q: placeholder survives", hookVal)
		}
		if !strings.Contains(rec.Body.String(), "dark") {
			t.Fatalf("hook %q: want dark fallback: %.120q", hookVal, rec.Body.String())
		}
	}
}

func TestIndexNoHookKeepsPrecompressed(t *testing.T) {
	s := stubThemeAssets(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("nil hook: encoding=%q, want br (precompressed path)", enc)
	}
	if got := decodeBody(t, rec); !strings.Contains(got, ThemePlaceholder) {
		t.Fatalf("nil hook: placeholder should survive, got %.120q", got)
	}

	// Non-index assets stay compressed even with a hook set.
	hooked := stubThemeAssets(func() string { return "midnight" })
	r2 := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r2.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec2 := httptest.NewRecorder()
	hooked.handler().ServeHTTP(rec2, r2)
	if enc := rec2.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("app.js with hook: encoding=%q, want br", enc)
	}
}
