# Storage forecast before intake (ST-01)

**Add torrent** and **Move torrent data** now include a Storage forecast panel. Choose the destination and input batch, then **Refresh forecast**. Each filesystem shows currently available space, reserve, additional data-growth bounds, peak projected usage, remaining headroom, and its largest peak contributor. Expand the operations list for logical sizes, allocation credit, and assumptions.

The Add dialog currently selects all files from uploaded v1 `.torrent` metadata. Existing incomplete torrents use cached file priorities and piece lengths where available; neighboring skipped files can still require boundary pieces. Magnets and remote `.torrent` URLs have unknown metadata and are not fetched by the preview. Unsupported v2/hybrid layouts remain unknown.

Optional inputs are explicit user assumptions:

- **Reserve per filesystem (GiB):** headroom kept outside the projected file-data budget. Defaults to zero and is remembered in this browser after a successful preview. It is applied once per filesystem, even when several roots refer to it.
- **Unknown batch downloads (GiB):** combined additional download growth assumed for all unresolved items in this intake batch. Blank means unknown; zero explicitly assumes no further growth.
- **Remaining extraction per filesystem (GiB):** combined remaining extraction-output growth assumed on each affected filesystem. Blank leaves modeled expansion unbounded. Entering a value also adds an extraction assumption at the chosen destination even without a saved unpack rule. Archive sizes do not establish an expansion ratio.

These estimates are advisory, not reservations or guarantees. Compression, copy-on-write, snapshots, filesystem metadata, quotas and external writers can change the actual requirement. Reserve provides headroom but cannot establish a guaranteed bound for these effects.

## Refresh and review

Every Add/Continue click refreshes filesystem evidence before starting the existing operation. If no preview has been reviewed, inputs changed, or the refreshed plan changes its filesystem identities, operation structure, configuration, uncertainty or capacity verdict, the first click displays the new forecast and starts nothing. Review it and submit again. An explicit Refresh forecast also establishes a reviewable preview.

Plans display a 30-second expiry. A later submission always obtains fresh evidence; if its verdict and modeled operations still agree, it proceeds using that refresh. Normal transfer progress/free-byte changes do not force an endless review loop. Unknown or at-risk forecasts can still be submitted after review. Inspection failures/timeouts start nothing and can be retried. Inputs are disabled during final inspection/submission.

This is a UI advisory workflow, not a mandatory API authorization gate. Existing add/move API callers remain compatible. The forecast and subsequent filesystem action are not atomic; another process can consume capacity between them. No space is preallocated or reserved during preview.

## Accounting model

- Existing paths resolve through symlinks to filesystem device identities. Nested roots and directory aliases on the same device share one pool. Missing path suffixes can use an existing configured root; an unavailable explicitly configured nested root stays unknown instead of silently borrowing its parent's capacity. Symlink escapes outside available download roots are refused.
- Available space comes from a fresh `statfs` sample, not the poller's displayed disk cache. Effective current usage is total minus space available to the process, including filesystem-reserved blocks as unavailable capacity. Separate devices with shared backing capacity, such as thin-provisioned pools, are not modeled as a shared physical allocator.
- Allocated bytes use `stat.st_blocks × 512`; logical length is shown separately. Normal in-place writes credit inspected regular-file allocation, capped by its current and expected logical extent. Sparse holes still require space. Hardlinks, symlinks, unexpected sizes/types and unavailable layouts receive no allocation credit. Filesystem/COW behavior remains an explicit assumption.
- All incomplete session torrents, including stopped torrents, are considered possible remaining writes. Inspected single files receive allocation credit. Cached multifile layouts account for selected logical bytes and the union of touched pieces, avoiding repeated boundary-piece overhead. Where layout/selection is unavailable, the modeled additional-growth range is zero to full torrent logical size; it is a conservative reserve, not a claim that already allocated bytes will be allocated again.
- A move within one filesystem and mount models a rename with zero additional **file-data** copy; reserve covers unmodeled metadata overhead. Linux bind mounts can share a capacity pool while requiring a full copy across mount boundaries. Linux mount IDs and macOS mount names distinguish these cases; unavailable or ambiguous mount identity keeps a zero-to-full-copy bound. A cross-filesystem move walks the source and includes a full logical destination copy before source deletion. The current copy engine materializes sparse holes and copies each hardlink path separately. Source allocation is deduplicated by inode for display. Both source and destination pools remain visible at peak.
- For cross-filesystem single-file moves, bytes materialized by the destination copy are not reserved a second time as future download growth. Multifile ownership/layout uncertainty can produce a wider upper estimate. “Set directory only” copies nothing but conservatively includes later download writes at the new location without transferring old-path allocation credit.
- Running move jobs are included separately. Without reliable partial-copy ownership, their remaining copy range can extend to the entire source logical size. Active extraction destinations are included with unknown or user-assumed remaining output. Queued extraction destinations are explicitly unknown.
- Saved completion-move rules contribute possible destination copies. They are conservative alternative branches, not assertions that every rule matches. Unknown final directory contents or extraction ordering can leave the copy bound unknown. Saved extraction rules contribute possible output filesystems; source archives remain present at the modeled peak.
- The peak retains all modeled downloads, output and copies together. It does not assume a favorable job order or subtract anticipated deletion/reclamation. It can exceed a strictly serialized workload's actual peak. The largest contributor is displayed, rather than presenting a fabricated exact execution timeline.

