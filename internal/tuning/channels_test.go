package tuning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/fakertorrent"
	"blackbird/internal/rtorrent"
)

func TestChannelEntries(t *testing.T) {
	if got := ChannelEntries(config.Tuning{}); len(got) != 0 {
		t.Fatalf("nil throttles = %+v", got)
	}
	entries := ChannelEntries(config.Tuning{Throttles: []config.ThrottleChannel{
		{Name: "slow", UpKB: 100, DownKB: 500},
		{Name: "seed", UpKB: 0, DownKB: 0},
	}})
	if len(entries) != 2 || entries[0].Name != "slow" || entries[0].UpKB != 100 || entries[0].DownKB != 500 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestChannelDiff(t *testing.T) {
	prev := []config.ThrottleChannel{{Name: "slow", UpKB: 100, DownKB: 500}, {Name: "gone", UpKB: 10, DownKB: 10}}
	// Nil next = untouched.
	if upsert, removed := ChannelDiff(prev, nil); len(upsert) != 0 || len(removed) != 0 {
		t.Fatalf("nil next = %v, %v", upsert, removed)
	}
	// Unchanged = nothing.
	same := []config.ThrottleChannel{{Name: "slow", UpKB: 100, DownKB: 500}, {Name: "gone", UpKB: 10, DownKB: 10}}
	if upsert, removed := ChannelDiff(prev, same); len(upsert) != 0 || len(removed) != 0 {
		t.Fatalf("unchanged = %v, %v", upsert, removed)
	}
	// Changed cap upserts; dropped name removes; new name upserts.
	next := []config.ThrottleChannel{{Name: "slow", UpKB: 200, DownKB: 500}, {Name: "fresh", UpKB: 0, DownKB: 0}}
	upsert, removed := ChannelDiff(prev, next)
	if len(upsert) != 2 || upsert[0].Name != "slow" || upsert[0].UpKB != 200 || upsert[1].Name != "fresh" {
		t.Fatalf("upsert = %+v", upsert)
	}
	if len(removed) != 1 || removed[0] != "gone" {
		t.Fatalf("removed = %+v", removed)
	}
	// Explicit empty list removes everything.
	upsert, removed = ChannelDiff(prev, []config.ThrottleChannel{})
	if len(upsert) != 0 || len(removed) != 2 {
		t.Fatalf("empty next = %v, %v", upsert, removed)
	}
}

func TestInUse(t *testing.T) {
	torrents := []rtorrent.Torrent{{Hash: "a", Throttle: "slow"}, {Hash: "b", Throttle: "slow"}, {Hash: "c"}}
	got := InUse(torrents)
	if got["slow"] != 2 || len(got) != 1 {
		t.Fatalf("inUse = %+v", got)
	}
}

// testClient starts a fake daemon and returns a client against it.
func testClient(t *testing.T) (*rtorrent.Client, *fakertorrent.Daemon) {
	t.Helper()
	dir, err := os.MkdirTemp("", "bb-throttle-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "rt.sock")
	daemon, err := fakertorrent.Start(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(daemon.Stop)
	client, err := rtorrent.New("unix://"+sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return client, daemon
}

func hasCall(daemon *fakertorrent.Daemon, method string) bool {
	for _, m := range daemon.CallMethods() {
		if m == method {
			return true
		}
	}
	return false
}

func TestApplyChannelsUpsert(t *testing.T) {
	client, daemon := testClient(t)
	ctx := context.Background()
	results := ApplyChannels(ctx, client,
		[]ChannelEntry{{Name: "slow", UpKB: 100, DownKB: 500}},
		nil, nil)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if !hasCall(daemon, "throttle.up") || !hasCall(daemon, "throttle.down") {
		t.Fatalf("calls = %v", daemon.CallMethods())
	}
	// The fake daemon mirrors real max semantics (bytes).
	max, err := client.ThrottleUpMax(ctx, "slow")
	if err != nil || max != 100*1024 {
		t.Fatalf("up.max = %d, %v", max, err)
	}
	max, err = client.ThrottleDownMax(ctx, "slow")
	if err != nil || max != 500*1024 {
		t.Fatalf("down.max = %d, %v", max, err)
	}
}

func TestApplyChannelsRemovalRefused(t *testing.T) {
	client, daemon := testClient(t)
	ctx := context.Background()
	results := ApplyChannels(ctx, client, nil, []string{"slow"}, map[string]int{"slow": 3})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %+v", results)
	}
	if got := results[0].Err.Error(); got != `still used by 3 torrent(s); unassign them first` {
		t.Fatalf("error = %q", got)
	}
	if hasCall(daemon, "throttle.up") || hasCall(daemon, "throttle.down") {
		t.Fatal("refused removal still touched the daemon")
	}
}

func TestApplyChannelsRemovalNeutralizes(t *testing.T) {
	client, _ := testClient(t)
	ctx := context.Background()
	if err := client.SetThrottleUp(ctx, "old", 100); err != nil {
		t.Fatal(err)
	}
	results := ApplyChannels(ctx, client, nil, []string{"old"}, nil)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	max, err := client.ThrottleUpMax(ctx, "old")
	if err != nil || max != 0 {
		t.Fatalf("neutralized max = %d, %v", max, err)
	}
}
