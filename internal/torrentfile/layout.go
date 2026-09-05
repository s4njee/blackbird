package torrentfile

import (
	"fmt"
	"strings"
)

// Layout is v1 file-size evidence; it does not fetch metadata or open data files.
type Layout struct {
	Name        string
	Files       []LayoutFile
	PieceLength int64
	Total       int64
	Multi       bool
}
type LayoutFile struct {
	Path string
	Size int64
}

func ParseLayout(data []byte) (Layout, error) {
	var out Layout
	v, rest, err := decodeValue(data)
	if err != nil {
		return out, err
	}
	if len(rest) > 0 {
		return out, fmt.Errorf("trailing bencode data")
	}
	top, ok := v.(map[string]any)
	if !ok {
		return out, fmt.Errorf("invalid torrent dictionary")
	}
	info, ok := top["info"].(map[string]any)
	if !ok {
		return out, fmt.Errorf("missing info dictionary")
	}
	out.Name = str(info["name"])
	if !component(out.Name) {
		return out, fmt.Errorf("unsafe torrent name")
	}
	if _, v2 := info["meta version"]; v2 {
		return out, fmt.Errorf("v2/hybrid file layout is not forecast yet")
	}
	out.PieceLength, _ = info["piece length"].(int64)
	if out.PieceLength <= 0 || out.PieceLength > 1<<40 {
		return out, fmt.Errorf("invalid piece length")
	}
	add := func(path string, n any) error {
		size, ok := n.(int64)
		if !ok || size < 0 || size > (1<<52)-out.Total {
			return fmt.Errorf("invalid or excessive file length")
		}
		out.Files = append(out.Files, LayoutFile{path, size})
		out.Total += size
		return nil
	}
	if entries, ok := info["files"].([]any); ok {
		out.Multi = true
		if len(entries) == 0 || len(entries) > 20000 {
			return out, fmt.Errorf("file count exceeds forecast bound")
		}
		seen := map[string]bool{}
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				return out, fmt.Errorf("invalid file entry")
			}
			parts, ok := entry["path"].([]any)
			if !ok || len(parts) == 0 {
				return out, fmt.Errorf("invalid file path")
			}
			segments := []string{}
			for _, part := range parts {
				s := str(part)
				if !component(s) {
					return out, fmt.Errorf("unsafe file path")
				}
				segments = append(segments, s)
			}
			path := strings.Join(segments, "/")
			if seen[path] {
				return out, fmt.Errorf("duplicate file path")
			}
			seen[path] = true
			if err := add(path, entry["length"]); err != nil {
				return out, err
			}
		}
	} else {
		if err := add(out.Name, info["length"]); err != nil {
			return out, err
		}
	}
	return out, nil
}
func component(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00:")
}

// SelectedBytes returns selected logical bytes and the union of their v1 piece
// ranges. Boundary pieces can touch skipped files. No per-file overhead is
// multiplied blindly when neighboring selections share a piece.
func SelectedBytes(sizes []int64, selected []bool, piece int64) (int64, int64, error) {
	if len(sizes) != len(selected) || piece <= 0 {
		return 0, 0, fmt.Errorf("selection metadata unavailable")
	}
	var total, logical, covered, start, end int64
	has := false
	for i, size := range sizes {
		if size < 0 || size > (1<<52)-total {
			return 0, 0, fmt.Errorf("invalid file lengths")
		}
		offset := total
		total += size
		if !selected[i] || size == 0 {
			continue
		}
		logical += size
		a, b := offset/piece*piece, ((total-1)/piece+1)*piece
		if !has {
			start, end, has = a, b, true
		} else if a <= end {
			end = max(end, b)
		} else {
			covered += end - start
			start, end = a, b
		}
	}
	if has {
		covered += min(total, end) - start
	}
	return logical, covered, nil
}
