// Package fakertorrent is a scripted SCGI/XML-RPC daemon used by tests and
// local development to exercise the console without a real rtorrent.
package fakertorrent

import (
	"encoding/xml"
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
}

// Options configures a started daemon.
type Options struct {
	// IncludeStopped adds a fourth, stopped torrent to the d.multicall2 list
	// (the default three are downloading/seeding/error).
	IncludeStopped bool
	// Fail scripts a fault for matching calls, so tests can exercise
	// per-hash partial failures and structured error surfacing.
	Fail *Failure
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
	d := &Daemon{ln: ln, includeStopped: opts.IncludeStopped, fail: opts.Fail}
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
	if d.LogCalls {
		d.mu.Lock()
		os.Stderr.WriteString("fakertorrent: " + method + "\n")
		d.mu.Unlock()
	}

	d.mu.Lock()
	fail := d.fail
	includeStopped := d.includeStopped
	d.mu.Unlock()
	if fail.Matches(method, params) {
		return nil, &xmlrpc.Fault{Code: 1, String: fail.Message}
	}

	switch method {
	case "d.multicall2":
		for _, param := range params {
			if strings.HasPrefix(param.Str, "f.multicall=") {
				return []xmlrpc.Value{detailRows(params)}, nil
			}
		}
		return []xmlrpc.Value{torrentRows(includeStopped)}, nil
	case "system.multicall":
		return []xmlrpc.Value{multiResponse(params)}, nil
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
				i(87), i(2048), i(1024), b(true), b(false), b(false),
			}},
			xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
				s("peer2xyz"), s("10.0.0.9"), i(6881), s("Deluge 2.1"),
				i(40), i(0), i(512), b(true), b(true), b(false),
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
		return []xmlrpc.Value{s("")}, nil
	}
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
		{Type: "array", Array: []xmlrpc.Value{s("peer1xyz"), s("10.0.0.5"), i(51413), s("qBittorrent 5.0"), i(87), i(2048), i(1024), b(true), b(false), b(false)}},
		{Type: "array", Array: []xmlrpc.Value{s("peer2xyz"), s("10.0.0.9"), i(6881), s("Deluge 2.1"), i(40), i(0), i(512), b(true), b(true), b(false)}},
	}}
	trackers := xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{
		{Type: "array", Array: []xmlrpc.Value{s("https://torrent.ubuntu.com/announce"), b(true), i(0), i(1840), i(220), i(1756700300)}},
		{Type: "array", Array: []xmlrpc.Value{s("https://torrent.ubuntu.com/tracker"), b(false), i(0), i(0), i(0), i(0)}},
	}}
	values := map[string]xmlrpc.Value{
		"d.down.total=":       i(3974842112),
		"d.up.total=":         i(9582132740),
		"d.chunk_size=":       i(4194304),
		"d.size_chunks=":      i(1000),
		"d.completed_chunks=": i(614),
		"d.directory=":        s("/mnt/data/iso"),
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

// torrentRows mirrors one d.multicall2 row (see rtorrent.listCommands order).
// includeStopped appends a fourth, stopped torrent for move-data tests.
func torrentRows(includeStopped bool) xmlrpc.Value {
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
		}},
		{Type: "array", Array: []xmlrpc.Value{
			s("bbbb2222bbbb2222"), s("linux-6.16.tar.xz"),
			i(150323855360), i(150323855360),
			i(1), b(true), b(false), i(0), s(""),
			i(0), i(2048000), i(3100), s("kernel"),
			i(1756600000), i(0), s("/mnt/data/kernel/linux-6.16.tar.xz"), b(false),
			i(44), i(0), b(false), b(false),
			i(2), i(1000), i(1000),
			{Type: "array", Array: []xmlrpc.Value{s("https://cdn.kernel.org/announce")}},
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
		}})
	}
	return xmlrpc.Value{Type: "array", Array: rows}
}

// multiResponse answers system.multicall entries per method name.
func multiResponse(params []xmlrpc.Value) xmlrpc.Value {
	one := func(v xmlrpc.Value) xmlrpc.Value {
		return xmlrpc.Value{Type: "array", Array: []xmlrpc.Value{v}}
	}
	byMethod := map[string]xmlrpc.Value{
		"throttle.global_down.rate=":     i(41200000),
		"throttle.global_up.rate=":       i(12800000),
		"throttle.global_down.total=":    i(7600000000000),
		"throttle.global_up.total=":      i(18400000000000),
		"system.client_version=":         s("0.15.4"),
		"system.client_version":          s("0.15.4"),
		"system.library_version=":        s("0.14.4"),
		"system.library_version":         s("0.14.4"),
		"network.port=":                  i(51413),
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
		"dht.statistics=": {Type: "struct", Struct: []xmlrpc.Member{
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
