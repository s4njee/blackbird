package rtorrent

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"blackbird/internal/geoip"
	"blackbird/internal/scgi"
	"blackbird/internal/scgi/xmlrpc"
)

// Client wraps the SCGI transport with typed rtorrent commands.
type Client struct {
	scgi *scgi.Client
}

// New builds a client for the given endpoint ("unix://..." or "tcp://...").
func New(endpoint string, timeout time.Duration) (*Client, error) {
	c, err := scgi.New(endpoint, timeout)
	if err != nil {
		return nil, err
	}
	return &Client{scgi: c}, nil
}

// SetMaxResponseBytes caps one SCGI response (0 = 64MB default,
// scgi.DefaultMaxResponseBytes). Exceeding it aborts the read with a typed
// error instead of growing memory without bound (PERF-6.3).
func (c *Client) SetMaxResponseBytes(n int64) {
	c.scgi.MaxResponseBytes = n
}

// ---- generic value helpers ----

// sval coerces any XML-RPC value to a string (rtorrent sometimes returns
// numerics as strings).
func sval(v xmlrpc.Value) string {
	switch v.Type {
	case "string", "base64", "datetime", "":
		return v.Str
	case "int", "i8":
		return strconv.FormatInt(v.Int, 10)
	case "bool":
		if v.Bool {
			return "1"
		}
		return "0"
	default:
		return v.Str
	}
}

// ival coerces any XML-RPC value to an int64.
func ival(v xmlrpc.Value) int64 {
	switch v.Type {
	case "int", "i8":
		return v.Int
	case "bool":
		if v.Bool {
			return 1
		}
		return 0
	default:
		n, _ := strconv.ParseInt(strings.TrimSpace(v.Str), 10, 64)
		return n
	}
}

// bval coerces any XML-RPC value to a bool.
func bval(v xmlrpc.Value) bool {
	switch v.Type {
	case "bool":
		return v.Bool
	default:
		return ival(v) != 0
	}
}

func aval(v xmlrpc.Value) []xmlrpc.Value { return v.Array }

func str(s string) xmlrpc.Value { return xmlrpc.Value{Type: "string", Str: s} }

// ---- torrent list ----

// listCommands is the d.multicall2 key set backing the torrents[] shape.
// The trailing t.multicall returns every tracker URL so the host column can
// be derived without per-torrent round trips.
var listCommands = []string{
	"d.hash=",               // 0
	"d.name=",               // 1
	"d.size_bytes=",         // 2
	"d.completed_bytes=",    // 3
	"d.state=",              // 4
	"d.complete=",           // 5
	"d.is_hash_checking=",   // 6
	"d.hashing=",            // 7
	"d.message=",            // 8
	"d.down.rate=",          // 9
	"d.up.rate=",            // 10
	"d.ratio=",              // 11 (per-mille)
	"d.custom1=",            // 12
	"d.timestamp.started=",  // 13
	"d.creation_date=",      // 14
	"d.base_path=",          // 15
	"d.is_private=",         // 16
	"d.peers_connected=",    // 17
	"d.peers_complete=",     // 18
	"d.is_open=",            // 19
	"d.is_active=",          // 20
	"d.priority=",           // 21
	"d.size_chunks=",        // 22
	"d.completed_chunks=",   // 23
	"t.multicall=,t.url=",   // 24 (array of tracker URLs)
	"d.left_bytes=",         // 25
	"d.up.total=",           // 26
	"d.down.total=",         // 27
	"d.timestamp.finished=", // 28
	"d.throttle_name=",      // 29
	"d.custom2=",            // 30
	"d.custom3=",            // 31
	"d.custom4=",            // 32
	"d.custom5=",            // 33
	"d.tied_to_file=",       // 34
	"d.skip.total=",         // 35
	"d.peers_accounted=",    // 36
	"d.chunks_hashed=",      // 37
	"d.is_multi_file=",      // 38
	"d.directory=",          // 39
	"d.connection_current=", // 40
	"d.creation_date=",      // 41
	"d.connection_seed=",    // 42
}

