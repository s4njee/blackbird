// Package mktorrent builds .torrent files from server-side data (PAR-5.4):
// it walks a file or directory under the configured download roots, hashes
// the byte stream into SHA-1 pieces, and emits a canonical bencoded metainfo
// dict. All filesystem and hashing work here is pure and cancellable; the
// bounded background workers live in service.go.
package mktorrent

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Piece length bounds for fixed and automatic selection.
const (
	MinPieceLength = 16384      // 16 KiB
	MaxPieceLength = 16777216   // 16 MiB
	AutoPieceCount = 2000       // auto picks the smallest piece length with at most this many pieces
	readChunk      = 256 * 1024 // streaming read size; progress reports per chunk
)

// File is one data file inside the torrent: its slash-joined relative path
// components (BEP-3) and absolute source path.
type File struct {
	Rel  []string // path components relative to the torrent root
	Abs  string   // absolute source path
	Size int64
}

// Input describes one build: the resolved source plus the metainfo fields.
type Input struct {
	Source      string   // absolute, symlink-resolved source path (file or dir)
	Name        string   // torrent name (defaults to the source basename)
	Trackers    []string // announce URLs; first is announce, each in its own announce-list tier
	PieceLength int64    // 0 = automatic
	Private     bool
	Comment     string
	SourceTag   string // info.source (private-tracker tagging)
}

// Result is a completed build.
type Result struct {
	Data        []byte // the .torrent file bytes
	Infohash    string // lowercase hex SHA-1 of the info dict
	Name        string
	TotalBytes  int64
	FileCount   int
	PieceLength int64
	PieceCount  int
	Private     bool
}

// Progress reports hashing progress; the service forwards it to job status.
type Progress struct {
	BytesHashed int64
	TotalBytes  int64
	PiecesDone  int
	PieceCount  int
	CurrentFile string // slash-joined rel path of the file being read
}

// Validate checks the request shape that does not need the filesystem:
// name, trackers, piece length, and source tag.
func Validate(in Input) error {
	if strings.TrimSpace(in.Source) == "" {
		return fmt.Errorf("source must not be empty")
	}
	if !filepath.IsAbs(in.Source) {
		return fmt.Errorf("source must be an absolute path")
	}
	name := in.Name
	if name == "" {
		name = filepath.Base(filepath.Clean(in.Source))
	}
	if strings.TrimSpace(name) == "" || name == "." || name == ".." || name == "/" || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name must be a single path component")
	}
	if len(name) > 255 {
		return fmt.Errorf("name must be at most 255 characters")
	}
	for _, u := range in.Trackers {
		if err := ValidateTracker(u); err != nil {
			return err
		}
	}
	if in.PieceLength != 0 {
		if in.PieceLength < MinPieceLength || in.PieceLength > MaxPieceLength || in.PieceLength&(in.PieceLength-1) != 0 {
			return fmt.Errorf("piece_length must be 0 (auto) or a power of two from 16KiB to 16MiB")
		}
	}
	if len(in.SourceTag) > 64 {
		return fmt.Errorf("source must be at most 64 characters")
	}
	if strings.ContainsAny(in.SourceTag, " \t\r\n") {
		return fmt.Errorf("source must not contain whitespace")
	}
	return nil
}

// ValidateTracker accepts http(s) and udp tracker URLs with a host.
func ValidateTracker(raw string) error {
	u := strings.TrimSpace(raw)
	if u == "" {
		return fmt.Errorf("tracker URL must not be empty")
	}
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return fmt.Errorf("tracker %q must be an http(s) or udp URL", u)
	}
	switch strings.ToLower(scheme) {
	case "http", "https", "udp":
	default:
		return fmt.Errorf("tracker %q must be an http(s) or udp URL", u)
	}
	host := rest
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("tracker %q has no host", u)
	}
	return nil
}

// Collect resolves the source to an ordered file list. Single files package
// as one entry; directories walk recursively in lexicographic order (the
// order defines the piece layout). Symlinks are refused: following one could
// pull data from outside the download roots into the torrent.
func Collect(source string) ([]File, bool, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("refusing to package a symlink source")
	}
	if !info.IsDir() {
		return []File{{Rel: []string{info.Name()}, Abs: source, Size: info.Size()}}, false, nil
	}
	var files []File
	walkErr := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in source: %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("source entry escapes its root: %s", path)
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, File{Rel: strings.Split(rel, string(filepath.Separator)), Abs: path, Size: fi.Size()})
		return nil
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	sort.Slice(files, func(i, j int) bool {
		return strings.Join(files[i].Rel, "/") < strings.Join(files[j].Rel, "/")
	})
	return files, true, nil
}

// TotalBytes sums the file list.
func TotalBytes(files []File) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}

