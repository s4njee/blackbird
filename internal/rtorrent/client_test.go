package rtorrent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/fakertorrent"
	"blackbird/internal/scgi"
	"blackbird/internal/scgi/xmlrpc"
)

// fakeRTorrent is a scripted SCGI/XML-RPC server that dispatches by method
// name, records every call, and returns canned responses.
type fakeRTorrent struct {
	listener net.Listener
	handler  func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error)

	calls []string
}

func newFakeRTorrent(t *testing.T, handler func(string, []xmlrpc.Value) ([]xmlrpc.Value, error)) *fakeRTorrent {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "rt.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeRTorrent{listener: ln, handler: handler}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.handle(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeRTorrent) handle(conn net.Conn) {
	defer conn.Close()
	_, body, err := scgi.ParseFrame(conn)
	if err != nil {
		return
	}
	method, params, err := xmlrpc.DecodeRequest(body)
	if err != nil {
		return
	}
	f.calls = append(f.calls, method)
	vals, err := f.handler(method, params)
	if err != nil {
		var fault *xmlrpc.Fault
		if errors.As(err, &fault) {
			conn.Write(xmlrpc.EncodeFault(fault.Code, fault.String))
		}
		return
	}
	conn.Write(xmlrpc.EncodeResponseParams(vals))
}

func (f *fakeRTorrent) saw(method string) bool {
	for _, c := range f.calls {
		if c == method {
			return true
		}
	}
	return false
}

func newTestClient(t *testing.T, f *fakeRTorrent) *Client {
	t.Helper()
	c, err := New("unix://"+f.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func strV(s string) xmlrpc.Value { return xmlrpc.Value{Type: "string", Str: s} }
func i8V(n int64) xmlrpc.Value   { return xmlrpc.Value{Type: "int", Int: n} }

// row builds one d.multicall2 row following listCommands order.
func row(hash, name string, size, done int64, state int, complete bool, checking bool, hashing int, message string, down, up int64, ratioPerMille int64, label string, started int64, basePath string, private bool, peers, seeds int64, priority int64, urls ...string) xmlrpc.Value {
	urlArr := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{}}
	for _, u := range urls {
		urlArr.Array = append(urlArr.Array, strV(u))
	}
	return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		strV(hash), strV(name), i8V(size), i8V(done),
		i8V(int64(state)), boolV(complete), boolV(checking), i8V(int64(hashing)), strV(message),
		i8V(down), i8V(up), i8V(ratioPerMille), strV(label),
		i8V(started), i8V(0), strV(basePath), boolV(private),
		i8V(peers), i8V(seeds), boolV(false), boolV(false),
		i8V(priority), i8V(100), i8V(50), urlArr,
	}}
}

func boolV(b bool) xmlrpc.Value { return xmlrpc.Value{Type: "bool", Bool: b} }

