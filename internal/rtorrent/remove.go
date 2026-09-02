package rtorrent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"blackbird/internal/scgi/xmlrpc"
)

// ErrPathOutsideDownloadDirs is returned when a torrent's base path resolves
// outside every configured download directory — a defense against corrupted
// or malicious base paths before any filesystem deletion happens.
var ErrPathOutsideDownloadDirs = errors.New("path is outside the configured download directories")

// CheckWithin reports whether path is inside at least one of the allowed
// directories (lexically, after symlink evaluation).
func CheckWithin(path string, allowedDirs []string) error {
	if path == "" {
		return fmt.Errorf("%w: empty path", ErrPathOutsideDownloadDirs)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", path, err)
	}
	// Best-effort symlink resolution; a missing path still resolves its
	// lexical parent chain.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		if resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(resolvedParent, filepath.Base(abs))
		}
	}
	for _, dir := range allowedDirs {
		if dir == "" {
			continue
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
			absDir = resolved
		}
		rel, err := filepath.Rel(absDir, abs)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("%w: %q not under %v", ErrPathOutsideDownloadDirs, path, allowedDirs)
}

// RemoveWithData erases the torrent from the daemon and deletes its files,
// refusing paths outside allowedDirs first. It returns the base path that
// was acted on so callers can report it.
func (c *Client) RemoveWithData(ctx context.Context, hash string, allowedDirs []string) (string, error) {
	basePath, err := c.torrentBasePath(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("resolve base path for %s: %w", hash, err)
	}
	if err := CheckWithin(basePath, allowedDirs); err != nil {
		return basePath, err
	}

	files, fileErr := c.Files(ctx, hash)
	if err := c.Erase(ctx, hash); err != nil {
		return basePath, err
	}

	// Delete after erase: single-file torrents remove the file itself,
	// multi-file torrents remove the base directory.
	if fileErr == nil && len(files) > 1 {
		err = os.RemoveAll(basePath)
	} else {
		err = removePath(basePath)
	}
	if err != nil {
		return basePath, fmt.Errorf("erase succeeded but data removal failed for %s: %w", hash, err)
	}
	return basePath, nil
}

// removePath deletes a file or (only if it is a directory) its tree.
func removePath(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// torrentBasePath reads d.base_path for one torrent.
func (c *Client) torrentBasePath(ctx context.Context, hash string) (string, error) {
	res, err := c.scgi.Call(ctx, "d.base_path=", []xmlrpc.Value{{Type: "string", Str: hash}})
	if err != nil {
		return "", err
	}
	if len(res) == 0 {
		return "", errors.New("empty d.base_path response")
	}
	return sval(res[0]), nil
}

// BasePath reads d.base_path for one torrent (exported for the move-data API).
func (c *Client) BasePath(ctx context.Context, hash string) (string, error) {
	return c.torrentBasePath(ctx, hash)
}