// ListTorrents returns the normalized torrent list via one d.multicall2.
func (c *Client) ListTorrents(ctx context.Context) ([]Torrent, error) {
	// d.multicall2 takes a download group followed by the view name. An empty
	// group selects all downloads; "main" is the built-in view in rTorrent.
	params := []xmlrpc.Value{str(""), str("main")}
	for _, cmd := range listCommands {
		params = append(params, str(cmd))
	}
	res, err := c.scgi.Call(ctx, "d.multicall2", params)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, nil
	}
	rows := aval(res[0])
	out := make([]Torrent, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapTorrent(aval(row)))
	}
	return out, nil
}

// mapTorrent normalizes one d.multicall2 row. Field order matches
// listCommands. Derivations (percent, ETA, ratio, state) happen here so
// every consumer sees consistent values.
func mapTorrent(v []xmlrpc.Value) Torrent {
	get := func(i int) xmlrpc.Value {
		if i < len(v) {
			return v[i]
		}
		return xmlrpc.Value{Type: "string"}
	}

	t := Torrent{
		Hash:            sval(get(0)),
		Name:            sval(get(1)),
		SizeBytes:       ival(get(2)),
		CompletedBytes:  ival(get(3)),
		Complete:        bval(get(5)),
		IsOpen:          bval(get(19)),
		LeftBytes:       ival(get(25)),
		UploadedBytes:   ival(get(26)),
		DownloadedBytes: ival(get(27)),
		DownRate:        ival(get(9)),
		UpRate:          ival(get(10)),
		Ratio:           float64(ival(get(11))) / 1000, // rtorrent reports per-mille
		Label:           sval(get(12)),
		Throttle:        sval(get(29)),
		Custom2:         sval(get(30)),
		Custom3:         sval(get(31)),
		Custom4:         sval(get(32)),
		Custom5:         sval(get(33)),
		TiedToFile:      sval(get(34)),
		SkippedBytes:    ival(get(35)),
		PeersAccounted:  int(ival(get(36))),
		ChunksHashed:    ival(get(37)),
		IsMultiFile:     bval(get(38)),
		Directory:       sval(get(39)),
		Connection:      sval(get(40)),
		BasePath:        sval(get(15)),
		IsPrivate:       bval(get(16)),
		Priority:        int(ival(get(21))),
		Superseeding:    bval(get(42)),
	}
	t.RatioGroup = t.Custom2
	if strings.TrimSpace(sval(get(8))) != "" {
		t.TrackerStatus = "Failed"
	}

	if ts := ival(get(13)); ts > 0 {
		t.AddedAt = time.Unix(ts, 0)
	} else if ts := ival(get(14)); ts > 0 {
		t.AddedAt = time.Unix(ts, 0)
	}
	creationTS := ival(get(41))
	if creationTS <= 0 {
		creationTS = ival(get(14))
	}
	if creationTS > 0 {
		t.CreationDate = time.Unix(creationTS, 0)
	}
	if ts := ival(get(28)); ts > 0 {
		t.FinishedAt = time.Unix(ts, 0)
	}

	sizeChunks := ival(get(22))
	hashedChunks := ival(get(37))
	if sizeChunks > 0 && t.SizeBytes <= 0 {
		// Some daemons report size 0 until metadata resolves; fall back to
		// chunk arithmetic is not possible without chunk size, keep 0.
		_ = sizeChunks
	}
	if t.SizeBytes > 0 {
		t.Percent = clamp100(float64(t.CompletedBytes) / float64(t.SizeBytes) * 100)
		if t.LeftBytes == 0 && t.CompletedBytes < t.SizeBytes {
			t.LeftBytes = t.SizeBytes - t.CompletedBytes
		}
	}
	if t.Directory == "" {
		t.Directory = t.BasePath
	}

	connected := ival(get(17))
	seedsConnected := ival(get(18))
	t.Seeds = int(seedsConnected)
	t.Peers = int(connected - seedsConnected)
	if t.Peers < 0 {
		t.Peers = 0
	}

	var urls []string
	if len(v) > 24 {
		for _, u := range aval(get(24)) {
			// A nested t.multicall returns one inner list per tracker
			// (t.url= is its single column), which is what a real daemon
			// sends; sval on that list yields "" and silently blanked the
			// tracker for every torrent. Flat rows are still accepted.
			if inner := aval(u); len(inner) > 0 {
				urls = append(urls, sval(inner[0]))
				continue
			}
			urls = append(urls, sval(u))
		}
	}
	t.TrackerHost = trackerHost(urls)
	if t.TrackerStatus == "" && t.TrackerHost != "" {
		t.TrackerStatus = "Not contacted"
	}

	t.State, t.Message = normalizeState(
		int(ival(get(4))), // state
		t.Complete,        // complete
		bval(get(6)),      // is_hash_checking
		int(ival(get(7))), // hashing
		sval(get(8)),      // message
		t.IsOpen,          // is_open
		bval(get(20)),     // is_active
	)
	if t.State == StateChecking && sizeChunks > 0 {
		// d.completed_chunks is the amount downloaded, not the amount
		// verified. Use d.chunks_hashed so a recheck does not appear to make
		// progress merely because the torrent was previously downloaded.
		t.CheckingPercent = clamp100(float64(hashedChunks) / float64(sizeChunks) * 100)
	}

	if t.State == StateDownloading && t.DownRate > 0 && t.SizeBytes > t.CompletedBytes {
		t.EtaSeconds = float64(t.SizeBytes-t.CompletedBytes) / float64(t.DownRate)
	} else {
		t.EtaSeconds = -1
	}
	return t
}

