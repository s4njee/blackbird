package attention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

func fixture(at time.Time, count int, tracker bool) Input {
	rows := make([]rtorrent.Torrent, count)
	for i := range rows {
		rows[i] = rtorrent.Torrent{Hash: fmt.Sprintf("%040x", i+1), State: rtorrent.StateError, Message: "Storage error token=secret", TrackerStatus: "Failed", TrackerHost: "tracker.example", BasePath: "/missing/data/file"}
		if tracker {
			rows[i].Message = "Tracker: [Tried all trackers.]"
		}
	}
	return Input{Snapshot: &poller.Snapshot{GeneratedAt: at, Status: poller.StatusConnected, Torrents: rows}}
}
func openStore(t *testing.T, opts Options) (*Store, func()) {
	t.Helper()
	if opts.Path == "" {
		opts.Path = filepath.Join(t.TempDir(), "attention.json")
	}
	if opts.Interval == 0 {
		opts.Interval = time.Hour
	}
	s, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Pure reconciliation tests drive the worker methods before Run starts.
	stop := func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		go s.Run(ctx)
		wait, cancelWait := context.WithTimeout(context.Background(), time.Second)
		defer cancelWait()
		if err := s.Wait(wait); err != nil {
			t.Fatal(err)
		}
	}
	var done bool
	once := func() {
		if !done {
			done = true
			stop()
		}
	}
	t.Cleanup(once)
	return s, once
}
func TestTrackerBurstGroupsOnlyConfidentSymptomsAndDoesNotRenotify(t *testing.T) {
	now := time.Now()
	s, _ := openStore(t, Options{Now: func() time.Time { return now }})
	input := fixture(now, 100, true)
	s.reconcile(input, now)
	if len(s.state.Incidents) != 1 || s.state.Incidents[0].Affected != 100 || len(s.state.Incidents[0].Hashes) != 100 || s.state.NoticeSequence != 1 {
		t.Fatalf("burst: %+v", s.state)
	}
	in := s.state.Incidents[0]
	if err := s.change(request{id: in.ID, episode: 1, action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 100; i++ {
		input.Snapshot.GeneratedAt = now.Add(time.Duration(i) * time.Second)
		s.reconcile(input, input.Snapshot.GeneratedAt)
	}
	if s.state.NoticeSequence != 1 || s.state.Incidents[0].Status != "acknowledged" {
		t.Fatal("unchanged incident reopened or renotified")
	}
	// The adapter's coarse Failed flag alone is not proof of tracker failure.
	generic := symptoms(fixture(now, 2, false), true)
	if len(generic) != 2 {
		t.Fatalf("unrelated errors grouped: %+v", generic)
	}
	for _, g := range generic {
		if g.Kind != "torrent" {
			t.Fatal("generic daemon error attributed to tracker")
		}
	}
	data, _ := json.Marshal(s.state)
	if strings.Contains(string(data), "secret") {
		t.Fatal("raw diagnostic text leaked")
	}
}
func TestAcknowledgementSnoozeVisitRecoveryAndRecurrenceSurviveRestart(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "attention.json")
	s, closeFirst := openStore(t, Options{Path: path, Now: func() time.Time { return now }})
	input := fixture(now, 1, true)
	s.reconcile(input, now)
	id := s.state.Incidents[0].ID
	if err := s.change(request{id: id, episode: 1, action: "snooze", duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := s.change(request{action: "visit", visited: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	closeFirst()
	s2, closeSecond := openStore(t, Options{Path: path, Now: func() time.Time { return now }})
	if s2.state.LastVisit == nil || s2.state.Incidents[0].Status != "snoozed" {
		t.Fatal("snooze/visit lost")
	}
	input.Snapshot.GeneratedAt = now.Add(time.Hour + time.Second)
	s2.reconcile(input, input.Snapshot.GeneratedAt)
	if s2.state.NoticeSequence != 2 || s2.state.Incidents[0].Status != "open" {
		t.Fatal("snooze did not expire once")
	}
	if err := s2.change(request{id: id, episode: 1, action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	if err := s2.save(); err != nil {
		t.Fatal(err)
	}
	closeSecond()
	s3, closeThird := openStore(t, Options{Path: path, Now: func() time.Time { return now }})
	if s3.state.Incidents[0].AcknowledgedAt == nil {
		t.Fatal("acknowledgement lost")
	}
	// Disconnect cannot resolve old errors; healthy recovery needs distinct samples.
	gap := fixture(now.Add(2*time.Hour), 0, false)
	gap.Snapshot.Stale = true
	s3.reconcile(gap, gap.Snapshot.GeneratedAt)
	if !s3.state.Incidents[0].Active {
		t.Fatal("gap resolved incident")
	}
	healthy := fixture(now.Add(2*time.Hour+time.Second), 0, false)
	s3.reconcile(healthy, healthy.Snapshot.GeneratedAt)
	s3.reconcile(healthy, healthy.Snapshot.GeneratedAt.Add(31*time.Second))
	if !s3.state.Incidents[0].Active {
		t.Fatal("same/stale sample confirmed recovery")
	}
	healthy.Snapshot.GeneratedAt = healthy.Snapshot.GeneratedAt.Add(40 * time.Second)
	s3.reconcile(healthy, healthy.Snapshot.GeneratedAt)
	// Previous stale sample clears the recovery candidate, so start over.
	healthy.Snapshot.GeneratedAt = healthy.Snapshot.GeneratedAt.Add(31 * time.Second)
	s3.reconcile(healthy, healthy.Snapshot.GeneratedAt)
	if s3.state.Incidents[0].Active {
		t.Fatal("healthy samples did not resolve")
	}
	if err := s3.save(); err != nil {
		t.Fatal(err)
	}
	closeThird()
	s4, _ := openStore(t, Options{Path: path, Now: func() time.Time { return now }})
	if s4.state.Incidents[0].Status != "resolved" {
		t.Fatal("recovery lost")
	}
	input.Snapshot.GeneratedAt = healthy.Snapshot.GeneratedAt.Add(time.Second)
	s4.reconcile(input, input.Snapshot.GeneratedAt)
	got := s4.state.Incidents[0]
	if got.Episode != 2 || got.AcknowledgedAt != nil || got.Status != "open" || got.ID != id {
		t.Fatalf("recurrence: %+v", got)
	}
	if err := s4.change(request{id: id, episode: 1, action: "acknowledge"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale UI acknowledged a new episode")
	}
}
func TestCapacityRetentionAndConfiguredVolumeEvidence(t *testing.T) {
	now := time.Now()
	s, _ := openStore(t, Options{Now: func() time.Time { return now }})
	input := fixture(now, 5000, false)
	s.reconcile(input, now)
	if len(s.state.Incidents) != MaxIncidents || s.state.Omitted != 5000-MaxIncidents {
		t.Fatalf("bounds: %d, %d", len(s.state.Incidents), s.state.Omitted)
	}
	data, _ := json.Marshal(s.state)
	if len(data) > MaxBytes {
		t.Fatal("byte bound exceeded")
	}
	healthy := fixture(now.Add(time.Minute), 0, false)
	s.reconcile(healthy, healthy.Snapshot.GeneratedAt)
	healthy.Snapshot.GeneratedAt = now.Add(2 * time.Minute)
	s.reconcile(healthy, healthy.Snapshot.GeneratedAt)
	healthy.Snapshot.GeneratedAt = now.Add(31 * 24 * time.Hour)
	s.reconcile(healthy, healthy.Snapshot.GeneratedAt)
	if len(s.state.Incidents) != 0 || s.state.Pruned != MaxIncidents {
		t.Fatal("resolved retention not bounded")
	}
	input = fixture(now, 2, false)
	input.Volumes = []string{"/missing/data"}
	input.Snapshot.Torrents[1].BasePath = "/missing/database/file"
	groups := symptoms(input, true)
	if groups["volume:/missing/data"].Affected != 1 {
		t.Fatal("volume membership ignored path boundary")
	}
	input.Snapshot.Volumes = []poller.Volume{{Path: "/missing/data"}}
	if symptoms(input, true)["volume:/missing/data"] != nil {
		t.Fatal("available volume reported missing")
	}
}
func TestFailedControlWritePreservesPreviousState(t *testing.T) {
	now := time.Now()
	var fail atomic.Bool
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "attention.json"), Now: func() time.Time { return now }, Write: func(path string, data []byte) error {
		if fail.Load() {
			return errors.New("disk full")
		}
		return writeState(path, data)
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.reconcile(fixture(now, 1, true), now)
	s.save()
	s.publish()
	id := s.state.Incidents[0].ID
	before, _ := os.ReadFile(s.opts.Path)
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	defer func() { cancel(); _ = s.Wait(context.Background()) }()
	fail.Store(true)
	if err := s.Update(context.Background(), id, "acknowledge", 1, 0, time.Time{}); err == nil {
		t.Fatal("failed write acknowledged")
	}
	after, _ := os.ReadFile(s.opts.Path)
	if string(before) != string(after) || s.Snapshot().Incidents[0].Status != "open" || s.Snapshot().Error == "" {
		t.Fatal("failed write changed durable acknowledgement")
	}
	fail.Store(false)
	if err := s.Update(context.Background(), id, "acknowledge", 1, 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Incidents[0].Status != "acknowledged" || s.Snapshot().Error != "" {
		t.Fatal("save did not recover")
	}
}
func TestBlockedStorageDoesNotBlockReadersOrCacheProducer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	now := time.Now()
	input := fixture(now, 100, true)
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "attention.json"), Source: func() Input { return input }, Write: func(string, []byte) error { close(entered); <-release; return nil }, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	<-entered
	read := make(chan struct{})
	go func() { _ = s.Snapshot(); close(read) }()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("snapshot reader waited for disk")
	}
	deadline, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := s.Update(deadline, "", "visit", 0, 0, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked update: %v", err)
	}
	cancel()
	close(release)
	if err := s.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestCorruptionAndCompetingWriterNeverOverwriteEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attention.json")
	s, release := openStore(t, Options{Path: path})
	s.save()
	competing, err := Open(Options{Path: path})
	if err == nil {
		t.Fatal("competing writer allowed")
	}
	if err := competing.save(); err == nil {
		t.Fatal("competing writer persisted")
	}
	release()
	before := []byte(`{"version":1,"incidents":[`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	bad, err := Open(Options{Path: path})
	if err == nil {
		t.Fatal("corruption accepted")
	}
	if err := bad.save(); err == nil {
		t.Fatal("corruption overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("original evidence not preserved")
	}
	// Close degraded workers too, releasing any acquired locks.
	for _, store := range []*Store{competing, bad} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		go store.Run(ctx)
		_ = store.Wait(context.Background())
	}
}
