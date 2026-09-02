package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func bcryptHash(t *testing.T, password string) string {
	t.Helper()
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAuthGatesAllRoutes(t *testing.T) {
	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))

	// The liveness probe is intentionally reachable without credentials.
	liveResp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if liveResp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz: status = %d, want 200", liveResp.StatusCode)
	}
	if challenge := liveResp.Header.Get("WWW-Authenticate"); challenge != "" {
		t.Fatalf("/healthz: unexpected auth challenge %q", challenge)
	}
	liveResp.Body.Close()

	// No credentials → 401 with a Basic challenge, on every surface.
	for _, path := range []string{"/", "/api/session", "/api/health", "/api/settings"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, resp.StatusCode)
		}
		if challenge := resp.Header.Get("WWW-Authenticate"); challenge == "" {
			t.Errorf("%s: missing WWW-Authenticate challenge", path)
		}
	}

	// Bad credentials → 401.
	client := &http.Client{}
	req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
	req.SetBasicAuth("op", "wrong")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad credentials status = %d", resp.StatusCode)
	}
}

func TestAuthAcceptsValidCredentials(t *testing.T) {
	ts, p := newTestAPI(t, bcryptHash(t, "hunter2"))
	waitForConnected(t, p)

	client := &http.Client{}
	req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
	req.SetBasicAuth("op", "hunter2")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Wrong username (even with right password) → 401.
	req2, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
	req2.SetBasicAuth("nope", "hunter2")
	resp2, _ := client.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("wrong user status = %d", resp2.StatusCode)
	}
}

func TestAuthRateLimiting(t *testing.T) {
	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))

	client := &http.Client{Timeout: 5 * time.Second}
	var saw429 bool
	for i := 0; i < 12; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
		req.SetBasicAuth("op", fmt.Sprintf("wrong-%d", i))
		// Add randomness so bcrypt comparison cost doesn't dominate.
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			saw429 = true
			if resp.Header.Get("Retry-After") == "" {
				t.Fatal("429 missing Retry-After")
			}
			break
		}
	}
	if !saw429 {
		t.Fatal("never rate-limited after repeated failures")
	}
}

func TestAuthDisabledWithoutHash(t *testing.T) {
	// Empty password hash = dev mode: open access (documented, warned).
	ts, _ := newTestAPI(t, "")
	resp, err := http.Get(ts.URL + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	_ = json.Marshal
}

// TestAuthRateLimitStateMachine exercises the per-IP counter directly:
// exact threshold behavior, window expiry, and reset-on-success.
func TestAuthRateLimitStateMachine(t *testing.T) {
	a := NewAuth("op", bcryptHash(t, "x"), nil)
	ip := "10.0.0.9"

	if a.blocked(ip) {
		t.Fatal("fresh IP starts blocked")
	}
	for i := 0; i < maxAuthFailures-1; i++ {
		a.recordFailure(ip)
	}
	if a.blocked(ip) {
		t.Fatal("blocked below the failure limit")
	}
	a.recordFailure(ip)
	if !a.blocked(ip) {
		t.Fatal("never blocked at the failure limit")
	}

	// Once the block window and the failure window both expire, the state is
	// cleared and the next failure starts a fresh count.
	a.mu.Lock()
	st := a.fails[ip]
	st.blockedUntil = time.Now().Add(-time.Second)
	st.lastFailure = time.Now().Add(-authFailureWindow - time.Second)
	a.mu.Unlock()
	if a.blocked(ip) {
		t.Fatal("still blocked after window expiry")
	}
	a.recordFailure(ip)
	a.mu.Lock()
	if got := a.fails[ip].failures; got != 1 {
		a.mu.Unlock()
		t.Fatalf("failure counter not reset after window: %d", got)
	}
	a.mu.Unlock()

	// reset() clears the state entirely (a successful login calls it).
	a.recordFailure(ip)
	a.recordFailure(ip)
	a.reset(ip)
	a.mu.Lock()
	_, exists := a.fails[ip]
	a.mu.Unlock()
	if exists {
		t.Fatal("reset did not clear the failure state")
	}
}

// TestAuthSuccessResetsFailureCounter verifies end to end that a valid login
// resets the per-IP failure counter, so the rate limit starts from zero after
// it.
func TestAuthSuccessResetsFailureCounter(t *testing.T) {
	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))
	client := &http.Client{Timeout: 5 * time.Second}
	status := func(user, pass string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
		req.SetBasicAuth(user, pass)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// A few failures below the limit.
	for i := 0; i < 3; i++ {
		if s := status("op", fmt.Sprintf("wrong-%d", i)); s != http.StatusUnauthorized {
			t.Fatalf("failure status = %d", s)
		}
	}
	// A success resets the counter.
	if s := status("op", "hunter2"); s != http.StatusOK {
		t.Fatalf("success status = %d", s)
	}
	// The threshold then applies from zero: the (max+1)-th consecutive
	// failure is rate-limited.
	var last int
	for i := 0; i <= maxAuthFailures; i++ {
		last = status("op", fmt.Sprintf("fresh-%d", i))
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after reset + limit, got %d", last)
	}
}

// TestAuthFailureLogsIPNotCredentials captures the middleware's failure log
// and asserts source IP is recorded while credentials never are.
func TestAuthFailureLogsIPNotCredentials(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))
	req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
	req.SetBasicAuth("op", "hunter2-wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body := buf.String()
	if !strings.Contains(body, "ip=127.0.0.1") {
		t.Errorf("failure log missing source ip: %s", body)
	}
	if strings.Contains(body, "hunter2-wrong") || strings.Contains(body, "hunter2") {
		t.Errorf("credentials leaked into logs: %s", body)
	}
}
