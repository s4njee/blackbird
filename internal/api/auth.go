package api

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
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
type Auth struct {
	Username     string
	PasswordHash string // bcrypt
	Realm        string

	// Failure rate limiting, per source IP.
	mu    sync.Mutex
	fails map[string]*ipState
}

const (
	maxAuthFailures   = 10
	authFailureWindow = time.Minute
)

type ipState struct {
	failures     int
	blockedUntil time.Time
	lastFailure  time.Time
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
	}
}

// Middleware wraps a handler with the auth gate.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Container liveness probes must work before credentials are available;
		// application and API routes remain protected below.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if a.PasswordHash == "" {
			next.ServeHTTP(w, r)
			return
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}

		if a.blocked(ip) {
			w.Header().Set("Retry-After", "30")
			writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts; try again later")
			return
		}

		user, pass, ok := r.BasicAuth()
		if ok && a.check(user, pass) {
			a.reset(ip)
			next.ServeHTTP(w, r)
			return
		}

		a.recordFailure(ip)
		// Never log the submitted credentials — only the source IP.
		slog.Warn("auth failed", "ip", ip, "path", r.URL.Path)
		w.Header().Set("WWW-Authenticate", `Basic realm="`+a.Realm+`", charset="UTF-8"`)
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
	})
}

// check verifies credentials with constant-time comparison where possible;
// bcrypt itself is deliberately slow and timing-safe.
func (a *Auth) check(user, pass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.Username)) == 1
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(pass)); err != nil {
		return false
	}
	return userOK
}

func (a *Auth) blocked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.fails[ip]
	if !ok {
		return false
	}
	if !st.blockedUntil.IsZero() && time.Now().Before(st.blockedUntil) {
		return true
	}
	// Window expired: start fresh.
	if time.Since(st.lastFailure) > authFailureWindow {
		delete(a.fails, ip)
	}
	return false
}

func (a *Auth) recordFailure(ip string) {
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
	if st.failures >= maxAuthFailures {
		st.blockedUntil = now.Add(authFailureWindow)
	}
}

func (a *Auth) reset(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fails, ip)
}
