# Attention inbox (EX-05)

Open **Attention** in the sidebar, **Attention inbox** in the notification centre, or `#/attention`. The inbox groups sampled problems into persistent incidents and offers acknowledgement, one-hour/day snooze, and evidence inspection. These controls do not change torrents or daemon settings.

Each incident shows its first and last observation, episode number, affected torrent count, a bounded hash list, evidence, and a suggested next step. **Why?** opens the current EX-01 explanation for an affected torrent. **Recording** opens its EX-02 evidence; **Recorded evidence** opens the session window around the incident. An explanation is current, while a recording is historical. Removed torrents or expired recordings may no longer have evidence available.

## Grouping and recovery

The initial detectors use cached data only:

- **Tracker messages:** messages explicitly beginning with `Tracker:` are grouped by the torrent's listed tracker host. A host shared by affected torrents is a correlation, not proof that it failed. Multi-tracker torrents may report a different failing tracker. The adapter's generic `TrackerStatus=Failed` flag is deliberately insufficient because it is also set for non-tracker daemon messages.
- **Other torrent errors:** a daemon error state or nonempty daemon message creates one incident per hash. Different torrents' free-form messages are not assumed to share a cause.
- **Unavailable configured volumes:** a configured path missing from cached filesystem samples creates one incident. Torrent membership uses base-path directory boundaries. An absent sample can mean unavailable or unreadable; it is not proof of hardware failure. The view identifies the volume by its position in the server's `volumes` configuration. Volume configuration follows the running poller's startup configuration.
- **Connection/coverage loss:** disconnected, stale, or overdue session data creates a session incident. Startup allows 30 seconds for the first observation to arrive.

Acknowledgement remains in effect for the current episode, even if additional torrents join the group. Snooze hides its notification until the deadline; an unacknowledged incident returns to attention once when that deadline expires. An acknowledged incident remains acknowledged after snooze expiry.

Recovery requires the symptom to be absent from two distinct, fresh session samples at least 30 seconds apart. A disconnected/stale sample resets that candidate; process restart resets it too. Recovery means the symptom is no longer observed, including when the affected torrent has left the session. It does not prove repair or identify an actor. A fresh failure after confirmed recovery opens a new episode, clears the old acknowledgement/snooze, and becomes eligible for a new notice. Old UI actions cannot acknowledge a different episode: they receive a conflict and must refresh.

Short failures and recoveries between the five-second inbox samples may be missed. Cached volume samples follow the poller's filesystem refresh interval. The inbox introduces no additional daemon RPCs and does not classify zero speed as a failure.

## Return summary and notices

“Since your last visit” shows unresolved incidents plus retained completed downloads, successful moves, completion-rule effects, settings saves, and observed recoveries. Individual successful rule requests are outcomes, not proof of later daemon state. Up to 100 outcomes are shown with direct recording links; the count includes all matching retained evidence. Expired or unrecorded work cannot be reconstructed. Without EX-02, the fallback is the latest 200 legacy history entries.

Opening the inbox saves a visit marker after loading its summary. That marker survives restart and is shared across browsers for this single-user server. The open view keeps its original summary boundary when refreshed; reopening starts from the prior visit. First use defaults to the past 24 hours. The inbox refreshes explicitly so it does not shift underneath a decision.

Visible consoles check a compact notice summary every ten seconds using the shared ticker. New episodes and expired snoozes produce one aggregate notice through the existing toast/notification centre, with an **Open inbox** action. Unchanged symptoms and membership changes do not repeatedly notify. A small browser-local delivery watermark survives reloads; clearing browser storage permits delivery again, and separate browsers keep their own watermark. If browser storage is unavailable, deduplication lasts for the page session. Unsaved transitions do not advance delivery. No external channel or new browser-notification permission is required.

## Persistence and limits

For `--config /data/blackbird.yml`, the store uses `/data/blackbird-attention.json` and `.json.lock`, alongside the flight recording. Keep the configuration directory writable and persistent.

- At most **256 incidents**, **100 listed hashes per incident**, and **8 MiB** per saved state.
- Resolved incidents expire after **30 days**. Resolved entries are evicted first at capacity; when every retained incident is active, new groups are omitted rather than discarding acknowledged/snoozed incidents. Counts expose omissions and evictions. After eviction, a later occurrence starts a new incident.
- The file contains fixed diagnostic descriptions, normalized tracker hosts, opaque identities, hashes, timestamps and operator state. It omits torrent names, raw daemon messages, tracker URLs, and configured volume paths. Use EX-02's reviewed export if sharing diagnostic evidence.
- Atomic temporary-file replacement, file/directory sync, owner-only creation, and a per-path process lock protect state. Budget temporary space up to twice the file bound. Abandoned rewrites are removed under the lock on startup. Corrupt/unsupported state or a competing writer disables persistence and surfaces an error; the original file is preserved for inspection. Correct the problem and restart.
- The independent worker reads immutable published caches. It performs disk work outside the read lock, so a blocked disk cannot block session polling or inbox reads. Observations save every five seconds; pending observations can be lost on crash. Operator changes return success only after persistence; a timed-out response is uncertain, so refresh before retrying. Failed changes are rolled back in memory and reported; disk failures are retried on subsequent worker ticks. Shutdown gives the worker up to three seconds to finish.

The error/status readout includes the last successful save and observation. A stuck filesystem can delay both observation processing and control requests; old evidence must not be treated as current.

## API

Existing authentication protects both versioned and legacy routes, and responses use `Cache-Control: no-store`.

- `GET /api/v1/attention`: incident state, return-summary boundary, and important retained outcomes. Optional `since` is a past RFC 3339 timestamp.
- `GET /api/v1/attention?summary=1`: compact store identity, notification sequence, open count, and persistence status.
- `POST /api/v1/attention`: `{ "id": "…", "episode": 1, "action": "acknowledge" }`, or `snooze` with `seconds` (60–604800), or `resume`.
- `POST /api/v1/attention`: `{ "action": "visit", "visitedAt": "…" }` advances the shared visit marker monotonically.

An unavailable store returns 503, invalid input 400, missing incident 404, and a stale/resolved episode conflict 409. Persistence or uncertain control failures return 503. New API paths do not change existing polling payloads.

## Verification

Tests exercise 100-torrent tracker bursts, unrelated errors, 5,000-group capacity, path boundaries, age retention, durable acknowledgement/snooze/visit/recovery, stale and disconnected samples, recurrence conflicts, disk-full rollback/retry, blocked storage, corrupt state and exclusive writers. API tests cover authorization, summaries, controls and validation. UI tests cover notice deduplication, failed saves, summaries and evidence navigation; the browser check exercises both controls across reloads, actual Why?/recording views, and the 900px desktop floor.

```sh
go test -race ./internal/attention ./internal/history
go test ./internal/api ./cmd/blackbird
cd web
npm run typecheck
npx vitest run test/attention.test.ts test/components/attention-view.test.tsx
npm run build
npm run size
npx playwright test e2e/attention.spec.ts e2e/flight.spec.ts --workers=1
```
