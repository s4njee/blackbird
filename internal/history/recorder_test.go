package history

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rtorrent"
)

func openTestRecorder(t *testing.T, opts RecorderOptions) *Recorder {
	t.Helper()
	if opts.Path == "" {
		opts.Path = filepath.Join(t.TempDir(), "flight.jsonl")
	}
	if opts.FlushInterval == 0 {
		opts.FlushInterval = time.Hour
	}
	r, err := OpenRecorder(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.Close(ctx)
	})
	return r
}

func flushRecording(t *testing.T, r *Recorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func findRecorded(t *testing.T, r *Recorder, id string) Event {
	t.Helper()
	for _, e := range r.Snapshot().Events {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("missing event %s", id)
	return Event{}
}

func TestRecorderRestartRestoresIdentityCausalityAndTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r := openTestRecorder(t, RecorderOptions{Path: path})
	l := New(Options{Recorder: r})
	l.RecordConfig(config.Config{}, "configuration", "")
	intent := l.Begin("abc", Entry{Actor: "op", Kind: KindAction, Action: "start", Before: map[string]string{"state": "stopped"}, After: map[string]string{"priority": "3"}})
	l.Add("abc", Entry{Kind: KindAction, Actor: "op", Action: "start", Result: "ok", Phase: "rpc_result", CauseID: intent})
	r.Observe(time.Now(), true, []rtorrent.Torrent{{Hash: "abc", State: rtorrent.StateStopped}})
	flushRecording(t, r)
	before := r.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"id":"torn`)
	_ = f.Close()
	restarted := openTestRecorder(t, RecorderOptions{Path: path})
	for _, e := range before.Events {
		restored := findRecorded(t, restarted, e.ID)
		if restored.Seq != e.Seq || restored.CauseID != e.CauseID || restored.Revision != e.Revision {
			t.Fatalf("identity changed: %+v -> %+v", e, restored)
		}
	}
	if restarted.Snapshot().Status.Dropped != 1 {
		t.Fatal("torn tail not reported")
	}
	restoredLog := New(Options{Recorder: restarted})
	entries := restoredLog.ForHash("abc")
	if len(entries) != 1 || entries[0].CauseID != intent {
		t.Fatalf("legacy log not restored: %+v", entries)
	}
	id := restarted.Record("abc", Entry{Phase: "intent", Action: "stop"})
	flushRecording(t, restarted)
	if e := findRecorded(t, restarted, id); e.Seq <= before.Status.DurableThrough {
		t.Fatal("sequence reused after restart")
	}
}

func TestRecorderRetentionHandlesOutOfOrderTimeAndByteBound(t *testing.T) {
	now := time.Now()
	r := openTestRecorder(t, RecorderOptions{MaxBytes: 8192, Retention: time.Hour, Now: func() time.Time { return now }, QueueSize: 256})
	kept := r.Record("abc", Entry{At: now, Phase: "observation", Message: "keep"})
	expired := r.Record("abc", Entry{At: now.Add(-2 * time.Hour), Phase: "observation", Message: "late old event"})
	flushRecording(t, r)
	findRecorded(t, r, kept)
	for _, e := range r.Snapshot().Events {
		if e.ID == expired {
			t.Fatal("out-of-order expired event retained")
		}
	}
	for i := 0; i < 100; i++ {
		r.Record("abc", Entry{Phase: "observation", Message: strings.Repeat("x", 500)})
	}
	flushRecording(t, r)
	st, err := os.Stat(r.opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 8192 || r.Snapshot().Status.Pruned == 0 {
		t.Fatalf("bounds: size=%d status=%+v", st.Size(), r.Snapshot().Status)
	}
}

func TestRecorderBlockedWriterAndOverflowNeverBlockProducers(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := openTestRecorder(t, RecorderOptions{QueueSize: 2, Write: func(_ string, _ []byte) error { once.Do(func() { close(entered) }); <-release; return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	flushed := make(chan error, 1)
	go func() { flushed <- r.Flush(ctx) }()
	<-entered
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			r.Record("abc", Entry{Phase: "intent", Action: "stop"})
			r.Observe(time.Now(), true, nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("disk blocked producer")
	}
	if r.dropped.Load() == 0 {
		close(release)
		t.Fatal("overflow not reported")
	}
	if err := <-flushed; !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("flush did not honor deadline: %v", err)
	}
	close(release)
	flushRecording(t, r)
	found := false
	for _, e := range r.Snapshot().Events {
		if e.Action == "dropped_input" {
			found = true
		}
	}
	if !found {
		t.Fatal("no durable overflow gap")
	}
}

func TestRecorderDiskFullPreservesPreviousSnapshotAndRecovers(t *testing.T) {
	var fail atomic.Bool
	r := openTestRecorder(t, RecorderOptions{Write: func(path string, data []byte) error {
		if fail.Load() {
			return errors.New("no space left")
		}
		return writeRecording(path, data)
	}})
	r.Record("abc", Entry{Phase: "intent", Action: "start"})
	flushRecording(t, r)
	old, err := os.ReadFile(r.opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	id := r.Record("abc", Entry{Phase: "rpc_result", Action: "start", Result: "ok"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Flush(ctx); err == nil {
		t.Fatal("disk failure hidden")
	}
	current, _ := os.ReadFile(r.opts.Path)
	if string(old) != string(current) {
		t.Fatal("failed write damaged last durable snapshot")
	}
	if r.Snapshot().Status.Error == "" || r.Snapshot().Status.Pending == 0 {
		t.Fatal("missing degraded state")
	}
	fail.Store(false)
	flushRecording(t, r)
	if r.Snapshot().Status.Error != "" {
		t.Fatal("recovery did not clear failure")
	}
	findRecorded(t, r, id)
}

func TestRecorderObservationGapAndPartialActionStayDistinct(t *testing.T) {
	r := openTestRecorder(t, RecorderOptions{})
	now := time.Now()
	row := rtorrent.Torrent{Hash: "abc", State: rtorrent.StateDownloading, DownRate: 100, TrackerStatus: "working"}
	r.Observe(now, true, []rtorrent.Torrent{row})
	flushRecording(t, r)
	intent := r.Record("abc", Entry{Phase: "intent", Action: "stop"})
	r.Record("abc", Entry{Phase: "rpc_result", Action: "stop", Result: "failed", CauseID: intent})
	row.TrackerStatus = "timeout"
	row.DownRate = 0
	r.Observe(now.Add(time.Second), true, []rtorrent.Torrent{row})
	flushRecording(t, r)
	r.Observe(now.Add(2*time.Second), false, nil)
	flushRecording(t, r)
	row.State = rtorrent.StateStopped
	r.Observe(now.Add(3*time.Second), true, []rtorrent.Torrent{row})
	flushRecording(t, r)
	checkpoint, gap, tracker := false, false, false
	for _, e := range r.Snapshot().Events {
		if e.Actor == "poller" && e.CauseID != "" {
			t.Fatal("fabricated observation causality")
		}
		if e.Phase == "checkpoint" && e.After["state"] == "stopped" {
			checkpoint = true
			if len(e.Before) != 0 {
				t.Fatal("reconnect bridged unknown state")
			}
		}
		if e.Phase == "gap" && e.Action == "connection" {
			gap = true
		}
		if e.Before["trackerStatus"] == "working" && e.After["trackerStatus"] == "timeout" {
			tracker = true
		}
	}
	if !checkpoint || !gap || !tracker {
		t.Fatalf("missing coverage: checkpoint=%v gap=%v tracker=%v", checkpoint, gap, tracker)
	}
}

func TestRecorderExportOmitsArbitraryCredentialsAndIsImmutable(t *testing.T) {
	r := openTestRecorder(t, RecorderOptions{})
	r.Record("abc", Entry{Phase: "intent", Actor: "alice", Action: "start", Name: "arbitrary-secret-name", Message: `http://user:password@example.test/passkey?a=secret token=verysecret 192.0.2.1`, After: map[string]string{"label": "123456789", "priority": "3", "state": "stopped", "custom": "unknown-secret"}})
	flushRecording(t, r)
	view := r.Snapshot()
	exported := ExportRecording(view)
	b, _ := json.Marshal(exported)
	for _, secret := range []string{"arbitrary-secret-name", "verysecret", "123456789", "unknown-secret", "192.0.2.1", "user:password", "alice"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("export leaked %q", secret)
		}
	}
	if !strings.Contains(string(b), `"priority":"3"`) {
		t.Fatal("numeric evidence lost")
	}
	if view.Events[len(view.Events)-1].Name != "arbitrary-secret-name" {
		t.Fatal("export mutated local evidence")
	}
	stored, _ := os.ReadFile(r.opts.Path)
	for _, secret := range []string{"verysecret", "192.0.2.1", "user:password"} {
		if strings.Contains(string(stored), secret) {
			t.Fatalf("stored text not redacted: %s", secret)
		}
	}
}

func TestRecorderConcurrentIDsAndInputOwnership(t *testing.T) {
	r := openTestRecorder(t, RecorderOptions{QueueSize: 1024})
	values := map[string]string{"state": "stopped"}
	id := r.Record("abc", Entry{Phase: "observation", After: values})
	values["state"] = "changed"
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				r.Record("abc", Entry{Phase: "intent", Action: "start"})
			}
		}()
	}
	wg.Wait()
	flushRecording(t, r)
	if e := findRecorded(t, r, id); e.After["state"] != "stopped" {
		t.Fatal("caller mutated queued record")
	}
	ids := map[string]bool{}
	var seq uint64
	for _, e := range r.Snapshot().Events {
		if ids[e.ID] || e.Seq <= seq {
			t.Fatal("duplicate identity or ingestion order")
		}
		ids[e.ID] = true
		seq = e.Seq
	}
}

func TestRecorderConfigRevisionIncludesChangesToOmittedSettings(t *testing.T) {
	r := openTestRecorder(t, RecorderOptions{})
	l := New(Options{Recorder: r})
	cfg := config.Config{}
	l.RecordConfig(cfg, "configuration", "")
	cfg.Auth.PasswordHash = "never-export-this"
	l.RecordConfig(cfg, "configuration", "")
	flushRecording(t, r)
	var revisions []string
	for _, e := range r.Snapshot().Events {
		if e.Phase == "configuration" {
			revisions = append(revisions, e.Revision)
		}
	}
	if len(revisions) != 2 || revisions[0] == revisions[1] {
		t.Fatal("configuration revisions conflated")
	}
	b, _ := json.Marshal(r.Snapshot())
	if strings.Contains(string(b), cfg.Auth.PasswordHash) {
		t.Fatal("credential included in configuration projection")
	}
}

func BenchmarkRecorderObserve5000(b *testing.B) {
	// Stall I/O: the benchmark measures only the bounded poller handoff.
	release := make(chan struct{})
	r, _ := OpenRecorder(RecorderOptions{Path: filepath.Join(b.TempDir(), "flight"), Write: func(string, []byte) error { <-release; return nil }})
	rows := make([]rtorrent.Torrent, 5000)
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r.Observe(now, true, rows)
	}
	b.StopTimer()
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = r.Close(ctx)
}

func TestRecorderExclusiveWriterAndAbandonedTemporaryCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	stale := filepath.Join(filepath.Dir(path), ".flight.jsonl.tmp-abandoned")
	unrelated := filepath.Join(filepath.Dir(path), ".other.jsonl.tmp-keep")
	for _, name := range []string{stale, unrelated} {
		if err := os.WriteFile(name, []byte("partial snapshot"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first := openTestRecorder(t, RecorderOptions{Path: path})
	flushRecording(t, first)
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned temporary file remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated temporary file removed: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenRecorder(RecorderOptions{Path: path})
	if err == nil || second == nil || second.Snapshot().Status.Error == "" {
		t.Fatal("competing writer must report degraded status")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = second.Close(ctx)
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("competing writer changed saved evidence: %v", err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened := openTestRecorder(t, RecorderOptions{Path: path})
	flushRecording(t, reopened)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("recording must have owner-only permissions: %v", err)
	}
}

func TestRecorderMiddleCorruptionPreservesSavedEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r := openTestRecorder(t, RecorderOptions{Path: path})
	r.Record("abc", Entry{Phase: "intent", Action: "start"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	corrupt := lines[0] + "\n{invalid\n" + strings.Join(lines[1:], "\n")
	if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRecorder(RecorderOptions{Path: path})
	if err == nil || reopened == nil || reopened.Snapshot().Status.Error == "" {
		t.Fatal("middle corruption must disable persistence")
	}
	reopened.Record("abc", Entry{Phase: "intent", Action: "stop"})
	if err := reopened.Close(ctx); err == nil {
		t.Fatal("degraded close must report failed persistence")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != corrupt {
		t.Fatalf("corrupt evidence was overwritten: %v", err)
	}
}

func TestRecorderZeroRetentionRestoresDefaultLive(t *testing.T) {
	r := openTestRecorder(t, RecorderOptions{Retention: time.Hour})
	r.SetRetention(0)
	if got := r.Snapshot().Status.RetentionSeconds; got != 24*60*60 {
		t.Fatalf("zero retention should restore the default, got %d seconds", got)
	}
}
