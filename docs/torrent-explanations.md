# Explain this torrent (EX-01)

Select a torrent, then open **Why?** in the detail panel (keyboard shortcut **9**). The view shows contributing factors with their sources and observation times. It refreshes every ten visible seconds, offers a manual refresh, and pauses while the browser tab is hidden. A stale/disconnected banner identifies cached observations that should not be treated as current.

The view distinguishes observations, recorded actions, current controls, possible contributors, and unknowns. It covers daemon messages, stopped state, skipped files, observed rates, connected peers, saved global and channel limits, scheduler profiles and manual overrides, and assigned seeding groups. Each suggested next step opens the relevant existing detail tab or Settings section with the channel/group/profile named in the link. Opening Why or following its links does not change the torrent.

A met seeding condition is not proof that it stopped the torrent. Recorded actions retain their actor, time, and outcome—including failures—and remain separate from the current policy evaluation. Changes made outside Blackbird and unflushed or expired history may be missing. EX-02 now restores retained outcomes through the [flight recorder](flight-recorder.md); without a recorder, history remains in memory.

Bandwidth settings are configured values, not live daemon cap measurements. A schedule or external action can supersede a saved default. The view does not invent an effective limiting control, diagnose disk contention from slow rates, or infer that a swarm is dead from zero connected peers. Zero configured bandwidth means unlimited.

Recent zero-rate observations are summarized only with at least ten seconds of sufficiently continuous, recent focused samples and a fresh session snapshot. They describe the samples, not guaranteed absence of progress between them. The evidence coverage section explains missing data and uncertainty.

## API contract

`GET /api/v1/torrents/{hash}?view=explanation` returns a read-only explanation for a torrent in the cached session. The legacy `/api/torrents/{hash}?view=explanation` spelling is also supported, consistent with other detail views. The existing authentication middleware applies. This additive view does not change REST or WebSocket payloads for existing consumers.

Response fields:

| Field | Meaning |
|---|---|
| `hash`, `name` | Torrent identity from the cached row. |
| `generatedAt` | Server time when evaluation began, RFC 3339. |
| `observedAt` | Last successful session snapshot time, or null when unavailable. |
| `stale` | Snapshot was flagged stale, disconnected, undated, future-dated, or older than its freshness threshold. |
| `staleAfterSeconds` | Maximum sample age: three times the larger configured polling interval/idle cap, with a 30-second floor. |
| `findings` | Ordered contributing factors, potentially several at once. |
| `findings[].id` | Identifier within this response; history action positions are not durable event IDs. |
| `findings[].kind` | `observation`, `recorded_action`, `constraint`, `hypothesis`, or `unknown`. |
| `findings[].title`, `summary` | Human-readable explanation and its limits. |
| `findings[].evidence` | Source, value, and nullable `observedAt` for each supporting item. Configuration timestamps mean read time, not last-change time. |
| `findings[].target` | Optional navigation hint: `kind` (`tab` or `settings`), `name`, and a descriptive `label`. Never an executable mutation. |
| `coverage` | Evidence limitations, including missing history and unmeasured live limits. |

The endpoint returns 404 (`not_found`) when the hash is absent from the cached session and 503 (`unavailable`) when the poller is not configured, using the standard error envelope. It sets `Cache-Control: no-store`. A known torrent remains explainable from cached evidence while disconnected. Clients should accept additional finding kinds/fields and ignore unknown navigation targets.

## Implementation and validation

The evaluator reads a published session snapshot, current configuration, scheduler status, up to three retained transport-action records, and the existing bounded focused speed history. It performs no daemon RPCs, filesystem scans, or mutations and adds no work to the poll loop. No new persistence or configuration keys are required.

Targeted tests cover conflicting global/channel/override settings, current-policy versus historical-action distinctions, missing and stale evidence, expired overrides, absent group/channel definitions, failed requests, sampling gaps, authentication, and cached operation without an RPC client. Component and browser tests cover uncertainty, retry, focus races, navigation, and the supported 900px desktop width.

Run the focused checks from the repository root:

```sh
go test ./internal/api -run '^TestExplanation' -count=1
go test ./internal/api -run '^$' -bench '^BenchmarkExplainTorrent$' -benchmem
cd web
npm run typecheck
npx vitest run test/components/why-tab.test.tsx
npm run build
npx playwright test e2e/explanation.spec.ts
```

The pure-evaluator benchmark is a microbenchmark, not an end-to-end latency or 5,000-torrent regression claim. The operator usability trial proposed in the backlog remains future product validation.