// AutoPieceLength picks the smallest piece length in [64KiB, 16MiB] with at
// most AutoPieceCount pieces.
func AutoPieceLength(total int64) int64 {
	for plen := int64(65536); plen <= MaxPieceLength; plen *= 2 {
		if (total+plen-1)/plen <= AutoPieceCount {
			return plen
		}
	}
	return MaxPieceLength
}

// Build streams the files into SHA-1 pieces and emits the metainfo. onProgress
// fires per read chunk (nil-safe); ctx cancellation aborts between chunks.
func Build(ctx context.Context, in Input, files []File, multi bool, onProgress func(Progress)) (Result, error) {
	total := TotalBytes(files)
	if total <= 0 {
		return Result{}, fmt.Errorf("nothing to package: source holds no data")
	}
	name := in.Name
	if name == "" {
		name = filepath.Base(filepath.Clean(in.Source))
	}
	plen := in.PieceLength
	if plen == 0 {
		plen = AutoPieceLength(total)
	}
	pieceCount := int((total + plen - 1) / plen)
	prog := Progress{TotalBytes: total, PieceCount: pieceCount}

	pieces := make([]byte, 0, pieceCount*sha1.Size)
	buf := make([]byte, 0, plen)
	flush := func() {
		sum := sha1.Sum(buf)
		pieces = append(pieces, sum[:]...)
		buf = buf[:0]
		prog.PiecesDone++
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		prog.CurrentFile = strings.Join(f.Rel, "/")
		if f.Size == 0 {
			continue // zero-length files occupy list entries but no piece bytes
		}
		fh, err := os.Open(f.Abs)
		if err != nil {
			return Result{}, err
		}
		fileErr := func() error {
			chunk := make([]byte, readChunk)
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				n, rerr := fh.Read(chunk)
				for _, b := range chunk[:n] {
					buf = append(buf, b)
					if int64(len(buf)) == plen {
						flush()
					}
				}
				prog.BytesHashed += int64(n)
				if onProgress != nil {
					onProgress(prog)
				}
				if rerr == io.EOF {
					return nil
				}
				if rerr != nil {
					return rerr
				}
			}
		}()
		cerr := fh.Close()
		if fileErr != nil {
			return Result{}, fmt.Errorf("read %s: %w", f.Abs, fileErr)
		}
		if cerr != nil {
			return Result{}, fmt.Errorf("read %s: %w", f.Abs, cerr)
		}
	}
	if len(buf) > 0 {
		flush()
	}
	if len(pieces) != pieceCount*sha1.Size {
		return Result{}, fmt.Errorf("internal error: hashed %d pieces, want %d", len(pieces)/sha1.Size, pieceCount)
	}

	info := map[string]any{
		"name":         name,
		"piece length": plen,
		"pieces":       string(pieces),
	}
	if multi {
		entries := make([]any, 0, len(files))
		for _, f := range files {
			comps := make([]any, 0, len(f.Rel))
			for _, c := range f.Rel {
				comps = append(comps, c)
			}
			entries = append(entries, map[string]any{"length": f.Size, "path": comps})
		}
		info["files"] = entries
	} else {
		info["length"] = files[0].Size
	}
	if in.Private {
		info["private"] = int64(1)
	}
	if in.SourceTag != "" {
		info["source"] = in.SourceTag
	}
	top := map[string]any{"info": info}
	if len(in.Trackers) > 0 {
		top["announce"] = in.Trackers[0]
		if len(in.Trackers) > 1 {
			tiers := make([]any, 0, len(in.Trackers))
			for _, u := range in.Trackers[1:] {
				tiers = append(tiers, []any{u})
			}
			top["announce-list"] = tiers
		}
	}
	if in.Comment != "" {
		top["comment"] = in.Comment
	}
	top["created by"] = "Blackbird"
	data := encode(top)
	sum := sha1.Sum(encode(info))
	return Result{
		Data:        data,
		Infohash:    fmt.Sprintf("%x", sum),
		Name:        name,
		TotalBytes:  total,
		FileCount:   len(files),
		PieceLength: plen,
		PieceCount:  pieceCount,
		Private:     in.Private,
	}, nil
}

// encode renders a metainfo value canonically: dict keys sort
// lexicographically (byte order for ASCII keys) so every client derives the
// same infohash.
func encode(v any) []byte {
	switch val := v.(type) {
	case int:
		return []byte("i" + itoa(int64(val)) + "e")
	case int64:
		return []byte("i" + itoa(val) + "e")
	case string:
		return []byte(itoa(int64(len(val))) + ":" + val)
	case []any:
		var buf []byte
		buf = append(buf, 'l')
		for _, item := range val {
			buf = append(buf, encode(item)...)
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
			buf = append(buf, encode(k)...)
			buf = append(buf, encode(val[k])...)
		}
		return append(buf, 'e')
	default:
		return nil
	}
}

func itoa(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
		if n == 0 {
			break
		}
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