// normalizeState maps raw daemon flags onto the UI state set. rtorrent
// signals queueing as stopped-but-open; error state comes from a non-empty
// d.message.
func normalizeState(state int, complete, hashChecking bool, hashing int, message string, isOpen, isActive bool) (State, string) {
	if hashChecking || hashing != 0 {
		return StateChecking, ""
	}
	if strings.TrimSpace(message) != "" {
		return StateError, message
	}
	switch {
	case state == 1 && complete:
		return StateSeeding, ""
	case state == 1:
		return StateDownloading, ""
	case isOpen && isActive:
		return StateQueued, ""
	default:
		return StateStopped, ""
	}
}

func clamp100(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 100 {
		return 100
	}
	return f
}

// trackerHost extracts the hostname of the first usable tracker URL.
func trackerHost(urls []string) string {
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "[DHT]") || strings.HasPrefix(raw, "[Peer") {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		// hostname only (drop port) per the design's tracker column
		return u.Hostname()
	}
	return ""
}

// ---- detail fetches ----

const (
	filesDetailCall = "f.multicall=,f.path=,f.size_bytes=,f.completed_chunks=,f.size_chunks=,f.priority="
	// p.down_total/p.up_total are the per-connection byte counters; p.is_snubbed
	// drives the composed S flag and the snub/unsnub moderation toggle.
	peersDetailCall    = "p.multicall=,p.id=,p.address=,p.port=,p.client_version=,p.completed_percent=,p.down_rate=,p.up_rate=,p.is_encrypted=,p.is_incoming=,p.is_snubbed=,p.down_total=,p.up_total="
	trackersDetailCall = "t.multicall=,t.url=,t.is_enabled=,t.group=,t.scrape_complete=,t.scrape_incomplete=,t.success_time_next=,t.latest_event=,t.failed_counter=,t.success_counter=,t.latest_new_peers="
)

// multicallParams builds the arguments for a nested f./p./t.multicall: the
// target hash, the empty pattern, and then ONE PARAMETER PER FIELD.
//
// rTorrent wants each field command as its own parameter. Passing them
// comma-joined inside a single string parameter appears to work on some
// builds but makes others — 0.15.x among them — treat the whole string as one
// command and return only the first field of every row. That silently emptied
// the detail drawer: files came back as paths with no sizes, trackers as URLs
// with no counters.
func multicallParams(hash, fields string) []xmlrpc.Value {
	params := []xmlrpc.Value{str(hash), str("")}
	for _, field := range strings.Split(fields, ",") {
		if field = strings.TrimSpace(field); field != "" {
			params = append(params, str(field))
		}
	}
	return params
}

