package api

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"

	"blackbird/web"
)

// Pre-compressed embedded frontend (PERF-6.5): text assets ship brotli and
// gzip variants built once at startup, negotiated per request. Already
// compressed formats (fonts, images) stream raw through the file server,
// preserving its range/ETag behavior.

// compressibleTypes are served with pre-compressed variants; everything else
// falls through to the plain file server.
var compressibleTypes = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".json": "application/json",
	".map":  "application/json",
	".txt":  "text/plain; charset=utf-8",
	".xml":  "application/xml",
}

// assetBody is one embedded file plus its encodings. An encoding is kept
// only when it shrinks the payload.
type assetBody struct {
	contentType  string
	cacheControl string
	raw          []byte
	gzip         []byte
	br           []byte
}

// assetServer serves the embedded Vite build. Compressed-eligible files are
// held fully encoded in memory (~1MB total); all other paths stream from
// the embedded file server exactly as before.
type assetServer struct {
	files    map[string]*assetBody
	fallback http.Handler
	dist     fs.FS
	// ThemeDefault supplies the operator-default theme id (ui.theme) for
	// the no-flash boot script in index.html. Nil disables injection and
	// keeps the precompressed variants exactly as before.
	ThemeDefault func() string
	// AccentDefault supplies the operator-default accent (ui.accent) for
	// the same script. Nil, empty, or invalid disables accent injection.
	AccentDefault func() string
}

// ThemePlaceholder is replaced in index.html with the operator-default
// theme id before serving (THM-9.2). The placeholder itself lives in
// web/index.html; it is referenced here so tests can use synthetic bodies.
const ThemePlaceholder = "__BLACKBIRD_THEME_DEFAULT__"

// validTheme reports whether id is an allowed ui.theme value.
func validTheme(id string) bool {
	switch id {
	case "dark", "light", "midnight", "contrast", "classic", "system":
		return true
	default:
		return false
	}
}

// themeChoice resolves the hook result to a safe theme id: the hook value
// when it names an allowed theme, otherwise "dark" (empty means dark).
func (s *assetServer) themeChoice() string {
	if s.ThemeDefault == nil {
		return ""
	}
	if t := s.ThemeDefault(); validTheme(t) {
		return t
	}
	return "dark"
}

// AccentPlaceholder is replaced in index.html with the operator-default
// accent (#rrggbb) so the boot script paints it pre-paint. Invalid or empty
// values inject "" and the script keeps the theme default.
const AccentPlaceholder = "__BLACKBIRD_ACCENT_DEFAULT__"