func TestListTorrentsMapping(t *testing.T) {
	f := newFakeRTorrent(t, func(method string, _ []xmlrpc.Value) ([]xmlrpc.Value, error) {
		if method != "d.multicall2" {
			return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + method}
		}
		rows := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			row("aaaa", "ubuntu-24.04.iso", 1000, 614, 1, false, false, 0, "",
				1024*1024, 512*1024, 2410, "iso", 1756700000, "/mnt/data/iso/ubuntu.iso", false,
				150, 38, 2, "https://torrent.ubuntu.com/announce"),
			row("bbbb", "kernel.tar.xz", 2000, 2000, 1, true, false, 0, "",
				0, 2048, 3100, "kernel", 1756600000, "/mnt/data/kernel", false,
				44, 0, 2, "https://cdn.kernel.org/announce"),
			row("cccc", "broken.torrent", 500, 0, 0, false, false, 0, "Tracker: [failure reason]",
				0, 0, 0, "", 1756500000, "/mnt/data/broken", true,
				0, 0, 0, "http://bad.example/announce"),
			row("dddd", "checking.iso", 800, 0, 0, false, true, 1, "",
				0, 0, 0, "", 1756400000, "/mnt/data/checking", false,
				0, 0, 0, "http://x.example/announce"),
		}}
		// d.completed_chunks (index 23) is downloaded data; d.chunks_hashed
		// (index 37) is the actual recheck progress.
		rows.Array[3].Array = append(rows.Array[3].Array,
			i8V(0), i8V(0), i8V(0), i8V(0), strV(""),
			strV(""), strV(""), strV(""), strV(""), strV(""),
			i8V(0), i8V(0), i8V(10), boolV(false), strV(""), strV(""),
			i8V(0), boolV(false), boolV(false),
		)
		// d.is_open is independent of the normalized state and powers the
		// ruTorrent-compatible Inactive category.
		rows.Array[1].Array[19] = boolV(true)
		return []xmlrpc.Value{rows}, nil
	})
	c := newTestClient(t, f)

	ts, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 4 {
		t.Fatalf("got %d torrents", len(ts))
	}

	dl := ts[0]
	if dl.Hash != "aaaa" || dl.Name != "ubuntu-24.04.iso" || dl.State != StateDownloading {
		t.Fatalf("downloading row: %+v", dl)
	}
	if dl.Percent != 61.4 {
		t.Fatalf("percent = %v, want 61.4", dl.Percent)
	}
	if dl.Ratio != 2.41 {
		t.Fatalf("ratio = %v, want 2.41", dl.Ratio)
	}
	if dl.TrackerHost != "torrent.ubuntu.com" {
		t.Fatalf("tracker host = %q", dl.TrackerHost)
	}
	if dl.Seeds != 38 || dl.Peers != 112 {
		t.Fatalf("seeds/peers = %d/%d, want 38/112", dl.Seeds, dl.Peers)
	}
	if dl.EtaSeconds <= 0 {
		t.Fatalf("ETA not derived: %v", dl.EtaSeconds)
	}
	if dl.AddedAt.Unix() != 1756700000 {
		t.Fatalf("added = %v", dl.AddedAt)
	}

	sd := ts[1]
	if sd.State != StateSeeding || !sd.Complete || !sd.IsOpen || sd.Percent != 100 || sd.EtaSeconds != -1 {
		t.Fatalf("seeding row: %+v", sd)
	}

	er := ts[2]
	if er.State != StateError || er.Message == "" || !er.IsPrivate {
		t.Fatalf("error row: %+v", er)
	}

	ch := ts[3]
	if ch.State != StateChecking {
		t.Fatalf("checking row state = %q", ch.State)
	}
	if ch.CheckingPercent != 10 {
		t.Fatalf("checking percent = %v, want 10 from chunks_hashed", ch.CheckingPercent)
	}
}

func TestListTorrentsExtendedFields(t *testing.T) {
	f := newFakeRTorrent(t, func(method string, _ []xmlrpc.Value) ([]xmlrpc.Value, error) {
		if method != "d.multicall2" {
			return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + method}
		}
		base := row("extended", "extended.torrent", 10000, 4000, 1, false, false, 0, "",
			100, 200, 1500, "media", 1756700000, "/downloads/media/extended", false,
			12, 4, 2, "https://tracker.example/announce")
		base.Array = append(base.Array,
			i8V(6000), i8V(123456), i8V(234567), i8V(1756800000), strV("slow"),
			strV("ratio-2"), strV("custom-3"), strV("custom-4"), strV("custom-5"),
			strV("/downloads/media/extended.torrent"), i8V(8), i8V(10), i8V(987),
			boolV(true), strV("/downloads/media"), strV("leech"), i8V(1756600000), boolV(true), boolV(false),
		)
		return []xmlrpc.Value{{Type: "array", Array: []xmlrpc.Value{base}}}, nil
	})
	torrents, err := newTestClient(t, f).ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(torrents) != 1 {
		t.Fatalf("got %d torrents", len(torrents))
	}
	torrent := torrents[0]
	if torrent.LeftBytes != 6000 || torrent.DownloadedBytes != 234567 || torrent.UploadedBytes != 123456 {
		t.Fatalf("transfer fields = %+v", torrent)
	}
	if torrent.FinishedAt.Unix() != 1756800000 || torrent.CreationDate.Unix() != 1756600000 {
		t.Fatalf("timestamps = finished %v created %v", torrent.FinishedAt, torrent.CreationDate)
	}
	if torrent.Throttle != "slow" || torrent.RatioGroup != "ratio-2" || torrent.Custom5 != "custom-5" {
		t.Fatalf("custom fields = %+v", torrent)
	}
	if torrent.TiedToFile == "" || torrent.SkippedBytes != 8 || torrent.PeersAccounted != 10 || torrent.ChunksHashed != 987 || !torrent.IsMultiFile {
		t.Fatalf("accounting fields = %+v", torrent)
	}
	if torrent.Directory != "/downloads/media" || torrent.Connection != "leech" {
		t.Fatalf("path fields = %+v", torrent)
	}
	if !torrent.Superseeding || torrent.Sequential {
		t.Fatalf("live action fields = %+v", torrent)
	}
}

