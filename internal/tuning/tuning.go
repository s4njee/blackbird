// Package tuning maps the YAML tuning: block onto rtorrent's *.set XML-RPC
// methods. Keys absent from YAML are never sent; per-key failures are
// reported, never fatal.
package tuning

import (
	"bytes"
	"context"
	"fmt"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
	"blackbird/internal/scgi/xmlrpc"
)

// Entry is one rtorrent key to apply: the XML-RPC setter method plus the
// value to pass.
type Entry struct {
	Key    string // rtorrent key, for reporting and the Settings UI hints
	Setter string // XML-RPC method, e.g. "throttle.max_uploads.set"
	Value  xmlrpc.Value
}

// Entries flattens a Tuning struct into the list of setters to apply, in a
// stable order. Nil (absent) fields produce no entry — the daemon is left
// untouched for those keys.
func Entries(t config.Tuning) []Entry {
	var e []Entry
	add := func(key, setter string, v xmlrpc.Value) {
		e = append(e, Entry{Key: key, Setter: setter, Value: v})
	}
	str := func(s string) xmlrpc.Value { return xmlrpc.Value{Type: "string", Str: s} }
	intV := func(n int64) xmlrpc.Value { return xmlrpc.Value{Type: "int", Int: n} }
	boolI := func(b bool) xmlrpc.Value {
		if b {
			return intV(1)
		}
		return intV(0)
	}

	if t.PortRange != nil {
		add("network.port_range", "network.port_range.set", str(*t.PortRange))
	}
	if t.PortRandom != nil {
		add("network.port_random", "network.port_random.set", boolI(*t.PortRandom))
	}
	if t.Encryption != nil {
		add("protocol.encryption", "protocol.encryption.set", str(*t.Encryption))
	}
	if t.DHTMode != nil {
		add("dht.mode", "dht.mode.set", str(*t.DHTMode))
	}
	if t.DHTPort != nil {
		add("dht.port", "dht.port.set", intV(int64(*t.DHTPort)))
	}
	if t.UseUDP != nil {
		add("trackers.use_udp", "trackers.use_udp.set", boolI(*t.UseUDP))
	}
	if t.PEX != nil {
		add("protocol.pex", "protocol.pex.set", boolI(*t.PEX))
	}
	if t.LocalAddress != nil && *t.LocalAddress != "" {
		add("network.local_address", "network.local_address.set", str(*t.LocalAddress))
	}
	if t.BindAddress != nil && *t.BindAddress != "" {
		add("network.bind_address", "network.bind_address.set", str(*t.BindAddress))
	}
	if t.HTTPMaxOpen != nil {
		add("network.http.max_open", "network.http.max_open.set", intV(int64(*t.HTTPMaxOpen)))
	}
	if t.MaxOpenSockets != nil {
		add("network.max_open_sockets", "network.max_open_sockets.set", intV(int64(*t.MaxOpenSockets)))
	}
	if t.MaxOpenFiles != nil {
		add("network.max_open_files", "network.max_open_files.set", intV(int64(*t.MaxOpenFiles)))
	}
	if t.MinPeersNormal != nil {
		add("throttle.min_peers.normal", "throttle.min_peers.normal.set", intV(int64(*t.MinPeersNormal)))
	}
	if t.MaxPeersNormal != nil {
		add("throttle.max_peers.normal", "throttle.max_peers.normal.set", intV(int64(*t.MaxPeersNormal)))
	}
	if t.MinPeersSeeded != nil {
		add("throttle.min_peers.seeded", "throttle.min_peers.seeded.set", intV(int64(*t.MinPeersSeeded)))
	}
	if t.MaxPeersSeeded != nil {
		add("throttle.max_peers.seeded", "throttle.max_peers.seeded.set", intV(int64(*t.MaxPeersSeeded)))
	}
	if t.MaxUploads != nil {
		add("throttle.max_uploads", "throttle.max_uploads.set", intV(int64(*t.MaxUploads)))
	}
	if t.MaxUploadsGlobal != nil {
		add("throttle.max_uploads.global", "throttle.max_uploads.global.set", intV(int64(*t.MaxUploadsGlobal)))
	}
	if t.GlobalDownRateKB != nil {
		add("throttle.global_down.max_rate", "throttle.global_down.max_rate.set", intV(*t.GlobalDownRateKB))
	}
	if t.GlobalUpRateKB != nil {
		add("throttle.global_up.max_rate", "throttle.global_up.max_rate.set", intV(*t.GlobalUpRateKB))
	}
	if t.MaxDownloadsGlobal != nil {
		add("throttle.max_downloads.global", "throttle.max_downloads.global.set", intV(int64(*t.MaxDownloadsGlobal)))
	}
	return e
}

