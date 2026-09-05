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

// methodRow is one row of the scalar tuning table (POL-8.8): the single
// source of truth mapping a tuning key to its rtorrent setter and getter
// methods. Entries() and Keys()/GetterFor() all derive from it so setters,
// getters, and the key list can never drift apart.
//
// Wire-form note: the setter/getter strings below are the pre-0.16 forms.
// Migrating them to target-first `.set_kb` / bare getters is a separate
// verified story (see PAR-4.3 Remaining); this table only unifies them.
var methodTable = []struct {
	Key    string
	Setter string
	Getter string
}{
	{"network.port_range", "network.port_range.set", "network.port_range="},
	{"network.port_random", "network.port_random.set", "network.port_random="},
	{"protocol.encryption", "protocol.encryption.set", "protocol.encryption="},
	{"dht.mode", "dht.mode.set", "dht.mode="},
	{"dht.port", "dht.port.set", "dht.port="},
	{"trackers.use_udp", "trackers.use_udp.set", "trackers.use_udp="},
	{"protocol.pex", "protocol.pex.set", "protocol.pex="},
	{"network.local_address", "network.local_address.set", "network.local_address="},
	{"network.bind_address", "network.bind_address.set", "network.bind_address="},
	{"network.http.max_open", "network.http.max_open.set", "network.http.max_open="},
	{"network.max_open_sockets", "network.max_open_sockets.set", "network.max_open_sockets="},
	{"network.max_open_files", "network.max_open_files.set", "network.max_open_files="},
	{"throttle.min_peers.normal", "throttle.min_peers.normal.set", "throttle.min_peers.normal="},
	{"throttle.max_peers.normal", "throttle.max_peers.normal.set", "throttle.max_peers.normal="},
	{"throttle.min_peers.seeded", "throttle.min_peers.seeded.set", "throttle.min_peers.seeded="},
	{"throttle.max_peers.seeded", "throttle.max_peers.seeded.set", "throttle.max_peers.seeded="},
	{"throttle.max_uploads", "throttle.max_uploads.set", "throttle.max_uploads="},
	{"throttle.max_uploads.global", "throttle.max_uploads.global.set", "throttle.max_uploads.global="},
	{"throttle.global_down.max_rate", "throttle.global_down.max_rate.set", "throttle.global_down.max_rate="},
	{"throttle.global_up.max_rate", "throttle.global_up.max_rate.set", "throttle.global_up.max_rate="},
	{"throttle.max_downloads.global", "throttle.max_downloads.global.set", "throttle.max_downloads.global="},
}

// setterFor returns the setter method for a table key. It returns false when
// the key is unknown, which can only happen on a programming error: every
// Entries() call site passes a table key (pinned by TestMethodTableCoversEntries).
func setterFor(key string) (string, bool) {
	for _, row := range methodTable {
		if row.Key == key {
			return row.Setter, true
		}
	}
	return "", false
}

// Keys returns the tuning keys in stable table order. GET /api/settings
// iterates this (not a map) so the live-value multicall is deterministic.
func Keys() []string {
	out := make([]string, 0, len(methodTable))
	for _, row := range methodTable {
		out = append(out, row.Key)
	}
	return out
}

// GetterFor returns the getter method for a table key.
func GetterFor(key string) (string, bool) {
	for _, row := range methodTable {
		if row.Key == key {
			return row.Getter, true
		}
	}
	return "", false
}

// Entries flattens a Tuning struct into the list of setters to apply, in a
// stable order. Nil (absent) fields produce no entry — the daemon is left
// untouched for those keys. Setter methods resolve through methodTable, the
// single source of truth shared with Keys/GetterFor.
func Entries(t config.Tuning) []Entry {
	var e []Entry
	add := func(key string, v xmlrpc.Value) {
		setter, ok := setterFor(key)
		if !ok {
			// Unreachable with the current call sites (see
			// TestMethodTableCoversEntries); failing open would send a
			// malformed method to the daemon, so drop loudly instead.
			panic("tuning: unknown key " + key)
		}
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
		add("network.port_range", str(*t.PortRange))
	}
	if t.PortRandom != nil {
		add("network.port_random", boolI(*t.PortRandom))
	}
	if t.Encryption != nil {
		add("protocol.encryption", str(*t.Encryption))
	}
	if t.DHTMode != nil {
		add("dht.mode", str(*t.DHTMode))
	}
	if t.DHTPort != nil {
		add("dht.port", intV(int64(*t.DHTPort)))
	}
	if t.UseUDP != nil {
		add("trackers.use_udp", boolI(*t.UseUDP))
	}
	if t.PEX != nil {
		add("protocol.pex", boolI(*t.PEX))
	}
	if t.LocalAddress != nil && *t.LocalAddress != "" {
		add("network.local_address", str(*t.LocalAddress))
	}
	if t.BindAddress != nil && *t.BindAddress != "" {
		add("network.bind_address", str(*t.BindAddress))
	}
	if t.HTTPMaxOpen != nil {
		add("network.http.max_open", intV(int64(*t.HTTPMaxOpen)))
	}
	if t.MaxOpenSockets != nil {
		add("network.max_open_sockets", intV(int64(*t.MaxOpenSockets)))
	}
	if t.MaxOpenFiles != nil {
		add("network.max_open_files", intV(int64(*t.MaxOpenFiles)))
	}
	if t.MinPeersNormal != nil {
		add("throttle.min_peers.normal", intV(int64(*t.MinPeersNormal)))
	}
	if t.MaxPeersNormal != nil {
		add("throttle.max_peers.normal", intV(int64(*t.MaxPeersNormal)))
	}
	if t.MinPeersSeeded != nil {
		add("throttle.min_peers.seeded", intV(int64(*t.MinPeersSeeded)))
	}
	if t.MaxPeersSeeded != nil {
		add("throttle.max_peers.seeded", intV(int64(*t.MaxPeersSeeded)))
	}
	if t.MaxUploads != nil {
		add("throttle.max_uploads", intV(int64(*t.MaxUploads)))
	}
	if t.MaxUploadsGlobal != nil {
		add("throttle.max_uploads.global", intV(int64(*t.MaxUploadsGlobal)))
	}
	if t.GlobalDownRateKB != nil {
		add("throttle.global_down.max_rate", intV(*t.GlobalDownRateKB))
	}
	if t.GlobalUpRateKB != nil {
		add("throttle.global_up.max_rate", intV(*t.GlobalUpRateKB))
	}
	if t.MaxDownloadsGlobal != nil {
		add("throttle.max_downloads.global", intV(int64(*t.MaxDownloadsGlobal)))
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