func TestVersionsAndGlobalStats(t *testing.T) {
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		if method == "system.multicall" {
			// Build one response entry per requested method, so callers
			// batching different request sets get aligned results.
			byMethod := map[string]xmlrpc.Value{
				"throttle.global_down.rate":  i8V(41_200_000),
				"throttle.global_up.rate":    i8V(12_800_000),
				"throttle.global_down.total": i8V(1000),
				"throttle.global_up.total":   i8V(7000),
				"system.client_version":      strV("0.15.4"),
				"system.library_version":     strV("0.14.4"),
				"network.port":               i8V(51413),
				"dht.statistics": xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
					{Name: "dht", Value: xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
						{Name: "nodes", Value: i8V(1204)},
					}}},
				}},
			}
			var entries []xmlrpc.Value
			for _, call := range params[0].Array {
				name := ""
				if m, ok := call.Member("methodName"); ok {
					name = m.Str
				}
				if v, ok := byMethod[name]; ok {
					entries = append(entries, one(v))
				} else {
					entries = append(entries, xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
						{Name: "faultCode", Value: i8V(-501)},
						{Name: "faultString", Value: strV("unknown method " + name)},
					}})
				}
			}
			return []xmlrpc.Value{xmlrpc.Value{Type: "array", Array: entries}}, nil
		}
		return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + method}
	})
	c := newTestClient(t, f)

	client, lib, err := c.Versions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client != "0.15.4" || lib != "0.14.4" {
		t.Fatalf("versions = %q / %q", client, lib)
	}

	g, err := c.GlobalStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g.DownRate != 41_200_000 || g.UpRate != 12_800_000 {
		t.Fatalf("rates = %d / %d", g.DownRate, g.UpRate)
	}
	if g.Port != 51413 || g.DHTNodes != 1204 {
		t.Fatalf("port/dht = %d / %d", g.Port, g.DHTNodes)
	}
	if g.SessionRatio != 7.0 {
		t.Fatalf("session ratio = %v", g.SessionRatio)
	}
	if g.Version != "0.15.4" || g.LibraryVersion != "0.14.4" {
		t.Fatalf("stats versions = %q / %q", g.Version, g.LibraryVersion)
	}
}

func one(v xmlrpc.Value) xmlrpc.Value {
	return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{v}}
}

func TestTrackerFieldsSeparate(t *testing.T) {
	// Regression: the detail multicalls must pass each field as its own
	// parameter. Comma-joining them into one string makes rTorrent 0.15.x
	// treat the whole string as a single command and return only the first
	// field of each row, which left the detail drawer showing tracker URLs
	// with no counters and files with no sizes.
	values := map[string]xmlrpc.Value{
		"t.url=":               strV("https://tracker.example/announce"),
		"t.is_enabled=":        boolV(true),
		"t.group=":             i8V(2),
		"t.scrape_complete=":   i8V(88),
		"t.scrape_incomplete=": i8V(12),
		"t.success_time_next=": i8V(1 << 40),
		"t.latest_event=":      strV("completed"),
		"t.failed_counter=":    i8V(3),
		"t.success_counter=":   i8V(4),
		"t.latest_new_peers=":  i8V(5),
	}
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		// hash + pattern + one parameter per field. A comma-joined field
		// list would arrive as exactly 3 params and fail here.
		if method != "t.multicall" || len(params) != 2+len(values) {
			return nil, &xmlrpc.Fault{Code: -501, String: "unexpected request"}
		}
		row := make([]xmlrpc.Value, 0, len(values))
		for _, param := range params[2:] {
			if strings.Contains(param.Str, ",") {
				return nil, &xmlrpc.Fault{Code: -501, String: "comma-joined field list: " + param.Str}
			}
			value, ok := values[param.Str]
			if !ok {
				return nil, &xmlrpc.Fault{Code: -501, String: "unexpected field " + param.Str}
			}
			row = append(row, value)
		}
		return []xmlrpc.Value{{Type: "array", Array: []xmlrpc.Value{
			{Type: "array", Array: row},
		}}}, nil
	})
	c := newTestClient(t, f)

	trackers, err := c.Trackers(context.Background(), "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(trackers) != 1 {
		t.Fatalf("trackers = %+v", trackers)
	}
	tr := trackers[0]
	if tr.URL != "https://tracker.example/announce" || !tr.IsEnabled || tr.Group != 2 ||
		tr.Seeds != 88 || tr.Leechers != 12 || tr.LatestEvent != "completed" ||
		tr.FailedCount != 3 || tr.SuccessCount != 4 || tr.NewPeers != 5 {
		t.Fatalf("tracker = %+v", tr)
	}
}

