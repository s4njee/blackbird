// Package torrentfile parses .torrent (bencoded) files to extract the
// metadata shown on the General detail tab: comment, created-by, creation
// date, and the infohash used to correlate a loaded torrent back to its
// metafile. Parsing is intentionally small and dependency-free.
package torrentfile

import (
	"crypto/sha1"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Meta is the subset of .torrent metadata Blackbird surfaces.
type Meta struct {
	Infohash     string     `json:"infohash"`     // lowercase hex SHA-1 of the info dict
	Name         string     `json:"name"`         // info.name (single-file or directory root)
	Comment      string     `json:"comment"`      // top-level "comment"; "" when absent
	CreatedBy    string     `json:"createdBy"`    // top-level "created by"; "" when absent
	CreationDate *time.Time `json:"creationDate"` // top-level "creation date"; nil when absent
	Private      bool       `json:"private"`      // info.private == 1
}

// Parse decodes a bencoded .torrent. A missing or malformed optional field
// degrades to a zero value; only an unparseable top-level structure or a
// missing info dict is an error.
func Parse(data []byte) (*Meta, error) {
	v, rest, err := decodeValue(data)
	if err != nil {
		return nil, err
	}
	if len(rest) > 0 {
		return nil, fmt.Errorf("trailing data after bencode value (%d bytes)", len(rest))
	}
	dict, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("torrent root is not a dictionary")
	}
	meta := &Meta{
		Comment:   str(dict["comment"]),
		CreatedBy: str(dict["created by"]),
	}
	if cd, ok := dict["creation date"]; ok {
		if n, ok := cd.(int64); ok && n > 0 {
			ts := time.Unix(n, 0)
			meta.CreationDate = &ts
		}
	}
	info, ok := dict["info"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("torrent info dictionary is missing")
	}
	meta.Name = str(info["name"])
	if p, ok := info["private"].(int64); ok {
		meta.Private = p == 1
	}
	// The infohash is the SHA-1 of the canonical, bencoded info dictionary.
	// Re-encode the parsed dict deterministically (bencode keys sort
	// lexicographically; decoded strings round-trip byte-identically).
	sum := sha1.Sum(encodeValue(info))
	meta.Infohash = fmt.Sprintf("%x", sum)
	return meta, nil
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// decodeValue parses one bencode value and returns the remaining bytes.
func decodeValue(data []byte) (any, []byte, error) { return decodeDepth(data, 0) }

func decodeDepth(data []byte, depth int) (any, []byte, error) {
	if depth > 128 {
		return nil, nil, fmt.Errorf("bencode nesting exceeds limit")
	}
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("unexpected end of data")
	}
	switch c := data[0]; {
	case c == 'i':
		end := indexByte(data, 'e')
		if end < 0 {
			return nil, nil, fmt.Errorf("unterminated integer")
		}
		n, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid integer %q", data[1:end])
		}
		return n, data[end+1:], nil
	case c >= '0' && c <= '9':
		colon := indexByte(data, ':')
		if colon < 0 {
			return nil, nil, fmt.Errorf("unterminated string length")
		}
		n, err := strconv.Atoi(string(data[:colon]))
		if err != nil || n < 0 {
			return nil, nil, fmt.Errorf("invalid string length %q", data[:colon])
		}
		if n > len(data)-(colon+1) {
			return nil, nil, fmt.Errorf("string of length %d exceeds input", n)
		}
		return string(data[colon+1 : colon+1+n]), data[colon+1+n:], nil
	case c == 'l':
		var list []any
		rest := data[1:]
		for {
			if len(rest) == 0 {
				return nil, nil, fmt.Errorf("unterminated list")
			}
			if rest[0] == 'e' {
				return list, rest[1:], nil
			}
			item, after, err := decodeDepth(rest, depth+1)
			if err != nil {
				return nil, nil, err
			}
			list = append(list, item)
			rest = after
		}
	case c == 'd':
		dict := map[string]any{}
		rest := data[1:]
		for {
			if len(rest) == 0 {
				return nil, nil, fmt.Errorf("unterminated dictionary")
			}
			if rest[0] == 'e' {
				return dict, rest[1:], nil
			}
			key, after, err := decodeDepth(rest, depth+1)
			if err != nil {
				return nil, nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, nil, fmt.Errorf("dictionary key is not a string")
			}
			if len(after) == 0 {
				return nil, nil, fmt.Errorf("dictionary %q has no value", k)
			}
			val, after2, err := decodeDepth(after, depth+1)
			if err != nil {
				return nil, nil, err
			}
			dict[k] = val
			rest = after2
		}
	default:
		return nil, nil, fmt.Errorf("unexpected bencode token %q", c)
	}
}

func indexByte(data []byte, b byte) int {
	for i, c := range data {
		if c == b {
			return i
		}
	}
	return -1
}

// encodeValue re-encodes a decoded value canonically (sorted dict keys) so the
// info-dict hash matches the one computed by torrent clients.
func encodeValue(v any) []byte {
	switch val := v.(type) {
	case int64:
		return []byte("i" + strconv.FormatInt(val, 10) + "e")
	case string:
		return []byte(strconv.Itoa(len(val)) + ":" + val)
	case []any:
		var buf []byte
		buf = append(buf, 'l')
		for _, item := range val {
			buf = append(buf, encodeValue(item)...)
		}
		return append(buf, 'e')
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf []byte
		buf = append(buf, 'd')
		for _, k := range keys {
			buf = append(buf, encodeValue(k)...)
			buf = append(buf, encodeValue(val[k])...)
		}
		return append(buf, 'e')
	default:
		return nil
	}
}

// HasInfohash reports whether a string is a plausible lowercase-hex infohash.
func HasInfohash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
