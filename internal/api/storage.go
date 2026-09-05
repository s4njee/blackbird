package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
	"blackbird/internal/storage"
	"blackbird/internal/torrentfile"
)

var forecastSlots = make(chan struct{}, 2)

// Forecast is intentionally independent of the poll loop. It reads published
// caches and bounded local filesystem evidence, never fetching URLs or peers.
func (s *Server) storageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opts.Store == nil || s.opts.Poller == nil {
		writeAPIError(w, 503, "unavailable", "storage forecast requires a configured session")
		return
	}
	select {
	case forecastSlots <- struct{}{}:
		defer func() { <-forecastSlots }()
	default:
		writeAPIError(w, 503, "busy", "another storage inspection is running; try again")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeAPIError(w, 400, "bad_request", "invalid or oversized forecast input")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	number := func(key string) (*int64, error) {
		raw := r.FormValue(key)
		if raw == "" {
			return nil, nil
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 || v > storage.MaxBytes {
			return nil, fmt.Errorf("%s must be a nonnegative byte count up to 2^52", key)
		}
		return &v, nil
	}
	reserve, err := number("reserve_bytes")
	if err != nil {
		writeAPIError(w, 400, "bad_request", err.Error())
		return
	}
	if reserve == nil {
		reserve = storage.Number(0)
	}
	unknown, err := number("unknown_bytes")
	if err != nil {
		writeAPIError(w, 400, "bad_request", err.Error())
		return
	}
	expansion, err := number("extraction_bytes")
	if err != nil {
		writeAPIError(w, 400, "bad_request", err.Error())
		return
	}
	kind := r.FormValue("kind")
	if kind != "add" && kind != "move" {
		writeAPIError(w, 400, "bad_request", "kind must be add or move")
		return
	}
	plan := storage.NewPlan()
	in := &storage.Inspector{Roots: s.opts.Store.DownloadDirs()}
	cfg := s.opts.Store.Get()
	snap := s.opts.Poller.Snapshot()
	if snap.Stale || snap.Status != poller.StatusConnected || snap.GeneratedAt.IsZero() || time.Since(snap.GeneratedAt) > max(30*time.Second, 3*cfg.Poll.EffectiveMaxInterval()) {
		plan.Unknown = append(plan.Unknown, "Session data is stale or disconnected; pending writes may be missing.")
	}
	dest := r.FormValue("destination")
	if dest == "" {
		dest = cfg.Directories.Default
	}
	destination, err := in.Resolve(dest)
	if err != nil {
		plan.Unknown = append(plan.Unknown, "Destination unavailable: "+err.Error())
	} else {
		plan.Include(destination, *reserve)
	}
	addOp := func(path string, op storage.Operation) {
		pool, err := in.Resolve(path)
		if err != nil {
			plan.Unknown = append(plan.Unknown, op.Description+": filesystem unavailable ("+path+").")
			return
		}
		op.Path = path
		plan.Append(pool, *reserve, op)
	}
	var futureLogical int64
	futureUnknown := false
	futureCopyUnknown := false
	sourcePools := map[string]storage.Pool{}
	addFuture := func(path string, logical int64) {
		futureLogical = storage.Add(futureLogical, logical)
		if pool, err := in.Resolve(path); err == nil {
			sourcePools[pool.ID+"|"+pool.Mount] = pool
		} else {
			futureUnknown = true
		}
	}
	selected := map[string]bool{}
	for _, hash := range r.Form["hashes"] {
		selected[hash] = true
	}
	if len(selected) > 256 {
		writeAPIError(w, 400, "bad_request", "forecast accepts at most 256 selected torrents")
		return
	}
	if kind == "move" && len(selected) == 0 {
		writeAPIError(w, 400, "bad_request", "select torrents to move")
		return
	}
	mode := r.FormValue("mode")
	if kind == "move" && mode != "move_files" && mode != "set_directory" {
		writeAPIError(w, 400, "bad_request", "invalid move mode")
		return
	}
	found := map[string]bool{}
	copiedLogical := map[string]int64{}
	processed := 0
	// The operator's selected move targets get the bounded inspection budget
	// before unrelated background downloads in large sessions.
	rows := make([]rtorrent.Torrent, 0, len(snap.Torrents))
	for _, t := range snap.Torrents {
		if selected[t.Hash] {
			rows = append(rows, t)
		}
	}
	for _, t := range snap.Torrents {
		if !selected[t.Hash] {
			rows = append(rows, t)
		}
	}
	for _, t := range rows {
		if !selected[t.Hash] && t.Complete {
			continue
		}
		if processed >= 256 || ctx.Err() != nil {
			plan.Unknown = append(plan.Unknown, "Remaining session rows exceed the 256-torrent or time inspection bound; their disk demand is unknown.")
			futureUnknown = true
			break
		}
		processed++
		if selected[t.Hash] {
			found[t.Hash] = true
			if mode == "set_directory" {
				addOp(dest, storage.Operation{Description: "Set directory only · " + t.Hash, Upper: storage.Number(0), Note: "No files are copied by this operation. Later writes at the new path are included below."})
			} else {
				source, sourceErr := in.Resolve(t.BasePath)
				if sourceErr == nil {
					plan.Include(source, *reserve)
				}
				copyBytes, allocated, copyErr := in.CopySize(ctx, t.BasePath)
				if sourceErr != nil || copyErr != nil {
					plan.Unknown = append(plan.Unknown, "Move source could not be fully inspected: "+t.Hash)
					addOp(dest, storage.Operation{Description: "Move copy · " + t.Hash, Upper: nil})
				} else {
					move := storage.MoveDemand(source, destination, copyBytes, allocated)
					move.Description += " · " + t.Hash
					if move.Upper != nil && *move.Upper > 0 && move.Lower > 0 {
						if info, e := os.Lstat(t.BasePath); !t.IsMultiFile && e == nil && info.Mode().IsRegular() {
							copiedLogical[t.Hash] = copyBytes
						}
					}
					addOp(dest, move)
				}
			}
		}
		if !t.Complete {
			path := t.BasePath
			op := s.remainingAllocation(ctx, in, t)
			if selected[t.Hash] {
				path = filepath.Join(dest, filepath.Base(t.BasePath))
				if copied, ok := copiedLogical[t.Hash]; ok {
					op.Allocated = 0
					op.Upper = storage.Number(max(0, t.SizeBytes-copied))
					op.Note = "Future growth after the destination logical copy. Copied bytes are not reserved twice."
				}
				if mode == "set_directory" {
					op = storage.Operation{Description: "Later writes after directory change · " + t.Hash, Logical: t.SizeBytes, Upper: storage.Number(max(0, t.SizeBytes)), Note: "The new directory is not verified as containing torrent data; no old-path allocation credit is transferred."}
				}
			}
			addOp(path, op)
			addFuture(path, max(0, t.SizeBytes))
			if t.IsMultiFile {
				futureCopyUnknown = true
			}
		}
	}
	for hash := range selected {
		if !found[hash] {
			plan.Unknown = append(plan.Unknown, "Selected torrent is not in the inspected session: "+hash)
		}
	}
	if kind == "add" {
		unresolved := 0
		items := 0
		for _, line := range strings.Split(r.FormValue("magnets"), "\n") {
			if strings.TrimSpace(line) != "" {
				unresolved++
				items++
			}
		}
		seen := map[string]bool{}
		for _, fh := range r.MultipartForm.File["files"] {
			items++
			if items > 128 {
				writeAPIError(w, 400, "bad_request", "forecast accepts at most 128 intake items")
				return
			}
			file, e := fh.Open()
			if e != nil {
				unresolved++
				continue
			}
			data, e := io.ReadAll(io.LimitReader(file, 16<<20))
			file.Close()
			if e != nil {
				unresolved++
				continue
			}
			sum := sha256.Sum256(data)
			key := hex.EncodeToString(sum[:])
			if seen[key] {
				plan.Unknown = append(plan.Unknown, "Duplicate torrent metadata in the batch; load behavior is not forecast.")
				continue
			}
			seen[key] = true
			layout, e := torrentfile.ParseLayout(data)
			if e != nil {
				unresolved++
				plan.Unknown = append(plan.Unknown, "One torrent's file layout is unavailable: "+e.Error())
				continue
			}
			if meta, e := torrentfile.Parse(data); e == nil {
				exists := false
				for _, t := range snap.Torrents {
					if strings.EqualFold(t.Hash, meta.Infohash) {
						exists = true
						break
					}
				}
				if exists {
					addOp(dest, storage.Operation{Description: "Already in session · " + layout.Name, Upper: storage.Number(0), Note: "Existing torrent demand is included in the session forecast, not added twice."})
					continue
				}
			}
			base := dest
			if layout.Multi {
				base = filepath.Join(base, layout.Name)
			}
			var credit int64
			uncertain := false
			for _, f := range layout.Files {
				allocated, e := in.Allocation(ctx, filepath.Join(base, f.Path), f.Size)
				if e != nil {
					uncertain = true
				} else {
					credit = storage.Add(credit, allocated)
				}
			}
			note := "All files in this intake are selected; no skipped-file piece overhead. Existing allocated blocks are credited once within each file."
			if uncertain {
				note += " Some allocation credit is unavailable; the upper estimate reserves those bytes again as a conservative bound."
			}
			addOp(dest, storage.Operation{Description: "Batch download · " + layout.Name, Logical: layout.Total, Allocated: credit, Upper: storage.Number(max(0, layout.Total-credit)), Note: note})
			dataPath := filepath.Join(dest, layout.Name)
			addFuture(dataPath, layout.Total)
			if layout.Multi {
				if _, e := os.Stat(dataPath); e == nil {
					futureCopyUnknown = true
				}
			}
		}
		if items == 0 || items > 128 {
			writeAPIError(w, 400, "bad_request", "forecast requires 1–128 intake items")
			return
		}
		if unresolved > 0 {
			note := fmt.Sprintf("%d magnets, URLs or unsupported metadata items have unknown sizes. URLs are not fetched during preview.", unresolved)
			if unknown != nil {
				note += " Upper bound is the user's combined additional-download assumption, not measured metadata."
				addFuture(dest, *unknown)
			} else {
				futureUnknown = true
			}
			addOp(dest, storage.Operation{Description: "Unknown intake sizes", Upper: unknown, Note: note})
		}
	}
	// Running copies still occupy source and destination simultaneously. Do not
	// subtract a partially written destination of unknown job ownership.
	s.moveMu.Lock()
	jobs := []*moveJob{}
	for _, job := range s.moves {
		if job.Status == "running" {
			jobs = append(jobs, s.copyMoveJobLocked(job))
		}
	}
	s.moveMu.Unlock()
	for _, job := range jobs {
		for _, result := range job.Results {
			if result.Status != "pending" && result.Status != "moving" {
				continue
			}
			if job.Mode == setDirectoryOnly {
				continue
			}
			var source string
			for _, t := range snap.Torrents {
				if t.Hash == result.Hash {
					source = t.BasePath
					break
				}
			}
			bytes, allocated, e := in.CopySize(ctx, source)
			from, fe := in.Resolve(source)
			if fe == nil {
				plan.Include(from, *reserve)
			}
			to, te := in.Resolve(job.Destination)
			op := storage.Operation{Description: "Active move · " + result.Hash, Logical: bytes, Allocated: allocated, Upper: storage.Number(bytes), Note: "Remaining copy is bounded by the full source logical size; partial destination allocation is not credited."}
			if e != nil || fe != nil || te != nil {
				op.Upper = nil
			} else {
				op.Upper = storage.MoveDemand(from, to, bytes, allocated).Upper
			}
			addOp(job.Destination, op)
		}
	}
	// Future rule branches are alternatives, not asserted matches. Reserve each
	// possible destination conservatively; do not invent archive expansion ratios.
	paths := map[string]bool{}
	for _, rule := range cfg.Automation.OnComplete {
		if rule.MoveTo != "" {
			paths[rule.MoveTo] = true
		}
	}
	for path := range paths {
		pool, e := in.Resolve(path)
		if e != nil {
			plan.Unknown = append(plan.Unknown, "Completion-move destination unavailable: "+path)
			continue
		}
		cross := futureUnknown
		for _, source := range sourcePools {
			if source.ID != pool.ID || source.Mount == "" || source.Mount != pool.Mount {
				cross = true
			}
		}
		upper := storage.Number(0)
		if cross {
			upper = storage.Number(futureLogical)
			if futureUnknown || futureCopyUnknown || len(cfg.Automation.Unpack.Rules) > 0 {
				upper = nil
			}
		}
		addOp(path, storage.Operation{Description: "Possible completion-rule copy", Logical: futureLogical, Upper: upper, Note: "Conservative alternative across saved move rules; rule matching and future ordering are not assumed. Source data remains present through copying. Final directory contents and extraction ordering can make the bound unknown."})
	}
	extraction := map[string]bool{}
	if expansion != nil {
		extraction[dest] = true
	}
	if futureLogical > 0 || futureUnknown {
		for _, rule := range cfg.Automation.Unpack.Rules {
			if rule.Destination != "" {
				extraction[rule.Destination] = true
			} else {
				for _, pool := range sourcePools {
					extraction[pool.Paths[0]] = true
				}
				for path := range paths {
					extraction[path] = true
				}
			}
		}
	}
	if s.opts.Unpack != nil {
		status := s.opts.Unpack.Status()
		for _, job := range status.Jobs {
			extraction[job.DestDir] = true
		}
		if status.Queue > 0 {
			plan.Unknown = append(plan.Unknown, "Queued extraction destinations and expansion sizes are not available.")
		}
	}
	extractionPools := map[string]bool{}
	extractionPaths := make([]string, 0, len(extraction))
	for path := range extraction {
		extractionPaths = append(extractionPaths, path)
	}
	sort.Strings(extractionPaths)
	for _, path := range extractionPaths {
		pool, e := in.Resolve(path)
		if e == nil && extractionPools[pool.ID] {
			continue
		}
		if e == nil {
			extractionPools[pool.ID] = true
		}
		addOp(path, storage.Operation{Description: "Possible or active extraction output", Upper: expansion, Note: "Archive expansion is unbounded without metadata. An entered bound is the user's combined remaining extraction-output assumption for each affected filesystem; archives are retained at peak."})
	}
	plan.Finish()
	// Review identity follows operation structure and capacity verdicts, not
	// ordinary progress/free-byte fluctuations. Every submit still re-inspects.
	sort.Slice(plan.Operations, func(i, j int) bool {
		a, b := plan.Operations[i], plan.Operations[j]
		return a.Pool+a.Description+a.Path < b.Pool+b.Description+b.Path
	})
	sort.Strings(plan.Unknown)
	parts := []string{}
	for _, pool := range plan.Pools {
		parts = append(parts, fmt.Sprint(pool.ID, pool.Status, pool.Reserve))
	}
	for _, op := range plan.Operations {
		parts = append(parts, fmt.Sprint(op.Pool, op.Path, op.Description, op.Logical, op.Upper == nil))
	}
	encoded, _ := json.Marshal(struct {
		Parts   []string
		Unknown []string
		Config  any
	}{parts, plan.Unknown, cfg})
	sum := sha256.Sum256(encoded)
	plan.Signature = hex.EncodeToString(sum[:])
	writeJSON(w, 200, plan)
}

func (s *Server) remainingAllocation(ctx context.Context, in *storage.Inspector, t rtorrent.Torrent) storage.Operation {
	op := storage.Operation{Description: "Remaining session download · " + t.Hash, Logical: max(0, t.SizeBytes), Upper: storage.Number(max(0, t.SizeBytes)), Note: "Includes stopped/incomplete torrents. File selection or allocation layout is unavailable; range is zero to full logical data, not a claim of additional unallocated bytes."}
	if t.SizeBytes < 0 || t.SizeBytes > storage.MaxBytes {
		op.Upper = nil
		return op
	}
	if !t.IsMultiFile {
		credit, err := in.Allocation(ctx, t.BasePath, t.SizeBytes)
		if err == nil {
			op.Allocated = credit
			op.Upper = storage.Number(max(0, t.SizeBytes-credit))
			op.Note = "Inspected single-file allocation. Logical completion counters are not subtracted from free space."
		}
		return op
	}
	detail, ok := s.opts.Poller.Detail(t.Hash)
	if !ok || len(detail.Files) == 0 {
		return op
	}
	sizes := []int64{}
	selected := []bool{}
	var credit, total int64
	for _, file := range detail.Files {
		sizes = append(sizes, file.SizeBytes)
		selected = append(selected, file.Priority > 0)
		total = storage.Add(total, max(0, file.SizeBytes))
		if file.Priority > 0 {
			path := filepath.Join(t.BasePath, file.Path)
			if !safeRelative(file.Path) {
				return op
			}
			n, err := in.Allocation(ctx, path, file.SizeBytes)
			if err == nil {
				credit = storage.Add(credit, n)
			}
		}
	}
	if total != t.SizeBytes {
		return op
	}
	logical, withPieces, err := torrentfile.SelectedBytes(sizes, selected, detail.Transfer.ChunkSize)
	if err != nil {
		return op
	}
	op.Logical = logical
	op.Allocated = credit
	op.Upper = storage.Number(max(0, withPieces-credit))
	op.Note = fmt.Sprintf("Cached file priorities: %d selected logical bytes plus %d piece-boundary bytes. Cached priorities may have changed externally; skipped-file boundary allocation is not credited.", logical, withPieces-logical)
	return op
}
func safeRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && path != ".." && !strings.HasPrefix(filepath.Clean(path), ".."+string(filepath.Separator))
}