func TestFileFieldsSeparate(t *testing.T) {
	// Same contract for f.multicall, which is what emptied the Files tab.
	fields := []string{"f.path=", "f.size_bytes=", "f.completed_chunks=", "f.size_chunks=", "f.priority="}
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		if method != "f.multicall" || len(params) != 2+len(fields) {
			return nil, &xmlrpc.Fault{Code: -501, String: "unexpected request"}
		}
		for i, param := range params[2:] {
			if param.Str != fields[i] {
				return nil, &xmlrpc.Fault{Code: -501, String: "unexpected field " + param.Str}
			}
		}
		return []xmlrpc.Value{{Type: "array", Array: []xmlrpc.Value{
			{Type: "array", Array: []xmlrpc.Value{strV("/mnt/data/a.iso"), i8V(1000), i8V(10), i8V(10), i8V(2)}},
		}}}, nil
	})
	c := newTestClient(t, f)

	files, err := c.Files(context.Background(), "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "/mnt/data/a.iso" || files[0].SizeBytes != 1000 || files[0].Priority != 2 {
		t.Fatalf("files = %+v", files)
	}
}

func TestFetchDetail(t *testing.T) {
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		if method == "system.multicall" && len(params) != 1 {
			return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + method}
		}
		if method != "system.multicall" && method != "f.multicall" && method != "p.multicall" && method != "t.multicall" {
			return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + method}
		}
		files := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			{Type: "array", Array: []xmlrpc.Value{strV("/mnt/data/a.iso"), i8V(1000), i8V(10), i8V(10), i8V(2)}},
			{Type: "array", Array: []xmlrpc.Value{strV("/mnt/data/b.nfo"), i8V(400), i8V(0), i8V(4), i8V(0)}},
		}}
		peers := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			{Type: "array", Array: []xmlrpc.Value{strV("peer1"), strV("10.0.0.5"), i8V(51413), strV("qBittorrent 5.0"), i8V(87), i8V(2048), i8V(1024), boolV(true), boolV(false), boolV(false), i8V(1000), i8V(7000)}},
		}}
		trackers := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			{Type: "array", Array: []xmlrpc.Value{strV("https://tracker.example/announce"), boolV(true), i8V(0), i8V(88), i8V(12), i8V(1 << 40)}},
		}}
		values := map[string]xmlrpc.Value{
			"f.multicall": files, "p.multicall": peers, "t.multicall": trackers,
			"d.down.total": i8V(1000), "d.up.total": i8V(7000), "d.chunk_size": i8V(16384),
			"d.size_chunks": i8V(100), "d.completed_chunks": i8V(61), "d.directory": strV("/mnt/data"),
			"d.bitfield": strV("f0f0"),
		}
		if value, ok := values[method]; ok && strings.HasSuffix(method, ".multicall") {
			return []xmlrpc.Value{value}, nil
		}
		entries := make([]xmlrpc.Value, 0, len(params[0].Array))
		for _, raw := range params[0].Array {
			name, ok := raw.Member("methodName")
			if !ok {
				return nil, &xmlrpc.Fault{Code: -501, String: "missing methodName"}
			}
			value, ok := values[name.Str]
			if !ok {
				return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + name.Str}
			}
			entries = append(entries, one(value))
		}
		return []xmlrpc.Value{{Type: "array", Array: entries}}, nil
	})
	c := newTestClient(t, f)

	d, err := c.FetchDetail(context.Background(), "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 || d.Files[1].Priority != 0 {
		t.Fatalf("files = %+v", d.Files)
	}
	if d.Files[0].Percent() != 100 || d.Files[1].Percent() != 0 {
		t.Fatalf("file percents = %v %v", d.Files[0].Percent(), d.Files[1].Percent())
	}
	if len(d.Peers) != 1 || d.Peers[0].Flags != "E" || d.Peers[0].Client != "qBittorrent 5.0" {
		t.Fatalf("peers = %+v", d.Peers)
	}
	if d.Peers[0].DownloadedBytes != 1000 || d.Peers[0].UploadedBytes != 7000 || d.Peers[0].IsSnubbed {
		t.Fatalf("peer totals = %+v", d.Peers[0])
	}
	if len(d.Trackers) != 1 || !d.Trackers[0].IsEnabled || d.Trackers[0].Seeds != 88 {
		t.Fatalf("trackers = %+v", d.Trackers)
	}
	if d.Transfer.DownloadedBytes != 1000 || d.Transfer.UploadedBytes != 7000 ||
		d.Transfer.ChunkSize != 16384 || d.Transfer.ChunksDone != 61 || d.Transfer.Directory != "/mnt/data" {
		t.Fatalf("transfer = %+v", d.Transfer)
	}
	if d.BitfieldHex != "f0f0" {
		t.Fatalf("bitfield = %q, want f0f0", d.BitfieldHex)
	}
}

