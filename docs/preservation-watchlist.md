# Preservation watchlist (PR-01)

Open **Preservation** in the console sidebar. Select a torrent first to carry it into the watch form, or search the session there. Watching starts new observations; it does not backfill old session history. The first sample arrives within five minutes.

Each watched torrent shows an availability band, observation coverage, local completion, last observed transfer activity, and separate tracker evidence. Use **Observation history** to inspect the retained source timestamps and gaps. Filters show all watches, sustained low observations, pins, or reviews due.

Check **Protect from cleanup**, enter an optional reason and review date, and choose **Save pin**. Review dates use UTC and never expire a pin automatically. Uncheck and save before stopping a watch or allowing removal. Stale edits are rejected so another operator's pin cannot silently disappear.

## Evidence and ranking

An independent worker reads the published torrent snapshot every five minutes. It adds no daemon RPCs, tracker announces/scrapes, peer discovery, or forced detail subscriptions. Private-torrent settings are untouched. Normal focused detail polling can provide cached tracker fields; unopened torrents may have no tracker evidence.

Connected seeds are eligible only when the snapshot is connected, at most one minute old, and the torrent is open and downloading or seeding. Missing torrents, stopped/checking/error states, disconnected sessions and stale snapshots record unknown counts, never zero. A complete flag is recorded separately whenever a fresh torrent row exists. Last activity means a positive upload or download rate in a sampled row; activity between samples can be missed.

The ranking uses the last six hours, or time since watching if shorter:

- **Few seeds observed repeatedly:** at least 12 eligible observations spanning 55 minutes, at least 75% coverage of expected five-minute slots, at least 80% with 0–1 connected seeds, and a latest low sample.
- **Recent low observation:** a current 0–1 count without enough sustained evidence.
- **Mixed observations:** current higher counts with some earlier low counts.
- **More connected seeds observed:** eligible observations have higher counts.
- **Insufficient current evidence:** latest sample is unknown, absent, or more than ten minutes old.

Duplicate sampling slots do not increase confidence. Restarts and observation gaps lower coverage. The UI ranks sustained observations above isolated low samples, then shows unknown evidence separately. Counts describe this client's connections, not all seeds in the swarm. In particular, lack of seed connections to an already complete torrent does not prove that other copies are absent. No bitfield-based global rarity claim is made.

Tracker evidence stores up to eight sources from an existing detail cache. Source identity is a SHA-256-derived identifier; only the hostname is retained, excluding credentials, passkeys, URL paths and queries. A nonnegative cached scrape count is retained only when the daemon reports a successful tracker interaction; otherwise it is unknown. **The successful cache-read time is not a tracker report time.** This daemon detail model supplies no scrape-report timestamp, so report age remains unknown and tracker counts never contribute to the risk band. Disabled sources remain labeled. Up to 32 individual tracker cache observations are retained; rereading an unchanged cache timestamp does not add evidence. Tracker histories expire after 24 hours; the latest cache evidence remains labeled with its original timestamp.

## Cleanup protection

Pins are enforced at execution time for Blackbird's manual remove/remove-with-data actions, automatic seeding erase/erase-with-data actions, and deletion of source archives after extraction. Extraction itself may proceed. A shared removal lease serializes cleanup and pin changes: an already executing deletion completes before a pin can be acknowledged, and later deletions see the pin. Blocked outcomes appear in the normal history/flight recorder. Pin edits are also recorded there.

Pins do not override stop, ratio, label or bandwidth policies, resume torrents, or reserve storage/upload capacity. Automatic stop overrides remain dependent on EX-04. The existing seeding-policy marker records a triggered cleanup as attempted even when blocked; unpinning does not automatically replay that attempt. Operators can remove manually after unpinning.

Protection is by torrent identity. Other daemon clients, shell commands, and deletion through an unrelated torrent sharing its files are outside this guard. Shared-file ownership is ST-02. Normal move operations remain available because they preserve the payload at the destination.

## Persistence and API

State lives next to the config as `<config-stem>-preservation.json` with an exclusive `.lock` file. Writes use a private temporary file, fsync and atomic replacement. Bounds are 128 watches, 288 connection samples per watch (24 hours), eight latest tracker sources, 32 historical tracker observations, a 500-byte reason and an 8 MiB file. Pins survive missing torrents and restarts. Load/lock failures disable changes and fail closed for cleanup; the UI reports degraded storage. Failed edits are not acknowledged as saved. Refresh before retrying an uncertain request.

Authenticated API aliases exist under both `/api` and `/api/v1`:

- `GET /preservation` returns ranked summaries and coverage, with no sample histories.
- `GET /preservation?hash=<hash>` includes that watch's retained samples and tracker history.
- `POST /preservation` accepts `action: watch|update|unwatch`, a 40-character v1 info hash, and optional `pinned`, `reason`, `reviewDate` (`YYYY-MM-DD`). Update/unwatch require the current `revision`. A new watch must be in the cached session. A pinned watch cannot be unwatched.

No existing action API shape or configuration schema changes. Remove actions continue returning per-hash results, with a preservation explanation when blocked. The generated fake-daemon session now uses full-length info hashes to exercise the real validation contract.

## Validation

```sh
go test -race ./internal/preservation ./internal/poller ./internal/seeding ./internal/unpack ./internal/api
go test ./internal/preservation -run '^$' -bench BenchmarkSample5000 -benchtime=100ms -benchmem
cd web
npm run test -- test/components/preservation.test.tsx test/router.test.ts
npm run typecheck
npm run build && npm run size
npx playwright test e2e/preservation.spec.ts --workers=1
```

Fixtures cover sustained versus transient counts, stale/stopped observations, bounded retention, tracker credentials and unknown report age, restart persistence, failed writes, conflicting pin edits, in-flight removal, seeding stops versus cleanup, source-archive protection, API authorization and the browser watch/pin/filter/unpin workflow. The sampler benchmark uses 5,000 cached torrents and 128 fully populated watches, including JSON serialization but excluding disk fsync. It performs no daemon RPCs and does not run on the poll loop. Operator validation of preservation choices remains proposed, not a measured benefit.

Measured on an Apple M1 Max with 5,000 session rows and 128 watches containing 288 connection samples each: 17.5 ms per sample/serialize cycle, 4,119,705 retained JSON bytes, approximately 10.6 MB transient allocations per five-minute cycle. Tracker history increases the retained size within the independent 8 MiB cap. Added daemon RPCs: zero; no extra callback runs in the poll cycle. The preservation route is roughly 3.9 kB gzip and loads on demand; the existing 80 KiB entry and 120 KiB total JavaScript budgets still pass.
