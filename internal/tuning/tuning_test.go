package tuning

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
	"blackbird/internal/scgi"
	"blackbird/internal/scgi/xmlrpc"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func intPtr(n int) *int       { return &n }
func int64Ptr(n int64) *int64 { return &n }

func TestEntriesMapsKeysAndSetters(t *testing.T) {
	tun := config.Tuning{
		PortRange:          strPtr("51413-51420"),
		PortRandom:         boolPtr(true),
		Encryption:         strPtr("require,require_RC4"),
		DHTMode:            strPtr("auto"),
		DHTPort:            intPtr(6881),
		UseUDP:             boolPtr(false),
		PEX:                boolPtr(true),
		LocalAddress:       strPtr("10.0.0.2"),
		HTTPMaxOpen:        intPtr(64),
		MaxOpenSockets:     intPtr(2048),
		MaxOpenFiles:       intPtr(2048),
		MinPeersNormal:     intPtr(50),
		MaxPeersNormal:     intPtr(200),
		MinPeersSeeded:     intPtr(10),
		MaxPeersSeeded:     intPtr(100),
		MaxUploads:         intPtr(12),
		MaxUploadsGlobal:   intPtr(64),
		GlobalDownRateKB:   int64Ptr(0),
		GlobalUpRateKB:     int64Ptr(20480),
		MaxDownloadsGlobal: intPtr(32),
	}
	entries := Entries(tun)
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Key] = e
	}

	wantSetters := map[string]string{
		"network.port_range":              "network.port_range.set",
		"network.port_random":             "network.port_random.set",
		"protocol.encryption":             "protocol.encryption.set",
		"dht.mode":                        "dht.mode.set",
		"dht.port":                        "dht.port.set",
		"trackers.use_udp":                "trackers.use_udp.set",
		"protocol.pex":                    "protocol.pex.set",
		"network.local_address":           "network.local_address.set",
		"network.http.max_open":           "network.http.max_open.set",
		"network.max_open_sockets":        "network.max_open_sockets.set",
		"network.max_open_files":          "network.max_open_files.set",
		"throttle.min_peers.normal":       "throttle.min_peers.normal.set",
		"throttle.max_peers.normal":       "throttle.max_peers.normal.set",
		"throttle.min_peers.seeded":       "throttle.min_peers.seeded.set",
		"throttle.max_peers.seeded":       "throttle.max_peers.seeded.set",
		"throttle.max_uploads":            "throttle.max_uploads.set",
		"throttle.max_uploads.global":     "throttle.max_uploads.global.set",
		"throttle.global_down.max_rate":   "throttle.global_down.max_rate.set",
		"throttle.global_up.max_rate":     "throttle.global_up.max_rate.set",
		"throttle.max_downloads.global":   "throttle.max_downloads.global.set",
	}
	for key, setter := range wantSetters {
		e, ok := got[key]
		if !ok {
			t.Errorf("missing entry for %s", key)
			continue
		}
		if e.Setter != setter {
			t.Errorf("%s setter = %s, want %s", key, e.Setter, setter)
		}
	}
	if len(entries) != len(wantSetters) {
		t.Errorf("entry count = %d, want %d", len(entries), len(wantSetters))
	}
	// Value spot checks.
	if got["throttle.global_up.max_rate"].Value.Int != 20480 {
		t.Errorf("global_up value = %+v", got["throttle.global_up.max_rate"].Value)
	}
	if got["protocol.encryption"].Value.Str != "require,require_RC4" {
		t.Errorf("encryption value = %+v", got["protocol.encryption"].Value)
	}
	if got["network.port_random"].Value.Int != 1 {
		t.Errorf("port_random value = %+v", got["network.port_random"].Value)
	}
}

func TestEntriesOmitsAbsentKeys(t *testing.T) {
	// Only two keys declared — everything else must stay untouched.
	tun := config.Tuning{
		DHTMode:      strPtr("off"),
		MaxUploads:   intPtr(8),
		LocalAddress: strPtr(""), // explicitly empty → also omitted
	}
	entries := Entries(tun)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want exactly 2", entries)
	}
	for _, e := range entries {
		if e.Key == "network.local_address" {
			t.Error("empty local_address must not produce an entry")
		}
	}
}

func TestEntriesEmptyTuning(t *testing.T) {
	if e := Entries(config.Tuning{}); len(e) != 0 {
		t.Fatalf("entries = %+v, want none", e)
	}
}

