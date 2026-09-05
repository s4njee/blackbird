package fakertorrent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"blackbird/internal/scgi"
	"blackbird/internal/scgi/xmlrpc"
)

// startBenchDaemon starts a daemon on a short socket path (darwin sun_path
// limits) with the given options.
func startBenchDaemon(t *testing.T, opts Options) (*Daemon, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "fk-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "rt.sock")
	d, err := StartOpts(sock, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	return d, sock
}

// TestGeneratedSessionDeterministic proves same seed+size yields identical
// rows with the full 43-column list shape.
func TestGeneratedSessionDeterministic(t *testing.T) {
	a := generateSession(500, 0.04, 7)
	b := generateSession(500, 0.04, 7)
	if len(a) != 500 || len(b) != 500 {
		t.Fatalf("sizes = %d, %d", len(a), len(b))
	}
	if a[0] != b[0] || a[499] != b[499] {
		t.Fatal("same seed produced different sessions")
	}
	c := generateSession(500, 0.04, 8)
	if a[0] == c[0] {
		t.Fatal("different seeds produced identical sessions")
	}
	d, _ := startBenchDaemon(t, Options{SessionSize: 500, ActiveFraction: 0.04, Seed: 7})
	rows := d.listRows()
	if len(rows.Array) != 500 {
		t.Fatalf("served rows = %d", len(rows.Array))
	}
	if cols := len(rows.Array[0].Array); cols != 43 {
		t.Fatalf("columns = %d, want the full 43-column list shape", cols)
	}
	// Category mix: downloading, seeding, stopped, and tracker-error rows.
	var down, seed, stopped, errors int
	for _, row := range rows.Array {
		cols := row.Array
		state, complete, msg := cols[4].Int, cols[5].Bool, cols[8].Str
		switch {
		case msg != "":
			errors++
		case state == 1 && !complete:
			down++
		case state == 1 && complete:
			seed++
		default:
			stopped++
		}
	}
	if down == 0 || seed == 0 || stopped == 0 || errors == 0 {
		t.Fatalf("mix down=%d seed=%d stopped=%d errors=%d", down, seed, stopped, errors)
	}
}

// TestGeneratedSessionActivity proves live rows advance across sequential
// polls while idle rows stay put, and a fresh daemon replays identically.
func TestGeneratedSessionActivity(t *testing.T) {
	d, _ := startBenchDaemon(t, Options{SessionSize: 500, ActiveFraction: 0.2, Seed: 7})
	first := d.listRows()
	second := d.listRows()
	changed, live := 0, 0
	for i, row := range first.Array {
		a, b := row.Array, second.Array[i].Array
		if a[9].Int != 0 || a[10].Int != 0 {
			live++
		}
		if a[9].Int != b[9].Int || a[10].Int != b[10].Int || a[3].Int != b[3].Int {
			changed++
		}
	}
	if live == 0 {
		t.Fatal("no live rows at 20% activity")
	}
	if changed == 0 {
		t.Fatal("live rows did not advance across polls")
	}
	if changed != live {
		t.Fatalf("changed=%d live=%d: idle rows must stay put", changed, live)
	}
	d2, _ := startBenchDaemon(t, Options{SessionSize: 500, ActiveFraction: 0.2, Seed: 7})
	replay := d2.listRows()
	for i, row := range first.Array {
		if row.Array[9].Int != replay.Array[i].Array[9].Int {
			t.Fatal("fresh daemon did not replay the first poll identically")
		}
	}
}

// TestGeneratedDetailSelectableByHash proves download-scoped detail on a
// generated session returns one row per torrent keyed by hash (like real
// rTorrent), so FetchDetail can select any of them. The canned default keeps
// its single hardcoded row.
func TestGeneratedDetailSelectableByHash(t *testing.T) {
	d, sock := startBenchDaemon(t, Options{SessionSize: 25, ActiveFraction: 0.2, Seed: 7})
	c, err := scgi.New("unix://"+sock, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) xmlrpc.Value { return xmlrpc.Value{Type: "string", Str: s} }
	params := []xmlrpc.Value{str(""), str("main"), str("d.hash="),
		str("f.multicall=,f.path=,f.size_bytes=,f.completed_chunks=,f.size_chunks=,f.priority="),
		str("p.multicall=,p.id=,p.address=,p.port="),
		str("t.multicall=,t.url=,t.is_enabled=,t.group="),
		str("d.down.total="), str("d.up.total="), str("d.chunk_size="),
		str("d.size_chunks="), str("d.completed_chunks="), str("d.directory="),
		str("d.bitfield="),
	}
	vals, err := c.Call(context.Background(), "d.multicall2", params)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 1 || len(vals[0].Array) != len(d.bench) {
		t.Fatalf("rows = %d, want %d", len(vals[0].Array), len(d.bench))
	}
	want := d.bench[3].hash
	var found *xmlrpc.Value
	for i := range vals[0].Array {
		row := vals[0].Array[i].Array
		if len(row) > 0 && row[0].Str == want {
			found = &vals[0].Array[i]
		}
	}
	if found == nil {
		t.Fatalf("no detail row for hash %q", want)
	}
	row := found.Array
	if len(row) != 1+3+7 {
		t.Fatalf("detail row has %d columns, want 11", len(row))
	}
	if len(row[1].Array) != 2 || len(row[2].Array) == 0 || len(row[3].Array) == 0 {
		t.Fatalf("files/peers/trackers = %d/%d/%d", len(row[1].Array), len(row[2].Array), len(row[3].Array))
	}
	if row[6].Int != 1<<20 {
		t.Fatalf("chunk size = %d, want %d", row[6].Int, 1<<20)
	}
	chunks := row[7].Int
	field := row[10].Str
	if len(field) != int((chunks+7)/8)*2 {
		t.Fatalf("bitfield %d chars for %d chunks", len(field), chunks)
	}
}

// TestUnknownMethodFaults proves typo'd commands fail instead of silently
// returning an empty string, while the known lifecycle calls still ack.
func TestUnknownMethodFaults(t *testing.T) {
	_, sock := startBenchDaemon(t, Options{})
	c, err := scgi.New("unix://"+sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "d.starp", []xmlrpc.Value{{Type: "string", Str: "x"}})
	var fault *xmlrpc.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("err = %v (%T), want *xmlrpc.Fault", err, err)
	}
	if fault.Code != -501 || fault.String != "unknown method d.starp" {
		t.Fatalf("fault = %+v", fault)
	}
	for _, m := range []string{"d.stop", "d.custom1.set", "load.raw_start", "throttle.global_down.max_rate.set", "ipv4_filter.load"} {
		if _, err := c.Call(context.Background(), m, nil); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
}

// TestSessionSizes smoke-covers the three benchmark fixtures, including the
// 20k-row response that exercises the SCGI size cap realistically.
func TestSessionSizes(t *testing.T) {
	for _, n := range []int{500, 5000, 20000} {
		d, sock := startBenchDaemon(t, Options{SessionSize: n, ActiveFraction: 0.04, Seed: 1})
		c, err := scgi.New("unix://"+sock, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		vals, err := c.Call(context.Background(), "d.multicall2", []xmlrpc.Value{
			{Type: "string", Str: ""}, {Type: "string", Str: "main"}, {Type: "string", Str: "d.hash="},
		})
		if err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		_ = d
		if len(vals) == 0 || len(vals[0].Array) != n {
			t.Fatalf("size %d: rows = %d", n, len(vals[0].Array))
		}
		fmt.Fprintf(t.Output(), "fixture %d: ok\n", n)
	}
}
