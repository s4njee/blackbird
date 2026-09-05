// Package fakertorrent is a scripted SCGI/XML-RPC daemon used by tests and
// local development to exercise the console without a real rtorrent.
package fakertorrent

import (
	"encoding/xml"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"

	"blackbird/internal/scgi"
	"blackbird/internal/scgi/xmlrpc"
)

func s(x string) xmlrpc.Value { return xmlrpc.Value{Type: "string", Str: x} }
func i(n int64) xmlrpc.Value  { return xmlrpc.Value{Type: "int", Int: n} }
func b(x bool) xmlrpc.Value   { return xmlrpc.Value{Type: "bool", Bool: x} }

// Daemon serves canned rtorrent responses on a unix socket.
type Daemon struct {
	ln       net.Listener
	mu       sync.Mutex
	LogCalls bool

	includeStopped bool
	fail           *Failure
	calls          []string
	// throttles holds named channel caps in KB/s (up, down), mirroring
	// throttle.up/down; throttleNames overlays d.throttle_name assignments
	// by hash so tests can observe reassignment in later list polls.
	throttles     map[string][2]int64
	throttleNames map[string]string
	// globalRates holds global caps in KB/s set via .set_kb, mirrored back
	// as bytes by the bare max_rate getters (like the real daemon).
	globalRates map[string]int64
	// sessionSize, activeFraction and seed drive deterministic synthetic
	// sessions (PERF-6.6 fixtures); bench holds the generated base rows and
	// listCalls counts served list polls for activity overlay.
	sessionSize    int
	activeFraction float64
	seed           int64
	bench          []benchRow
	listCalls      int64
}

// benchRow is one generated fixture torrent: stable base fields plus a live
// flag. Rates and progress overlay deterministically from the list-call
// counter so sequential polls observe steady change.
type benchRow struct {
	hash      string
	name      string
	size      int64
	completed int64
	complete  bool
	state     int64
	label     string
	group     string
	tracker   string
	private   bool
	live      bool
	baseDown  int64
	baseUp    int64
	seeds     int64
	peers     int64
	started   int64
	finished  int64
	priority  int64
	message   string
}

// Options configures a started daemon.
type Options struct {
	// IncludeStopped adds a fourth, stopped torrent to the d.multicall2 list
	// (the default three are downloading/seeding/error). Ignored when
	// SessionSize generates a synthetic session.
	IncludeStopped bool
	// Fail scripts a fault for matching calls, so tests can exercise
	// per-hash partial failures and structured error surfacing.
	Fail *Failure
	// SessionSize generates a deterministic synthetic session of N torrents
	// instead of the canned 3-4 rows. 0 keeps the canned rows (every
	// existing test). Benchmark fixtures use 500, 5000, and 20000.
	SessionSize int
	// ActiveFraction is the share of generated torrents with live rates
	// (0-1); the rest idle at zero. Live rows advance per served list poll
	// so benchmarks observe steady change. Only meaningful with
	// SessionSize.
	ActiveFraction float64
	// Seed makes generation deterministic: same seed and size always yield
	// the same session (columns, states, labels, trackers).
	Seed int64
}

// Failure scripts one fault response.
type Failure struct {
	// Method is the exact XML-RPC method to fault; empty matches any.
	Method string
	// Hashes restricts the fault to calls whose first string param matches
	// one of these; empty matches any (including calls with no params).
	Hashes []string
	// Message is the faultString reported to the client.
	Message string
}

// Matches reports whether a received call should be faulted.
func (f *Failure) Matches(method string, params []xmlrpc.Value) bool {
	if f == nil {
		return false
	}
	if f.Method != "" && f.Method != method {
		return false
	}
	if len(f.Hashes) > 0 {
		if len(params) == 0 {
			return false
		}
		for _, h := range f.Hashes {
			for _, param := range params {
				if param.Str == h {
					return true
				}
			}
		}
		// Nested detail multicalls are selected by the client after rTorrent
		// returns the view rows, so the hash is not an XML-RPC parameter.
		// A detail command is sufficient for this canned daemon to target it.
		if method == "d.multicall2" {
			for _, param := range params {
				if strings.HasPrefix(param.Str, "f.multicall=") {
					return true
				}
			}
		}
		if method == "system.multicall" && len(params) > 0 {
			for _, raw := range params[0].Array {
				callParams, ok := raw.Member("params")
				if !ok {
					continue
				}
				for _, param := range callParams.Array {
					for _, h := range f.Hashes {
						if param.Str == h {
							return true
						}
					}
				}
			}
		}
		return false
	}
	return true
}