// detailRow evaluates the f./p./t.multicall commands and d.* getters for one
// torrent. The target hash is passed directly to every command. rTorrent
// rejects p.multicall and t.multicall inside system.multicall on some builds,
// so nested detail calls are made directly while scalar d.* getters are
// batched. Using d.multicall2 here would scan the entire main view and, on
// large sessions, can exceed rTorrent's response limits before the requested
// row is found.
func (c *Client) detailRow(ctx context.Context, hash string, calls ...string) ([]xmlrpc.Value, error) {
	if len(calls) == 0 {
		return []xmlrpc.Value{str(hash)}, nil
	}
	values := make([]xmlrpc.Value, len(calls))
	reqs := make([]Request, 0, len(calls))
	requestIndexes := make([]int, 0, len(calls))
	for i, call := range calls {
		switch {
		case strings.HasPrefix(call, "f.multicall="):
			res, err := c.scgi.Call(ctx, "f.multicall", multicallParams(hash, strings.TrimPrefix(call, "f.multicall=,")))
			if err != nil {
				return nil, fmt.Errorf("%s for torrent %q: %w", call, hash, err)
			}
			if len(res) == 0 {
				return nil, fmt.Errorf("%s returned no values for torrent %q", call, hash)
			}
			values[i] = res[0]
		case strings.HasPrefix(call, "p.multicall="):
			res, err := c.scgi.Call(ctx, "p.multicall", multicallParams(hash, strings.TrimPrefix(call, "p.multicall=,")))
			if err != nil {
				return nil, fmt.Errorf("%s for torrent %q: %w", call, hash, err)
			}
			if len(res) == 0 {
				return nil, fmt.Errorf("%s returned no values for torrent %q", call, hash)
			}
			values[i] = res[0]
		case strings.HasPrefix(call, "t.multicall="):
			res, err := c.scgi.Call(ctx, "t.multicall", multicallParams(hash, strings.TrimPrefix(call, "t.multicall=,")))
			if err != nil {
				return nil, fmt.Errorf("%s for torrent %q: %w", call, hash, err)
			}
			if len(res) == 0 {
				return nil, fmt.Errorf("%s returned no values for torrent %q", call, hash)
			}
			values[i] = res[0]
		default:
			requestIndexes = append(requestIndexes, i)
			reqs = append(reqs, Request{Method: strings.TrimSuffix(call, "="), Params: []xmlrpc.Value{str(hash)}})
		}
	}
	if len(reqs) > 0 {
		results, err := c.MultiCall(ctx, reqs)
		if err != nil {
			return nil, err
		}
		for j, result := range results {
			i := requestIndexes[j]
			if result.Err != nil {
				return nil, fmt.Errorf("%s for torrent %q: %w", calls[i], hash, result.Err)
			}
			if len(result.Values) == 0 {
				return nil, fmt.Errorf("%s returned no values for torrent %q", calls[i], hash)
			}
			values[i] = result.Values[0]
		}
	}
	row := []xmlrpc.Value{str(hash)}
	for _, value := range values {
		row = append(row, value)
	}
	return row, nil
}

func nestedRows(row []xmlrpc.Value, index int) []xmlrpc.Value {
	if index >= len(row) {
		return nil
	}
	return aval(row[index])
}

func mapFiles(rows []xmlrpc.Value) []File {
	out := make([]File, 0, len(rows))
	for i, raw := range rows {
		v := aval(raw)
		g := func(j int) xmlrpc.Value {
			if j < len(v) {
				return v[j]
			}
			return xmlrpc.Value{Type: "string"}
		}
		out = append(out, File{
			Index:           i,
			Path:            sval(g(0)),
			SizeBytes:       ival(g(1)),
			CompletedChunks: ival(g(2)),
			SizeChunks:      ival(g(3)),
			Priority:        int(ival(g(4))),
		})
	}
	return out
}

