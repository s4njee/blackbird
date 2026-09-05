// Command generate refreshes the embedded DB-IP country database used by
// internal/geoip. It is not part of the normal build (no build tag, so it is
// skipped by `go build ./...` unless invoked explicitly).
//
// Usage:
//
//	cd internal/geoip
//	go run generate/generate.go
//
// The script downloads the DB-IP Lite country ranges (the ip-location-db
// mirror of the CC-BY-4.0 dbip-country-lite dataset) and writes the gzipped
// numeric-CSV file the geoip package embeds.
package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const source = "https://cdn.jsdelivr.net/npm/@ip-location-db/dbip-country/dbip-country-ipv4-num.csv"

func main() {
	resp, err := http.Get(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "download:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "download: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	out := filepath.Join("dbip-country-ipv4-num.csv.gz")
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	gz := gzip.NewWriter(f)
	n, err := io.Copy(gz, resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	if err := gz.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "gzip:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes raw)\n", out, n)
	fmt.Println("review the diff and commit; keep the source URL above in THIRD_PARTY_NOTICES.md")
}
