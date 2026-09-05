package history

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
)

var urlPattern = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://|magnet:\?)[^\s"'<>]+`)
var credentialPattern = regexp.MustCompile(`(?i)(password|password_hash|passkey|token|secret|authorization|cookie|api[_-]?key)["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|\S+)`)
var ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`)
var ipv6Pattern = regexp.MustCompile(`(?i)\b[0-9a-f]*:[0-9a-f:]+:[0-9a-f:]+\b`)

func redactText(s string) string {
	s = urlPattern.ReplaceAllString(s, "[URL omitted]")
	s = credentialPattern.ReplaceAllString(s, "$1=[credential omitted]")
	s = ipv4Pattern.ReplaceAllString(s, "[IP omitted]")
	s = ipv6Pattern.ReplaceAllStringFunc(s, func(v string) string {
		if net.ParseIP(v) != nil {
			return "[IP omitted]"
		}
		return v
	})
	if len(s) > 4096 {
		s = s[:4096] + " [truncated]"
	}
	return s
}

func redactEvent(e Event) Event {
	e.Hash = redactText(e.Hash)
	e.Before, e.After = copyValues(e.Before), copyValues(e.After)
	e.Name, e.Message, e.Actor, e.Action = redactText(e.Name), redactText(e.Message), redactText(e.Actor), redactText(e.Action)
	for _, values := range []map[string]string{e.Before, e.After} {
		for k, v := range values {
			delete(values, k)
			values[redactText(k)] = redactText(v)
		}
	}
	return e
}

// Export omits all free-form strings that could contain arbitrary credentials.
// Local views keep redacted diagnostic text; portable bundles retain numeric
// evidence and machine identities, never torrent names, paths or messages.
func ExportRecording(in Recording) Recording {
	out := in
	out.Events = make([]Event, len(in.Events))
	for i, e := range in.Events {
		if e.Hash != "" && !regexpHash.MatchString(e.Hash) {
			e.Hash = "[nonstandard hash omitted]"
		}
		e.Name, e.Message = "", "[Free-form text omitted from export]"
		switch e.Actor {
		case "poller", "scheduler", "seeding", "automation", "recorder", "configuration":
		default:
			e.Actor = "operator"
		}
		if !safeAction(e.Action) {
			e.Action = "event"
		}
		e.Before, e.After = exportValues(e.Before), exportValues(e.After)
		out.Events[i] = e
	}
	out.Coverage = append(append([]string{}, out.Coverage...), "Export omits all free-form text, names, URLs, paths, and configuration string values; inspect the local timeline for redacted descriptions.")
	return out
}

func safeAction(v string) bool {
	switch v {
	case "start", "pause", "stop", "force_start", "stop_and_set_label", "recheck", "remove", "remove_with_data", "set_label", "priority", "file_priority", "set_throttle", "set_custom", "sequential", "superseed", "tracker_add", "tracker_remove", "tracker_enable", "reannounce", "rename", "checkpoint", "state_change", "rates", "connection", "startup", "dropped_input", "storage_failure", "configuration", "settings_save", "schedule_profile", "schedule_override", "schedule_override_clear", "schedule_override_expired", "completion_rule", "move", "move_to", "webhook", "add_tracker":
		return true
	}
	return false
}

func exportValues(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		// Keys are fixed field paths produced by our evidence extractors.
		if !regexpKey.MatchString(k) {
			continue
		}
		leaf := k[strings.LastIndex(k, ".")+1:]
		numeric := false
		switch leaf {
		case "downRate", "upRate", "completedBytes", "uploadedBytes", "ratio", "priority", "fileIndex", "globalDownKB", "globalUpKB", "downKB", "upKB", "down_kb", "up_kb", "min_ratio", "max_ratio", "min_upload_bytes", "max_seeding_time", "min_size", "max_size":
			numeric = true
		}
		if _, err := strconv.ParseFloat(v, 64); err == nil && numeric {
			out[k] = v
			continue
		}
		if leaf != "state" && leaf != "complete" && leaf != "open" && leaf != "enabled" && leaf != "connection" {
			out[k] = "[omitted]"
			continue
		}
		switch v {
		case "true", "false", "downloading", "seeding", "stopped", "queued", "checking", "error", "absent", "connected", "disconnected":
			out[k] = v
		default:
			out[k] = "[omitted]"
		}
	}
	return out
}

var regexpKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.\[\]]{0,100}$`)
var regexpHash = regexp.MustCompile(`^[a-fA-F0-9]{1,64}$`)