func TestDiffOnlyChangedKeys(t *testing.T) {
	prev := config.Tuning{
		DHTMode:      strPtr("auto"),
		MaxUploads:   intPtr(12),
		GlobalUpRateKB: int64Ptr(20480),
	}
	next := config.Tuning{
		DHTMode:        strPtr("auto"),   // unchanged
		MaxUploads:     intPtr(16),       // changed
		GlobalUpRateKB: int64Ptr(20480),  // unchanged
		PEX:            boolPtr(false),   // newly declared
	}
	changed := Diff(prev, next)
	if len(changed) != 2 {
		t.Fatalf("changed = %+v, want 2 entries", changed)
	}
	keys := map[string]bool{}
	for _, e := range changed {
		keys[e.Key] = true
	}
	if !keys["throttle.max_uploads"] || !keys["protocol.pex"] {
		t.Fatalf("changed keys = %+v", keys)
	}

	// Identical tuning → nothing to apply.
	if d := Diff(prev, prev); len(d) != 0 {
		t.Fatalf("self diff = %+v", d)
	}
}

// fakeRT for Apply tests: records multicall entries, can fault per method.
type fakeRT struct {
	ln         net.Listener
	seen       map[string]xmlrpc.Value // setter → value
	faultOn    map[string]bool
}

func startFake(t *testing.T, faultOn map[string]bool) *fakeRT {
	t.Helper()
	f := &fakeRT{seen: map[string]xmlrpc.Value{}, faultOn: faultOn}
	sock := filepath.Join(t.TempDir(), "rt.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeRT) handle(conn net.Conn) {
	defer conn.Close()
	_, body, err := scgi.ParseFrame(conn)
	if err != nil {
		return
	}
	method, params, err := xmlrpc.DecodeRequest(body)
	if err != nil || method != "system.multicall" || len(params) == 0 {
		return
	}
	var results []xmlrpc.Value
	for _, call := range params[0].Array {
		name := ""
		var val xmlrpc.Value
		if m, ok := call.Member("methodName"); ok {
			name = m.Str
		}
		if p, ok := call.Member("params"); ok && len(p.Array) > 0 {
			val = p.Array[0]
		}
		f.seen[name] = val
		if f.faultOn[name] {
			results = append(results, xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
				{Name: "faultCode", Value: xmlrpc.Value{Type: "int", Int: -501}},
				{Name: "faultString", Value: xmlrpc.Value{Type: "string", Str: "boom"}},
			}})
		} else {
			results = append(results, xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				{Type: "string", Str: ""},
			}})
		}
	}
	conn.Write(xmlrpc.EncodeResponse(xmlrpc.Value{Type: "array", Array: results}))
}

func newClient(t *testing.T, f *fakeRT) *rtorrent.Client {
	t.Helper()
	c, err := rtorrent.New("unix://"+f.ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestApplyReportsPerKeyFailures(t *testing.T) {
	// protocol.pex faults; every other key must still be applied.
	f := startFake(t, map[string]bool{"protocol.pex.set": true})
	c := newClient(t, f)

	tun := config.Tuning{
		DHTMode:      strPtr("on"),
		PEX:          boolPtr(false),
		MaxUploads:   intPtr(12),
		PortRandom:   boolPtr(true),
	}
	results := Apply(context.Background(), c, Entries(tun))
	if len(results) != 4 {
		t.Fatalf("results = %+v, want 4", results)
	}
	sawFault := false
	for _, r := range results {
		if r.Key == "protocol.pex" {
			if r.Err == nil {
				t.Fatal("expected pex failure")
			}
			sawFault = true
		} else if r.Err != nil {
			t.Errorf("key %s unexpectedly failed: %v", r.Key, r.Err)
		}
	}
	if !sawFault {
		t.Fatal("pex result missing")
	}
	// Non-faulting keys actually reached the daemon.
	if v, ok := f.seen["dht.mode.set"]; !ok || v.Str != "on" {
		t.Fatalf("dht.mode not applied: %+v", f.seen)
	}
	if v, ok := f.seen["throttle.max_uploads.set"]; !ok || v.Int != 12 {
		t.Fatalf("max_uploads not applied: %+v", f.seen)
	}
}

func TestApplyNothingDeclared(t *testing.T) {
	f := startFake(t, nil)
	c := newClient(t, f)
	if results := Apply(context.Background(), c, Entries(config.Tuning{})); results != nil {
		t.Fatalf("results = %+v, want nil", results)
	}
	if len(f.seen) != 0 {
		t.Fatalf("daemon touched despite empty tuning: %+v", f.seen)
	}
}

func TestApplyBatchTransportFailure(t *testing.T) {
	// Client pointing at a socket with no daemon: every key reports an error.
	c, err := rtorrent.New("unix:///tmp/blackbird-no-daemon-sock", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	tun := config.Tuning{DHTMode: strPtr("on"), MaxUploads: intPtr(4)}
	results := Apply(context.Background(), c, Entries(tun))
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, r := range results {
		if r.Err == nil {
			t.Errorf("key %s should have failed", r.Key)
		}
		if errors.Is(r.Err, context.DeadlineExceeded) {
			t.Errorf("unexpected deadline error type")
		}
	}
}
