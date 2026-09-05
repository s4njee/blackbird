# Session flight recorder (EX-02)

Open **History → Flight recorder**, or select a torrent and open **Logger → Flight recorder**. Choose a torrent hash and optional local-time bounds, then **Load incident**. A selected torrent's window also includes session-wide settings, scheduler, and coverage events.

The slider moves through recording positions. The event list displays occurrence times separately because simultaneous or delayed events can arrive in a different order. **Inspect linked intent** follows an explicit request/result relationship; proximity in the timeline is never treated as causality. Before and after values describe recorded evidence. The replayed torrent state changes only when a daemon observation arrives, and becomes unknown after a gap until another sample establishes it.

To share an incident, choose **Preview incident export**, review the JSON, then **Download reviewed bundle**. The downloaded file is exactly the preview, with no second live fetch and no upload. Export strips free-form messages, torrent names, operator names, URLs, paths, and configuration string values; numeric evidence, phases, timestamps, and machine identities remain. Local views retain diagnostic text with URL, credential-pattern, and IP redaction. Arbitrary sensitive words in local free text cannot reliably be recognized, which is why export omits all free text.

## Evidence captured

- Manual torrent actions: intent with the last cached before-values, requested safe fields, and a separately linked RPC result. A successful RPC response does not prove a later state transition was caused by it.
- Completion rules and seeding actions: the selected rule/group's intent and linked action outcomes. A rule's multiple effects may partially succeed.
- Scheduler profiles and manual override changes: intended changes and results, including failures. Expiry events remain explicit.
- Configuration saves and reloads: a new opaque revision and a bounded projection of non-secret bandwidth, seeding, scheduler, and completion-rule settings. This is not a full config backup or an EX-03 simulator input set. Endpoints, credentials and paths are excluded; unsupported or omitted inputs cannot be reconstructed.
- State, completion, labels, priorities, throttle assignments, daemon messages, and tracker status: sampled observations with before/after values. State/message/tracker changes are recorded as observed; changing rates are sampled at most once per 15 seconds when other state is unchanged. Idle torrents receive a fresh full sample after a minute. Rate/state observations include a full compact state map, so earlier missing samples need not be invented.
- Startup, connection loss, dropped input, and storage failures: coverage gaps. Reconnection resets the comparison baseline and captures fresh checkpoints.

Existing Logger and History lists restore retained action outcomes at startup. Detailed observations and intents live in the flight recorder to avoid flooding those lists. EX-01's Why view describes this new history coverage. Events from before the recorder was installed cannot be recovered.

## Storage and retention

For `--config /data/blackbird.yml`, the recorder writes `/data/blackbird-flight.jsonl` and holds `/data/blackbird-flight.jsonl.lock`. Use a writable persistent config directory; this follows the existing traffic-history location convention. Files are created with owner-only access. Back up the recording alongside config/session state if it is needed beyond retention.

```yaml
history:
  recorder_bytes: 16777216  # 16 MiB; 0 selects this default
  action_log_retention: 24h
```

The byte bound accepts 1–128 MiB or 0 for the default and takes effect on restart. It is editable under **Settings → History**, marked as requiring restart. The age bound follows `action_log_retention` live; 0 uses the recorder's 24-hour default. The recorder also caps retained events at 20,000 and each encoded event at 32 KiB. Legacy action/message ring counts do not disable the recorder.

Producers use bounded queues (1,024 events and one published session snapshot) and never wait for file I/O. A background worker redacts, aggregates observations, prunes, and persists dirty recordings every five seconds. It writes and syncs a temporary snapshot before atomically replacing the last good file, then syncs the parent directory. Allow up to twice the configured file bound while rewriting. A per-recording process lock prevents competing writers; startup removes abandoned temporary files for that recording while holding the lock.

The status readout reports pending, dropped, expired/evicted events, the last successful save, and persistence failures. A full queue drops input rather than delaying polling and records a gap. A failed write preserves the last durable snapshot, keeps bounded new evidence in memory, and retries on a later flush. Pending evidence can be lost on crash. A torn final record is recoverable; middle corruption, unsupported versions, an inaccessible file or a competing writer disables persistence for that process and surfaces degraded status instead of overwriting the recording. Correct the problem and restart. Shutdown allows up to three seconds for recorder drain/flush.

## API and file format

`GET /api/v1/history/flight` (also available under legacy `/api/history/flight`) uses the existing authentication middleware and sends `Cache-Control: no-store`.

Parameters: `hash` (optional; includes global events), `from`/`to` (RFC 3339), `limit` (1–1,000; default 500), and `export=1` (strict export projection). Invalid bounds return 400; an unconfigured recorder returns 503. A truncated window explicitly says so; narrow time bounds to inspect older retained data.

The version-1 response contains `events`, `status`, and `coverage`. Events have durable `id`, ingestion `seq`, `at`, `hash`, `actor`, `action`, `result`, `phase`, optional `causeId` and `revision`, and optional `before`/`after` maps. Phases distinguish `intent`, `rpc_result`, `outcome`, `observation`, `checkpoint`, `configuration`, and `gap`. Missing parents/revisions can reflect retention or dropped events. Consumers must accept unknown phases and additional fields.

The JSONL file starts with a versioned header containing the persisted sequence high-water mark, write time, and retention/drop counters, followed by events. Event IDs include a random process prefix and counter; IDs and sequence positions of retained records survive restart. Existing REST and WebSocket shapes remain compatible; recorder metadata on legacy history entries is additive. No payload format for torrent polling was changed.

## Verification

Tests cover restart reconstruction, a torn final record, preservation of middle corruption, exclusive writers and abandoned temporary files, out-of-order event times, byte/age retention, disk-full preservation and recovery, blocked writes, overflow gaps, concurrent IDs, redaction/export immutability, config revisions, API authorization and filters, replay across gaps, and preview-before-download with sub-millisecond timestamps. Browser checks cover both entry points and 900px layout.

```sh
go test -race ./internal/history
go test ./internal/api ./internal/seeding ./internal/automation ./internal/schedule ./internal/config
go test ./internal/history -run '^$' -bench '^BenchmarkRecorderObserve5000$' -benchmem
cd web
npm run typecheck
npx vitest run test/flight.test.ts test/components/flight-recorder.test.tsx
npm run build
npx playwright test e2e/flight.spec.ts --workers=1
```

The handoff benchmark measures only the nonblocking subscription call against a 5,000-row snapshot, including overload/drop behavior. It does not claim that full recording, serialization, or disk persistence is free; those run on the background worker. No new daemon RPCs or peer-list collection are introduced.
