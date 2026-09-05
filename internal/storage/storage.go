// Package storage builds conservative, read-only filesystem capacity forecasts.
package storage

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const MaxBytes int64 = 1 << 52 // exact integer arithmetic in browser clients
const MaxFiles = 20000

type Pool struct {
	ID        string   `json:"id"`
	Mount     string   `json:"-"` // rename domain; separate from shared capacity identity
	Paths     []string `json:"paths"`
	Total     int64    `json:"totalBytes"`
	Free      int64    `json:"freeBytes"`
	Reserve   int64    `json:"reserveBytes"`
	Lower     int64    `json:"additionalLowerBytes"`
	Upper     *int64   `json:"additionalUpperBytes"`
	Peak      *int64   `json:"peakUsedBytes"`
	After     *int64   `json:"freeAfterBytes"`
	Status    string   `json:"status"`
	PeakCause string   `json:"peakCause"`
}
type Operation struct {
	Pool        string `json:"pool"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Logical     int64  `json:"logicalBytes"`
	Allocated   int64  `json:"allocatedBytes"`
	Lower       int64  `json:"lowerBytes"`
	Upper       *int64 `json:"upperBytes"`
	Note        string `json:"note"`
}
type Plan struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	ExpiresAt   time.Time   `json:"expiresAt"`
	Pools       []Pool      `json:"pools"`
	Operations  []Operation `json:"operations"`
	Unknown     []string    `json:"unknown"`
	Coverage    []string    `json:"coverage"`
	Status      string      `json:"status"`
	Signature   string      `json:"signature"`
}
type File struct {
	Path     string
	Size     int64
	Selected bool
}
type mountPoint struct{ id, path string }

type Inspector struct {
	mounts       []mountPoint
	mountsLoaded bool
	Roots        []string
	Count        int
}

func Number(n int64) *int64 { return &n }
func Add(a, b int64) int64 {
	if b > MaxBytes-a {
		return MaxBytes
	}
	return a + b
}
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Resolve permits a missing suffix only beneath an existing configured root.
// Resolve the deepest existing ancestor before authorization so a symlink in
// a missing path's ancestry cannot escape into another filesystem.
func (in *Inspector) Resolve(path string) (Pool, error) {
	abs, err := filepath.Abs(path)
	if err != nil || path == "" {
		return Pool{}, fmt.Errorf("path is unavailable")
	}
	// A missing explicitly configured nested root must not silently borrow
	// the parent's free-space pool (for example an unavailable mount).
	closest := ""
	for _, root := range in.Roots {
		candidate, e := filepath.Abs(root)
		if e == nil && inside(candidate, abs) && len(candidate) > len(closest) {
			closest = candidate
		}
	}
	if closest != "" {
		if _, e := os.Stat(closest); e != nil {
			return Pool{}, fmt.Errorf("configured root unavailable")
		}
	}
	ancestor := abs
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return Pool{}, err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return Pool{}, err
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return Pool{}, err
	}
	suffix, _ := filepath.Rel(ancestor, abs)
	full := filepath.Join(resolved, suffix)
	allowed := false
	for _, root := range in.Roots {
		actual, e := filepath.EvalSymlinks(root)
		if e != nil {
			continue
		}
		actual, _ = filepath.Abs(actual)
		if inside(actual, full) && inside(actual, resolved) {
			allowed = true
			break
		}
	}
	if !allowed {
		return Pool{}, fmt.Errorf("path is outside available configured download roots")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Pool{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Pool{}, fmt.Errorf("filesystem identity unavailable")
	}
	var disk syscall.Statfs_t
	if err = syscall.Statfs(resolved, &disk); err != nil {
		return Pool{}, err
	}
	total, free := uint64(disk.Blocks)*uint64(disk.Bsize), uint64(disk.Bavail)*uint64(disk.Bsize)
	if total > uint64(MaxBytes) || free > uint64(MaxBytes) {
		return Pool{}, fmt.Errorf("filesystem exceeds supported byte bound")
	}
	return Pool{ID: fmt.Sprint(st.Dev), Mount: in.mount(resolved, &disk), Paths: []string{path}, Total: int64(total), Free: int64(free)}, nil
}

// Allocation only credits an existing regular file's allocated blocks within
// its expected logical extent. Sparse holes still need space. Shared links and
// unexpected size/type are not credited because ownership is not established.
func (in *Inspector) Allocation(ctx context.Context, path string, size int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	in.Count++
	if in.Count > MaxFiles {
		return 0, fmt.Errorf("file inspection limit reached")
	}
	if _, err := in.Resolve(path); err != nil {
		return 0, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("non-regular file or symlink; allocation unknown")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink > 1 || info.Size() > size {
		return 0, fmt.Errorf("shared or unexpected file layout; allocation credit unknown")
	}
	return min(size, info.Size(), max(0, int64(st.Blocks)*512)), nil
}

// CopySize follows the move engine: logical file bytes are copied, including
// sparse holes and each hardlink path, while symlinks are refused. No source
// deletion credit is taken before destination copying and verification finish.
func (in *Inspector) CopySize(ctx context.Context, path string) (int64, int64, error) {
	pool, err := in.Resolve(path)
	if err != nil {
		return 0, 0, err
	}
	var logical, allocated int64
	seen := map[string]bool{}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		in.Count++
		if in.Count > MaxFiles {
			return fmt.Errorf("file inspection limit reached")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("move contains symlink or special file")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || fmt.Sprint(st.Dev) != pool.ID {
			return fmt.Errorf("move source spans filesystems")
		}
		logical = Add(logical, info.Size())
		key := fmt.Sprintf("%d:%d", st.Dev, st.Ino)
		if !seen[key] {
			allocated = Add(allocated, max(0, st.Blocks*512))
			seen[key] = true
		}
		return nil
	})
	return logical, allocated, err
}

func NewPlan() *Plan {
	now := time.Now()
	return &Plan{GeneratedAt: now, ExpiresAt: now.Add(30 * time.Second), Pools: []Pool{}, Operations: []Operation{}, Unknown: []string{}, Coverage: []string{
		"Advisory filesystem data-growth bounds, not a reservation. External writers, quotas, metadata, filesystem snapshots, compression and copy-on-write can change required space; reserve is headroom, not a guarantee.",
		"Existing free space already excludes allocated blocks. Allocation credit assumes ordinary in-place writes to inspected regular files; hardlinks, unexpected layouts and missing metadata are uncertain.",
		"Peak retains all modeled downloads, extraction output and destination copies together, without assuming source deletion or job ordering. This conservative overlap can exceed a serialized plan's peak.",
		"Only Blackbird's visible session and jobs are included. Inspection is bounded to 20,000 entries and the request deadline; unavailable evidence is reported as unknown, never zero demand.",
	}}
}
func (p *Plan) Include(pool Pool, reserve int64) {
	for i := range p.Pools {
		if p.Pools[i].ID == pool.ID {
			p.Pools[i].Free = min(p.Pools[i].Free, pool.Free)
			for _, path := range pool.Paths {
				found := false
				for _, old := range p.Pools[i].Paths {
					if old == path {
						found = true
					}
				}
				if !found {
					p.Pools[i].Paths = append(p.Pools[i].Paths, path)
				}
			}
			return
		}
	}
	pool.Reserve = reserve
	p.Pools = append(p.Pools, pool)
}
func (p *Plan) Append(pool Pool, reserve int64, op Operation) {
	p.Include(pool, reserve)
	op.Pool = pool.ID
	p.Operations = append(p.Operations, op)
}
func (p *Plan) Finish() {
	p.Status = "within_bound"
	for i := range p.Pools {
		pool := &p.Pools[i]
		upper := int64(0)
		unknown := false
		largest := int64(-1)
		for _, op := range p.Operations {
			if op.Pool != pool.ID {
				continue
			}
			pool.Lower = Add(pool.Lower, op.Lower)
			if op.Upper == nil {
				unknown = true
				pool.PeakCause = op.Description
			} else {
				upper = Add(upper, *op.Upper)
				if *op.Upper > largest && !unknown {
					largest = *op.Upper
					pool.PeakCause = op.Description
				}
			}
		}
		if upper >= MaxBytes || pool.Lower >= MaxBytes {
			unknown = true
			p.Unknown = append(p.Unknown, "Summed demand reached the supported byte bound; the upper estimate is unknown.")
		}
		pool.Status = "within_bound"
		if !unknown {
			pool.Upper = Number(upper)
			pool.Peak = Number(Add(pool.Total-pool.Free, upper))
			pool.After = Number(pool.Free - upper)
			if upper > pool.Free-pool.Reserve {
				pool.Status = "at_risk"
			}
		} else {
			pool.Status = "unknown"
		}
		if pool.Lower > pool.Free-pool.Reserve {
			pool.Status = "insufficient"
		}
		if pool.Status != "within_bound" {
			p.Status = "review"
		}
	}
	if len(p.Unknown) > 0 {
		p.Status = "review"
	}
	sort.Slice(p.Pools, func(i, j int) bool { return p.Pools[i].ID < p.Pools[j].ID })
}

// MoveDemand separates the shared capacity pool from the rename domain. Linux
// bind mounts can have the same device number but require an EXDEV copy.
func MoveDemand(source, target Pool, logical, allocated int64) Operation {
	op := Operation{Description: "Copy before delete", Logical: logical, Allocated: allocated, Lower: logical, Upper: Number(logical), Note: "A full logical destination copy remains alongside source data until verification finishes. Sparse holes are materialized by the move engine."}
	if source.ID == target.ID {
		if source.Mount != "" && source.Mount == target.Mount {
			op.Description = "Same-filesystem rename"
			op.Lower = 0
			op.Upper = Number(0)
			op.Note = "Same device and mount: zero additional modeled file-data copy; reserve covers metadata overhead."
		} else if source.Mount == "" || target.Mount == "" {
			op.Description = "Rename or mount-boundary copy"
			op.Lower = 0
			op.Note = "Capacity is shared, but rename-domain identity is unknown. Upper bound includes a full copy fallback."
		} else {
			op.Description = "Copy across mount boundaries"
			op.Note = "Mounts share the same capacity pool but rename crosses mount boundaries. Budget the full logical copy before source deletion."
		}
	}
	return op
}