// Files returns the file list for one torrent.
func (c *Client) Files(ctx context.Context, hash string) ([]File, error) {
	row, err := c.detailRow(ctx, hash, filesDetailCall)
	if err != nil {
		return nil, err
	}
	return mapFiles(nestedRows(row, 1)), nil
}

// mapPeers normalizes peer rows and annotates each with a GeoIP country code
// ("" when the database cannot resolve the address). Field order matches
// peersDetailCall.
func mapPeers(rows []xmlrpc.Value) []Peer {
	out := make([]Peer, 0, len(rows))
	for _, raw := range rows {
		v := aval(raw)
		g := func(j int) xmlrpc.Value {
			if j < len(v) {
				return v[j]
			}
			return xmlrpc.Value{Type: "string"}
		}
		var flags strings.Builder
		if bval(g(7)) {
			flags.WriteByte('E')
		}
		if bval(g(8)) {
			flags.WriteByte('I')
		}
		snubbed := bval(g(9))
		if snubbed {
			flags.WriteByte('S')
		}
		address := sval(g(1))
		peer := Peer{
			ID:               sval(g(0)),
			Address:          address,
			Port:             int(ival(g(2))),
			Client:           sval(g(3)),
			CompletedPercent: float64(ival(g(4))),
			DownRate:         ival(g(5)),
			UpRate:           ival(g(6)),
			IsSnubbed:        snubbed,
			DownloadedBytes:  ival(g(10)),
			UploadedBytes:    ival(g(11)),
			CountryCode:      geoip.Lookup(address),
			Flags:            flags.String(),
		}
		out = append(out, peer)
	}
	return out
}

// Peers returns the connected peer list for one torrent.
func (c *Client) Peers(ctx context.Context, hash string) ([]Peer, error) {
	row, err := c.detailRow(ctx, hash, peersDetailCall)
	if err != nil {
		return nil, err
	}
	return mapPeers(nestedRows(row, 1)), nil
}

func mapTrackers(rows []xmlrpc.Value) []Tracker {
	out := make([]Tracker, 0, len(rows))
	for i, raw := range rows {
		v := aval(raw)
		g := func(j int) xmlrpc.Value {
			if j < len(v) {
				return v[j]
			}
			return xmlrpc.Value{Type: "string"}
		}
		tr := Tracker{
			Index:       i,
			URL:         sval(g(0)),
			IsEnabled:   bval(g(1)),
			Group:       int(ival(g(2))),
			Seeds:       int(ival(g(3))),
			Leechers:    int(ival(g(4))),
			LatestEvent: sval(g(6)), FailedCount: int(ival(g(7))), SuccessCount: int(ival(g(8))), NewPeers: int(ival(g(9))),
		}
		if ns := ival(g(5)); ns > 0 {
			tr.NextAnnounce = time.Unix(ns, 0)
		}
		out = append(out, tr)
	}
	return out
}

// Trackers returns the tracker list for one torrent.
func (c *Client) Trackers(ctx context.Context, hash string) ([]Tracker, error) {
	row, err := c.detailRow(ctx, hash, trackersDetailCall)
	if err != nil {
		return nil, err
	}
	return mapTrackers(nestedRows(row, 1)), nil
}

// FetchDetail gathers files, peers, trackers and transfer facts for one
// torrent using hash-scoped nested calls plus one system.multicall for the
// scalar getters. The trailing
// d.bitfield= column is captured into Detail.BitfieldHex (hex of the piece
// bitfield); it is not part of the JSON detail payload (the WS layer diffs it).
func (c *Client) FetchDetail(ctx context.Context, hash string) (Detail, error) {
	d := Detail{Hash: hash}
	row, err := c.detailRow(ctx, hash,
		filesDetailCall, peersDetailCall, trackersDetailCall,
		"d.down.total=", "d.up.total=", "d.chunk_size=", "d.size_chunks=", "d.completed_chunks=", "d.directory=", "d.bitfield=",
	)
	if err != nil {
		return d, err
	}
	d.Files = mapFiles(nestedRows(row, 1))
	d.Peers = mapPeers(nestedRows(row, 2))
	d.Trackers = mapTrackers(nestedRows(row, 3))
	gi := func(i int) int64 {
		if i < len(row) {
			return ival(row[i])
		}
		return 0
	}
	d.Transfer = Transfer{
		DownloadedBytes: gi(4),
		UploadedBytes:   gi(5),
		ChunkSize:       gi(6),
		ChunkCount:      gi(7),
		ChunksDone:      gi(8),
		Directory: sval(func() xmlrpc.Value {
			if len(row) > 9 {
				return row[9]
			}
			return xmlrpc.Value{}
		}()),
	}
	if len(row) > 10 {
		d.BitfieldHex = sval(row[10])
	}
	return d, nil
}

