package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"blackbird/internal/config"
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

// TestAuthRateLimitingSlowsRepeatedFailures proves brute-force guessing is
// still throttled after the lockout was removed: past the threshold, wrong
// passwords are measurably delayed. The response stays a 401 — a hard 429
// would also refuse the correct password from that address, which behind a
// reverse proxy means every user at once.
func TestAuthRateLimitingSlowsRepeatedFailures(t *testing.T) {
	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))

	client := &http.Client{Timeout: 30 * time.Second}
	attempt := func(i int) (int, time.Duration) {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
		req.SetBasicAuth("op", fmt.Sprintf("wrong-%d", i))
		started := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, time.Since(started)
	}

	// Everything up to the threshold is answered promptly.
	for i := 0; i < maxAuthFailures; i++ {
		if status, _ := attempt(i); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, status)
		}
	}

	// Past it, the same wrong password costs real time.
	status, elapsed := attempt(maxAuthFailures)
	if status != http.StatusUnauthorized {
		t.Fatalf("throttled attempt: status = %d, want 401", status)
	}
	if elapsed < authPenaltyStep {
		t.Fatalf("throttled attempt returned in %v, expected at least %v", elapsed, authPenaltyStep)
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

// TestAuthThrottleDelaysButNeverRefuses exercises the per-address counter
// directly. The throttle is a delay, not a block: attempts past the
// threshold are slowed and the delay is capped, but nothing here can refuse
// a request outright — that is what stops a shared source address (every
// user behind one reverse proxy) from being locked out by an attacker.
func TestAuthThrottleDelaysButNeverRefuses(t *testing.T) {
	a := NewAuth("op", bcryptHash(t, "x"), nil)
	ip := "10.0.0.9"

	// Up to and including the threshold, failures are not delayed at all.
	for i := 0; i < maxAuthFailures; i++ {
		if d := a.recordFailure(ip); d != 0 {
			t.Fatalf("failure %d delayed by %v, below the threshold", i+1, d)
		}
	}
	// Past it, the delay grows per failure.
	first := a.recordFailure(ip)
	if first != authPenaltyStep {
		t.Fatalf("first delay = %v, want %v", first, authPenaltyStep)
	}
	second := a.recordFailure(ip)
	if second <= first {
		t.Fatalf("delay did not grow: %v then %v", first, second)
	}
	// And is capped, so a sustained attack cannot stall responses forever.
	for i := 0; i < 200; i++ {
		a.recordFailure(ip)
	}
	if d := a.recordFailure(ip); d != maxAuthPenalty {
		t.Fatalf("delay = %v, want the %v cap", d, maxAuthPenalty)
	}

	// Once the failure window expires the count starts fresh.
	a.mu.Lock()
	a.fails[ip].lastFailure = time.Now().Add(-authFailureWindow - time.Second)
	a.mu.Unlock()
	if d := a.recordFailure(ip); d != 0 {
		t.Fatalf("delay %v carried across the window boundary", d)
	}

	// reset() clears the state entirely (a successful login calls it).
	a.recordFailure(ip)
	a.reset(ip)
	a.mu.Lock()
	_, exists := a.fails[ip]
	a.mu.Unlock()
	if exists {
		t.Fatal("reset did not clear the failure state")
	}
}

// TestValidCredentialsSurviveAThrottledAddress is the property the old
// design got wrong: it checked the block before the password, so ten bad
// guesses from anywhere locked out everyone sharing that source address —
// which, behind the documented reverse proxy, is every user. A correct
// password must still work from a thoroughly-failing address.
func TestValidCredentialsSurviveAThrottledAddress(t *testing.T) {
	ts, _ := newTestAPI(t, bcryptHash(t, "hunter2"))
	client := &http.Client{Timeout: 30 * time.Second}

	// Bury the address (127.0.0.1, shared by every request here) in
	// failures, well past the old lockout threshold.
	for i := 0; i < maxAuthFailures+5; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
		req.SetBasicAuth("op", "wrong")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bad password gave %d, want 401", resp.StatusCode)
		}
	}

	req, _ := http.NewRequest("GET", ts.URL+"/api/session", nil)
	req.SetBasicAuth("op", "hunter2")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct password refused with %d from a throttled address", resp.StatusCode)
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
	// The threshold then applies from zero: the next full run of failures
	// is answered promptly, which is only true if the counter restarted.
	// (Were it still counting, these would land past the threshold and be
	// delayed by authPenaltyStep or more.)
	started := time.Now()
	for i := 0; i < maxAuthFailures; i++ {
		if s := status("op", fmt.Sprintf("fresh-%d", i)); s != http.StatusUnauthorized {
			t.Fatalf("post-reset failure %d: status = %d, want 401", i, s)
		}
	}
	if elapsed := time.Since(started); elapsed > time.Duration(maxAuthFailures)*authPenaltyStep {
		t.Fatalf("post-reset failures took %v: the counter did not reset", elapsed)
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

// TestCrossOriginMutationsRefused is the CSRF gate. HTTP Basic credentials
// are ambient — a browser replays them on cross-site requests — so without
// this a page an authenticated operator visits can drive the whole API,
// including the raw XML-RPC endpoint.
func TestCrossOriginMutationsRefused(t *testing.T) {
	ts, _ := newTestAPI(t, "")
	client := &http.Client{Timeout: 10 * time.Second}

	do := func(method, path string, headers map[string]string) int {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// A hostile page: the browser labels it, and sends its own Origin.
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"},
		{"Origin": "https://evil.example"}, // older browser, no Sec-Fetch-Site
		{"Sec-Fetch-Site": "none"},         // not initiated by a document
	} {
		if got := do("POST", "/api/torrents/action", h); got != http.StatusForbidden {
			t.Fatalf("cross-origin POST %v: status = %d, want 403", h, got)
		}
	}

	// The app's own requests are same-origin and must pass the gate. (405
	// or 400 here is fine — anything but the 403 the gate returns.)
	if got := do("POST", "/api/torrents/action", map[string]string{"Sec-Fetch-Site": "same-origin"}); got == http.StatusForbidden {
		t.Fatal("same-origin POST was refused by the CSRF gate")
	}

	// Reads are never gated: a cross-origin GET cannot act, and gating them
	// would break ordinary navigation.
	if got := do("GET", "/api/health", map[string]string{
		"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example",
	}); got != http.StatusOK {
		t.Fatalf("cross-origin GET = %d, want 200 (reads are not gated)", got)
	}

	// A non-browser client sends no Origin at all and carries no ambient
	// credentials, so there is nothing to forge; curl must keep working.
	if got := do("POST", "/api/torrents/action", nil); got == http.StatusForbidden {
		t.Fatal("originless client refused: non-browser callers must still work")
	}
}

// TestTrustedOriginAllowsCrossOrigin covers the configured escape hatch for
// a genuinely separate origin (a dev server, typically).
func TestTrustedOriginAllowsCrossOrigin(t *testing.T) {
	a := NewAuth("", "", nil)
	a.SetServerPolicy(config.Server{TrustedOrigins: []string{"http://localhost:5173"}})

	req := httptest.NewRequest("POST", "http://blackbird.local/api/torrents/action", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "http://localhost:5173")
	if !a.SameOrigin(req) {
		t.Fatal("configured trusted origin was refused")
	}

	req.Header.Set("Origin", "http://localhost:5174")
	if a.SameOrigin(req) {
		t.Fatal("an unlisted origin was accepted")
	}
}

// TestClientIPIgnoresUntrustedForwardedFor is the other half of the
// rate-limit fix: X-Forwarded-For is believed only from a configured proxy,
// so a direct client can neither dodge its own failures nor poison another
// address's record.
func TestClientIPIgnoresUntrustedForwardedFor(t *testing.T) {
	req := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/api/session", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}

	// No trusted proxies configured: the header is ignored entirely.
	a := NewAuth("", "", nil)
	if got := a.clientIP(req("203.0.113.9:1234", "1.2.3.4")); got != "203.0.113.9" {
		t.Fatalf("unconfigured: clientIP = %q, want the peer address", got)
	}

	a.SetServerPolicy(config.Server{TrustedProxies: []string{"127.0.0.1/32"}})

	// From the trusted proxy, the forwarded client is used.
	if got := a.clientIP(req("127.0.0.1:5555", "198.51.100.7")); got != "198.51.100.7" {
		t.Fatalf("via proxy: clientIP = %q, want the forwarded client", got)
	}
	// A forged chain from an untrusted peer is still ignored.
	if got := a.clientIP(req("203.0.113.9:1234", "198.51.100.7")); got != "203.0.113.9" {
		t.Fatalf("forged: clientIP = %q, want the peer address", got)
	}
	// Only the rightmost untrusted hop counts; earlier entries are
	// attacker-supplied and must not win.
	if got := a.clientIP(req("127.0.0.1:5555", "9.9.9.9, 198.51.100.7")); got != "198.51.100.7" {
		t.Fatalf("chain: clientIP = %q, want the rightmost untrusted hop", got)
	}
}