func TestSupportsMethodProbe(t *testing.T) {
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		switch method {
		case "system.methodExist":
			if len(params) == 0 {
				return nil, &xmlrpc.Fault{Code: -501, String: "no params"}
			}
			return []xmlrpc.Value{boolV(params[0].Str == "d.name.set")}, nil
		}
		return nil, &xmlrpc.Fault{Code: -501, String: "unexpected " + method}
	})
	c := newTestClient(t, f)
	ctx := context.Background()

	ok, err := c.SupportsMethod(ctx, "d.name.set")
	if err != nil || !ok {
		t.Fatalf("SupportsMethod(d.name.set) = %v, %v", ok, err)
	}
	missing, err := c.SupportsMethod(ctx, "d.nope.set")
	if err != nil || missing {
		t.Fatalf("SupportsMethod(missing) = %v, %v", missing, err)
	}
}

func TestActionsRecorded(t *testing.T) {
	f := newFakeRTorrent(t, func(method string, _ []xmlrpc.Value) ([]xmlrpc.Value, error) {
		return []xmlrpc.Value{strV("")}, nil // ack everything
	})
	c := newTestClient(t, f)
	ctx := context.Background()

	if err := c.Start(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Recheck(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLabel(ctx, "h1", "iso"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDirectory(ctx, "h1", "/mnt/other"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetFilePriority(ctx, "h1", 3, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPriority(ctx, "h1", 3); err != nil {
		t.Fatal(err)
	}
	if err := c.ForceStart(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSuperseeding(ctx, "h1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSequential(ctx, "h1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSession(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCustom(ctx, "h1", "custom4", "note"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCustom(ctx, "h1", "custom1", "forbidden"); err == nil {
		t.Fatal("SetCustom(custom1) unexpectedly succeeded")
	}
	if err := c.Announce(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddTracker(ctx, "h1", "http://t/announce", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTrackerEnabled(ctx, "h1", 0, true); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveTracker(ctx, "h1", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.BanPeer(ctx, "h1", "peer1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPeerSnubbed(ctx, "h1", "peer1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPeerSnubbed(ctx, "h1", "peer1", false); err != nil {
		t.Fatal(err)
	}
	if err := c.DisconnectPeer(ctx, "h1", "peer1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename(ctx, "h1", "new name"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abc", AddOptions{Start: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abc", AddOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetGlobal(ctx, "throttle.global_up.max_rate.set", xmlrpc.Value{Type: "int", Int: 20480}); err != nil {
		t.Fatal(err)
	}

	for method, want := range map[string]bool{
		"d.open": true, "d.start": true, "d.resume": true, "d.pause": true, "d.stop": true, "d.check_hash": true,
		"d.custom1.set": true, "d.directory.set": true, "f.priority.set": true,
		"d.priority.set": true, "d.tracker_announce": true, "d.tracker.insert": true,
		"d.connection_seed.set": true, "d.sequential.set": true, "d.save_full_session": true, "d.custom4.set": true,
		"t.is_enabled.set": true, "d.tracker.remove": true, "load.start": true, "load.normal": true,
		"p.banned.set": true, "p.snubbed.set": true, "p.disconnect": true,
		"d.name.set":                      true,
		"throttle.global_up.max_rate.set": true,
	} {
		if f.saw(method) != want {
			t.Errorf("saw(%q) = false, want %v (calls: %v)", method, want, f.calls)
		}
	}
	if f.saw("load.raw") {
		t.Error("load.raw should not have been called")
	}
}

// TestLoadIPFilterWireForm pins the blocklist daemon call (PAR-5.6): the
// path plus the "unwanted" table value, per the upstream ip-filtering manual.
func TestLoadIPFilterWireForm(t *testing.T) {
	var gotMethod string
	var gotParams []xmlrpc.Value
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		gotMethod = method
		gotParams = params
		return []xmlrpc.Value{strV("")}, nil
	})
	c := newTestClient(t, f)
	if err := c.LoadIPFilter(context.Background(), "/data/filters/ipfilter.dat"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "ipv4_filter.load" {
		t.Fatalf("method = %q", gotMethod)
	}
	if len(gotParams) != 2 || gotParams[0].Str != "/data/filters/ipfilter.dat" || gotParams[1].Str != "unwanted" {
		t.Fatalf("params = %+v", gotParams)
	}
}

func TestAddTorrentFileUsesRawStart(t *testing.T) {
	var gotMethod string
	var gotParams []xmlrpc.Value
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		gotMethod = method
		gotParams = params
		return []xmlrpc.Value{strV("")}, nil
	})
	c := newTestClient(t, f)

	data := []byte("d4:infod4:name4:teste")
	if err := c.AddTorrentFile(context.Background(), data, AddOptions{Start: true}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "load.raw_start" {
		t.Fatalf("method = %q", gotMethod)
	}
	if len(gotParams) != 2 || gotParams[1].Type != "base64" || gotParams[1].Str != string(data) {
		t.Fatalf("params = %+v", gotParams)
	}
}

func TestFaultSurfacesTyped(t *testing.T) {
	f := newFakeRTorrent(t, func(string, []xmlrpc.Value) ([]xmlrpc.Value, error) {
		return nil, &xmlrpc.Fault{Code: -501, String: "Could not find called command."}
	})
	c := newTestClient(t, f)
	err := c.Start(context.Background(), "h")
	var fault *xmlrpc.Fault
	if !errors.As(err, &fault) || fault.Code != -501 {
		t.Fatalf("err = %v, want typed fault", err)
	}
}

func TestRemoveWithDataSafety(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(filepath.Join(base, "dl", "iso"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(base, "dl", "iso", "t")

	calls := map[string]int{}
	f := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		calls[method]++
		switch method {
		case "d.base_path=":
			return []xmlrpc.Value{strV(outside)}, nil
		case "d.erase":
			return []xmlrpc.Value{strV("")}, nil
		}
		return []xmlrpc.Value{xmlrpc.Value{Type: "array"}}, nil
	})
	c := newTestClient(t, f)

	allowed := []string{filepath.Join(base, "dl")}
	_, err := c.RemoveWithData(context.Background(), "h", allowed)
	if err == nil || !errors.Is(err, ErrPathOutsideDownloadDirs) {
		t.Fatalf("err = %v, want ErrPathOutsideDownloadDirs", err)
	}
	if calls["d.erase"] != 0 {
		t.Fatal("erase must be refused for paths outside allowed dirs")
	}

	// Now with the base path inside: erase proceeds.
	f2 := newFakeRTorrent(t, func(method string, params []xmlrpc.Value) ([]xmlrpc.Value, error) {
		switch method {
		case "d.base_path=":
			return []xmlrpc.Value{strV(inside)}, nil
		case "f.multicall":
			// Two files → base directory removal.
			return []xmlrpc.Value{xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{strV(inside + "/a"), i8V(1), i8V(1), i8V(1), i8V(1)}},
				xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{strV(inside + "/b"), i8V(1), i8V(1), i8V(1), i8V(1)}},
			}}}, nil
		case "d.erase":
			return []xmlrpc.Value{strV("")}, nil
		}
		return []xmlrpc.Value{xmlrpc.Value{Type: "array"}}, nil
	})
	c2 := newTestClient(t, f2)
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := c2.RemoveWithData(context.Background(), "h", allowed)
	if err != nil {
		t.Fatal(err)
	}
	if path != inside {
		t.Fatalf("path = %q", path)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("data dir still exists: %v", err)
	}
}

func TestCheckWithin(t *testing.T) {
	allowed := []string{"/mnt/data", "/mnt/archive"}
	ok := []string{"/mnt/data/iso/a.iso", "/mnt/archive/x", "/mnt/data"}
	bad := []string{"", "/mnt/datax/escape", "/etc/passwd", "relative/path"}
	for _, p := range ok {
		if err := CheckWithin(p, allowed); err != nil {
			t.Errorf("CheckWithin(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range bad {
		if err := CheckWithin(p, allowed); !errors.Is(err, ErrPathOutsideDownloadDirs) {
			t.Errorf("CheckWithin(%q) = %v, want ErrPathOutsideDownloadDirs", p, err)
		}
	}
}

// TestListAndGlobalsSingleRoundTrip proves the PERF-6.3 combined poll: one
// system.multicall (nesting d.multicall2) returns both the list rows and the
// globals, with no top-level d.multicall2 call.
func TestListAndGlobalsSingleRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "rt-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "rt.sock")
	daemon, err := fakertorrent.Start(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Stop()

	c, err := New("unix://"+sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	torrents, g, err := c.ListAndGlobals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(torrents) != 3 || torrents[0].Hash != "aaaa1111aaaa1111" {
		t.Fatalf("torrents = %+v", torrents)
	}
	if g.DownRate != 41200000 || g.Version != "0.15.4" {
		t.Fatalf("global = %+v", g)
	}
	var multicall, direct int
	for _, m := range daemon.CallMethods() {
		switch m {
		case "system.multicall":
			multicall++
		case "d.multicall2":
			direct++
		}
	}
	if multicall != 1 || direct != 0 {
		t.Fatalf("calls: system.multicall=%d d.multicall2=%d, want 1 and 0", multicall, direct)
	}
}

// TestMapTorrentNestedTrackerMulticall covers the shape a real daemon
// returns for "t.multicall=,t.url=": a list holding one inner list per
// tracker. Reading that inner list as a string yields "", which blanked
// TrackerHost for every torrent — emptying the Tracker column and the
// sidebar's tracker grouping, and making tracker-matched automation rules
// unable to fire. The flat shape used by the fixtures still works.
func TestMapTorrentNestedTrackerMulticall(t *testing.T) {
	mk := func(cell xmlrpc.Value) Torrent {
		v := make([]xmlrpc.Value, 43)
		for i := range v {
			v[i] = xmlrpc.Value{Type: "string"}
		}
		v[0] = strV("aaaa1111")
		v[24] = cell
		return mapTorrent(v)
	}

	nested := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{strV("https://torrent.ubuntu.com/announce")}},
	}}
	if got := mk(nested).TrackerHost; got != "torrent.ubuntu.com" {
		t.Fatalf("nested multicall: TrackerHost = %q, want torrent.ubuntu.com", got)
	}
	if got := mk(nested).TrackerStatus; got != "Not contacted" {
		t.Fatalf("nested multicall: TrackerStatus = %q", got)
	}

	flat := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		strV("https://torrent.ubuntu.com/announce"),
	}}
	if got := mk(flat).TrackerHost; got != "torrent.ubuntu.com" {
		t.Fatalf("flat multicall: TrackerHost = %q, want torrent.ubuntu.com", got)
	}
}