Previews do not test whether a move's final target already exists, validate content hashes, execute completion rules, list archive contents, fetch URLs, or prove a destination is writable. The existing operation still performs its own validation.

## Bounds and coverage

Inspection happens in the HTTP request outside the polling loop. It adds no daemon RPCs; cached session/detail state can be stale and is identified as such. The server permits two simultaneous inspections, up to 128 intake items, 256 inspected session torrents (selected move hashes first), 20,000 filesystem entries, a 32 MiB multipart body, and 16 MiB per metadata read. Inspection has a five-second cooperative deadline; the browser aborts after 15 seconds. Individual blocked filesystem syscalls cannot be cancelled, but concurrent inspection slots remain bounded and the poll loop does not wait for them.

Uninspected rows, inaccessible filesystems, unknown layouts and queued jobs are surfaced as missing evidence, never zero demand. Byte values are bounded by `2^52` for exact browser integers; a saturated aggregate becomes unknown. No new persistent server files or configuration fields are introduced.

## API

`POST /api/v1/storage/forecast` (legacy `/api/storage/forecast`) is authenticated and returns `Cache-Control: no-store`. It accepts multipart fields:

- `kind=add`: `destination`, `label`, newline-separated `magnets`, and uploaded `files`.
- `kind=move`: `destination`, `mode=move_files|set_directory`, and repeated `hashes` fields.
- Optional nonnegative byte counts: `reserve_bytes`, `unknown_bytes`, `extraction_bytes`.

The response contains `generatedAt`, `expiresAt`, `signature`, `status`, `pools`, `operations`, `unknown`, and `coverage`. A null upper bound/peak means unknown, not zero. Per-pool states are `within_bound`, `at_risk`, `insufficient`, and `unknown`, all subject to the stated model assumptions. The signature identifies review-relevant structure and verdicts; it is not a capacity reservation token. Invalid or excessive input returns 400; unavailable services or a full inspection queue return 503.

## Verification

Fixtures cover shared filesystem aliases, bind-mount copy fallback, missing nested roots, symlink escape, allocated versus sparse files, source/destination overlap, unknown and assumed extraction, selected-file piece unions, malformed/unsafe metainfo, authentication, uploaded allocation credit and unknown magnet sizes. UI tests verify pre-submit refresh, changed-plan review, input invalidation and failed-inspection handling. Browser tests exercise actual fake-daemon intake, the move review gate, and a 900px modal layout.

```sh
go test ./internal/storage ./internal/torrentfile ./internal/api ./cmd/blackbird
cd web
npm run typecheck
npx vitest run test/components/storage-forecast.test.tsx
npm run build
npm run size
npx playwright test e2e/storage.spec.ts --workers=1
```
