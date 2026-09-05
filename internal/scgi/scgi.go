// Package scgi implements the SCGI transport (netstring header + body) used
// to reach rtorrent directly over a unix socket or TCP, replacing the
// nginx/Apache scgi_pass proxy.
package scgi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"blackbird/internal/scgi/xmlrpc"
)

// Endpoint is a resolved SCGI dial target.
type Endpoint struct {
	Network string // "unix" or "tcp"
	Address string
}

// ParseEndpoint accepts "unix:///path/to/socket", "tcp://host:port", or a
// bare absolute path (treated as a unix socket).
func ParseEndpoint(s string) (Endpoint, error) {
	switch {
	case strings.HasPrefix(s, "unix://"):
		addr := strings.TrimPrefix(s, "unix://")
		if addr == "" {
			return Endpoint{}, fmt.Errorf("scgi: empty unix socket path in %q", s)
		}
		return Endpoint{Network: "unix", Address: addr}, nil
	case strings.HasPrefix(s, "tcp://"):
		addr := strings.TrimPrefix(s, "tcp://")
		if addr == "" {
			return Endpoint{}, fmt.Errorf("scgi: empty tcp address in %q", s)
		}
		return Endpoint{Network: "tcp", Address: addr}, nil
	case strings.HasPrefix(s, "/"):
		return Endpoint{Network: "unix", Address: s}, nil
	default:
		return Endpoint{}, fmt.Errorf("scgi: endpoint must be unix:// or tcp:// (got %q)", s)
	}
}

// Client issues one SCGI request per connection (rtorrent's SCGI server
// closes the connection after each response). Calls are concurrent-safe:
// each request dials independently, bounded by a connection semaphore.
type Client struct {
	Endpoint Endpoint
	// Timeout bounds a single call (dial + write + read) when the context
	// carries no earlier deadline.
	Timeout time.Duration
	// MaxResponseBytes caps one SCGI response (headers + payload); 0 means
	// DefaultMaxResponseBytes. Exceeding it aborts the read and returns a
	// *TooLargeError — memory stays bounded no matter what the daemon (or
	// anything else on the socket) sends.
	MaxResponseBytes int64
	maxConns         chan struct{}
}

// DefaultMaxConcurrentCalls bounds concurrent SCGI connections.
const DefaultMaxConcurrentCalls = 8

// DefaultMaxResponseBytes caps one SCGI response unless the client sets
// MaxResponseBytes. A 5,000-torrent list poll is single-digit megabytes, so
// 64 MB leaves wide headroom while stopping unbounded reads (PERF-6.3).
const DefaultMaxResponseBytes = 64 << 20

// TooLargeError reports a response that exceeded the configured cap. The
// poller treats it like any poll failure: disconnect, back off, reconnect.
type TooLargeError struct {
	Limit int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("scgi: response exceeded %d bytes", e.Limit)
}

// TimeoutError reports a call that exceeded its deadline (dial, write, or
// read). It matches errors.Is(err, os.ErrDeadlineExceeded) and unwraps to
// the underlying error; match *TimeoutError to distinguish timeouts from
// other transport failures.
type TimeoutError struct {
	Op  string // dial | write | read
	Err error
}

func (e *TimeoutError) Error() string { return e.Err.Error() }
func (e *TimeoutError) Unwrap() error { return e.Err }

// Is reports timeout errors as deadline-exceeded for errors.Is — matching
// both os.ErrDeadlineExceeded and context.DeadlineExceeded — whatever the
// underlying transport error shape.
func (e *TimeoutError) Is(target error) bool {
	return target == os.ErrDeadlineExceeded || target == context.DeadlineExceeded
}

// New parses endpoint (unix:// or tcp://) and returns a client.
func New(endpoint string, timeout time.Duration) (*Client, error) {
	ep, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		Endpoint: ep,
		Timeout:  timeout,
		maxConns: make(chan struct{}, DefaultMaxConcurrentCalls),
	}, nil
}

// Call sends one XML-RPC request and returns the response params. Daemon
// faults surface as *xmlrpc.Fault; connection problems as plain transport
// errors, distinguishable via errors.As.
func (c *Client) Call(ctx context.Context, method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
	resp, err := c.roundTrip(ctx, xmlrpc.EncodeRequest(method, params))
	if err != nil {
		return nil, err // transport error (never a Fault)
	}
	return xmlrpc.DecodeResponse(resp)
}

