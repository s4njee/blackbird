package attention

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"blackbird/internal/rtorrent"
)

type symptom struct {
	Kind, Title, Evidence, NextStep string
	Hashes                          []string
	Affected                        int
}

var validHash = regexp.MustCompile(`^[a-fA-F0-9]{1,64}$`)

func host(raw string) string {
	u, err := url.Parse("https://" + raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || len(raw) > 253 {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
func symptoms(input Input, fresh bool) map[string]*symptom {
	out := map[string]*symptom{}
	if !fresh {
		out["connection"] = &symptom{Kind: "connection", Title: "Session observations unavailable", Evidence: "The cached session is disconnected, stale, or not yet sampled. Torrent health is unknown.", NextStep: "Check the daemon connection and its logs; inspect the flight recorder for the last observed state.", Hashes: []string{}}
		return out
	}
	add := func(key, kind, title, evidence, next, hash string) {
		g := out[key]
		if g == nil {
			g = &symptom{Kind: kind, Title: title, Evidence: evidence, NextStep: next, Hashes: []string{}}
			out[key] = g
		}
		if hash != "" {
			g.Affected++
			if len(g.Hashes) < MaxAffected && validHash.MatchString(hash) {
				g.Hashes = append(g.Hashes, hash)
			}
		}
	}
	for _, t := range input.Snapshot.Torrents {
		if t.State != rtorrent.StateError && strings.TrimSpace(t.Message) == "" {
			continue
		}
		// TrackerStatus is derived from *any* d.message in the daemon adapter.
		// Only an explicit tracker prefix establishes a shared tracker symptom.
		h := host(t.TrackerHost)
		if h != "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(t.Message)), "tracker:") {
			add("tracker:"+h, "tracker", "Tracker messages · "+h, "Explicit tracker messages were observed on torrents listing this host. This does not prove this host caused the failures, or identify which tracker in a multi-tracker torrent failed.", "Inspect an affected torrent's Why? and Trackers tabs before changing tracker or network settings.", t.Hash)
		} else {
			add("torrent:"+t.Hash, "torrent", "Torrent reported an error", "A daemon error state or message was observed. It has not been correlated with other failures.", "Open Why? for the daemon message and relevant controls, then inspect the incident's recorded evidence.", t.Hash)
		}
	}
	available := map[string]bool{}
	for _, v := range input.Snapshot.Volumes {
		available[v.Path] = true
	}
	for i, path := range input.Volumes {
		if available[path] {
			continue
		}
		key := "volume:" + path
		add(key, "volume", fmt.Sprintf("Configured volume %d unavailable", i+1), "The configured path is absent from the cached filesystem samples; it may be unreadable or unavailable. This does not prove a disk failure.", "Check this volume in the server configuration, mount availability, and Blackbird's filesystem permissions.", "")
		for _, t := range input.Snapshot.Torrents {
			rel, err := filepath.Rel(path, t.BasePath)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				g := out[key]
				g.Affected++
				if len(g.Hashes) < MaxAffected && validHash.MatchString(t.Hash) {
					g.Hashes = append(g.Hashes, t.Hash)
				}
			}
		}
	}
	return out
}
