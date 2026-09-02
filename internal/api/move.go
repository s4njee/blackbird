package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"

	"blackbird/internal/rtorrent"
)

type moveMode string

const (
	moveFiles        moveMode = "move_files"
	setDirectoryOnly moveMode = "set_directory"
)

type moveRequest struct {
	Hashes      []string `json:"hashes"`
	Destination string   `json:"destination"`
	Mode        moveMode `json:"mode"`
}

type moveResult struct {
	Hash   string `json:"hash"`
	Status string `json:"status"` // pending | moving | completed | failed | cancelled
	Error  string `json:"error,omitempty"`
}

type moveJob struct {
	ID          string       `json:"id"`
	Mode        moveMode     `json:"mode"`
	Destination string       `json:"destination"`
	Status      string       `json:"status"` // running | completed | cancelled
	Results     []moveResult `json:"results"`
	cancel      context.CancelFunc
}

type directoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type directoryResponse struct {
	Roots   []string         `json:"roots"`
	Path    string           `json:"path"`
	Parent  string           `json:"parent,omitempty"`
	Entries []directoryEntry `json:"entries"`
}

func (s *Server) directoryHandler(w http.ResponseWriter, r *http.Request) {
	roots := uniqueDirs(s.opts.Store.DownloadDirs())
	if len(roots) == 0 {
		writeAPIError(w, http.StatusBadRequest, "no_download_roots", "no download roots are configured")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = roots[0]
	}
	if err := rtorrent.CheckWithin(path, roots); err != nil {
		writeAPIError(w, http.StatusBadRequest, "path_outside_download_dirs", "directory browser is limited to configured download roots: "+err.Error())
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "directory_unavailable", err.Error())
		return
	}
	result := directoryResponse{Roots: roots, Path: path}
	if parent := filepath.Dir(path); parent != path && rtorrent.CheckWithin(parent, roots) == nil {
		result.Parent = parent
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if rtorrent.CheckWithin(child, roots) == nil {
			result.Entries = append(result.Entries, directoryEntry{Name: entry.Name(), Path: child})
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		return strings.ToLower(result.Entries[i].Name) < strings.ToLower(result.Entries[j].Name)
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) moveStartHandler(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Hashes) == 0 || strings.TrimSpace(req.Destination) == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "hashes and destination are required")
		return
	}
	if req.Mode == "" {
		req.Mode = moveFiles
	}
	if req.Mode != moveFiles && req.Mode != setDirectoryOnly {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "mode must be move_files or set_directory")
		return
	}
	if err := rtorrent.CheckWithin(req.Destination, s.opts.Store.DownloadDirs()); err != nil {
		writeAPIError(w, http.StatusBadRequest, "path_outside_download_dirs", err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := fmt.Sprintf("move-%d", atomic.AddUint64(&s.moveSeq, 1))
	job := &moveJob{ID: id, Mode: req.Mode, Destination: req.Destination, Status: "running", cancel: cancel, Results: make([]moveResult, len(req.Hashes))}
	for i, hash := range req.Hashes {
		job.Results[i] = moveResult{Hash: hash, Status: "pending"}
	}
	s.moveMu.Lock()
	s.moves[id] = job
	s.moveMu.Unlock()
	go s.runMoveJob(ctx, job)
	writeJSON(w, http.StatusAccepted, s.copyMoveJob(job))
}

func (s *Server) moveStatusHandler(w http.ResponseWriter, r *http.Request) {
	job := s.lookupMoveJob(r.PathValue("id"))
	if job == nil {
		writeAPIError(w, http.StatusNotFound, "move_not_found", "move job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) moveCancelHandler(w http.ResponseWriter, r *http.Request) {
	s.moveMu.Lock()
	job := s.moves[r.PathValue("id")]
	if job == nil {
		s.moveMu.Unlock()
		writeAPIError(w, http.StatusNotFound, "move_not_found", "move job not found")
		return
	}
	if job.Status == "running" {
		job.cancel()
	}
	copy := s.copyMoveJobLocked(job)
	s.moveMu.Unlock()
	writeJSON(w, http.StatusOK, copy)
}

func (s *Server) runMoveJob(ctx context.Context, job *moveJob) {
	for i := range job.Results {
		if ctx.Err() != nil {
			s.finishMoveJob(job, i, "cancelled", "")
			continue
		}
		s.updateMoveResult(job, i, "moving", "")
		err := s.moveTorrent(ctx, job.Results[i].Hash, job.Destination, job.Mode)
		if errors.Is(err, context.Canceled) {
			s.finishMoveJob(job, i, "cancelled", "")
		} else if err != nil {
			s.finishMoveJob(job, i, "failed", err.Error())
		} else {
			s.finishMoveJob(job, i, "completed", "")
		}
	}
	s.moveMu.Lock()
	if ctx.Err() != nil {
		job.Status = "cancelled"
	} else {
		job.Status = "completed"
	}
	s.moveMu.Unlock()
}

func (s *Server) updateMoveResult(job *moveJob, index int, status, detail string) {
	s.moveMu.Lock()
	job.Results[index].Status, job.Results[index].Error = status, detail
	s.moveMu.Unlock()
}

func (s *Server) finishMoveJob(job *moveJob, index int, status, detail string) {
	s.updateMoveResult(job, index, status, detail)
}

func (s *Server) lookupMoveJob(id string) *moveJob {
	s.moveMu.Lock()
	defer s.moveMu.Unlock()
	if job := s.moves[id]; job != nil {
		return s.copyMoveJobLocked(job)
	}
	return nil
}

func (s *Server) copyMoveJob(job *moveJob) *moveJob {
	s.moveMu.Lock()
	defer s.moveMu.Unlock()
	return s.copyMoveJobLocked(job)
}

func (s *Server) copyMoveJobLocked(job *moveJob) *moveJob {
	copy := *job
	copy.Results = append([]moveResult(nil), job.Results...)
	copy.cancel = nil
	return &copy
}

// moveTorrent preserves a torrent's prior running state. Running torrents are
// stopped before changing their directory and restarted after a successful
// operation; stopped torrents remain stopped.
func (s *Server) moveTorrent(ctx context.Context, hash, destination string, mode moveMode) (err error) {
	var torrentFound bool
	var running bool
	for _, torrent := range s.opts.Poller.Snapshot().Torrents {
		if torrent.Hash == hash {
			torrentFound = true
			running = torrent.State != rtorrent.StateStopped
			break
		}
	}
	if !torrentFound {
		return fmt.Errorf("torrent %s is not in the current session", hash)
	}
	roots := s.opts.Store.DownloadDirs()
	if err := rtorrent.CheckWithin(destination, roots); err != nil {
		return err
	}
	basePath, err := s.opts.RTorrent.BasePath(ctx, hash)
	if err != nil {
		return err
	}
	if err := rtorrent.CheckWithin(basePath, roots); err != nil {
		return err
	}
	if running {
		if err := s.opts.RTorrent.Stop(ctx, hash); err != nil {
			return fmt.Errorf("stop torrent before move: %w", err)
		}
		defer func() {
			if startErr := s.opts.RTorrent.Start(context.Background(), hash); startErr != nil && err == nil {
				err = fmt.Errorf("move completed but restart failed: %w", startErr)
			}
		}()
	}
	if mode == setDirectoryOnly {
		return s.opts.RTorrent.SetDirectory(ctx, hash, destination)
	}
	target := filepath.Join(destination, filepath.Base(basePath))
	if filepath.Clean(target) == filepath.Clean(basePath) {
		return errors.New("destination resolves to the same path")
	}
	if err := rtorrent.CheckWithin(target, roots); err != nil {
		return err
	}
	if err := movePath(ctx, basePath, target); err != nil {
		return err
	}
	if err := s.opts.RTorrent.SetDirectory(ctx, hash, target); err != nil {
		return fmt.Errorf("files moved but rTorrent directory update failed: %w", err)
	}
	return nil
}

func uniqueDirs(dirs []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		result = append(result, dir)
	}
	sort.Strings(result)
	return result
}

func movePath(ctx context.Context, source, target string) error {
	return movePathWithRename(ctx, source, target, os.Rename)
}

func movePathWithRename(ctx context.Context, source, target string, rename func(string, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to move a symlink source")
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("destination already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := rename(source, target); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyPath(ctx, source, target); err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	if err := verifyCopiedPath(ctx, source, target); err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	return os.RemoveAll(source)
}

func copyPath(ctx context.Context, source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(ctx, source, target, info.Mode())
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out := filepath.Join(target, rel)
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in move source: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(ctx, path, out, fileInfo.Mode())
	})
}

func copyFile(ctx context.Context, source, target string, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, &contextReader{ctx: ctx, reader: in})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func verifyCopiedPath(ctx context.Context, source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in move source: %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		return sameDigest(ctx, path, filepath.Join(target, rel))
	})
}

func sameDigest(ctx context.Context, left, right string) error {
	digest := func(path string) (string, error) {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		h := sha256.New()
		if _, err = io.Copy(h, &contextReader{ctx: ctx, reader: file}); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	a, err := digest(left)
	if err != nil {
		return err
	}
	b, err := digest(right)
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("copy verification failed for %s", left)
	}
	return nil
}