// Start removes any stale socket and begins serving.
func Start(sock string) (*Daemon, error) {
	return StartOpts(sock, Options{})
}

// StartOpts is Start with scripted behavior for tests.
func StartOpts(sock string, opts Options) (*Daemon, error) {
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	d := &Daemon{ln: ln, includeStopped: opts.IncludeStopped, fail: opts.Fail, throttles: map[string][2]int64{}, throttleNames: map[string]string{}, globalRates: map[string]int64{}}
	d.sessionSize, d.activeFraction, d.seed = opts.SessionSize, opts.ActiveFraction, opts.Seed
	if d.sessionSize > 0 {
		d.bench = generateSession(d.sessionSize, d.activeFraction, d.seed)
	}
	go d.accept()
	return d, nil
}

func (d *Daemon) accept() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.handle(conn)
	}
}

func (d *Daemon) handle(conn net.Conn) {
	defer conn.Close()
	_, body, err := scgi.ParseFrame(conn)
	if err != nil {
		return
	}
	method, params, err := xmlrpc.DecodeRequest(body)
	if err != nil {
		return
	}
	resp, fault := d.response(method, params)
	if fault != nil {
		conn.Write(xmlrpc.EncodeFault(fault.Code, fault.String))
		return
	}
	conn.Write(xmlrpc.EncodeResponseParams(resp))
}