// Result reports one key's application outcome.
type Result struct {
	Key string
	Err error
}

// Apply sends all entries in one system.multicall batch and reports per-key
// outcomes. Failures are returned, never fatal.
func Apply(ctx context.Context, client *rtorrent.Client, entries []Entry) []Result {
	if len(entries) == 0 {
		return nil
	}
	reqs := make([]rtorrent.Request, len(entries))
	for i, e := range entries {
		reqs[i] = rtorrent.Request{Method: e.Setter, Params: []xmlrpc.Value{e.Value}}
	}
	results, err := client.MultiCall(ctx, reqs)
	if err != nil {
		// The whole batch failed (e.g. connection lost mid-flight).
		out := make([]Result, len(entries))
		for i, e := range entries {
			out[i] = Result{Key: e.Key, Err: err}
		}
		return out
	}
	out := make([]Result, len(entries))
	for i := range entries {
		if i < len(results) {
			out[i] = Result{Key: entries[i].Key, Err: results[i].Err}
		} else {
			out[i] = Result{Key: entries[i].Key, Err: fmt.Errorf("no result returned")}
		}
	}
	return out
}

// ApplySequential applies each setting independently. The Settings UI uses
// this path so a fault from one daemon key never prevents subsequent changed
// keys from being attempted, and each failure can be shown inline.
func ApplySequential(ctx context.Context, client *rtorrent.Client, entries []Entry) []Result {
	out := make([]Result, 0, len(entries))
	for _, entry := range entries {
		out = append(out, Result{Key: entry.Key, Err: client.SetGlobal(ctx, entry.Setter, entry.Value)})
	}
	return out
}

// Diff returns the entries from next whose values differ from prev (or that
// are newly declared). Used by SIGHUP reload so only changed keys are
// re-applied.
func Diff(prev, next config.Tuning) []Entry {
	old := serialize(Entries(prev))
	var changed []Entry
	for _, e := range Entries(next) {
		if oldV, ok := old[e.Key]; !ok || oldV != serialize1(e) {
			changed = append(changed, e)
		}
	}
	return changed
}

// serialize maps key → encoded value for diffing.
func serialize(entries []Entry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Key] = serialize1(e)
	}
	return m
}

func serialize1(e Entry) string {
	var b bytes.Buffer
	xmlrpc.AppendValue(&b, e.Value)
	return b.String()
}

// GetterMethods maps each tuning key to its rtorrent getter method, used by
// GET /api/settings to show live daemon values alongside YAML values.
func GetterMethods() map[string]string {
	return map[string]string{
		"network.port_range":            "network.port_range=",
		"network.port_random":           "network.port_random=",
		"protocol.encryption":           "protocol.encryption=",
		"dht.mode":                      "dht.mode=",
		"dht.port":                      "dht.port=",
		"trackers.use_udp":              "trackers.use_udp=",
		"protocol.pex":                  "protocol.pex=",
		"network.local_address":         "network.local_address=",
		"network.bind_address":          "network.bind_address=",
		"network.http.max_open":         "network.http.max_open=",
		"network.max_open_sockets":      "network.max_open_sockets=",
		"network.max_open_files":        "network.max_open_files=",
		"throttle.min_peers.normal":     "throttle.min_peers.normal=",
		"throttle.max_peers.normal":     "throttle.max_peers.normal=",
		"throttle.min_peers.seeded":     "throttle.min_peers.seeded=",
		"throttle.max_peers.seeded":     "throttle.max_peers.seeded=",
		"throttle.max_uploads":          "throttle.max_uploads=",
		"throttle.max_uploads.global":   "throttle.max_uploads.global=",
		"throttle.global_down.max_rate": "throttle.global_down.max_rate=",
		"throttle.global_up.max_rate":   "throttle.global_up.max_rate=",
		"throttle.max_downloads.global": "throttle.max_downloads.global=",
	}
}