type sessionObservation struct {
	At        time.Time
	Connected bool
	Rows      []rtorrent.Torrent // immutable published poller snapshot, never modified
	Revision  string
}

type observedTorrent struct {
	Values     map[string]string
	LastSample time.Time
}

// Observe is a constant-time, nonblocking handoff. It does no encoding,
// scanning or filesystem work on the poller's subscription callback.
func (r *Recorder) Observe(at time.Time, connected bool, rows []rtorrent.Torrent) {
	if r == nil || r.closed.Load() {
		return
	}
	revision, _ := r.revision.Load().(string)
	select {
	case r.samples <- sessionObservation{at, connected, rows, revision}:
	default:
		r.dropped.Add(1)
	}
}

func TorrentValues(t rtorrent.Torrent) map[string]string {
	return map[string]string{
		"state": string(t.State), "complete": strconv.FormatBool(t.Complete), "open": strconv.FormatBool(t.IsOpen),
		"downRate": strconv.FormatInt(t.DownRate, 10), "upRate": strconv.FormatInt(t.UpRate, 10),
		"completedBytes": strconv.FormatInt(t.CompletedBytes, 10), "uploadedBytes": strconv.FormatInt(t.UploadedBytes, 10),
		"ratio": strconv.FormatFloat(t.Ratio, 'f', 3, 64), "priority": strconv.Itoa(t.Priority),
		"label": t.Label, "throttle": t.Throttle, "message": t.Message, "trackerStatus": t.TrackerStatus,
	}
}

func (r *Recorder) observe(s sessionObservation) {
	state := "disconnected"
	if s.Connected {
		state = "connected"
	}
	if r.connection != state {
		phase := "observation"
		if !s.Connected {
			phase = "gap"
		}
		r.append(Event{Entry: Entry{ID: r.nextID(), At: s.At, Phase: phase, Actor: "poller", Action: "connection", Revision: s.Revision, Before: map[string]string{"connection": r.connection}, After: map[string]string{"connection": state}, Message: "Observed connection state. No torrent observations cover disconnected intervals."}})
		r.connection = state
		// After reconnect, new checkpoints establish coverage without assuming
		// what happened during the gap.
		r.previous = map[string]observedTorrent{}
	}
	if !s.Connected {
		return
	}
	seen := map[string]bool{}
	for i, t := range s.Rows {
		if i >= maxRecordedEvents {
			r.dropped.Add(uint64(len(s.Rows) - i))
			break
		}
		seen[t.Hash] = true
		values := TorrentValues(t)
		values["observedAt"] = s.At.Format(time.RFC3339Nano)
		old, exists := r.previous[t.Hash]
		changed := false
		for _, k := range []string{"state", "complete", "open", "priority", "label", "throttle", "message", "trackerStatus"} {
			if old.Values[k] != values[k] {
				changed = true
			}
		}
		phase, action := "observation", "state_change"
		if !exists || s.At.Sub(old.LastSample) >= time.Minute {
			phase, action = "checkpoint", "checkpoint"
		} else if !changed {
			// Active throughput samples are deliberately slower than polling.
			if s.At.Sub(old.LastSample) < 15*time.Second || (values["downRate"] == old.Values["downRate"] && values["upRate"] == old.Values["upRate"]) {
				continue
			}
			action = "rates"
		}
		r.append(Event{Hash: t.Hash, Entry: Entry{ID: r.nextID(), At: s.At, Phase: phase, Actor: "poller", Action: action, Revision: s.Revision, Name: t.Name, Before: copyValues(old.Values), After: values, Message: "Sampled daemon state; no causal actor is inferred."}})
		r.previous[t.Hash] = observedTorrent{copyValues(values), s.At}
	}
	// A truncated snapshot cannot establish absence. The dropped-input gap
	// reports missing coverage without inventing torrent removals.
	for hash, old := range r.previous {
		if seen[hash] {
			continue
		}
		if len(s.Rows) <= maxRecordedEvents {
			r.append(Event{Hash: hash, Entry: Entry{ID: r.nextID(), At: s.At, Phase: "observation", Actor: "poller", Action: "state_change", Revision: s.Revision, Before: copyValues(old.Values), After: map[string]string{"state": "absent"}, Message: "Absent from the sampled session; actor and removal time are unknown."}})
		}
		delete(r.previous, hash)
	}
}