func (d *Daemon) response(method string, params []xmlrpc.Value) ([]xmlrpc.Value, *xmlrpc.Fault) {
	d.mu.Lock()
	d.calls = append(d.calls, method)
	if d.LogCalls {
		os.Stderr.WriteString("fakertorrent: " + method + "\n")
	}
	fail := d.fail
	d.mu.Unlock()
	if fail.Matches(method, params) {
		return nil, &xmlrpc.Fault{Code: 1, String: fail.Message}
	}

	switch method {
	case "d.multicall2":
		for _, param := range params {
			if strings.HasPrefix(param.Str, "f.multicall=") {
				return []xmlrpc.Value{d.detailResponse(params)}, nil
			}
		}
		return []xmlrpc.Value{d.listRows()}, nil
	case "throttle.up", "throttle.down":
		// Wire format: ('', name, '<KB/s>'). Records the channel caps.
		if len(params) == 3 && params[1].Str != "" {
			var kb int64
			fmt.Sscanf(params[2].Str, "%d", &kb)
			d.mu.Lock()
			pair := d.throttles[params[1].Str]
			if method == "throttle.up" {
				pair[0] = kb
			} else {
				pair[1] = kb
			}
			d.throttles[params[1].Str] = pair
			d.mu.Unlock()
		}
		return []xmlrpc.Value{i(0)}, nil
	case "throttle.up.max", "throttle.down.max":
		// Returns bytes/s like the real daemon (-1 when undefined).
		name := ""
		if len(params) > 1 {
			name = params[1].Str
		}
		d.mu.Lock()
		pair, ok := d.throttles[name]
		d.mu.Unlock()
		if !ok {
			return []xmlrpc.Value{i(-1)}, nil
		}
		if method == "throttle.up.max" {
			return []xmlrpc.Value{i(pair[0] * 1024)}, nil
		}
		return []xmlrpc.Value{i(pair[1] * 1024)}, nil
	case "throttle.up.rate", "throttle.down.rate":
		return []xmlrpc.Value{i(0)}, nil
	case "d.throttle_name.set":
		if len(params) == 2 {
			d.mu.Lock()
			d.throttleNames[params[0].Str] = params[1].Str
			d.mu.Unlock()
		}
		return []xmlrpc.Value{s("")}, nil
	case "throttle.global_down.max_rate.set_kb", "throttle.global_up.max_rate.set_kb":
		// Wire format: ('', kb) with kb as int or string. Stored in KB/s
		// and mirrored back as bytes, like the real daemon.
		if len(params) == 2 {
			var kb int64
			if params[1].Type == "int" {
				kb = params[1].Int
			} else {
				fmt.Sscanf(params[1].Str, "%d", &kb)
			}
			d.mu.Lock()
			d.globalRates[method] = kb
			d.mu.Unlock()
		}
		return []xmlrpc.Value{i(0)}, nil
	case "system.methodExist":
		// The fake daemon pretends to be a rename-capable build (rTorrent-PS).
		if len(params) > 0 {
			return []xmlrpc.Value{b(params[0].Str == "d.name.set")}, nil
		}
		return []xmlrpc.Value{b(false)}, nil
	case "d.name.set":
		return []xmlrpc.Value{s("")}, nil // ack rename
	case "system.multicall":
		return []xmlrpc.Value{d.multiResponse(params)}, nil
	case "f.multicall":
		return []xmlrpc.Value{xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("/mnt/data/iso/ubuntu-24.04.2-desktop-amd64.iso"), i(6474842112), i(614), i(1000), i(2),
			}},
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("/mnt/data/iso/ubuntu-24.04.2.nfo"), i(4096), i(4), i(4), i(0),
			}},
		}}}, nil
	case "p.multicall":
		return []xmlrpc.Value{xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("peer1xyz"), s("10.0.0.5"), i(51413), s("qBittorrent 5.0"),
				i(87), i(2048), i(1024), b(true), b(false), b(false), i(73400320), i(104857600),
			}},
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("peer2xyz"), s("10.0.0.9"), i(6881), s("Deluge 2.1"),
				i(40), i(0), i(512), b(true), b(true), b(false), i(4194304), i(5242880),
			}},
		}}}, nil
	case "t.multicall":
		return []xmlrpc.Value{xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("https://torrent.ubuntu.com/announce"), b(true), i(0), i(1840), i(220), i(300),
			}},
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("https://torrent.ubuntu.com/tracker"), b(false), i(0), i(0), i(0), i(0),
			}},
		}}}, nil
	case "d.base_path=":
		if len(params) > 0 {
			switch params[0].Str {
			case "cccc3333cccc3333":
				// The broken torrent reports a base path outside every
				// configured download dir (remove-with-data refusal).
				return []xmlrpc.Value{s("/etc/evil/broken")}, nil
			case "dddd4444dddd4444":
				// The stopped torrent for move-data tests.
				return []xmlrpc.Value{s("/mnt/data/shared/linux-master.tar.xz")}, nil
			}
		}
		return []xmlrpc.Value{s("/mnt/data/iso/ubuntu-24.04.2-desktop-amd64.iso")}, nil
	default:
		if _, ok := ackMethods[method]; ok {
			return []xmlrpc.Value{s("")}, nil
		}
		// Unknown methods fault (PERF-6.6): a typo'd rtorrent command fails
		// tests instead of silently returning an empty string.
		return nil, &xmlrpc.Fault{Code: -501, String: "unknown method " + method}
	}
}

