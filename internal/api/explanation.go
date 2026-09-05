package api

import (
	"fmt"
	"net/http"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
	"blackbird/internal/schedule"
	"blackbird/internal/seeding"
)

// Explanation is deliberately built from cached observations. It neither
// probes nor changes the daemon, and does not infer causality from a match.
type explanationEvidence struct {
	Source     string     `json:"source"`
	Value      string     `json:"value"`
	ObservedAt *time.Time `json:"observedAt"`
}

type explanationTarget struct {
	Kind  string `json:"kind"` // tab | settings
	Name  string `json:"name"`
	Label string `json:"label"`
}

type explanationFinding struct {
	ID       string                `json:"id"`
	Kind     string                `json:"kind"` // observation | recorded_action | constraint | hypothesis | unknown
	Title    string                `json:"title"`
	Summary  string                `json:"summary"`
	Evidence []explanationEvidence `json:"evidence"`
	Target   *explanationTarget    `json:"target,omitempty"`
}

type explanationResponse struct {
	Hash        string               `json:"hash"`
	Name        string               `json:"name"`
	GeneratedAt time.Time            `json:"generatedAt"`
	ObservedAt  *time.Time           `json:"observedAt"`
	Stale       bool                 `json:"stale"`
	StaleAfter  int64                `json:"staleAfterSeconds"`
	Findings    []explanationFinding `json:"findings"`
	Coverage    []string             `json:"coverage"`
}

func explanationTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Server) explanationHandler(w http.ResponseWriter, r *http.Request) {
	if s.opts.Poller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "poller is not running")
		return
	}
	snap := s.opts.Poller.Snapshot()
	hash := r.PathValue("hash")
	var torrent *rtorrent.Torrent
	for i := range snap.Torrents {
		if snap.Torrents[i].Hash == hash {
			torrent = &snap.Torrents[i]
			break
		}
	}
	if torrent == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "torrent is not in the cached session")
		return
	}
	now := time.Now()
	var cfg *config.Config
	if s.opts.Store != nil {
		c := s.opts.Store.Get()
		cfg = &c
	}
	var sched *schedule.Status
	if s.opts.Schedule != nil {
		st := s.opts.Schedule.Status(now)
		sched = &st
	}
	response := explainTorrent(*torrent, snap, cfg, sched, s.history.ForHash(hash), s.opts.Poller.SpeedHistory(hash), now)
	if s.history.Recorder() != nil {
		response.Coverage[1] = "History restores retained outcomes from the flight recorder. Unflushed events, expired evidence and external actions may be missing. A recorded action does not prove the cause of the current state."
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func explainTorrent(t rtorrent.Torrent, snap *poller.Snapshot, cfg *config.Config, sched *schedule.Status, events []history.Entry, samples []poller.Sample, now time.Time) explanationResponse {
	maxInterval := config.DefaultPollMaxInterval
	if cfg != nil {
		maxInterval = max(cfg.Poll.Interval, cfg.Poll.EffectiveMaxInterval())
	}
	staleAfter := max(30*time.Second, 3*maxInterval)
	out := explanationResponse{
		Hash: t.Hash, Name: t.Name, GeneratedAt: now, ObservedAt: explanationTime(snap.GeneratedAt),
		Stale:      snap.Stale || snap.Status != poller.StatusConnected || snap.GeneratedAt.IsZero() || now.Sub(snap.GeneratedAt) > staleAfter || snap.GeneratedAt.After(now),
		StaleAfter: int64(staleAfter.Seconds()), Findings: []explanationFinding{},
		Coverage: []string{
			"Settings are read at request time; their last change time and live daemon rate limits are not sampled here. Configured limits do not prove that a transfer is being throttled.",
			"History is bounded and in memory, so earlier actions and actions taken outside Blackbird may be missing. A recorded action does not prove the cause of the current state.",
		},
	}
	observed := func(source, value string) explanationEvidence {
		return explanationEvidence{source, value, explanationTime(snap.GeneratedAt)}
	}
	configured := func(source, value string) explanationEvidence {
		return explanationEvidence{source + " (read now)", value, &now}
	}
	tab := func(name, label string) *explanationTarget { return &explanationTarget{"tab", name, label} }
	settings := func(name, label string) *explanationTarget { return &explanationTarget{"settings", name, label} }
	add := func(id, kind, title, summary string, target *explanationTarget, evidence ...explanationEvidence) {
		out.Findings = append(out.Findings, explanationFinding{id, kind, title, summary, evidence, target})
	}

	add("state", "observation", "Observed state: "+string(t.State),
		fmt.Sprintf("Observed download %d B/s; upload %d B/s. These are measured rates, not configured limits.", t.DownRate, t.UpRate), tab("speed", "Inspect speed history"),
		observed("Session poll", fmt.Sprintf("state=%s; complete=%t; open=%t; downloaded=%d bytes; remaining=%d bytes", t.State, t.Complete, t.IsOpen, t.DownloadedBytes, t.LeftBytes)))
	if t.Message != "" {
		add("message", "observation", "Daemon reported a message", "The daemon message is evidence of a reported condition; it may not explain every symptom.", tab("logger", "Inspect torrent log"), observed("d.message", t.Message))
	}
	if t.State == rtorrent.StateStopped {
		add("stop-cause", "unknown", "Why it stopped is not established", "The cached state does not identify who stopped this torrent. Check recorded actions below; an external change or an older action may be unrecorded.", tab("logger", "Inspect torrent log"), observed("Session poll", "state=stopped"))
	}
	if t.State == rtorrent.StateError && t.Message == "" {
		add("missing-error", "unknown", "Error detail is unavailable", "The daemon reported an error state without a message in this snapshot.", tab("logger", "Inspect torrent log"), observed("Session poll", "state=error; message empty"))
	}
	if t.SkippedBytes > 0 {
		add("skipped", "constraint", "Some files are skipped", "Skipped content is not requested normally. Inspect file priorities before expecting the whole torrent to complete; boundary pieces may still be downloaded.", tab("files", "Review skipped files"), observed("d.skip.total", fmt.Sprintf("%d bytes skipped", t.SkippedBytes)))
	}
	if !t.Complete && t.DownRate == 0 {
		add("no-download", "observation", "No download traffic in the latest sample", "One zero-rate sample does not establish a stall or its cause.", tab("speed", "Inspect speed history"), observed("Session poll", "download=0 B/s"))
		if t.State == rtorrent.StateDownloading && t.Seeds == 0 && t.Peers == 0 {
			add("peers", "hypothesis", "Peer availability may be contributing", "No connected seeds or leechers were observed. This does not establish global availability or prove the swarm is dead; inspect tracker status and peer connections.", tab("trackers", "Inspect trackers"), observed("Connected peers", "seeds=0; leechers=0"))
		}
	}

	// Only claim a recent series when its coverage is continuous enough and
	// ends near this snapshot. Focused rate history does not measure progress
	// between polls and may contain gaps while disconnected or unfocused.
	recent := []poller.Sample{}
	for _, sample := range samples {
		if !sample.At.Before(snap.GeneratedAt.Add(-time.Minute)) && !sample.At.After(snap.GeneratedAt) {
			recent = append(recent, sample)
		}
	}
	continuous := len(recent) >= 2
	for i, sample := range recent {
		if sample.DownRate != 0 || (i > 0 && (sample.At.Sub(recent[i-1].At) > maxInterval*2 || !sample.At.After(recent[i-1].At))) {
			continuous = false
		}
	}
	if continuous && !t.Complete && t.State == rtorrent.StateDownloading && !out.Stale &&
		recent[len(recent)-1].At.Sub(recent[0].At) >= 10*time.Second && snap.GeneratedAt.Sub(recent[len(recent)-1].At) <= maxInterval*2 {
		last := recent[len(recent)-1]
		add("quiet-window", "observation", "Recent download samples are all zero", "No download traffic was observed at these sample times. This is not proof of no progress between samples, or of disk, network, or swarm failure.", tab("speed", "Inspect sampled window"),
			explanationEvidence{"Focused speed history", fmt.Sprintf("%d samples from %s to %s; all download=0 B/s", len(recent), recent[0].At.Format(time.RFC3339), last.At.Format(time.RFC3339)), &last.At})
	} else {
		out.Coverage = append(out.Coverage, "No sustained zero-download conclusion: recent focused samples may be missing, too short, gapped, stale, or show activity. Open Speed to inspect coverage.")
	}

	// History is a separate evidence class: never use a current policy match
	// to manufacture a historical action or infer a current-state cause.
	retention := 24 * time.Hour
	if cfg != nil && cfg.History.ActionLogRetention > 0 {
		retention = cfg.History.ActionLogRetention
	}
	recorded := 0
	for _, e := range events {
		if e.Kind != history.KindAction || e.At.IsZero() || e.At.Before(now.Add(-retention)) || e.At.After(now) {
			continue
		}
		switch e.Action {
		case "start", "force_start", "pause", "stop", "stop_and_set_label", "recheck":
		default:
			continue
		}
		add(fmt.Sprintf("action-%d", recorded), "recorded_action", "Recorded action: "+e.Action,
			"This is the recorded request outcome, not a confirmed explanation of the current state. Later external changes may be absent.", tab("logger", "Inspect action history"),
			explanationEvidence{"Blackbird history", fmt.Sprintf("actor=%s; action=%s; result=%s; %s", e.Actor, e.Action, e.Result, e.Message), &e.At})
		recorded++
		if recorded == 3 {
			break
		}
	}
	if recorded == 0 {
		out.Coverage = append(out.Coverage, "No retained transport action for this torrent. This does not mean no action occurred.")
	}

	if cfg == nil {
		add("missing-config", "unknown", "Configured controls are unavailable", "Seeding rules and configured bandwidth limits could not be inspected.", nil, configured("Configuration store", "unavailable"))
		return out
	}
	rate := func(v *int64) string {
		if v == nil {
			return "not configured"
		}
		if *v == 0 {
			return "unlimited (0)"
		}
		return fmt.Sprintf("%d KiB/s", *v)
	}
	limits := func(down, up int64) string { return "download=" + rate(&down) + "; upload=" + rate(&up) }
	add("global-limits", "constraint", "Configured global bandwidth", "These are saved defaults; schedules, manual overrides, or external changes can supersede them. No effective bottleneck is inferred.", settings("Bandwidth", "Review global bandwidth"), configured("Saved tuning", "download="+rate(cfg.Tuning.GlobalDownRateKB)+"; upload="+rate(cfg.Tuning.GlobalUpRateKB)))
	if t.Throttle != "" {
		value := "channel=" + t.Throttle + "; limits unknown (not in saved channels)"
		for _, ch := range cfg.Tuning.Throttles {
			if ch.Name == t.Throttle {
				value = "channel=" + ch.Name + "; " + limits(ch.DownKB, ch.UpKB)
				break
			}
		}
		add("channel", "constraint", "Assigned throttle channel: "+t.Throttle, "Channel settings and global limits can both constrain transfers. A scheduler profile can replace saved channel limits; the live channel cap is not sampled.", settings("Bandwidth", "Review channel "+t.Throttle), observed("d.throttle_name", t.Throttle), configured("Saved channels", value))
	}
	if sched == nil {
		out.Coverage = append(out.Coverage, "Scheduler status is unavailable.")
	} else if sched.Overridden {
		title, kind := "Manual bandwidth override", "constraint"
		if !sched.OverrideUntil.After(now) {
			title, kind = "Override expiry is awaiting reconciliation", "unknown"
		}
		add("override", kind, title, "The scheduler reports this override. Saved defaults and the underlying schedule do not establish the live daemon cap.", settings("Scheduler", "Review manual override"), configured("Scheduler status", limits(sched.OverrideDownKB, sched.OverrideUpKB)+"; expires="+sched.OverrideUntil.Format(time.RFC3339)))
	} else if sched.ActiveProfile != "" {
		value := "profile=" + sched.ActiveProfile + "; settings unavailable"
		for _, p := range cfg.Schedule.Bandwidth.Profiles {
			if p.Name == sched.ActiveProfile {
				value = "profile=" + p.Name + "; " + limits(p.DownKB, p.UpKB)
				for _, ch := range p.Throttles {
					if ch.Name == t.Throttle {
						value += "; channel=" + ch.Name + ": " + limits(ch.DownKB, ch.UpKB)
					}
				}
				break
			}
		}
		add("schedule", "constraint", "Scheduler profile: "+sched.ActiveProfile, "This is the profile reported by the scheduler with its current saved settings. It is not a live limit reading or proof that every setting applied successfully.", settings("Scheduler", "Review profile "+sched.ActiveProfile), configured("Scheduler and saved profiles", value))
	}
	groupName := t.SlotValue(cfg.Seeding.EffectiveSlot())
	if groupName != "" {
		group := seeding.FindGroup(cfg.Seeding.Groups, groupName)
		if group == nil {
			add("seeding", "unknown", "Assigned seeding group is not configured", "The assigned group has no saved definition to evaluate.", settings("Seeding", "Review seeding group "+groupName), observed(cfg.Seeding.EffectiveSlot(), groupName))
		} else {
			title := "Assigned seeding group: " + groupName
			value := "No condition is met by the cached torrent values."
			if trigger, met := seeding.Evaluate(*group, t, snap.GeneratedAt); met {
				title = "Seeding condition is met: " + groupName
				value = trigger.Detail + "; configured action=" + group.Action
			}
			add("seeding", "constraint", title, "A match is not evidence that the rule ran or caused a stop. Enforcement also depends on completion, open state, and the persisted once-only marker, which is not inspected here.", settings("Seeding", "Review seeding group "+groupName), observed("Cached torrent / "+cfg.Seeding.EffectiveSlot(), groupName), configured("Saved seeding policy evaluated at snapshot time", value))
		}
	}
	return out
}
