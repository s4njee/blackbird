package tuning

import (
	"context"
	"fmt"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
)

// ChannelEntry creates or updates one named throttle channel: an upload and
// a download cap in KB/s (0 = unlimited), applied with throttle.up/down.
// Verified against rTorrent 0.16.18: both methods take an empty target, the
// channel name, and the cap in KiB/s as a string.
type ChannelEntry struct {
	Name   string
	UpKB   int64
	DownKB int64
}

// ChannelEntries flattens the throttles list into channel entries in YAML
// order. A nil slice produces no entries — the daemon is left untouched.
func ChannelEntries(t config.Tuning) []ChannelEntry {
	var out []ChannelEntry
	for _, ch := range t.Throttles {
		out = append(out, ChannelEntry{Name: ch.Name, UpKB: ch.UpKB, DownKB: ch.DownKB})
	}
	return out
}

// ChannelDiff compares two throttle lists: channels to upsert (new or with
// changed caps) and channel names that disappeared. A nil next slice means
// "untouched" and yields neither.
func ChannelDiff(prev, next []config.ThrottleChannel) (upsert []ChannelEntry, removed []string) {
	if next == nil {
		return nil, nil
	}
	old := map[string]config.ThrottleChannel{}
	for _, ch := range prev {
		old[ch.Name] = ch
	}
	keep := map[string]bool{}
	for _, ch := range next {
		keep[ch.Name] = true
		if prev, ok := old[ch.Name]; !ok || prev.UpKB != ch.UpKB || prev.DownKB != ch.DownKB {
			upsert = append(upsert, ChannelEntry{Name: ch.Name, UpKB: ch.UpKB, DownKB: ch.DownKB})
		}
	}
	for _, ch := range prev {
		if !keep[ch.Name] {
			removed = append(removed, ch.Name)
		}
	}
	return upsert, removed
}

// ChannelResult reports one channel's application outcome.
type ChannelResult struct {
	Name string
	Err  error
}

// InUse counts live torrents per assigned throttle channel (empty
// assignments excluded) for the removal guard.
func InUse(torrents []rtorrent.Torrent) map[string]int {
	out := map[string]int{}
	for _, t := range torrents {
		if t.Throttle != "" {
			out[t.Throttle]++
		}
	}
	return out
}

// ApplyChannels upserts channels and neutralizes removals. rTorrent has no
// throttle delete, so a removed channel is reset to 0/0 (unlimited) instead.
// A removal is refused while torrents still reference the channel — the
// caller must unassign them first.
func ApplyChannels(ctx context.Context, client *rtorrent.Client, upsert []ChannelEntry, removed []string, inUse map[string]int) []ChannelResult {
	var out []ChannelResult
	for _, e := range upsert {
		key := "throttle." + e.Name
		if err := client.SetThrottleUp(ctx, e.Name, e.UpKB); err != nil {
			out = append(out, ChannelResult{Name: key, Err: fmt.Errorf("throttle.up: %w", err)})
			continue
		}
		if err := client.SetThrottleDown(ctx, e.Name, e.DownKB); err != nil {
			out = append(out, ChannelResult{Name: key, Err: fmt.Errorf("throttle.down: %w", err)})
			continue
		}
		out = append(out, ChannelResult{Name: key})
	}
	for _, name := range removed {
		key := "throttle." + name
		if n := inUse[name]; n > 0 {
			out = append(out, ChannelResult{Name: key, Err: fmt.Errorf("still used by %d torrent(s); unassign them first", n)})
			continue
		}
		if err := client.SetThrottleUp(ctx, name, 0); err != nil {
			out = append(out, ChannelResult{Name: key, Err: fmt.Errorf("throttle.up: %w", err)})
			continue
		}
		if err := client.SetThrottleDown(ctx, name, 0); err != nil {
			out = append(out, ChannelResult{Name: key, Err: fmt.Errorf("throttle.down: %w", err)})
			continue
		}
		out = append(out, ChannelResult{Name: key})
	}
	return out
}