// ackMethods are the side-effect lifecycle/setter/load calls the console
// can emit, answered with an empty string like the real daemon's void
// returns. Anything else faults so command typos surface in tests. Keep in
// sync with internal/rtorrent (actions, remove, rename, ipfilter, load) and
// the tuning.Entries setter table.
var ackMethods = map[string]bool{
	"d.open": true, "d.start": true, "d.resume": true,
	"d.pause": true, "d.stop": true, "d.check_hash": true,
	"d.erase": true, "d.tracker_announce": true, "d.save_full_session": true,
	"d.custom1.set": true, "d.custom2.set": true, "d.custom3.set": true,
	"d.custom4.set": true, "d.custom5.set": true,
	"d.directory.set": true, "directory.default.set": true,
	"d.priority.set": true, "d.connection_seed.set": true, "d.sequential.set": true,
	"f.priority.set":   true,
	"d.tracker.insert": true, "d.tracker.remove": true, "t.is_enabled.set": true,
	"p.banned.set": true, "p.snubbed.set": true, "p.disconnect": true,
	"load.normal": true, "load.start": true, "load.raw": true, "load.raw_start": true,
	"ipv4_filter.load":       true,
	"network.port_range.set": true, "network.port_random.set": true,
	"protocol.encryption.set": true, "dht.mode.set": true, "dht.port.set": true,
	"trackers.use_udp.set": true, "protocol.pex.set": true,
	"network.local_address.set": true, "network.bind_address.set": true,
	"network.http.max_open.set": true, "network.max_open_sockets.set": true,
	"network.max_open_files.set":    true,
	"throttle.min_peers.normal.set": true, "throttle.max_peers.normal.set": true,
	"throttle.min_peers.seeded.set": true, "throttle.max_peers.seeded.set": true,
	"throttle.max_uploads.set": true, "throttle.max_uploads.global.set": true,
	"throttle.max_downloads.global.set": true,
	"throttle.global_down.max_rate.set": true, "throttle.global_up.max_rate.set": true,
}

// detailResponse serves download-scoped detail with one row per session
// torrent, so the client can select by hash exactly like against real
// rTorrent. The canned row covers the default session; generated sessions
// (PERF-6.6 fixtures, Playwright e2e) get one row per bench hash with
// transfer facts derived from the bench row.
func (d *Daemon) detailResponse(params []xmlrpc.Value) xmlrpc.Value {
	if d.sessionSize > 0 && len(d.bench) > 0 {
		rows := make([]xmlrpc.Value, 0, len(d.bench))
		for idx := range d.bench {
			rows = append(rows, benchDetailRow(d.bench[idx], idx, params))
		}
		return xmlrpc.Value{Type: "array", Array: rows}
	}
	return detailRows(params)
}

// benchDetailRow builds one detail row for a generated fixture torrent:
// canned file/peer/tracker lists (valid shape is what matters here) with
// transfer facts and a bitfield scaled to the bench row's size.
func benchDetailRow(b benchRow, idx int, params []xmlrpc.Value) xmlrpc.Value {
	const chunkSize = int64(1 << 20)
	chunks := (b.size + chunkSize - 1) / chunkSize
	if chunks < 1 {
		chunks = 1
	}
	done := b.completed / chunkSize
	if done > chunks {
		done = chunks
	}
	nbytes := (chunks + 7) / 8
	field := make([]byte, nbytes)
	for i := range field {
		field[i] = 0x00
	}
	// Complete rows read fully done; partial rows show the first half done.
	full := nbytes
	if done < chunks {
		full = nbytes / 2
	}
	for i := int64(0); i < full; i++ {
		field[i] = 0xff
	}
	row := []xmlrpc.Value{s(b.hash)}
	for _, command := range params[3:] {
		switch {
		case strings.HasPrefix(command.Str, "f.multicall="):
			row = append(row, benchFiles(idx))
		case strings.HasPrefix(command.Str, "p.multicall="):
			row = append(row, benchPeers(idx))
		case strings.HasPrefix(command.Str, "t.multicall="):
			row = append(row, benchTrackers(b))
		case command.Str == "d.down.total=":
			row = append(row, i(b.baseDown*900))
		case command.Str == "d.up.total=":
			row = append(row, i(b.baseUp*900))
		case command.Str == "d.chunk_size=":
			row = append(row, i(chunkSize))
		case command.Str == "d.size_chunks=":
			row = append(row, i(chunks))
		case command.Str == "d.completed_chunks=":
			row = append(row, i(done))
		case command.Str == "d.directory=":
			row = append(row, s("/mnt/data/bench/bench-torrent-"+fmt.Sprintf("%06d", idx)))
		case command.Str == "d.bitfield=":
			row = append(row, s(fmt.Sprintf("%x", field)))
		default:
			row = append(row, xmlrpc.Value{Type: "string"})
		}
	}
	return xmlrpc.Value{Type: "array", Array: row}
}