// ---- actions ----

// Simple torrent-scoped actions. Each returns an error unless the daemon
// accepted the call.
func (c *Client) call(ctx context.Context, method string, params ...string) error {
	vals := make([]xmlrpc.Value, len(params))
	for i, p := range params {
		vals[i] = str(p)
	}
	_, err := c.scgi.Call(ctx, method, vals)
	return err
}

// Execute runs an operator-supplied XML-RPC method with string parameters.
// It is intentionally exposed only through the authenticated settings escape
// hatch; normal UI controls use typed methods instead.
func (c *Client) Execute(ctx context.Context, method string, params ...string) error {
	return c.call(ctx, method, params...)
}

// Start opens the download, marks it started, then resumes its transfer
// engine. d.start alone only changes rTorrent's view/state; without
// d.resume the torrent remains open but never connects to peers.
func (c *Client) Start(ctx context.Context, hash string) error {
	for _, method := range []string{"d.open", "d.start", "d.resume"} {
		if err := c.call(ctx, method, hash); err != nil {
			return fmt.Errorf("%s for %s: %w", method, hash, err)
		}
	}
	return nil
}

// ForceStart opens and starts a torrent without waiting for rTorrent's queue.
// The normal start path already uses the required d.open + d.start sequence;
// keeping a named method makes the transport contract explicit to callers.
func (c *Client) ForceStart(ctx context.Context, hash string) error { return c.Start(ctx, hash) }
func (c *Client) Pause(ctx context.Context, hash string) error      { return c.call(ctx, "d.pause", hash) }
func (c *Client) Stop(ctx context.Context, hash string) error       { return c.call(ctx, "d.stop", hash) }
func (c *Client) Recheck(ctx context.Context, hash string) error {
	return c.call(ctx, "d.check_hash", hash)
}
func (c *Client) Erase(ctx context.Context, hash string) error { return c.call(ctx, "d.erase", hash) }

// Announce forces a tracker reannounce.
func (c *Client) Announce(ctx context.Context, hash string) error {
	return c.call(ctx, "d.tracker_announce", hash)
}

// SetLabel sets d.custom1.
func (c *Client) SetLabel(ctx context.Context, hash, label string) error {
	return c.call(ctx, "d.custom1.set", hash, label)
}

// SetThrottleName assigns a torrent to a named throttle channel via
// d.throttle_name.set. An empty name clears the assignment (the daemon falls
// back to the global limits). Verified against rTorrent 0.16.18: the call
// faults on an active download, so callers must stop the torrent first (see
// the API layer's stop/set/start cycle).
func (c *Client) SetThrottleName(ctx context.Context, hash, name string) error {
	return c.call(ctx, "d.throttle_name.set", hash, name)
}

// SetThrottleUp creates or updates a named upload channel limit.
// Verified against rTorrent 0.16.18: throttle.up takes an empty target, the
// channel name, and the cap in KiB/s as a string (0 = unlimited).
func (c *Client) SetThrottleUp(ctx context.Context, name string, kb int64) error {
	return c.call(ctx, "throttle.up", "", name, strconv.FormatInt(kb, 10))
}

