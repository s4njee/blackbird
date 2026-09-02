// Command blackbird is a web console for rTorrent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"blackbird/internal/api"
	"blackbird/internal/config"
	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
	"blackbird/internal/tuning"
)

// Stamped at build time via -ldflags "-X main.version=...".
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	var (
		configPath     = flag.String("config", "config.yml", "path to YAML configuration file")
		checkConfig    = flag.Bool("check-config", false, "validate the config file and exit")
		showVersion    = flag.Bool("version", false, "print version info and exit")
		bootstrapDir   = flag.String("bootstrap", "", "create a first-run config and credentials in this directory")
		rotatePassword = flag.Bool("rotate-password", false, "replace bootstrap credentials (use with --bootstrap)")
	)
	flag.Parse()

	if *bootstrapDir != "" {
		result, err := config.Bootstrap(*bootstrapDir, *rotatePassword)
		if err != nil {
			fmt.Fprintf(os.Stderr, "blackbird: bootstrap failed: %v\n", err)
			os.Exit(1)
		}
		if !result.Created {
			fmt.Printf("Blackbird is already initialized at %s; no credentials were changed.\n", result.ConfigPath)
			return
		}
		fmt.Printf("Blackbird initialized.\nConfig: %s\nEnvironment: %s\nURL: http://127.0.0.1:8222/\nUsername: %s\nPassword (save it now; it will not be shown again): %s\n", result.ConfigPath, result.EnvPath, result.Username, result.Password)
		return
	}

	if *showVersion {
		fmt.Printf("blackbird %s (commit %s, built %s)\n", version, commit, buildDate)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blackbird: %v\n", err)
		os.Exit(1)
	}
	if *checkConfig {
		fmt.Printf("config %s OK\n", *configPath)
		return
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	// rTorrent integration (Epic 2): SCGI client, typed client, poller.
	rtc, err := rtorrent.New(cfg.RTorrent.SCGI, cfg.RTorrent.Timeout)
	if err != nil {
		logger.Error("invalid rtorrent endpoint", "err", err)
		os.Exit(1)
	}

	// Current config is swapped atomically on SIGHUP reloads.
	var cfgMu sync.RWMutex
	current := *cfg

	applyEntries := func(ctx context.Context, entries []tuning.Entry, cause string) {
		results := tuning.Apply(ctx, rtc, entries)
		for _, r := range results {
			if r.Err != nil {
				logger.Warn("tuning key failed", "cause", cause, "key", r.Key, "err", r.Err)
				continue
			}
			logger.Info("tuning key applied", "cause", cause, "key", r.Key)
		}
	}

	pollCtx, stopPoller := context.WithCancel(context.Background())
	defer stopPoller()
	poller := poller.New(rtc, poller.Options{
		Interval:       cfg.Poll.Interval,
		DetailInterval: cfg.Poll.DetailInterval,
		VolumeInterval: cfg.Poll.VolumeInterval,
		Volumes:        cfg.Volumes,
		OnConnect: func(ctx context.Context) {
			cfgMu.RLock()
			t := current.Tuning
			cfgMu.RUnlock()
			applyEntries(ctx, tuning.Entries(t), "connect")
		},
	})
	go poller.Run(pollCtx)

	// SIGHUP reloads the config and re-applies changed tuning keys without a
	// restart (per Epic 3.2; file-watch may be added later).
	sighups := make(chan os.Signal, 1)
	signal.Notify(sighups, syscall.SIGHUP)
	defer signal.Stop(sighups)
	go func() {
		for range sighups {
			loaded, err := config.Load(*configPath)
			if err != nil {
				logger.Warn("SIGHUP reload failed; keeping previous config", "err", err)
				continue
			}
			cfgMu.Lock()
			previous := current
			current = *loaded
			cfgMu.Unlock()
			logger.Info("SIGHUP config reloaded")
			changed := tuning.Diff(previous.Tuning, loaded.Tuning)
			if len(changed) > 0 {
				for _, e := range changed {
					logger.Info("tuning key changed on reload", "key", e.Key)
				}
				// Re-apply only the changed keys, per Epic 3.2.
				applyEntries(context.Background(), changed, "sighup")
			}
		}
	}()

	addr := cfg.Server.Listen
	auth := api.NewAuth(cfg.Auth.Username, cfg.Auth.PasswordHash, logger)
	apiSrv := api.New(api.Options{
		Health:   healthInfo(poller),
		Poller:   poller,
		RTorrent: rtc,
		Store: &yamlStore{
			mu:      &cfgMu,
			current: &current,
			path:    *configPath,
		},
	}, auth)
	srv := &http.Server{
		Addr:              addr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Closers drained after the HTTP server stops (SCGI connections are
	// per-request; the poller loop is cancelled below).
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			if err := c.Close(); err != nil {
				logger.Warn("shutdown close failed", "err", err)
			}
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("blackbird starting", "version", version, "addr", addr, "config", *configPath)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
		stopPoller()   // stops the poller loop (closes the SCGI usage)
		apiSrv.Close() // drains WebSocket clients
		drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(drainCtx); err != nil {
			logger.Warn("graceful shutdown timed out", "err", err)
		}
	}
	logger.Info("blackbird stopped")
}

// yamlStore persists settings edits: whole-file atomic YAML save plus an
// atomic swap of the in-memory config (Settings UI → daemon + disk).
type yamlStore struct {
	mu      *sync.RWMutex
	current *config.Config
	path    string
}

func (s *yamlStore) Get() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.current
}

func (s *yamlStore) SaveTuning(t config.Tuning) error {
	s.mu.RLock()
	updated := *s.current
	s.mu.RUnlock()
	updated.Tuning = t
	if err := config.Save(s.path, &updated); err != nil {
		return err
	}
	s.mu.Lock()
	*s.current = updated
	s.mu.Unlock()
	return nil
}

func (s *yamlStore) SaveSettings(updated config.Config) error {
	if err := config.Save(s.path, &updated); err != nil {
		return err
	}
	s.mu.Lock()
	*s.current = updated
	s.mu.Unlock()
	return nil
}

func (s *yamlStore) ConfigPath() string { return s.path }

// DownloadDirs is the safety boundary for remove-with-data and move-data:
// both refuse paths outside these directories.
func (s *yamlStore) DownloadDirs() []string {
	cfg := s.Get()
	dirs := []string{}
	if cfg.Directories.Default != "" {
		dirs = append(dirs, cfg.Directories.Default)
	}
	for _, d := range cfg.Directories.PerLabel {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// healthInfo adapts the poller state for /api/health.
func healthInfo(p *poller.Poller) func() api.HealthInfo {
	return func() api.HealthInfo {
		s := p.Snapshot()
		return api.HealthInfo{
			Connection: string(s.Status),
			LastError:  s.LastError,
			Stale:      s.Stale,
			Torrents:   len(s.Torrents),
		}
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel(),
	}))
}