func benchFiles(idx int) xmlrpc.Value {
	base := "/mnt/data/bench/bench-torrent-" + fmt.Sprintf("%06d", idx)
	return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s(base + ".iso"), i(500 << 20), i(400), i(500), i(1)}},
		{Type: "array", Array: []xmlrpc.Value{s(base + ".nfo"), i(4096), i(4), i(4), i(1)}},
	}}
}

func benchPeers(idx int) xmlrpc.Value {
	return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s("peer1xyz"), s("10.0.0.5"), i(51413), s("qBittorrent 5.0"), i(87), i(2048), i(1024), b(true), b(false), b(false), i(73400320), i(104857600)}},
	}}
}

func benchTrackers(row benchRow) xmlrpc.Value {
	return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s(row.tracker), b(true), i(0), i(1840), i(220), i(1756700300)}},
	}}
}

// detailRows mirrors the download-scoped nested multicalls that current
// rTorrent uses for file, peer, and tracker detail. The requested fields are
// selected from the command strings so this fixture follows the real daemon's
// response shape rather than the obsolete standalone f./p./t.multicall form.
func detailRows(params []xmlrpc.Value) xmlrpc.Value {
	files := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s("/mnt/data/iso/ubuntu-24.04.2-desktop-amd64.iso"), i(6474842112), i(614), i(1000), i(2)}},
		{Type: "array", Array: []xmlrpc.Value{s("/mnt/data/iso/ubuntu-24.04.2.nfo"), i(4096), i(4), i(4), i(0)}},
	}}
	peers := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s("peer1xyz"), s("10.0.0.5"), i(51413), s("qBittorrent 5.0"), i(87), i(2048), i(1024), b(true), b(false), b(false), i(73400320), i(104857600)}},
		{Type: "array", Array: []xmlrpc.Value{s("peer2xyz"), s("10.0.0.9"), i(6881), s("Deluge 2.1"), i(40), i(0), i(512), b(true), b(true), b(false), i(4194304), i(5242880)}},
	}}
	trackers := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s("https://torrent.ubuntu.com/announce"), b(true), i(0), i(1840), i(220), i(1756700300)}},
		{Type: "array", Array: []xmlrpc.Value{s("https://torrent.ubuntu.com/tracker"), b(false), i(0), i(0), i(0), i(0)}},
	}}
	// 1000-piece torrent with 614 complete. The bitfield is ceil(1000/8)=125
	// bytes (250 hex chars): bytes 0..75 = 0xff (pieces 0-607 done), byte 76 =
	// 0xfc (pieces 608-615, top 6 bits set → 608-613 done), bytes 77..124 = 0.
	bitfieldHex := strings.Repeat("ff", 76) + "fc" + strings.Repeat("00", 125-77)
	values := map[string]xmlrpc.Value{
		"d.down.total=":       i(3974842112),
		"d.up.total=":         i(9582132740),
		"d.chunk_size=":       i(4194304),
		"d.size_chunks=":      i(1000),
		"d.completed_chunks=": i(614),
		"d.directory=":        s("/mnt/data/iso"),
		"d.bitfield=":         s(bitfieldHex),
	}
	row := []xmlrpc.Value{s("aaaa1111aaaa1111")}
	for _, command := range params[3:] {
		switch {
		case strings.HasPrefix(command.Str, "f.multicall="):
			row = append(row, files)
		case strings.HasPrefix(command.Str, "p.multicall="):
			row = append(row, peers)
		case strings.HasPrefix(command.Str, "t.multicall="):
			row = append(row, trackers)
		default:
			row = append(row, values[command.Str])
		}
	}
	return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{{Type: "array", Array: row}}}
}

