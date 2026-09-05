package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"blackbird/internal/config"
)

// Auth is the single-user HTTP Basic gate (Epic 4.3). Basic was chosen over
// a login form deliberately: it works without JS gymnastics, covers the
// WebSocket upgrade in the same handshake, and is documented in
// internal/config/example.yml. TLS expectation: terminate TLS in a front
// proxy, or configure the built-in listener with cert/key (docs, Epic 11).
//
// When PasswordHash is empty, auth is DISABLED (dev / first-run convenience);
// the constructor logs a loud warning. Any configured hash enables
// enforcement on every route, including the WebSocket upgrade.
//
// The middleware also carries the same-origin gate. Basic credentials are
// ambient: a browser replays them on cross-site requests automatically, so
// authentication alone does not establish that a state-changing request
// came from this app. See SameOrigin.
type Auth struct {
	Username     string
	PasswordHash string // bcrypt
	Realm        string

	// Origin/proxy policy, set once at startup by SetServerPolicy.
	policyMu       sync.RWMutex
	trustedOrigins []string
	trustedProxies []*net.IPNet

	// Failure rate limiting, per source address.
	mu    sync.Mutex
	fails map[string]*ipState

	// hashSem bounds concurrent bcrypt verifications. bcrypt is
	// deliberately slow, so unbounded guessing is a CPU exhaustion vector;
	// bounding it is what lets a wrong password be merely slow instead of
	// having to refuse the address outright.
	hashSem chan struct{}
}

const (
	// maxAuthFailures is how many failures from one address inside the
	// window pass before attempts start being delayed.
	maxAuthFailures   = 10
	authFailureWindow = time.Minute
	// Delay applied to a *failed* attempt once past the threshold, growing
	// per additional failure and capped. Correct credentials are never
	// delayed and never refused.
	authPenaltyStep = 250 * time.Millisecond
	maxAuthPenalty  = 5 * time.Second
	// maxConcurrentHashes caps simultaneous password verifications.
	maxConcurrentHashes = 4
)

type ipState struct {
	failures    int
	lastFailure time.Time
}

func NewAuth(username, passwordHash string, logger *slog.Logger) *Auth {
	if passwordHash != "" && username == "" {
		username = "admin"
	}
	if passwordHash == "" && logger != nil {
		logger.Warn("auth.password_hash is empty — API and WebSocket are UNAUTHENTICATED; set a bcrypt hash for any exposed deployment")
	}
	return &Auth{
		Username:     username,
		PasswordHash: passwordHash,
		Realm:        "blackbird",
		fails:        map[string]*ipState{},
		hashSem:      make(chan struct{}, maxConcurrentHashes),
	}
}

// SetServerPolicy installs the origin and proxy policy from config. Call it
// before serving; the values are read on every request but written once.
// An unparseable proxy entry is skipped rather than silently widening
// trust — config validation rejects those before startup anyway.
func (a *Auth) SetServerPolicy(srv config.Server) {
	nets := make([]*net.IPNet, 0, len(srv.TrustedProxies))
	for _, entry := range srv.TrustedProxies {
		if _, n, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, n)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	origins := make([]string, 0, len(srv.TrustedOrigins))
	for _, o := range srv.TrustedOrigins {
		origins = append(origins, strings.TrimRight(o, "/"))
	}

	a.policyMu.Lock()
	defer a.policyMu.Unlock()
	a.trustedOrigins = origins
	a.trustedProxies = nets
}

// stateChanging reports whether the method can alter server state. Only
// these are gated on origin: a cross-origin GET cannot be used to act, and
// gating reads would break ordinary navigation.
func stateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// SameOrigin reports whether a request came from this app rather than from
// a page under someone else's control. It is the CSRF defense: HTTP Basic
// credentials are replayed automatically by the browser, so without this a
// hostile page a logged-in operator visits can drive the whole API.
//
// A token was not used deliberately — Basic auth is stateless, so a token
// would need a session store the design intentionally avoids, and a
// Content-Type check would not cover the multipart upload endpoint, whose
// content type is CORS-safe and therefore forgeable.
func (a *Auth) SameOrigin(r *http.Request) bool {
	// Sec-Fetch-Site is set by every current browser and, unlike comparing
	// Origin to Host, needs no assumptions about how a proxy rewrites Host.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site":
		return true
	case "cross-site":
		return a.originTrusted(r.Header.Get("Origin"))
	case "none":
		// A typed URL or a bookmark: legitimate for navigation, never how
		// this app issues a mutation or opens its socket.
		return false
	}

	// No Sec-Fetch-Site: an older browser, or a non-browser client.
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Nothing to spoof from: curl and the Go client carry no ambient
		// browser credentials, so there is no cross-site request to forge.
		return true
	}
	if u, err := neturl.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return a.originTrusted(origin)
}

