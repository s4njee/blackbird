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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"blackbird/internal/api"
	"blackbird/internal/attention"
	"blackbird/internal/automation"
	"blackbird/internal/config"
	"blackbird/internal/history"
	"blackbird/internal/ipfilter"
	"blackbird/internal/mktorrent"
	pollercache "blackbird/internal/poller"
	"blackbird/internal/preservation"
	"blackbird/internal/rss"
	"blackbird/internal/rtorrent"
	"blackbird/internal/schedule"
	"blackbird/internal/seeding"
	"blackbird/internal/trackers"
	"blackbird/internal/traffic"
	"blackbird/internal/tuning"
	"blackbird/internal/unpack"
	"blackbird/internal/watchdir"
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

	// Non-fatal config notices (deprecated keys, migrations) surface once at
	// startup so silent compatibility shims stay visible to operators.
	for _, warning := range cfg.Warnings {
		logger.Warn("config warning", "warning", warning)
	}

	// rTorrent integration (Epic 2): SCGI client, typed client, poller.
	rtc, err := rtorrent.New(cfg.RTorrent.SCGI, cfg.RTorrent.Timeout)
	if err != nil {
		logger.Error("invalid rtorrent endpoint", "err", err)
		os.Exit(1)
	}
	// Bound one SCGI response (PERF-6.3): 0 selects the 64MB default.
	rtc.SetMaxResponseBytes(cfg.RTorrent.MaxResponseBytes)

	// Current config is swapped atomically on SIGHUP reloads.
	var cfgMu sync.RWMutex
	current := *cfg
	var historyLog *history.Log

	applyEntries := func(ctx context.Context, entries []tuning.Entry, cause string) {
		intent := historyLog.Begin("", history.Entry{Actor: "configuration", Action: "tuning_apply", Message: "Applying tuning after " + cause})
		results := tuning.Apply(ctx, rtc, entries)
		for _, r := range results {
			if recorder := historyLog.Recorder(); recorder != nil {
				result, message := "ok", "Setter returned successfully; live state must be observed separately."
				if r.Err != nil {
					result, message = "failed", r.Err.Error()
				}
				recorder.Record("", history.Entry{Phase: "rpc_result", Actor: "configuration", Action: "tuning_apply", CauseID: intent, Result: result, Message: message, After: map[string]string{"key": r.Key}})
			}
			if r.Err != nil {
				logger.Warn("tuning key failed", "cause", cause, "key", r.Key, "err", r.Err)
				continue
			}
			logger.Info("tuning key applied", "cause", cause, "key", r.Key)
		}
	}

	// applyChannels creates/updates named throttle channels (PAR-4.1);
	// assigned below once the poller exists for the in-use guard.
	var applyChannels func(ctx context.Context, upsert []tuning.ChannelEntry, removed []string, cause string)

	// apiSrv is assigned after the poller exists; the poller's Active probe
	// reads it for adaptive polling (PERF-6.3).
	var apiSrv *api.Server

	pollCtx, stopPoller := context.WithCancel(context.Background())
	defer stopPoller()
	// Shared per-torrent action/message log for the Logger tab (PAR-2.5).
	// Created before the poller so message transitions are recorded too.
	recorder, recorderErr := history.OpenRecorder(history.RecorderOptions{
		Path:     strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-flight.jsonl",
		MaxBytes: cfg.History.RecorderBytes, Retention: cfg.History.ActionLogRetention,
	})
	if recorderErr != nil {
		logger.Warn("flight recorder degraded", "err", recorderErr)
	}
	if recorder != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := recorder.Close(ctx); err != nil {
				logger.Warn("flight recorder close", "err", err)
			}
		}()
	}
	historyLog = history.New(history.Options{
		Recorder:             recorder,
		MaxEntriesPerTorrent: cfg.History.ActionLogEntries,
		Retention:            cfg.History.ActionLogRetention,
		MaxEvents:            cfg.History.EffectiveGlobalEntries(),
	})
	historyLog.RecordConfig(*cfg, "configuration", "")
	preservationStore, preservationErr := preservation.Open(preservation.Options{
		Path: strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-preservation.json", History: historyLog,
	})
	if preservationErr != nil {
		logger.Warn("preservation state unavailable; cleanup blocked", "err", preservationErr)
	}
	defer preservationStore.Close()

	// Completion rules (PAR-3.2): the poller reports d.complete 0→1
	// transitions; the engine evaluates automation.on_complete (first match
	// wins) on its own goroutine and records outcomes in the history log.
	// Rules re-read from the live config, so Settings saves and SIGHUP
	// reloads apply without a restart. Processed hashes are persisted next
	// to the config so rules never run twice across restarts.
	autoCtx, stopAuto := context.WithCancel(context.Background())
	defer stopAuto()
	statePath := strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-state.json"
	completionMarker, err := automation.NewMarker(statePath)
	if err != nil {
		logger.Warn("automation: completion marker unavailable; rules may re-run after restart", "path", statePath, "err", err)
	}
	autoEngine := automation.New(automation.Options{
		Log:     logger,
		Daemon:  rtc,
		History: historyLog,
		Marker:  completionMarker,
		Rules: func() []config.CompletionRule {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Automation.OnComplete
		},
	})

	// Seeding policy (PAR-4.2): the poller evaluates assigned seeding
	// torrents per cycle against the live groups and fires each
	// (torrent, group) pair at most once (persisted marker); the engine
	// executes stops, labels, and erases on its own workers.
	seedCtx, stopSeeding := context.WithCancel(context.Background())
	defer stopSeeding()
	seedingStatePath := strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-seeding-state.json"
	seedingMarker, err := seeding.NewMarker(seedingStatePath)
	if err != nil {
		logger.Warn("seeding: marker unavailable; rules may re-fire after restart", "path", seedingStatePath, "err", err)
	}
	seedingEngine := seeding.New(seeding.Options{
		CleanupGuard: preservationStore.Guard,
		Log:          logger,
		Daemon:       rtc,
		History:      historyLog,
		Roots: func() []string {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			dirs := []string{}
			if current.Directories.Default != "" {
				dirs = append(dirs, current.Directories.Default)
			}
			for _, d := range current.Directories.PerLabel {
				if d != "" {
					dirs = append(dirs, d)
				}
			}
			return dirs
		},
	})

	// Transfer history (PAR-5.2): per-day/hour down/up totals derived from
	// the daemon's throttle.global_*.total counters, persisted to a compact
	// append-only file next to the config. Counter resets (daemon restarts)
	// are detected per poll; retention follows stats.traffic_days live.
	trafficPath := strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-traffic.jsonl"
	trafficTracker, err := traffic.New(traffic.Options{
		Log:           logger,
		Path:          trafficPath,
		RetentionDays: cfg.Stats.EffectiveTrafficDays(),
	})
	if err != nil {
		logger.Warn("traffic: history unavailable; counting this session in memory only", "path", trafficPath, "err", err)
		trafficTracker, _ = traffic.New(traffic.Options{Log: logger, RetentionDays: cfg.Stats.EffectiveTrafficDays()})
	}
	trafficCtx, stopTraffic := context.WithCancel(context.Background())
	defer stopTraffic()
	go trafficTracker.Run(trafficCtx)

	// Bandwidth scheduler (PAR-4.3): named limit profiles painted onto a 7×24
	// grid apply on the minute boundary and after reconnect, in the
	// configured time zone. A manual override pauses the schedule until it
	// expires or is cleared.
	schedCtx, stopSched := context.WithCancel(context.Background())
	defer stopSched()
	scheduler := schedule.New(schedule.Options{
		Log:    logger,
		Daemon: rtc,
		Config: func() config.Schedule {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Schedule
		},
		History: historyLog,
	})
	go scheduler.Run(schedCtx)

	// IP blocklist (PAR-5.6): a P2P/DAT list from a local file or URL is
	// loaded into the daemon's ipv4_filter table on connect and on a
	// refresh cadence. URL lists are cached next to the config; the config
	// is read live so Settings saves and SIGHUP reloads apply without a
	// restart.
	ipfilterSvc := ipfilter.New(ipfilter.Options{
		Log:       logger,
		Daemon:    rtc,
		CachePath: strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-ipfilter.dat",
		Config: func() config.IPFilter {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Network.IPFilter
		},
	})
	ipfilterCtx, stopIPFilter := context.WithCancel(context.Background())
	defer stopIPFilter()
	go ipfilterSvc.Run(ipfilterCtx)

	// Tracker ramp: rtorrent.rc boots the session with every tracker
	// disabled, and this turns them back on a batch at a time so a large
	// session cannot announce itself into the daemon's file-descriptor
	// limit (see internal/trackers). The ramp outlives any single poll
	// cycle, so it runs against its own context rather than the poller's.
	trackerSvc := trackers.New(trackers.Options{
		Log:    logger,
		Daemon: rtc,
		Config: func() config.Trackers {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Trackers
		},
	})
	trackerCtx, stopTrackers := context.WithCancel(context.Background())
	defer stopTrackers()

	poller := pollercache.New(rtc, pollercache.Options{
		Interval:       cfg.Poll.Interval,
		DetailInterval: cfg.Poll.DetailInterval,
		VolumeInterval: cfg.Poll.VolumeInterval,
		MaxInterval:    cfg.Poll.EffectiveMaxInterval(),
		Volumes:        cfg.Volumes,
		// Adaptive polling (PERF-6.3): idle stretches toward max_interval,
		// the first visible client snaps back. apiSrv is assigned below;
		// nil (startup) counts as active so polling never stalls early.
		Active: func() bool {
			if apiSrv == nil {
				return true
			}
			return apiSrv.HasVisibleClients()
		},
		OnConnect: func(ctx context.Context) {
			// Tracker enablement is stored per torrent in rTorrent's session;
			// re-assert the operator's appliance-wide policy after every daemon
			// reconnect, since a restarted daemon comes back with announces off.
			// The ramp returns immediately and continues in the background.
			trackerSvc.OnConnect(trackerCtx)
			cfgMu.RLock()
			t := current.Tuning
			cfgMu.RUnlock()
			applyEntries(ctx, tuning.Entries(t), "connect")
			applyChannels(ctx, tuning.ChannelEntries(t), nil, "connect")
			// Load the blocklist (PAR-5.6): the daemon holds the table
			// between loads, so a restart must re-apply it.
			if err := ipfilterSvc.ApplyNow(ctx); err != nil {
				logger.Warn("ipfilter: connect-time load failed", "err", err)
			}
			// Re-assert the scheduled profile: the daemon may have restarted
			// with different limits while Blackbird was away.
			scheduler.ApplyNow(ctx)
		},
		OnTorrentMessage: func(hash, message string) {
			historyLog.Add(hash, history.Entry{
				Kind: history.KindMessage, Actor: "daemon", Action: "message", Result: "info", Message: message,
			})
		},
		OnTorrentComplete: func(hash string, t rtorrent.Torrent) {
			// Completions are history even when no rule matches (PAR-5.3),
			// and fan out to open consoles for toasts + the notification
			// centre (POL-8.3).
			historyLog.Add(hash, history.Entry{
				Kind: history.KindComplete, Actor: "daemon", Action: "complete",
				Result: "ok", Name: t.Name,
			})
			apiSrv.BroadcastNotice(api.Notice{Kind: "completed", Hash: hash, Title: t.Name})
			autoEngine.Enqueue(t)
		},
		SeedingGroups: func() []config.SeedingGroup {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Seeding.Groups
		},
		SeedingSlot: func() string {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Seeding.EffectiveSlot()
		},
		SeedingMarker: seedingMarker,
		OnSeedingTrigger: func(job seeding.Job) bool {
			return seedingEngine.Enqueue(job)
		},
		OnGlobalStats: func(g rtorrent.GlobalStats, at time.Time) {
			trafficTracker.Feed(at, g.SessionDownTotal, g.SessionUpTotal)
		},
	})
	// poller.Run is deliberately started further down, once apiSrv and the
	// automation/seeding engines its callbacks reach into are all live.

	// applyChannels creates/updates named throttle channels (PAR-4.1) and
	// neutralizes removals, refusing removals still referenced by torrents.
	applyChannels = func(ctx context.Context, upsert []tuning.ChannelEntry, removed []string, cause string) {
		if len(upsert) == 0 && len(removed) == 0 {
			return
		}
		inUse := tuning.InUse(poller.Snapshot().Torrents)
		for _, r := range tuning.ApplyChannels(ctx, rtc, upsert, removed, inUse) {
			if r.Err != nil {
				logger.Warn("throttle channel failed", "cause", cause, "channel", r.Name, "err", r.Err)
				continue
			}
			logger.Info("throttle channel applied", "cause", cause, "channel", r.Name)
		}
	}

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
			historyLog.RecordConfig(*loaded, "configuration", "")
			logger.Info("SIGHUP config reloaded")
			for _, warning := range loaded.Warnings {
				logger.Warn("config warning", "warning", warning)
			}
			trafficTracker.SetRetentionDays(loaded.Stats.EffectiveTrafficDays())
			historyLog.SetBounds(loaded.History.ActionLogEntries, loaded.History.ActionLogRetention, loaded.History.EffectiveGlobalEntries())
			poller.SetMaxInterval(loaded.Poll.EffectiveMaxInterval())
			if loaded.Network.IPFilter.Enabled() {
				if err := ipfilterSvc.ApplyNow(context.Background()); err != nil {
					logger.Warn("ipfilter: reload after SIGHUP failed", "err", err)
				}
			}
			changed := tuning.Diff(previous.Tuning, loaded.Tuning)
			if len(changed) > 0 {
				for _, e := range changed {
					logger.Info("tuning key changed on reload", "key", e.Key)
				}
				// Re-apply only the changed keys, per Epic 3.2.
				applyEntries(context.Background(), changed, "sighup")
			}
			upsert, removed := tuning.ChannelDiff(previous.Tuning.Throttles, loaded.Tuning.Throttles)
			if len(upsert) > 0 || len(removed) > 0 {
				for _, e := range upsert {
					logger.Info("throttle channel changed on reload", "channel", e.Name)
				}
				applyChannels(context.Background(), upsert, removed, "sighup")
			}
		}
	}()

	addr := cfg.Server.Listen
	auth := api.NewAuth(cfg.Auth.Username, cfg.Auth.PasswordHash, logger)
	// Same-origin policy for state-changing requests and the WebSocket
	// handshake, plus which proxies may speak for a client address.
	auth.SetServerPolicy(cfg.Server)

	store := &yamlStore{
		mu:      &cfgMu,
		current: &current,
		path:    *configPath,
	}

	// Unpack on completion (PAR-3.4): archives from completed torrents are
	// extracted in place or under a configured root by a bounded pool of 7z
	// workers. Rules re-read from the live config; the automation engine
	// hands over completed hashes after its own rule actions (so moves are
	// already applied) with exactly-once semantics from the shared marker.
	unpackSvc := unpack.New(unpack.Options{
		CleanupGuard: preservationStore.Guard,
		Log:          logger,
		History:      historyLog,
		Runner:       &unpack.SevenZipRunner{Log: logger},
		Config: func() config.UnpackConfig {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Automation.Unpack
		},
		Snapshot: func() []rtorrent.Torrent {
			return poller.Snapshot().Torrents
		},
		Roots: func() []string {
			return store.DownloadDirs()
		},
	})
	autoEngine.SetUnpack(func(hash string) {
		unpackSvc.Enqueue(unpack.Job{Hash: hash})
	}, func() bool {
		cfgMu.RLock()
		defer cfgMu.RUnlock()
		return len(current.Automation.Unpack.Rules) > 0
	})

	// RSS intake (PAR-3.3): feeds poll on their own goroutines (never
	// blocking the torrent poller), matching items auto-load with the
	// filter's label/destination/start options. Feeds and filters re-read
	// from the live config, so Settings saves and SIGHUP reloads apply
	// without a restart.
	rssSvc := rss.New(rss.Options{
		Log:     logger,
		Daemon:  rtc,
		History: historyLog,
		Snapshot: func() []rtorrent.Torrent {
			return poller.Snapshot().Torrents
		},
		Feeds: func() []config.RSSFeed {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Automation.Rss.Feeds
		},
		Filters: func() []config.RSSFilter {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return current.Automation.Rss.Filters
		},
	})
	preserveCtx, stopPreservation := context.WithCancel(context.Background())
	preserveDone := make(chan struct{})
	go func() {
		defer close(preserveDone)
		preservationStore.Run(preserveCtx, func() preservation.Input {
			return preservation.Input{Snapshot: poller.Snapshot(), Trackers: poller.CachedTrackers}
		})
	}()
	defer func() { stopPreservation(); <-preserveDone }()
	inbox, inboxErr := attention.Open(attention.Options{
		Path:    strings.TrimSuffix(*configPath, filepath.Ext(*configPath)) + "-attention.json",
		History: historyLog,
		Source: func() attention.Input {
			return attention.Input{Snapshot: poller.Snapshot(), Volumes: cfg.Volumes,
				StaleAfter: max(30*time.Second, 3*max(cfg.Poll.Interval, cfg.Poll.EffectiveMaxInterval()))}
		},
	})
	if inboxErr != nil {
		logger.Warn("attention inbox degraded", "err", inboxErr)
	}
	inboxCtx, stopInbox := context.WithCancel(context.Background())
	go inbox.Run(inboxCtx)
	defer func() {
		stopInbox()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := inbox.Wait(ctx); err != nil {
			logger.Warn("attention inbox close", "err", err)
		}
	}()
	apiSrv = api.New(api.Options{
		Health:       healthInfo(poller),
		Poller:       poller,
		RTorrent:     rtc,
		History:      historyLog,
		Attention:    inbox,
		Preservation: preservationStore,
		Store:        store,
		Build:        api.BuildInfo{Version: version, Commit: commit, BuildDate: buildDate},
		RSS:          rssSvc,
		Unpack:       unpackSvc,
		Schedule:     scheduler,
		Traffic:      trafficTracker,
		IPFilter:     ipfilterSvc,
		Create: mktorrent.New(mktorrent.Options{
			Log:     logger,
			Daemon:  rtc,
			History: historyLog,
			Roots: func() []string {
				return store.DownloadDirs()
			},
		}),
	}, auth)
	rssSvc.SetOnMeta(apiSrv.IngestTorrentMeta)
	rssSvc.SetOnLoaded(func(feed, title, hash string) {
		apiSrv.BroadcastNotice(api.Notice{Kind: "rss-loaded", Hash: hash, Title: title, Message: feed})
	})
	rssCtx, stopRSS := context.WithCancel(context.Background())
	defer stopRSS()
	go rssSvc.Run(rssCtx)
	unpackCtx, stopUnpack := context.WithCancel(context.Background())
	defer stopUnpack()
	go unpackSvc.Run(unpackCtx)

	// Watch directories (PAR-3.1): .torrent files dropped into configured
	// paths are loaded into the session with the Add API's semantics, logged
	// to the per-torrent history, and toasted to open consoles. Entries are
	// re-read from the live config each second, so Settings saves and SIGHUP
	// reloads apply without a restart.
	autoEngine.SetMover(apiSrv)
	autoEngine.Subscribe(func(n automation.Notice) {
		apiSrv.BroadcastAutomation(api.AutomationNotice{
			Hash:    n.Hash,
			Torrent: n.Torrent,
			Rule:    n.Rule,
			Kind:    n.Kind,
			Message: n.Message,
		})
	})
	go autoEngine.Run(autoCtx)
	go seedingEngine.Run(seedCtx)
	watchCtx, stopWatcher := context.WithCancel(context.Background())
	defer stopWatcher()
	watch := watchdir.New(watchdir.DefaultLoad{
		Client:  rtc,
		OnMeta:  apiSrv.IngestTorrentMeta,
		History: historyLog,
	}, watchdir.Options{Log: logger})
	watch.SetSource(func() []config.WatchDir {
		cfgMu.RLock()
		defer cfgMu.RUnlock()
		return current.Directories.Watch
	})
	go watch.Run(watchCtx)
	watch.Subscribe(func(e watchdir.Event) {
		apiSrv.BroadcastWatch(api.WatchNotice{
			WatchDir: e.WatchDir,
			File:     e.File,
			Kind:     e.Kind,
			Hash:     e.Hash,
			Message:  e.Message,
		})
	})

	// Start polling only now: OnTorrentComplete reaches apiSrv and
	// autoEngine, and OnSeedingTrigger reaches seedingEngine, so the loop
	// must not run until every one of them is assigned and running.
	// Starting it earlier also raced the main goroutine's write of apiSrv.
	if recorder != nil {
		unsub := poller.Subscribe(func(delta pollercache.Delta) {
			snap := poller.Snapshot()
			recorder.Observe(delta.At, snap.Status == pollercache.StatusConnected && !snap.Stale, snap.Torrents)
		})
		defer unsub()
	}
	go poller.Run(pollCtx)

	srv := &http.Server{
		Addr:    addr,
		Handler: apiSrv.Handler(),
		// Timeouts (PERF-6.5, shared with SEC-2.2): header reads stay tight
		// against slowloris, body reads bounded, writes generous enough for
		// multi-GB .torrent downloads on a LAN, idle keep-alives reaped.
		// Hijacked WebSocket connections are exempt from these.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
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
func healthInfo(p *pollercache.Poller) func() api.HealthInfo {
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