// generateSession builds N deterministic fixture rows: same seed and size
// always yield the same columns, states, labels, and trackers. Roughly 80%
// download, 15% seed, 3% stopped, 2% tracker-error; the live share from
// ActiveFraction carries nonzero rates. Every 50th row is a private
// tracker-error to exercise the error category.
func generateSession(n int, activeFraction float64, seed int64) []benchRow {
	rng := rand.New(rand.NewSource(seed))
	labels := []string{"", "iso", "media", "kernel", "apps"}
	trackers := []string{
		"https://torrent.ubuntu.com/announce",
		"https://cdn.kernel.org/announce",
		"https://archive.example.com/announce",
		"udp://tracker.example:1337/announce",
	}
	rows := make([]benchRow, n)
	liveBudget := int(float64(n) * activeFraction)
	for idx := range rows {
		size := int64(500<<20) + rng.Int63n(20<<30)
		complete := idx%100 < 15
		var completed int64
		if complete {
			completed = size
		} else {
			completed = size * int64(20+rng.Intn(60)) / 100
		}
		state := int64(1)
		if idx%100 >= 95 {
			state = 0 // stopped slice
		}
		message := ""
		if idx%50 == 49 {
			message = "Tracker: [Tried all trackers.]"
			state = 0
		}
		rows[idx] = benchRow{
			hash:      fmt.Sprintf("%040x", uint64(idx)^uint64(seed)*0x9e3779b97f4a7c15),
			name:      fmt.Sprintf("bench-torrent-%06d", idx),
			size:      size,
			completed: completed,
			complete:  complete,
			state:     state,
			label:     labels[idx%len(labels)],
			group:     "",
			tracker:   trackers[idx%len(trackers)],
			private:   idx%50 == 49,
			live:      idx < liveBudget,
			baseDown:  100000 + rng.Int63n(40000000),
			baseUp:    50000 + rng.Int63n(10000000),
			seeds:     rng.Int63n(200),
			peers:     rng.Int63n(150),
			started:   1756700000 + int64(idx),
			finished:  0,
			priority:  int64(idx % 4),
			message:   message,
		}
		if complete {
			rows[idx].finished = 1756600000 + int64(idx)
		}
		if idx%7 == 0 {
			rows[idx].group = "archive"
		}
	}
	return rows
}

// listRows serves the d.multicall2 list: canned rows by default, the
// generated session when SessionSize is set. Live rows advance with the
// served-call counter so sequential polls observe steady, deterministic
// change (concurrent polls would interleave the counter; benchmarks poll
// sequentially).
func (d *Daemon) listRows() xmlrpc.Value {
	d.mu.Lock()
	d.listCalls++
	calls := d.listCalls
	size := d.sessionSize
	rows := d.bench
	throttle := func(hash string) string { return d.throttleNames[hash] }
	d.mu.Unlock()
	if size <= 0 || len(rows) == 0 {
		return d.torrentRows()
	}
	out := make([]xmlrpc.Value, 0, len(rows))
	for idx := range rows {
		r := rows[idx]
		down, up, completed := int64(0), int64(0), r.completed
		peers, seeds := int64(0), int64(0)
		if r.live && r.message == "" {
			jitter := int64((calls + int64(idx)) % 5)
			down, up = r.baseDown+jitter*1024, r.baseUp+jitter*512
			if !r.complete {
				completed = r.completed + calls*(1<<20)
				if completed > r.size {
					completed = r.size
				}
			}
			peers = r.peers + (calls+int64(idx))%3 - 1
			if peers < 0 {
				peers = 0
			}
			seeds = r.seeds
		}
		left := r.size - completed
		if left < 0 {
			left = 0
		}
		base := "/mnt/data/bench/bench-torrent-" + fmt.Sprintf("%06d", idx)
		out = append(out, xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			s(r.hash), s(r.name),
			i(r.size), i(completed),
			i(r.state), b(r.complete), b(false), i(0), s(r.message),
			i(down), i(up), i(completed * 1000 / max1(r.size)), s(r.label),
			i(r.started), i(0), s(base), b(r.private),
			i(peers), i(seeds), b(r.state == 1), b(r.state == 1),
			i(r.priority), i(1000), i(completed * 1000 / max1(r.size)),
			{Type: "array", Array: []xmlrpc.Value{s(r.tracker)}},
			i(left), i(r.baseUp * 900), i(r.baseDown * 900), i(r.finished), s(throttle(r.hash)),
			s(r.group), s(""), s(""), s(""),
			s(""), i(0),
			i(peers + seeds), i(completed * 1000 / max1(r.size)), b(true),
			s("/mnt/data/bench"), s("connected"),
			i(0), i(0),
		}})
	}
	return xmlrpc.Value{Type: "array", Array: out}
}

