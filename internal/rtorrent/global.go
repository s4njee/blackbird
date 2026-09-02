package rtorrent

import (
	"context"
	"errors"
	"fmt"

	"blackbird/internal/scgi/xmlrpc"
)

// Request is one entry of a system.multicall batch.
type Request struct {
	Method string
	Params []xmlrpc.Value
}

// Result is the per-request outcome of a system.multicall batch: either
// response params or a per-call fault. Individual failures never fail the
// whole batch.
type Result struct {
	Values []xmlrpc.Value
	Err    error
}

// MultiCall batches independent XML-RPC calls into one round trip via
// system.multicall. Calls that fault are reported per-entry.
func (c *Client) MultiCall(ctx context.Context, reqs []Request) ([]Result, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	entries := make([]xmlrpc.Value, len(reqs))
	for i, r := range reqs {
		params := xmlrpc.Value{Type: "array", Array: append([]xmlrpc.Value(nil), r.Params...)}
		entries[i] = xmlrpc.Value{Type: "struct", Struct: []xmlrpc.Member{
			{Name: "methodName", Value: xmlrpc.Value{Type: "string", Str: r.Method}},
			{Name: "params", Value: params},
		}}
	}
	res, err := c.scgi.Call(ctx, "system.multicall", []xmlrpc.Value{
		{Type: "array", Array: entries},
	})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, errors.New("rtorrent: empty system.multicall response")
	}
	outer := res[0].Array
	out := make([]Result, 0, len(reqs))
	for i := range reqs {
		if i >= len(outer) {
			out = append(out, Result{Err: errors.New("rtorrent: truncated multicall response")})
			continue
		}
		entry := outer[i]
		// Each entry is either an array wrapping the response values, or a
		// struct with faultCode/faultString members.
		if entry.Type == "struct" {
			f := &Fault{}
			if code, ok := entry.Member("faultCode"); ok {
				f.Code = int(code.Int)
			}
			if str, ok := entry.Member("faultString"); ok {
				f.String = str.Str
			}
			out = append(out, Result{Err: f})
			continue
		}
		var vals []xmlrpc.Value
		if len(entry.Array) > 0 {
			vals = entry.Array
		}
		out = append(out, Result{Values: vals})
	}
	return out, nil
}

// Fault mirrors xmlrpc.Fault so per-entry multicall errors keep the type.
type Fault = xmlrpc.Fault

// GlobalSettingsKeys maps the Settings-surface tuning keys to their
// rtorrent getter methods (the setter is getter + ".set"). This is the
// contract Epic 8's UI reads and writes via GetGlobal/SetGlobal.
var GlobalSettingsKeys = []string{
	// Connection & network
	"network.port_range",
	"network.port_random",
	"protocol.encryption",
	"dht.mode",
	"dht.port",
	"trackers.use_udp",
	"protocol.pex",
	"network.local_address",
	"network.bind_address",
	"network.http.max_open",
	"network.max_open_sockets",
	"network.max_open_files",
	// Peer limits
	"throttle.min_peers.normal",
	"throttle.max_peers.normal",
	"throttle.min_peers.seeded",
	"throttle.max_peers.seeded",
	"throttle.max_uploads",
	"throttle.max_uploads.global",
	// Bandwidth (KB/s, 0 = unlimited)
	"throttle.global_down.max_rate",
	"throttle.global_up.max_rate",
	// Queue
	"throttle.max_downloads.global",
	"throttle.max_uploads.global.queue", // deprecated alias; see throttle.max_uploads.global
	// Directories
	"directory.default",
}

// GetGlobal reads one global setting by getter method name (e.g.
// "throttle.global_down.max_rate").
func (c *Client) GetGlobal(ctx context.Context, getter string) (xmlrpc.Value, error) {
	res, err := c.scgi.Call(ctx, getter, nil)
	if err != nil {
		return xmlrpc.Value{}, err
	}
	if len(res) == 0 {
		return xmlrpc.Value{}, fmt.Errorf("rtorrent: empty response for %s", getter)
	}
	return res[0], nil
}

// GetGlobalString reads a global setting coerced to a string.
func (c *Client) GetGlobalString(ctx context.Context, getter string) (string, error) {
	v, err := c.GetGlobal(ctx, getter)
	if err != nil {
		return "", err
	}
	return sval(v), nil
}

