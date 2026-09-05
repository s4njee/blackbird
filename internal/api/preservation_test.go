package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"blackbird/internal/fakertorrent"
	"blackbird/internal/preservation"
)

func TestPreservationAPIAndRemovalGuard(t *testing.T) {
	stack := newTestStack(t, "", fakertorrent.Options{SessionSize: 4})
	waitForConnected(t, stack.p)
	st, err := preservation.Open(preservation.Options{Path: filepath.Join(t.TempDir(), "watch.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Options{Poller: stack.p, RTorrent: stack.rtc, Store: stack.store, Preservation: st}, NewAuth("", "", nil))
	defer srv.Close()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(w, r)
		return w
	}
	hash := stack.p.Snapshot().Torrents[0].Hash
	w := call("POST", "/api/v1/preservation", fmt.Sprintf(`{"action":"watch","hash":%q,"pinned":true,"reason":"keep source"}`, hash))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, action := range []string{"remove", "remove_with_data"} {
		w = call("POST", "/api/v1/torrents/action", fmt.Sprintf(`{"action":%q,"hashes":[%q]}`, action, hash))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "preservation pin blocks removal") || !strings.Contains(w.Body.String(), `"ok":false`) {
			t.Fatalf("cleanup guard: %d %s", w.Code, w.Body.String())
		}
	}
	st.Sample(preservation.Input{Snapshot: stack.p.Snapshot()})
	w = call("GET", "/api/v1/preservation?hash="+hash, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code)
	}
	var body struct {
		Watches []preservation.Summary `json:"watches"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Watches) != 1 || len(body.Watches[0].Samples) != 1 || !body.Watches[0].Pinned {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "/api/v1/preservation", fmt.Sprintf(`{"action":"update","hash":%q,"revision":99}`, hash))
	if w.Code != 409 {
		t.Fatal("stale edit accepted", w.Code)
	}
	w = call("POST", "/api/v1/preservation", fmt.Sprintf(`{"action":"unwatch","hash":%q,"revision":1}`, hash))
	if w.Code != 409 {
		t.Fatal("pinned watch removed", w.Code)
	}
	w = call("POST", "/api/v1/preservation", fmt.Sprintf(`{"action":"update","hash":%q,"revision":1,"pinned":false}`, hash))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("POST", "/api/v1/torrents/action", fmt.Sprintf(`{"action":"remove","hashes":[%q]}`, hash))
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatal(w.Body.String())
	}
}
func TestPreservationAPIAuthAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		auth *Auth
		code int
	}{{NewAuth("", "", nil), 503}, {NewAuth("operator", "invalid-hash", nil), 401}} {
		s := New(Options{}, tc.auth)
		for _, method := range []string{"GET", "POST"} {
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest(method, "/api/v1/preservation", strings.NewReader(`{}`)))
			if w.Code != tc.code {
				t.Fatal(method, w.Code)
			}
		}
		s.Close()
	}
}