func max1(n int64) int64 {
	if n <= 0 {
		return 1
	}
	return n
}

// includeStopped appends a fourth, stopped torrent for move-data tests.
// Columns 25-29 (left/up-total/down-total/finished/throttle_name) are
// included so throttle assignments surface in later list polls; the
// throttle_name cell honors assignments received via d.throttle_name.set.
func (d *Daemon) torrentRows() xmlrpc.Value {
	d.mu.Lock()
	includeStopped := d.includeStopped
	throttle := func(hash string) string { return d.throttleNames[hash] }
	d.mu.Unlock()
	rows := []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{
			s("aaaa1111aaaa1111"), s("ubuntu-24.04.2-desktop-amd64.iso"),
			i(6474842112), i(3974842112),
			i(1), b(false), b(false), i(0), s(""),
			i(41200000), i(12800000), i(2410), s("iso"),
			i(1756700000), i(0), s("/mnt/data/iso/ubuntu-24.04.2-desktop-amd64.iso"), b(false),
			i(150), i(38), b(false), b(false),
			i(2), i(1000), i(614),
			{Type: "array", Array: []xmlrpc.Value{s("https://torrent.ubuntu.com/announce")}},
			i(2500000000), i(9582132740), i(3974842112), i(0), s(throttle("aaaa1111aaaa1111")),
		}},
		{Type: "array", Array: []xmlrpc.Value{
			s("bbbb2222bbbb2222"), s("linux-6.16.tar.xz"),
			i(150323855360), i(150323855360),
			i(1), b(true), b(false), i(0), s(""),
			i(0), i(2048000), i(3100), s("kernel"),
			i(1756600000), i(0), s("/mnt/data/kernel/linux-6.16.tar.xz"), b(false),
			i(44), i(0), b(true), b(false),
			i(2), i(1000), i(1000),
			{Type: "array", Array: []xmlrpc.Value{s("https://cdn.kernel.org/announce")}},
			i(0), i(214748364800), i(150323855360), i(1756600000), s(throttle("bbbb2222bbbb2222")),
		}},
		{Type: "array", Array: []xmlrpc.Value{
			s("cccc3333cccc3333"), s("some-broken-torrent"),
			i(1000000), i(0),
			i(0), b(false), b(false), i(0), s("Tracker: [Tried all trackers.]"),
			i(0), i(0), i(0), s(""),
			i(1756500000), i(0), s("/mnt/data/broken"), b(true),
			i(0), i(0), b(false), b(false),
			i(0), i(1000), i(0),
			{Type: "array", Array: []xmlrpc.Value{s("http://bad.example.invalid/announce")}},
			i(1000000), i(0), i(0), i(0), s(throttle("cccc3333cccc3333")),
		}},
	}
	if includeStopped {
		rows = append(rows, xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
			s("dddd4444dddd4444"), s("linux-master.tar.xz"),
			i(1572864000), i(314572800),
			i(0), b(false), b(false), i(0), s(""),
			i(0), i(0), i(0), s("archive"),
			i(1756300000), i(0), s("/mnt/data/shared/linux-master.tar.xz"), b(false),
			i(0), i(0), b(false), b(false),
			i(2), i(1000), i(250),
			{Type: "array", Array: []xmlrpc.Value{s("https://archive.example.com/announce")}},
			i(1258291200), i(0), i(314572800), i(0), s(throttle("dddd4444dddd4444")),
		}})
	}
	return xmlrpc.Value{Type: "array", Array: rows}
}

