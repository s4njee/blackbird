// Command fakertorrent serves canned rtorrent responses over SCGI for
// local development and smoke-testing the console without a real daemon.
//
// FAKE_SESSION_SIZE / FAKE_ACTIVE_FRACTION / FAKE_SEED shape a deterministic
// synthetic session (see fakertorrent.Options); unset means canned rows.
// Used by the Playwright suite (web/e2e) for deterministic end-to-end flows.
package main

import (
	"fmt"
	"os"
	"strconv"

	"blackbird/internal/fakertorrent"
)

func main() {
	sock := "/tmp/rtorrent-fake.sock"
	if len(os.Args) > 1 {
		sock = os.Args[1]
	}
	opts := fakertorrent.Options{}
	if n, err := strconv.Atoi(os.Getenv("FAKE_SESSION_SIZE")); err == nil && n > 0 {
		opts.SessionSize = n
	}
	if f, err := strconv.ParseFloat(os.Getenv("FAKE_ACTIVE_FRACTION"), 64); err == nil {
		opts.ActiveFraction = f
	}
	if s, err := strconv.ParseInt(os.Getenv("FAKE_SEED"), 10, 64); err == nil {
		opts.Seed = s
	}
	d, err := fakertorrent.StartOpts(sock, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakertorrent:", err)
		os.Exit(1)
	}
	d.LogCalls = true
	fmt.Println("fakertorrent listening on", sock)
	select {}
}