// SetThrottleDown creates or updates a named download channel limit (same
// wire format as SetThrottleUp).
func (c *Client) SetThrottleDown(ctx context.Context, name string, kb int64) error {
	return c.call(ctx, "throttle.down", "", name, strconv.FormatInt(kb, 10))
}

// throttleInfo queries one throttle.*.max/rate value in bytes/s (-1 when the
// channel is undefined; 0 when throttling is inactive or unlimited).
func (c *Client) throttleInfo(ctx context.Context, method, name string) (int64, error) {
	res, err := c.scgi.Call(ctx, method, []xmlrpc.Value{str(""), str(name)})
	if err != nil {
		return 0, err
	}
	if len(res) != 1 {
		return 0, fmt.Errorf("%s returned %d values, want 1", method, len(res))
	}
	return res[0].Int, nil
}

// ThrottleUpMax returns a channel's upload cap in bytes/s.
func (c *Client) ThrottleUpMax(ctx context.Context, name string) (int64, error) {
	return c.throttleInfo(ctx, "throttle.up.max", name)
}

// ThrottleDownMax returns a channel's download cap in bytes/s.
func (c *Client) ThrottleDownMax(ctx context.Context, name string) (int64, error) {
	return c.throttleInfo(ctx, "throttle.down.max", name)
}

// ThrottleUpRate returns a channel's current upload throughput in bytes/s.
func (c *Client) ThrottleUpRate(ctx context.Context, name string) (int64, error) {
	return c.throttleInfo(ctx, "throttle.up.rate", name)
}

// ThrottleDownRate returns a channel's current download throughput in bytes/s.
func (c *Client) ThrottleDownRate(ctx context.Context, name string) (int64, error) {
	return c.throttleInfo(ctx, "throttle.down.rate", name)
}

// SetDirectory sets the torrent's download directory (move-data flow uses
// this after the files are transferred).
func (c *Client) SetDirectory(ctx context.Context, hash, path string) error {
	return c.call(ctx, "d.directory.set", hash, path)
}

// SetDefaultDirectory changes rTorrent's runtime default destination.
func (c *Client) SetDefaultDirectory(ctx context.Context, path string) error {
	return c.call(ctx, "directory.default.set", path)
}

// LoadIPFilter loads a local blocklist file into rTorrent's ipv4_filter
// table as "unwanted" (PAR-5.6). The path is read by the daemon, so it must
// be visible to rTorrent, not just to Blackbird. Wire form per the upstream
// manual: ipv4_filter.load = <path>, <unwanted>.
func (c *Client) LoadIPFilter(ctx context.Context, path string) error {
	return c.call(ctx, "ipv4_filter.load", path, "unwanted")
}

// SetFilePriority sets one file's priority (0 skip, 1 normal, 2 high) using
// the "hash:fINDEX" sub-target.
func (c *Client) SetFilePriority(ctx context.Context, hash string, fileIndex, priority int) error {
	return c.call(ctx, "f.priority.set", fmt.Sprintf("%s:f%d", hash, fileIndex), strconv.Itoa(priority))
}

// SetPriority sets the torrent priority (0 off, 1 low, 2 normal, 3 high).
func (c *Client) SetPriority(ctx context.Context, hash string, priority int) error {
	return c.call(ctx, "d.priority.set", hash, strconv.Itoa(priority))
}

// SetSuperseeding controls rTorrent's superseed (connection seed) mode.
func (c *Client) SetSuperseeding(ctx context.Context, hash string, enabled bool) error {
	return c.call(ctx, "d.connection_seed.set", hash, boolParam(enabled))
}

// SetSequential controls live sequential download mode.
func (c *Client) SetSequential(ctx context.Context, hash string, enabled bool) error {
	return c.call(ctx, "d.sequential.set", hash, boolParam(enabled))
}

// SaveSession writes this torrent's current resume/session state to disk.
func (c *Client) SaveSession(ctx context.Context, hash string) error {
	return c.call(ctx, "d.save_full_session", hash)
}

