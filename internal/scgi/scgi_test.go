package scgi

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"blackbird/internal/scgi/xmlrpc"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in          string
		network     string
		address     string
		expectError bool
	}{
		{"unix:///tmp/rtorrent.sock", "unix", "/tmp/rtorrent.sock", false},
		{"/tmp/rtorrent.sock", "unix", "/tmp/rtorrent.sock", false},
		{"tcp://127.0.0.1:5000", "tcp", "127.0.0.1:5000", false},
		{"http://x", "", "", true},
		{"unix://", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		ep, err := ParseEndpoint(c.in)
		if c.expectError {
			if err == nil {
				t.Errorf("ParseEndpoint(%q) = %+v, want error", c.in, ep)
			}
			continue
		}
		if err != nil || ep.Network != c.network || ep.Address != c.address {
			t.Errorf("ParseEndpoint(%q) = %+v, %v; want %s/%s", c.in, ep, err, c.network, c.address)
		}
	}
}

func TestFrameAndParse(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><methodCall/>`)
	frame := Frame(body)
	headers, parsed, err := ParseFrame(bytesReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	if headers["SCGI"] != "1" {
		t.Fatalf("SCGI header = %q", headers["SCGI"])
	}
	if headers["CONTENT_LENGTH"] != strconv.Itoa(len(body)) {
		t.Fatalf("CONTENT_LENGTH = %q", headers["CONTENT_LENGTH"])
	}
	if string(parsed) != string(body) {
		t.Fatal("body mismatch")
	}
}

// fakeServer is a minimal SCGI/XML-RPC server for exercising the transport.
type fakeServer struct {
	listener net.Listener
	// respond maps method name to a raw XML-RPC response document.
	respond func(method string, params []xmlrpc.Value) []byte

	mu    sync.Mutex
	calls []string
}

// callNames returns a copy of the recorded method names.
func (s *fakeServer) callNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func startFakeServer(t *testing.T, respond func(string, []xmlrpc.Value) []byte) *fakeServer {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "rtorrent.sock") // short enough for darwin/linux
	// darwin limits sun_path to 104 bytes; t.TempDir paths are fine here.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{listener: ln, respond: respond}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	_, body, err := ParseFrame(conn)
	if err != nil {
		return
	}
	method, params, err := xmlrpc.DecodeRequest(body)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, method)
	s.mu.Unlock()
	if _, err := conn.Write(s.respond(method, params)); err != nil {
		return
	}
}

func TestCallRoundTrip(t *testing.T) {
	const resp = `<?xml version="1.0" encoding="UTF-8"?>
<methodResponse><params><param><value><string>0.15.4</string></value></param></params></methodResponse>`
	srv := startFakeServer(t, func(string, []xmlrpc.Value) []byte { return []byte(resp) })

	c, err := New("unix://"+srv.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := c.Call(context.Background(), "system.client_version", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 1 || vals[0].Str != "0.15.4" {
		t.Fatalf("call result = %+v", vals)
	}
	if calls := srv.callNames(); len(calls) != 1 || calls[0] != "system.client_version" {
		t.Fatalf("server saw calls %v", calls)
	}
}

func TestCallFaultIsTyped(t *testing.T) {
	const fault = `<?xml version="1.0" encoding="UTF-8"?>
<methodResponse><fault><value><struct>
  <member><name>faultCode</name><value><i8>-501</i8></value></member>
  <member><name>faultString</name><value><string>Could not find called command.</string></value></member>
</struct></value></fault></methodResponse>`
	srv := startFakeServer(t, func(string, []xmlrpc.Value) []byte { return []byte(fault) })
	c, _ := New("unix://"+srv.listener.Addr().String(), 2*time.Second)

	_, err := c.Call(context.Background(), "bogus.command", nil)
	var f *xmlrpc.Fault
	if !errors.As(err, &f) {
		t.Fatalf("err = %v (%T), want *xmlrpc.Fault", err, err)
	}
	if f.Code != -501 || !strings.Contains(f.String, "Could not find called command.") {
		t.Fatalf("fault = %+v", f)
	}
}

func TestTransportErrorIsNotFault(t *testing.T) {
	// A socket that nothing is listening on.
	c, _ := New("unix:///tmp/blackbird-does-not-exist-sock", 500*time.Millisecond)
	_, err := c.Call(context.Background(), "system.client_version", nil)
	if err == nil {
		t.Fatal("expected transport error")
	}
	var f *xmlrpc.Fault
	if errors.As(err, &f) {
		t.Fatalf("transport error surfaced as fault: %v", err)
	}
}

func TestCallRespectsTimeout(t *testing.T) {
	srv := startFakeServer(t, func(string, []xmlrpc.Value) []byte {
		time.Sleep(300 * time.Millisecond)
		return []byte(`<?xml version="1.0"?><methodResponse><params><param><value><string>late</string></value></param></params></methodResponse>`)
	})
	c, _ := New("unix://"+srv.listener.Addr().String(), 50*time.Millisecond)
	start := time.Now()
	_, err := c.Call(context.Background(), "slow", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestConcurrentCalls(t *testing.T) {
	const resp = `<?xml version="1.0"?><methodResponse><params><param><value><i8>1</i8></value></param></params></methodResponse>`
	srv := startFakeServer(t, func(string, []xmlrpc.Value) []byte { return []byte(resp) })
	c, _ := New("unix://"+srv.listener.Addr().String(), 2*time.Second)

	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			_, err := c.Call(context.Background(), "d.ping", nil)
			done <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}
}

func bytesReader(b []byte) *strings.Reader {
	return strings.NewReader(string(b))
}