// validAccent reports whether s is a #rrggbb hex color.
func validAccent(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// accentChoice resolves the hook result to a safe accent: the hook value
// when it is a valid hex color, otherwise "" (theme default).
func (s *assetServer) accentChoice() string {
	if s.AccentDefault == nil {
		return ""
	}
	if c := s.AccentDefault(); validAccent(c) {
		return c
	}
	return ""
}

// serveIndexHTML serves index.html with the theme placeholder replaced by
// the validated operator default. It always serves identity (no
// Content-Encoding) with correct Content-Length and the same
// Content-Type/Cache-Control/Vary headers as serveAsset. When no hook is
// set it delegates to serveAsset, preserving the precompressed path.
func (s *assetServer) serveIndexHTML(w http.ResponseWriter, r *http.Request, body *assetBody) {
	if s.ThemeDefault == nil {
		s.serveAsset(w, r, body)
		return
	}
	data := bytes.ReplaceAll(body.raw, []byte(ThemePlaceholder), []byte(s.themeChoice()))
	data = bytes.ReplaceAll(data, []byte(AccentPlaceholder), []byte(s.accentChoice()))
	h := w.Header()
	h.Set("Content-Type", body.contentType)
	h.Set("Cache-Control", body.cacheControl)
	h.Set("Vary", "Accept-Encoding")
	h.Set("Content-Length", itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

var embeddedAssets = sync.OnceValue(func() *assetServer {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic("web/dist missing from embed: " + err.Error())
	}
	s := &assetServer{files: map[string]*assetBody{}, fallback: http.FileServer(http.FS(dist)), dist: dist}
	_ = fs.WalkDir(dist, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ct, ok := compressibleTypes[strings.ToLower(path.Ext(p))]
		if !ok {
			return nil
		}
		raw, err := fs.ReadFile(dist, p)
		if err != nil {
			return nil
		}
		body := &assetBody{contentType: ct, cacheControl: cacheControlFor(p), raw: raw}
		if gz := gzipBytes(raw); len(gz) < len(raw) {
			body.gzip = gz
		}
		if br := brotliBytes(raw); len(br) < len(body.smallest()) {
			body.br = br
		}
		s.files[p] = body
		return nil
	})
	return s
})

func (b *assetBody) smallest() []byte {
	if b.gzip != nil {
		return b.gzip
	}
	return b.raw
}

func gzipBytes(raw []byte) []byte {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := w.Write(raw); err != nil {
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

func brotliBytes(raw []byte) []byte {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, brotli.BestCompression)
	if _, err := w.Write(raw); err != nil {
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

// cacheControlFor mirrors the previous file-server headers: hashed assets
// immutable, index.html no-cache, everything else a day.
func cacheControlFor(p string) string {
	if p == "" || p == "index.html" {
		return "no-cache"
	}
	if strings.HasPrefix(p, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=86400"
}

// negotiateEncoding picks br, gzip, or "" (identity) from Accept-Encoding.
// Quality values of zero exclude the coding; anything else is accepted.
func negotiateEncoding(header string) string {
	var gzip, br bool
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		params = strings.ToLower(strings.TrimSpace(params))
		if params == "q=0" || strings.HasPrefix(params, "q=0.") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(token)) {
		case "br":
			br = true
		case "gzip", "x-gzip":
			gzip = true
		}
	}
	switch {
	case br:
		return "br"
	case gzip:
		return "gzip"
	default:
		return ""
	}
}

// serveAsset writes one pre-compressed body with negotiated encoding.
func (s *assetServer) serveAsset(w http.ResponseWriter, r *http.Request, body *assetBody) {
	data := body.raw
	encoding := ""
	switch negotiateEncoding(r.Header.Get("Accept-Encoding")) {
	case "br":
		if body.br != nil {
			data, encoding = body.br, "br"
		} else if body.gzip != nil {
			data, encoding = body.gzip, "gzip"
		}
	case "gzip":
		if body.gzip != nil {
			data, encoding = body.gzip, "gzip"
		}
	}
	h := w.Header()
	h.Set("Content-Type", body.contentType)
	h.Set("Cache-Control", body.cacheControl)
	h.Set("Vary", "Accept-Encoding")
	h.Set("Content-Length", itoa(len(data)))
	if encoding != "" {
		h.Set("Content-Encoding", encoding)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handler routes SPA paths (unknown → index.html) like the previous file
// server, preferring pre-compressed variants and falling back for binary
// formats.
func (s *assetServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if body, ok := s.files[p]; ok {
			if p == "index.html" {
				s.serveIndexHTML(w, r, body)
				return
			}
			s.serveAsset(w, r, body)
			return
		}
		if p == "" {
			p = "index.html"
			if body, ok := s.files[p]; ok {
				s.serveIndexHTML(w, r, body)
				return
			}
		} else if f, err := s.dist.Open(p); err == nil {
			f.Close()
			// Existing but incompressible (fonts, images): legacy behavior
			// with the same cache headers, including range/ETag support.
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			s.fallback.ServeHTTP(w, r)
			return
		}
		// SPA fallback: unknown paths render the app shell.
		if body, ok := s.files["index.html"]; ok {
			s.serveIndexHTML(w, r, body)
			return
		}
		http.Error(w, "frontend not built", http.StatusNotFound)
	})
}
