package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"blackbird/internal/themefile"
)

// ---- custom themes + operator CSS (THM-9.4) ----

// themesListHandler serves GET /api/themes: every valid custom theme plus
// per-file validation errors as "file:line: message" strings, so one broken
// file never hides the working themes.
func (s *Server) themesListHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "settings store not wired")
		return
	}
	dir := filepath.Dir(s.opts.Store.ConfigPath())
	themes, ferrs := themefile.LoadDir(dir)
	if themes == nil {
		themes = []themefile.Theme{}
	}
	errStrs := make([]string, 0, len(ferrs))
	for _, e := range ferrs {
		errStrs = append(errStrs, e.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": themes, "errors": errStrs})
}

type themeImportRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// themesImportHandler serves POST /api/themes/import: validates the YAML and
// stores it as <configDir>/themes/<sanitized-name>.yml (0644). Overwriting an
// existing file with the same sanitized name updates the theme.
func (s *Server) themesImportHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "settings store not wired")
		return
	}
	var req themeImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	theme, ferrs := themefile.ValidateContent("import.yml", []byte(req.Content))
	if len(ferrs) > 0 {
		msgs := make([]string, 0, len(ferrs))
		for _, e := range ferrs {
			msgs = append(msgs, e.Error())
		}
		writeAPIError(w, http.StatusBadRequest, "validation", strings.Join(msgs, "; "))
		return
	}
	want := req.Name
	if strings.TrimSpace(want) == "" {
		want = theme.Name
	}
	base, ok := themefile.SanitizeName(want)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid theme name")
		return
	}
	dir := filepath.Dir(s.opts.Store.ConfigPath())
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0o755); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "io_error", err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "themes", base+".yml"), []byte(req.Content), 0o644); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "io_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"theme": theme})
}

// themeDeleteHandler serves DELETE /api/themes/{name}: removes the theme file
// (either .yml or .yaml spelling). Path separators and ".." are rejected so
// the sanitized base can never escape the themes directory.
func (s *Server) themeDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "settings store not wired")
		return
	}
	raw := r.PathValue("name")
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "/") || strings.Contains(raw, "\\") || strings.Contains(raw, "..") {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid theme name")
		return
	}
	base, ok := themefile.SanitizeName(raw)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid theme name")
		return
	}
	dir := filepath.Join(filepath.Dir(s.opts.Store.ConfigPath()), "themes")
	removed := false
	for _, ext := range []string{".yml", ".yaml"} {
		if err := os.Remove(filepath.Join(dir, base+ext)); err == nil {
			removed = true
			break
		} else if !os.IsNotExist(err) {
			writeAPIError(w, http.StatusInternalServerError, "io_error", err.Error())
			return
		}
	}
	if !removed {
		writeAPIError(w, http.StatusNotFound, "not_found", "theme not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// customCSSHandler serves GET /api/custom-css: the operator's custom.css as
// text/css, 200 with an empty body when absent (absence is the normal
// state; empty is unambiguous for fetch clients, unlike 204 which some
// Chromium versions report as aborted), 413 when it exceeds the size limit.
func (s *Server) customCSSHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured", "settings store not wired")
		return
	}
	dir := filepath.Dir(s.opts.Store.ConfigPath())
	data, err := themefile.ReadCustomCSS(dir)
	if err != nil {
		switch {
		case errors.Is(err, themefile.ErrTooLarge):
			writeAPIError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
		case errors.Is(err, os.ErrNotExist):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
		default:
			writeAPIError(w, http.StatusInternalServerError, "io_error", err.Error())
		}
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Content-Length", itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