// GetGlobalInt reads a global setting coerced to an int64.
func (c *Client) GetGlobalInt(ctx context.Context, getter string) (int64, error) {
	v, err := c.GetGlobal(ctx, getter)
	if err != nil {
		return 0, err
	}
	return ival(v), nil
}

// SetGlobal applies one global setting by its *.set method name, e.g.
// SetGlobal("throttle.global_down.max_rate.set", 20480). Values may be
// strings, ints or bools; rtorrent coerces.
func (c *Client) SetGlobal(ctx context.Context, setter string, value xmlrpc.Value) error {
	_, err := c.scgi.Call(ctx, setter, []xmlrpc.Value{value})
	return err
}

// SetGlobalInt is SetGlobal with an integer payload.
func (c *Client) SetGlobalInt(ctx context.Context, setter string, n int64) error {
	return c.SetGlobal(ctx, setter, xmlrpc.Value{Type: "int", Int: n})
}

// SetGlobalString is SetGlobal with a string payload.
func (c *Client) SetGlobalString(ctx context.Context, setter, s string) error {
	return c.SetGlobal(ctx, setter, xmlrpc.Value{Type: "string", Str: s})
}

// SetGlobalBool is SetGlobal with a boolean payload (rtorrent accepts 0/1).
func (c *Client) SetGlobalBool(ctx context.Context, setter string, b bool) error {
	return c.SetGlobal(ctx, setter, xmlrpc.Value{Type: "int", Int: boolToInt(b)})
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Versions returns (rtorrent, libtorrent) version strings for the status
// bar.
func (c *Client) Versions(ctx context.Context) (client, library string, err error) {
	results, err := c.MultiCall(ctx, []Request{
		{Method: "system.client_version"},
		{Method: "system.library_version"},
	})
	if err != nil {
		return "", "", err
	}
	for i, name := range []string{"client", "library"} {
		if i >= len(results) {
			break
		}
		r := results[i]
		if r.Err != nil || len(r.Values) == 0 {
			continue
		}
		if name == "client" {
			client = sval(r.Values[0])
		} else {
			library = sval(r.Values[0])
		}
	}
	if client == "" {
		return "", "", errors.New("rtorrent: version detection returned nothing")
	}
	return client, library, nil
}

// GlobalStats gathers the status-bar / stat-card globals in one
// system.multicall.
func (c *Client) GlobalStats(ctx context.Context) (GlobalStats, error) {
	var g GlobalStats
	results, err := c.MultiCall(ctx, []Request{
		{Method: "throttle.global_down.rate="},
		{Method: "throttle.global_up.rate="},
		{Method: "throttle.global_down.total="},
		{Method: "throttle.global_up.total="},
		{Method: "system.client_version="},
		{Method: "system.library_version="},
		{Method: "network.port="},
		{Method: "dht.statistics="},
	})
	if err != nil {
		return g, err
	}
	iv := func(i int) int64 {
		if i < len(results) && results[i].Err == nil && len(results[i].Values) > 0 {
			return ival(results[i].Values[0])
		}
		return 0
	}
	sv := func(i int) string {
		if i < len(results) && results[i].Err == nil && len(results[i].Values) > 0 {
			return sval(results[i].Values[0])
		}
		return ""
	}
	g.DownRate = iv(0)
	g.UpRate = iv(1)
	g.SessionDownTotal = iv(2)
	g.SessionUpTotal = iv(3)
	g.Version = sv(4)
	g.LibraryVersion = sv(5)
	g.Port = int(iv(6))

	if len(results) > 7 && results[7].Err == nil && len(results[7].Values) > 0 {
		g.DHTNodes = dhtNodes(results[7].Values[0])
	}
	if g.SessionDownTotal > 0 {
		g.SessionRatio = float64(g.SessionUpTotal) / float64(g.SessionDownTotal)
	}
	return g, nil
}

// dhtNodes digs the node count out of dht.statistics (a struct: {"dht":
// {"nodes": N, ...}}), tolerating shape changes across daemon versions.
func dhtNodes(v xmlrpc.Value) int {
	if dht, ok := v.Member("dht"); ok {
		if nodes, ok := dht.Member("nodes"); ok {
			return int(ival(nodes))
		}
	}
	if nodes, ok := v.Member("nodes"); ok {
		return int(ival(nodes))
	}
	return 0
}