// SetCustom writes one of rTorrent's auxiliary custom fields. custom1 is
// deliberately excluded here because SetLabel owns the user-facing label.
func (c *Client) SetCustom(ctx context.Context, hash, field, value string) error {
	switch field {
	case "custom2", "custom3", "custom4", "custom5":
	default:
		return fmt.Errorf("unsupported custom field %q", field)
	}
	return c.call(ctx, "d."+field+".set", hash, value)
}

func boolParam(enabled bool) string {
	if enabled {
		return "1"
	}
	return "0"
}

// AddTracker inserts a tracker (group 0) for a torrent.
func (c *Client) AddTracker(ctx context.Context, hash, url string, group int) error {
	return c.call(ctx, "d.tracker.insert", hash, strconv.Itoa(group), url)
}

// RemoveTracker removes the tracker entry entirely (rather than disabling it).
func (c *Client) RemoveTracker(ctx context.Context, hash string, trackerIndex int) error {
	return c.call(ctx, "d.tracker.remove", hash, strconv.Itoa(trackerIndex))
}

// SetTrackerEnabled enables/disables a tracker by its index in the tracker
// list ("hash:tINDEX" sub-target).
func (c *Client) SetTrackerEnabled(ctx context.Context, hash string, trackerIndex int, enabled bool) error {
	val := "0"
	if enabled {
		val = "1"
	}
	return c.call(ctx, "t.is_enabled.set", fmt.Sprintf("%s:t%d", hash, trackerIndex), val)
}

// peerTarget builds the "hash:pPEERID" sub-target used by every p.* command.
func peerTarget(hash, peerID string) string {
	return fmt.Sprintf("%s:p%s", hash, peerID)
}

// BanPeer permanently bans a peer ("hash:pPEERID"). rTorrent cannot unban a
// banned peer; only a daemon restart clears the ban list.
func (c *Client) BanPeer(ctx context.Context, hash, peerID string) error {
	return c.call(ctx, "p.banned.set", peerTarget(hash, peerID), "1")
}

// SetPeerSnubbed controls whether rTorrent stops uploading to a peer. The
// daemon exposes p.snubbed.set (p.is_snubbed is a read alias); ruTorrent's
// "snub" is the same command with 1, "unsnub" with 0.
func (c *Client) SetPeerSnubbed(ctx context.Context, hash, peerID string, snubbed bool) error {
	return c.call(ctx, "p.snubbed.set", peerTarget(hash, peerID), boolParam(snubbed))
}

// DisconnectPeer drops the connection to a peer immediately. The peer may
// reconnect later; banning is required to keep it away.
func (c *Client) DisconnectPeer(ctx context.Context, hash, peerID string) error {
	return c.call(ctx, "p.disconnect", peerTarget(hash, peerID))
}

// AddOptions control the load.* variant used when adding torrents.
type AddOptions struct {
	Start bool // use load.start/load.raw_start so the torrent starts immediately
	// ExtraCommands are appended to the load call and executed on the loaded
	// torrent, e.g. "d.directory.set=/mnt/data", "d.custom1.set=iso".
	// A daemon that rejects a command faults the whole load; callers report
	// per-item failures.
	ExtraCommands []string
}

// AddMagnet adds a magnet URI (or http(s) .torrent URL).
func (c *Client) AddMagnet(ctx context.Context, uri string, opts AddOptions) error {
	method := "load.normal"
	if opts.Start {
		method = "load.start"
	}
	params := []xmlrpc.Value{str(""), str(uri)}
	for _, cmd := range opts.ExtraCommands {
		params = append(params, str(cmd))
	}
	_, err := c.scgi.Call(ctx, method, params)
	return err
}

// AddTorrentFile adds a .torrent from raw bytes via load.raw_start/load.raw.
func (c *Client) AddTorrentFile(ctx context.Context, data []byte, opts AddOptions) error {
	method := "load.raw"
	if opts.Start {
		method = "load.raw_start"
	}
	params := []xmlrpc.Value{str(""), xmlrpc.Base64Value(data)}
	for _, cmd := range opts.ExtraCommands {
		params = append(params, str(cmd))
	}
	_, err := c.scgi.Call(ctx, method, params)
	return err
}