// ConfigValues intentionally excludes auth, network endpoints, RSS secrets,
// filesystem paths and webhook destinations. Indexed fields keep arbitrary
// user labels out of keys, allowing safe numeric export.
func ConfigValues(cfg config.Config) map[string]string {
	out := map[string]string{}
	if cfg.Tuning.GlobalDownRateKB != nil {
		out["globalDownKB"] = fmt.Sprint(*cfg.Tuning.GlobalDownRateKB)
	}
	if cfg.Tuning.GlobalUpRateKB != nil {
		out["globalUpKB"] = fmt.Sprint(*cfg.Tuning.GlobalUpRateKB)
	}
	flattenConfig(out, "channels", reflect.ValueOf(cfg.Tuning.Throttles))
	flattenConfig(out, "seeding", reflect.ValueOf(cfg.Seeding))
	flattenConfig(out, "schedule", reflect.ValueOf(cfg.Schedule.Bandwidth.Profiles))
	out["scheduleTimezone"] = cfg.Schedule.Timezone
	for _, day := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
		out["schedule."+day] = strings.Join(cfg.Schedule.Bandwidth.Grid[day], ",")
	}
	// Store match criteria and non-secret actions, not any URLs/paths.
	for i, rule := range cfg.Automation.OnComplete {
		if len(out) > 230 {
			out["coverage"] = "Configuration projection truncated at its field bound"
			break
		}
		data, _ := json.Marshal(rule)
		var fields map[string]any
		_ = json.Unmarshal(data, &fields)
		for _, key := range []string{"name", "label", "tracker", "name_regex", "min_size", "max_size", "private", "set_label"} {
			if v, ok := fields[key]; ok {
				out[fmt.Sprintf("completion[%d].%s", i, key)] = fmt.Sprint(v)
			}
		}
	}
	return out
}

func flattenConfig(out map[string]string, prefix string, v reflect.Value) {
	if len(out) >= 200 {
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			flattenConfig(out, prefix+"."+strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0], v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len() && len(out) < 200; i++ {
			flattenConfig(out, fmt.Sprintf("%s[%d]", prefix, i), v.Index(i))
		}
	default:
		out[prefix] = fmt.Sprint(v.Interface())
	}
}

func (l *Log) RecordConfig(cfg config.Config, actor, cause string) {
	if l == nil || l.opts.Recorder == nil {
		return
	}
	r := l.opts.Recorder
	values := ConfigValues(cfg)
	revision := r.nextID()
	previous, _ := r.revision.Swap(revision).(string)
	r.Record("", Entry{Phase: "configuration", Actor: actor, Action: "configuration", Revision: revision, CauseID: cause, Before: map[string]string{"revision": previous}, After: values, Result: "saved", Message: "Selected non-secret configuration fields; this is not proof of successful daemon application."})
}
