// Command fakertorrent serves canned rtorrent responses over SCGI for
// local development and smoke-testing the console without a real daemon.
package main

import (
	"fmt"
	"os"

	"blackbird/internal/fakertorrent"
)

func main() {
	sock := "/tmp/rtorrent-fake.sock"
	if len(os.Args) > 1 {
		sock = os.Args[1]
	}
	d, err := fakertorrent.Start(sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakertorrent:", err)
		os.Exit(1)
	}
	d.LogCalls = true
	fmt.Println("fakertorrent listening on", sock)
	select {}
}