func (c *Client) roundTrip(ctx context.Context, body []byte) ([]byte, error) {
	select {
	case c.maxConns <- struct{}{}:
		defer func() { <-c.maxConns }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	dialer := &net.Dialer{Timeout: c.Timeout}
	conn, err := dialer.DialContext(ctx, c.Endpoint.Network, c.Endpoint.Address)
	if err != nil {
		return nil, timeoutOr(fmt.Errorf("scgi: dial %s://%s: %w", c.Endpoint.Network, c.Endpoint.Address, err), "dial", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(c.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write(Frame(body)); err != nil {
		return nil, timeoutOp("write", err)
	}

	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	resp, err := io.ReadAll(io.LimitReader(conn, limit+1))
	if err != nil {
		return nil, timeoutOp("read", err)
	}
	if int64(len(resp)) > limit {
		return nil, &TooLargeError{Limit: limit}
	}
	return resp, nil
}

// timeoutOp reports timeout-shaped failures (missed deadline, expired
// context) as *TimeoutError so callers can match them with errors.As;
// anything else (refused dial, reset connection, caller cancellation)
// keeps the plain "scgi: <op>: ..." message.
func timeoutOp(op string, cause error) error {
	wrapped := fmt.Errorf("scgi: %s: %w", op, cause)
	if isTimeout(cause) {
		return &TimeoutError{Op: op, Err: wrapped}
	}
	return wrapped
}

// timeoutOr is timeoutOp with a prebuilt message (for dial, which names the
// endpoint). Only the classification differs, never the text.
func timeoutOr(msg error, op string, cause error) error {
	if isTimeout(cause) {
		return &TimeoutError{Op: op, Err: msg}
	}
	return msg
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}

// Frame builds the SCGI wire format: a netstring-encoded header dict
// (CONTENT_LENGTH + SCGI 1) followed by the raw body.
func Frame(body []byte) []byte {
	header := "CONTENT_LENGTH\x00" + strconv.Itoa(len(body)) + "\x00SCGI\x001\x00"
	netstring := strconv.Itoa(len(header)) + ":" + header + ","
	return append([]byte(netstring), body...)
}

// ParseFrame reads one SCGI request from r, returning the header dict and
// body. Used by test fake servers.
func ParseFrame(r io.Reader) (map[string]string, []byte, error) {
	lenStr, err := readUntil(r, ':')
	if err != nil {
		return nil, nil, fmt.Errorf("scgi: bad netstring: %w", err)
	}
	hlen, err := strconv.Atoi(lenStr)
	if err != nil || hlen <= 0 || hlen > 1<<20 {
		return nil, nil, fmt.Errorf("scgi: bad netstring length %q", lenStr)
	}
	headerBytes := make([]byte, hlen)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return nil, nil, fmt.Errorf("scgi: short header: %w", err)
	}
	comma := make([]byte, 1)
	if _, err := io.ReadFull(r, comma); err != nil || comma[0] != ',' {
		return nil, nil, errors.New("scgi: netstring not terminated by comma")
	}

	headers := map[string]string{}
	parts := strings.Split(string(headerBytes), "\x00")
	if len(parts)%2 != 1 { // trailing NUL leaves an empty final part
		return nil, nil, errors.New("scgi: malformed header dict")
	}
	for i := 0; i+1 < len(parts); i += 2 {
		headers[parts[i]] = parts[i+1]
	}
	// Bound before allocating: an unbounded CONTENT_LENGTH makes the
	// make() below panic with "makeslice: len out of range", which takes
	// down whichever fake daemon is serving the frame.
	clen, err := strconv.Atoi(headers["CONTENT_LENGTH"])
	if err != nil || clen < 0 || clen > DefaultMaxResponseBytes {
		return nil, nil, fmt.Errorf("scgi: bad CONTENT_LENGTH %q", headers["CONTENT_LENGTH"])
	}
	body := make([]byte, clen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, nil, fmt.Errorf("scgi: short body: %w", err)
	}
	return headers, body, nil
}

func readUntil(r io.Reader, delim byte) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		if _, err := r.Read(buf); err != nil {
			return "", err
		}
		if buf[0] == delim {
			return b.String(), nil
		}
		b.WriteByte(buf[0])
		if b.Len() > 24 {
			return "", errors.New("scgi: netstring length prefix too long")
		}
	}
}