func (a *Auth) originTrusted(origin string) bool {
	if origin == "" {
		return false
	}
	origin = strings.TrimRight(origin, "/")
	a.policyMu.RLock()
	defer a.policyMu.RUnlock()
	for _, t := range a.trustedOrigins {
		if strings.EqualFold(t, origin) {
			return true
		}
	}
	return false
}

// clientIP resolves the address a request is attributed to for failed-login
// accounting. X-Forwarded-For is believed only when the immediate peer is a
// configured trusted proxy: otherwise any client could name itself and
// either dodge its own failures or poison another address's record.
func (a *Auth) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	a.policyMu.RLock()
	proxies := a.trustedProxies
	a.policyMu.RUnlock()
	if len(proxies) == 0 {
		return host
	}
	peer := net.ParseIP(host)
	if peer == nil || !ipInAny(peer, proxies) {
		return host
	}

	// Walk right to left and take the first address that is not itself a
	// trusted hop: everything further left was supplied by an untrusted
	// client and can say anything.
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		ip := net.ParseIP(candidate)
		if ip == nil || ipInAny(ip, proxies) {
			continue
		}
		return candidate
	}
	return host
}

func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Middleware wraps a handler with the origin gate and the auth gate.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Container liveness probes must work before credentials are available;
		// application and API routes remain protected below.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		// The origin gate runs even when auth is disabled: it costs a header
		// lookup, and a dev instance is still reachable from a browser.
		if stateChanging(r.Method) && !a.SameOrigin(r) {
			slog.Warn("cross-origin request refused",
				"path", r.URL.Path, "method", r.Method, "origin", r.Header.Get("Origin"))
			writeAPIError(w, http.StatusForbidden, "cross_origin",
				"cross-origin requests are not allowed; add the origin to server.trusted_origins if this is intentional")
			return
		}

		if a.PasswordHash == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Credentials are checked before any throttling is applied, so a
		// correct password always succeeds. The old order refused the
		// request first, which meant every user behind one reverse proxy
		// shared a single failure budget and could be locked out by an
		// unauthenticated attacker.
		user, pass, ok := r.BasicAuth()
		if ok && a.check(r.Context(), user, pass) {
			a.reset(a.clientIP(r))
			next.ServeHTTP(w, r)
			return
		}

		ip := a.clientIP(r)
		delay := a.recordFailure(ip)
		if delay > 0 {
			// Slow the *failure* down. Guessing stays bounded, but nobody
			// is ever denied a login they hold the password for.
			t := time.NewTimer(delay)
			defer t.Stop()
			select {
			case <-t.C:
			case <-r.Context().Done():
				return
			}
		}
		// Never log the submitted credentials — only the source address.
		slog.Warn("auth failed", "ip", ip, "path", r.URL.Path, "delay", delay)
		w.Header().Set("WWW-Authenticate", `Basic realm="`+a.Realm+`", charset="UTF-8"`)
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
	})
}

// check verifies credentials with constant-time comparison where possible;
// bcrypt itself is deliberately slow and timing-safe. Verifications are
// bounded by hashSem so a flood of guesses cannot monopolize the CPU.
func (a *Auth) check(ctx context.Context, user, pass string) bool {
	select {
	case a.hashSem <- struct{}{}:
		defer func() { <-a.hashSem }()
	case <-ctx.Done():
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.Username)) == 1
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(pass)); err != nil {
		return false
	}
	return userOK
}

// recordFailure notes a failed attempt and returns how long this response
// should be held back. The delay grows with consecutive failures inside the
// window and is capped; it is never a refusal.
func (a *Auth) recordFailure(ip string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.fails[ip]
	if st == nil {
		st = &ipState{}
		a.fails[ip] = st
	}
	now := time.Now()
	if now.Sub(st.lastFailure) > authFailureWindow {
		st.failures = 0
	}
	st.lastFailure = now
	st.failures++

	if st.failures <= maxAuthFailures {
		return 0
	}
	delay := time.Duration(st.failures-maxAuthFailures) * authPenaltyStep
	if delay > maxAuthPenalty {
		delay = maxAuthPenalty
	}
	return delay
}

func (a *Auth) reset(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fails, ip)
}

// sameHostOrigin is the originless fallback used when no Auth is wired
// (tests, and the unauthenticated dev path): same-origin only, with no
// configurable trust list to consult.
func sameHostOrigin(r *http.Request) bool {
	var zero Auth
	return zero.SameOrigin(r)
}
