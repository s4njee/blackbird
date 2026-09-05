// Package portcheck verifies the rTorrent listening port through a
// configurable external probe (PAR-5.5): a user-initiated GET whose URL
// carries the port, answered by a probe that connects back to the requester.
//
// Probe protocol (implemented by any compatible service; a reference
// implementation is documented in deploy/README.md):
//
//	GET <url with {port} substituted>  →  200 + {"reachable": true|false}
//
// A top-level "open" boolean is accepted as an alias for "reachable".
// Anything else (non-200 status, unparsable body, missing field) is a probe
// failure, reported as an error rather than a reachability verdict.
//
// The package never initiates requests on its own: Check runs only when the
// API layer calls it for an explicit user action.
package portcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds one probe round trip when unconfigured.
const DefaultTimeout = 10 * time.Second

// Result is one completed check: the verdict plus how and when it was
// obtained. It is what the status bar and Settings show.
type Result struct {
	Port      int       `json:"port"`
	Reachable bool      `json:"reachable"`
	Method    string    `json:"method"` // e.g. "probe portchecker.example"
	CheckedAt time.Time `json:"checkedAt"`
}

// Check asks the probe template about port and interprets the answer.
// An empty template means disabled (ErrDisabled). Only http(s) templates
// containing {port} are accepted.
func Check(ctx context.Context, template string, port int, timeout time.Duration) (Result, error) {
	raw := strings.TrimSpace(template)
	if raw == "" {
		return Result{}, ErrDisabled
	}
	if port < 1 || port > 65535 {
		return Result{}, fmt.Errorf("invalid port %d", port)
	}
	if !strings.Contains(raw, "{port}") {
		return Result{}, fmt.Errorf("probe URL must contain a {port} placeholder")
	}
	target := strings.ReplaceAll(raw, "{port}", fmt.Sprintf("%d", port))
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Result{}, fmt.Errorf("probe URL must be an absolute http(s) address")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "blackbird-portcheck")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("probe request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return Result{}, fmt.Errorf("probe response unreadable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("probe answered HTTP %d: %s", resp.StatusCode, truncate(string(body), 120))
	}
	var verdict struct {
		Reachable *bool `json:"reachable"`
		Open      *bool `json:"open"`
	}
	if err := json.Unmarshal(body, &verdict); err != nil {
		return Result{}, fmt.Errorf("probe answer is not JSON: %w", err)
	}
	reachable := false
	switch {
	case verdict.Reachable != nil:
		reachable = *verdict.Reachable
	case verdict.Open != nil:
		reachable = *verdict.Open
	default:
		return Result{}, fmt.Errorf("probe answer has no reachable/open field: %s", truncate(string(body), 120))
	}
	return Result{
		Port:      port,
		Reachable: reachable,
		Method:    "probe " + u.Host,
		CheckedAt: time.Now(),
	}, nil
}

// ErrDisabled reports a check with no probe configured. Callers compare
// with errors.Is.
var ErrDisabled = errors.New("no port-check probe is configured")

// ErrNoPort reports a check with no listening port to test: the daemon
// reports none and no port_range is configured.
var ErrNoPort = errors.New("no listening port: the daemon reports none and no port_range is configured")

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
