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

// SetGlobal applies one global setting by its *.set method name, e.g.
// SetGlobal("throttle.global_down.max_rate.set", 20480). Values may be
// strings, ints or bools; rtorrent coerces.
func (c *Client) SetGlobal(ctx context.Context, setter string, value xmlrpc.Value) error {
	_, err := c.scgi.Call(ctx, setter, []xmlrpc.Value{value})
	return err
}

// DisableAllTrackers disables every tracker currently attached to every loaded
// torrent. rTorrent exposes this as a command rather than a persistent
// boolean, so callers should invoke it after each successful connection.
func (c *Client) DisableAllTrackers(ctx context.Context) error {
	_, err := c.scgi.Call(ctx, "trackers.disable", []xmlrpc.Value{str(""), str("0")})
	return err
}

// TrackerTarget names one tracker by the torrent that owns it and its index
// in that torrent's tracker list — the pair that forms the "hash:tN"
// sub-target every t.* command takes.
type TrackerTarget struct {
	Hash  string
	Index int
}

// TrackerTargets enumerates every tracker attached to every loaded torrent
// in one round trip, ordered by torrent. rTorrent has no global tracker
// list, so this reads each torrent's tracker count and expands it into
// per-tracker targets; callers that must not announce all at once (see
// internal/trackers) work through the result in batches.
func (c *Client) TrackerTargets(ctx context.Context) ([]TrackerTarget, error) {
	res, err := c.scgi.Call(ctx, "d.multicall2", []xmlrpc.Value{
		str(""), str("main"), str("d.hash="), str("d.tracker_size="),
	})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, nil
	}
	var out []TrackerTarget
	for _, row := range aval(res[0]) {
		cols := aval(row)
		if len(cols) < 2 {
			continue
		}
		hash := sval(cols[0])
		if hash == "" {
			continue
		}
		for i := 0; i < int(ival(cols[1])); i++ {
			out = append(out, TrackerTarget{Hash: hash, Index: i})
		}
	}
	return out, nil
}

// SetTrackersEnabled flips a batch of trackers in a single system.multicall.
// Per-tracker faults are counted rather than returned: a torrent erased
// between enumeration and this call faults only its own entries, and failing
// the whole batch for that would stall a ramp. The error is non-nil only
// when the round trip itself failed.
func (c *Client) SetTrackersEnabled(ctx context.Context, targets []TrackerTarget, enabled bool) (failed int, err error) {
	if len(targets) == 0 {
		return 0, nil
	}
	val := "0"
	if enabled {
		val = "1"
	}
	reqs := make([]Request, len(targets))
	for i, t := range targets {
		reqs[i] = Request{
			Method: "t.is_enabled.set",
			Params: []xmlrpc.Value{str(fmt.Sprintf("%s:t%d", t.Hash, t.Index)), str(val)},
		}
	}
	results, err := c.MultiCall(ctx, reqs)
	if err != nil {
		return 0, err
	}
	for _, r := range results {
		if r.Err != nil {
			failed++
		}
	}
	return failed, nil
}

// SetGlobalRateKB sets a global throttle cap in KiB/s via the .set_kb
// variant (e.g. "throttle.global_down.max_rate.set_kb"). Verified against
// rTorrent 0.16.18: like all CMD2_ANY commands it takes an explicit empty
// target first, and unlike plain .set (bytes with >>10 rounding) it carries
// exact KiB/s values.
func (c *Client) SetGlobalRateKB(ctx context.Context, setter string, kb int64) error {
	_, err := c.scgi.Call(ctx, setter, []xmlrpc.Value{str(""), {Type: "int", Int: kb}})
	return err
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

// globalMethods are the system.multicall getters backing GlobalStats, in
// order. GlobalStats and ListAndGlobals share them so the two paths can
// never disagree on what "global" means.
var globalMethods = []string{
	"throttle.global_down.rate",
	"throttle.global_up.rate",
	"throttle.global_down.total",
	"throttle.global_up.total",
	"system.client_version",
	"system.library_version",
	"network.port",
	"dht.statistics",
}

// GlobalStats gathers the status-bar / stat-card globals in one
// system.multicall. Getter names are bare (no `=` suffix): the suffixed form
// is undefined on rTorrent 0.16 (verified live), while bare names work on
// both old and new daemons.
func (c *Client) GlobalStats(ctx context.Context) (GlobalStats, error) {
	reqs := make([]Request, 0, len(globalMethods))
	for _, m := range globalMethods {
		reqs = append(reqs, Request{Method: m})
	}
	results, err := c.MultiCall(ctx, reqs)
	if err != nil {
		return GlobalStats{}, err
	}
	return parseGlobalResults(results), nil
}

// ListAndGlobals fetches the torrent list and the global stats in one
// system.multicall (PERF-6.3): the first entry nests the d.multicall2 list
// poll, followed by the global getters. One SCGI round trip per poll cycle
// instead of two.
func (c *Client) ListAndGlobals(ctx context.Context) ([]Torrent, GlobalStats, error) {
	params := []xmlrpc.Value{str(""), str("main")}
	for _, cmd := range listCommands {
		params = append(params, str(cmd))
	}
	reqs := make([]Request, 0, len(globalMethods)+1)
	reqs = append(reqs, Request{Method: "d.multicall2", Params: params})
	for _, m := range globalMethods {
		reqs = append(reqs, Request{Method: m})
	}
	results, err := c.MultiCall(ctx, reqs)
	if err != nil {
		return nil, GlobalStats{}, err
	}
	if len(results) != len(reqs) {
		return nil, GlobalStats{}, fmt.Errorf("rtorrent: truncated multicall response (%d of %d)", len(results), len(reqs))
	}
	if results[0].Err != nil {
		return nil, GlobalStats{}, results[0].Err
	}
	if len(results[0].Values) == 0 {
		return nil, GlobalStats{}, errors.New("rtorrent: empty d.multicall2 response")
	}
	rows := aval(results[0].Values[0])
	torrents := make([]Torrent, 0, len(rows))
	for _, row := range rows {
		torrents = append(torrents, mapTorrent(aval(row)))
	}
	return torrents, parseGlobalResults(results[1:]), nil
}

// parseGlobalResults normalizes one global-getter result slice (in
// globalMethods order) into GlobalStats. Per-entry faults read as zero,
// matching the old GlobalStats behavior.
func parseGlobalResults(results []Result) GlobalStats {
	var g GlobalStats
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
	return g
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