// multiResponse answers system.multicall entries per method name. The two
// global max_rate getters mirror values set via .set_kb (bytes, like the
// real daemon); everything else is canned.
func (d *Daemon) multiResponse(params []xmlrpc.Value) xmlrpc.Value {
	one := func(v xmlrpc.Value) xmlrpc.Value {
		return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{v}}
	}
	byMethod := map[string]xmlrpc.Value{
		"throttle.global_down.rate":      i(41200000),
		"throttle.global_up.rate":        i(12800000),
		"throttle.global_down.total":     i(7600000000000),
		"throttle.global_up.total":       i(18400000000000),
		"system.client_version":          s("0.15.4"),
		"system.library_version":         s("0.14.4"),
		"network.port":                   i(51413),
		"network.port_range=":            s("51413"),
		"network.port_random=":           s("0"),
		"protocol.encryption=":           s("require,require_RC4"),
		"dht.mode=":                      s("auto"),
		"dht.port=":                      i(6881),
		"trackers.use_udp=":              s("1"),
		"protocol.pex=":                  s("1"),
		"network.local_address=":         s(""),
		"network.bind_address=":          s(""),
		"network.http.max_open=":         i(32),
		"network.max_open_sockets=":      i(1024),
		"network.max_open_files=":        i(1024),
		"throttle.min_peers.normal=":     i(50),
		"throttle.max_peers.normal=":     i(200),
		"throttle.min_peers.seeded=":     i(10),
		"throttle.max_peers.seeded=":     i(100),
		"throttle.max_uploads=":          i(12),
		"throttle.max_uploads.global=":   i(64),
		"throttle.max_downloads.global=": i(32),
		"throttle.global_down.max_rate=": i(0),
		"throttle.global_up.max_rate=":   i(20480),
		"d.down_total=":                  i(3974842112),
		"d.up_total=":                    i(9582132740),
		"d.chunk_size=":                  i(4194304),
		"d.size_chunks=":                 i(1000),
		"d.completed_chunks=":            i(614),
		"d.directory=":                   s("/mnt/data/iso"),
		"dht.statistics": {Type: "struct", Struct: []xmlrpc.Member{
			{Name: "dht", Value: xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
				{Name: "nodes", Value: i(1204)},
			}}},
		}},
	}
	var entries []xmlrpc.Value
	if len(params) > 0 {
		for _, call := range params[0].Array {
			name := ""
			if m, ok := call.Member("methodName"); ok {
				name = m.Str
			}
			// A nested d.multicall2 list poll (PERF-6.3 ListAndGlobals)
			// returns the served rows wrapped as the call's single return
			// value, mirroring the real daemon's shape.
			if name == "d.multicall2" {
				entries = append(entries, one(d.listRows()))
				continue
			}
			// Hash-scoped detail calls are returned as one value per nested
			// request, matching rTorrent's system.multicall wire shape.
			if strings.HasSuffix(name, ".multicall") || strings.HasPrefix(name, "d.") {
				nested := []xmlrpc.Value{s(""), s("main"), s("d.hash=")}
				if name == "f.multicall" {
					nested = append(nested, s("f.multicall="))
				} else if name == "p.multicall" {
					nested = append(nested, s("p.multicall="))
				} else if name == "t.multicall" {
					nested = append(nested, s("t.multicall="))
				} else {
					nested = append(nested, s(name+"="))
				}
				value := detailRows(nested).Array[0].Array[1]
				entries = append(entries, one(value))
				continue
			}
			// Bare global max_rate getters mirror .set_kb state (bytes).
			if name == "throttle.global_down.max_rate" || name == "throttle.global_up.max_rate" {
				d.mu.Lock()
				kb := d.globalRates[name+".set_kb"]
				d.mu.Unlock()
				entries = append(entries, one(i(kb*1024)))
				continue
			}
			if v, ok := byMethod[name]; ok {
				entries = append(entries, one(v))
			} else {
				entries = append(entries, xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
					{Name: "faultCode", Value: i(-501)},
					{Name: "faultString", Value: s("unknown method " + name)},
				}})
			}
		}
	}
	return xmlrpc.Value{Type: "array", Array: entries}
}

var _ = xml.Name{}

// Stop closes the listener, ending the daemon (tests restart it to exercise
// disconnect/reconnect flows).
func (d *Daemon) Stop() { d.ln.Close() }

// CallMethods returns a stable copy of every XML-RPC method received. Tests
// use it to assert that typed API actions produce the intended rTorrent call.
func (d *Daemon) CallMethods() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}
