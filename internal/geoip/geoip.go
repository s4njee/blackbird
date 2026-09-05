// Package geoip resolves an IPv4 address to an ISO 3166-1 alpha-2 country
// code using an embedded, license-compatible country database.
//
// The bundled database is DB-IP's `dbip-country-lite` (IPv4 numeric ranges,
// CC-BY-4.0, https://db-ip.com). The full lite file is ~8.7 MB as CSV and is
// committed gzip-compressed (~2.5 MB); the parser decompresses it once, on
// first use, into a binary-searchable range table. Regenerating the file is
// documented in generate/generate.go.
//
// The database is always present in the binary, but peers whose address is
// unknown or outside the lite ranges simply resolve to "" and callers degrade
// to an em dash.
package geoip

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed dbip-country-ipv4-num.csv.gz
var embeddedGZ []byte

// record is one CSV row: a start/end IPv4 range and a country code.
type record struct {
	start uint32
	end   uint32
	code  string
}

// Resolver answers "which country does this IP belong to?" from a sorted
// range table using binary search. It is safe for concurrent use.
type Resolver struct {
	once sync.Once
	rows []record
}

var std = &Resolver{}

// Enabled reports whether an embedded database is present. The DB is built
// into the binary, so this is always true; it exists so callers can treat a
// future build-time exclusion consistently.
func Enabled() bool { return len(embeddedGZ) > 0 }

// Lookup returns the ISO country code for an IPv4 string, or "" when the
// database is absent or the address is unknown/not IPv4. Lookup is safe for
// concurrent use.
func Lookup(ip string) string { return std.Lookup(ip) }

// Lookup resolves one IPv4 string to its two-letter country code ("" if none).
func (r *Resolver) Lookup(ip string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !addr.Is4() {
		return ""
	}
	key := ipv4ToUint32(addr)
	r.once.Do(func() { r.rows = parseCSV(embeddedGZ) })
	rows := r.rows
	i := sort.Search(len(rows), func(i int) bool { return rows[i].end >= key })
	if i < len(rows) && key >= rows[i].start {
		return rows[i].code
	}
	return ""
}

// ipv4ToUint32 converts a parsed IPv4 address to its big-endian integer form.
func ipv4ToUint32(addr netip.Addr) uint32 {
	b := addr.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// parseCSV decompresses and decodes the DB-IP lite CSV (start,end,code).
// Rows are sorted by start so Lookup can binary-search by end. Parsing happens
// once, on first use; a malformed row is skipped rather than failing startup.
func parseCSV(data []byte) []record {
	var rows []record
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		start, err1 := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 32)
		end, err2 := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 32)
		code := strings.ToUpper(strings.TrimSpace(fields[2]))
		if err1 != nil || err2 != nil || len(code) != 2 || start > end {
			continue
		}
		rows = append(rows, record{start: uint32(start), end: uint32(end), code: code})
	}
	_ = io.EOF
	sort.Slice(rows, func(i, j int) bool { return rows[i].start < rows[j].start })
	return rows
}
