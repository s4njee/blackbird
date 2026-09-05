# Blackbird — Product Backlog v3: Explain, Predict, Preserve

Reviewed: 2026-09-03. Stories are **proposed** unless an implementation note says otherwise; priorities are not release commitments.

Blackbird's next distinctive feature should help an operator answer a question that a table of torrent statistics cannot: **Why is this happening, what will happen next, and what is worth keeping?**

This backlog complements `backlog.md` (shipping and packaging) and `backlogv2.md` (parity, performance, polish, and themes). It does not replace their release gates. Priorities here rank new product opportunities, not v1 blockers.

## Project review

The review covered the current working tree, including uncommitted work, rather than only the last commit. Findings are based on source inspection and project documentation; the application and test suites were not run for this planning exercise. The original baseline and parity matrix near the top of v2 are historical: several features described there as absent now have implementations.

| Current foundation | Evidence in this project | Opportunity it enables |
|---|---|---|
| Go service controlling one rTorrent daemon through typed SCGI/XML-RPC, with REST and WebSocket interfaces | `cmd/blackbird/main.go`, `internal/rtorrent/client.go`, `internal/api/server.go` | Add intelligence in the control plane without replacing the torrent engine. |
| SolidJS console, structured filtering, detail views, and performance fixtures | `web/src/App.tsx`, `web/src/lib/filter.ts`, `web/src/components/DetailPanel.tsx`, `internal/fakertorrent/` | Surface explanations in existing workflows and measure their cost at large session sizes. |
| Watch intake, RSS, completion rules, unpacking, ratio groups, throttle channels, and bandwidth scheduling are wired into the service | `cmd/blackbird/main.go`, `internal/automation/`, `internal/rss/`, `internal/seeding/`, `internal/schedule/` | Explain interactions between these features rather than propose them again. |
| Completion-rule dry run evaluates first-match-wins rules against a current snapshot | `internal/api/automation.go` | Extend previews to consequences, conflicts, and historical replay; basic rule matching is already present. |
| Bounded, in-memory action/message history; separate persisted hourly/daily traffic totals | `internal/history/history.go`, `internal/traffic/traffic.go` | Add a durable causal record. Existing traffic buckets cannot reconstruct per-torrent decisions. |
| Torrent/peer/tracker statistics and a local completion bitfield | `internal/rtorrent/models.go`, `internal/rtorrent/client.go` | Diagnose observed conditions; swarm-wide piece availability is not in the current model. |
| Filesystem free-space sampling, directory browsing, move jobs, and copy verification | `internal/poller/volumes.go`, `internal/api/move.go` | Forecast temporary storage demand and understand shared data before cleanup. |
| Single-user, desktop-oriented scope with explicit v1 exclusions | End of `backlogv2.md` | Keep ideas usable in this product without adding accounts, mobile clients, streaming, or a plugin platform. |

**Architectural constraint:** several services can independently cause actions or change limits. Explanations can initially observe those services, but new automatic planners need a common decision boundary to avoid fighting the scheduler, seeding engine, and operator. New long-lived records also need explicit persistence; extending the current in-memory history ring alone is insufficient.

**Performance constraint:** `docs/performance.md` reports a 229 ms synthetic 5,000-torrent poll against a 150 ms target on its recorded reference run. New intelligence should use cached snapshots and bounded background workers, not add full-session detail RPCs to every poll. These are existing reported measurements, not fresh results from this review.

## What counts as uncommon

These are differentiation hypotheses, not claims that no other client has a similar feature. The comparison is a lightweight check of official feature descriptions, not an exhaustive competitor audit.

- RSS, scheduling, sequential downloads, content selection, and torrent creation are established capabilities, so they are not new differentiators here. See the [qBittorrent feature list](https://www.qbittorrent.org/).
- Watch directories, blocklists, and remote web control are also established. See [Transmission's overview](https://transmissionbt.com/).
- Swarm merging, sophisticated tag controls, and overall capacity limits already have precedent. Any related Blackbird feature needs a narrower, useful distinction. See the [BiglyBT feature list](https://www.biglybt.com/features.php).

The strongest bets are **evidence-backed explanations, consequence previews, storage ownership, and preservation workflows**. No external AI service is required for their initial versions.

## Priority shortlist

P1 = validate/build first; P2 = follow after the foundations; P3 = research before committing. Effort is relative: M = contained vertical feature; L = multiple subsystems or new persistence; XL = substantial architecture or daemon/filesystem uncertainty. Sizes include the initial scope below, not every possible extension, and are not calendar estimates.

| ID | Feature | Priority | Effort | Differentiating experience | Depends on |
|---|---|---|---|---|---|
| EX-01 | Explain this torrent | P1 | M | “Why did it stop, and what evidence supports that?” | Existing snapshots, settings, history |
| EX-02 | Session flight recorder | P1 | L | Reconstruct an incident across restarts | New durable event store |
| EX-03 | Automation consequence preview | P1 | L | Inspect effects and conflicts before saving a rule | Existing dry run; EX-02 for historical replay |
| ST-01 | Storage forecast before intake | P1 | L | See peak disk demand, including unpack and moves | Existing volume/move/unpack services |
| EX-04 | Explainable policy arbitration | P2 | XL | One visible reason wins when controls conflict | EX-01, EX-03 |
| ST-02 | Storage ownership and reclaim planner | P2 | XL | “What would deleting this actually free or break?” | ST-01; new file ownership index |
| PR-01 | Preservation watchlist | P2 | L | Protect torrents with sustained low observed availability | EX-02; EX-04 for automated protection |
| PL-01 | Finish-by planning | P2 | L | Preview a deadline's effect on the rest of the queue | EX-02, ST-01; EX-04 for execution |
| PL-02 | Transfer allowance forecast | P2 | L | Predict allowance exhaustion and reserve seed capacity | Existing traffic history; EX-04 for enforcement |
| PR-02 | Reproducible collection manifests | P2 | L | Describe and reconstruct a curated data collection | New manifest schema and import planner |
| PR-03 | Integrity patrol | P2 | L | Plan low-impact checks for long-lived seed data | EX-02; EX-04 for scheduling |
| EX-05 | Attention inbox | P2 | M | One actionable incident instead of repeated symptoms | EX-01, EX-02 |
| ST-03 | Verified local data reuse | P3 | XL | Reuse matching local content with a verification receipt | ST-02; daemon capability spike |

## Explain and inspect

### EX-01 — Explain this torrent

**Implementation:** Added on `experimental`: a read-only Why? detail tab and cached explanation API. See `docs/torrent-explanations.md` for behavior, API contract, and validation commands. The operator trial below remains proposed product validation.

**User outcome:** From a stalled or unexpectedly stopped torrent, open **Why?** and see the relevant controls, observations, and next useful check.

**Initial scope:** Deterministic explanations for explicit errors, stopped state, active scheduler/manual limits, assigned throttle channel, met seeding policy, skipped files, and absence of observed transfer progress. Present contributing factors rather than force one diagnosis.

**Acceptance criteria:**

- Every explanation includes evidence, observation time, and whether it is a recorded cause, current constraint, or hypothesis. A historical stop event and a currently met ratio condition are not interchangeable.
- The view separates configured limits from observed rates. Low speed alone never proves throttling, disk contention, or a dead swarm; zero connected seeds never proves zero seeds globally.
- Unknown causes remain unknown, including changes made outside Blackbird. Missing or stale observations are visible.
- A suggested change opens the relevant existing control with its target identified. Tests cover conflicting limits, stale state, external changes, and multiple simultaneous causes.

**Implementation anchors:** `internal/rtorrent/models.go`, `internal/history/history.go`, `internal/schedule/schedule.go`, `internal/api/throttles.go`, and the detail panel. Add a read-only explanation endpoint using cached data; fetch expensive evidence only on demand.

**Validation signal:** In a small operator trial, users can identify the documented cause of seeded test incidents without opening raw logs; record unsupported or misleading explanations separately.

### EX-02 — Session flight recorder

**Implementation:** Added on `experimental`: bounded durable recording, linked intents/results, sampled checkpoints, configuration revisions, History/Logger incident replay, and reviewed export. See `docs/flight-recorder.md` for storage, coverage limits, and validation commands.

**User outcome:** Scrub an incident timeline to answer “What changed before this download stopped?” even after Blackbird restarts.

**Initial scope:** Persist decision events and compact state checkpoints, then correlate rate changes, tracker failures, settings revisions, manual actions, completion rules, and scheduler transitions. This extends the existing Logger and History views.

**Acceptance criteria:**

- Events carry a durable identity, actor/service, affected hash, relevant before/after values, result, configuration revision, and causal link when known. Concurrent events are not given a fabricated causal order.
- Timeline replay distinguishes intended actions, successful RPC responses, observed state, and gaps while disconnected. It never implies that an observed state transition identifies its actor.
- Retention is bounded by age and bytes; crash recovery tolerates an incomplete final write. Peer IP retention is unnecessary for the initial scope; URLs and credentials are redacted in exportable records.
- A selected incident can be exported as a local, versioned diagnostic bundle after preview. Restart, disk-full, retention, and ordering tests prove the recorder cannot block the poll loop.

**Implementation anchors:** `internal/history/`, `internal/poller/history.go`, action handlers, and scheduler/automation event producers. Design a shared recorder interface and durable storage format before adding producers.

**Validation signal:** A known incident can be reconstructed after restart with the same evidence and explicit coverage gaps.

### EX-03 — Automation consequence preview

**User outcome:** Before saving a rule, see “This would move 12 torrents, leave 3 unmatched, and conflict with another policy on 2.”

**Initial scope:** Extend the existing completion dry run with current-versus-draft differences, ordered proposed effects, shadowed rules, and read-only comparisons against seeding and scheduling decisions. Historical replay is a second slice after EX-02 captures the required inputs.

**Acceptance criteria:**

- Preview uses the same pure match/evaluation functions as live services, preserves first-match-wins semantics, and explains why a rule matched or was shadowed.
- Each proposed effect lists affected torrents, destination or setting changes, and known conflicts. Filesystem capacity estimates use ST-01 when available; otherwise capacity remains explicitly unassessed.
- Simulation performs no mutations, outbound webhooks, tracker announces, extraction, or completion-marker writes. Those actions appear as intentions only.
- Applying a draft checks its configuration revision and relevant state freshness. Replay shows unavailable inputs and never presents today's torrent state as historical state.

**Implementation anchors:** `internal/api/automation.go`, `internal/automation/automation.go`, `internal/seeding/seeding.go`, `internal/schedule/schedule.go`, and Automation settings.

**Validation signal:** Preview and live evaluation choose the same rule/actions for identical inputs; an operator can spot a deliberately shadowed rule before applying it.

### EX-04 — Explainable policy arbitration

**User outcome:** An operator's temporary intent—such as “keep this torrent stopped until tomorrow”—has predictable precedence over automation, with the winning reason visible.

**Initial scope:** Introduce a common policy decision service for start/stop, priority, and bandwidth intents. Migrate existing writers incrementally; avoid a general workflow language in the first version.

**Acceptance criteria:**

- A documented precedence table covers manual holds, temporary overrides, schedules, seeding rules, and new planners. Each decision exposes the winning and suppressed intents plus expiry.
- Repeated evaluation is idempotent and does not flap limits or repeatedly start/stop a torrent. Rate changes have hysteresis and a bounded update frequency.
- Reconnect and restart preserve durable holds, expire temporary intents correctly, and reconcile observed daemon state before writing. External daemon changes are surfaced as drift with an explicit reconciliation policy.
- Migration preserves existing behavior through fixtures, including current seeding-condition ordering and scheduler overrides. Do not silently reinterpret existing settings as new constraints.

**Implementation anchors:** Shared service between `internal/schedule/`, `internal/seeding/`, completion automation, and API actions. Preview and execution share a decision representation.

**Validation signal:** A hold remains effective across an automation trigger and reconnect, and its expiry restores the expected underlying policy without duplicate actions.

### EX-05 — Attention inbox

**Implementation:** Added on `experimental`: a durable incident store, conservative tracker/error/volume/connection grouping, acknowledgement and timed snooze, confirmed recovery and recurrence, return summaries, evidence links, and deduplicated inbox notices. See `docs/attention-inbox.md` for behavior, limits, and validation.

**User outcome:** Return to the console and see three issues requiring a decision instead of a stream of repetitive failures.

**Initial scope:** Group related failures into durable incidents with first/last occurrence, affected torrents, evidence, suggested next step, and resolved/snoozed status. Use the existing notice system for delivery.

**Acceptance criteria:**

- Group confidently shared symptoms, such as errors for the same tracker host or unavailable configured volume. A group is not labeled a proven root cause without evidence.
- Acknowledgement, timed snooze, and recovery state survive reload/restart; an incident reopens only when a defined recurrence condition is met.
- A locally generated “since your last visit” summary includes important completed actions as well as unresolved incidents, with direct links to EX-01/EX-02 evidence.
- Burst fixtures verify bounded storage and no repeated notifications for unchanged incidents. No external notification channel is required.

**Implementation anchors:** `web/src/store/notifications.ts`, `web/src/components/Notices.tsx`, global history, and a new incident store.

**Validation signal:** A tracker outage affecting 100 torrents produces one inspectable incident rather than 100 independent prompts.

## Predict resource use

### ST-01 — Storage forecast before intake

**Implementation:** Added on `experimental`: Add/Move forecasts with filesystem identity grouping, allocation and piece-boundary accounting, active/future job demand, explicit unknown-size assumptions, reserve, and pre-submit refresh/review. See `docs/storage-forecast.md` for the advisory model, coverage bounds, and validation.

**User outcome:** Before adding a batch, see whether it fits through download, extraction, and any completion move—not merely whether its final size fits now.

**Initial scope:** An advisory per-filesystem capacity plan in Add Torrent and move previews, accounting for current free space, expected remaining writes, active jobs, and configurable reserve.

**Acceptance criteria:**

- Resolve configured paths to filesystem identities so aliases and nested roots are not counted as separate pools. Unavailable mounts are an unknown/error state, never zero demand.
- Distinguish logical sizes from allocated blocks, avoid counting already allocated capacity twice, include copy-before-delete demand for cross-filesystem moves, and treat same-filesystem renames separately.
- Account for selected files and piece-boundary overhead where knowable. Magnet metadata and archive expansion can be unknown; use clearly labeled bounds or user assumptions, not invented precise values.
- Show peak projected usage and the operation causing it. Refresh before submission and invalidate stale plans; forecasts are advisory because external processes can consume disk space.

**Implementation anchors:** `internal/poller/volumes.go`, `internal/api/move.go`, `internal/unpack/`, `AddTorrentModal.tsx`, and `MoveDataModal.tsx`. Filesystem inspection runs outside the poller hot path.

**Validation signal:** Fixtures for preallocation, shared mounts, extraction, and cross-device moves produce conservative, explainable capacity estimates.

### ST-02 — Storage ownership and reclaim planner

**User outcome:** Select “free roughly 100 GiB” and inspect a ranked plan showing what each removal would reclaim and which torrents still depend on the data.

**Initial scope:** A read-only ownership graph and proposed cleanup plan. Rank by operator preferences, seeding commitments, preservation pins, and expected reclaimed space. Reuse existing explicit remove controls for execution after review.

**Acceptance criteria:**

- Index managed file paths against torrent references and filesystem identities, recognizing identical paths and hardlinks. Report external links, reflinks, snapshots, or unsupported accounting as uncertainty.
- Show logical torrent size separately from estimated uniquely reclaimable bytes. Removing one torrent record is not equivalent to deleting its files or freeing their blocks.
- Explain every candidate's ranking and exclusions; include retained consumers and unknown ownership. A displayed space target is an estimate, not a guaranteed result.
- Revalidate file identity, references, policies, and free space immediately before each deletion; stale plans fail safely and partial results are explicit. Shared data is not deleted while a retained torrent depends on it.

**Implementation anchors:** `internal/api/move.go`, removal paths in `internal/api/rest.go`, seeding removal actions, and Stats. Add one ownership service reused by all destructive entry points.

**Validation signal:** Shared-file fixtures never recommend unsafe reclamation, and measured reclaimed bytes are compared with the stated estimate and uncertainty.

### PL-01 — Finish-by planning

**User outcome:** Ask for a selected dataset by 08:00 and see a plausible plan, its assumptions, and what other downloads would be delayed.

**Initial scope:** Advisory scheduling from recent throughput, selected remaining bytes, configured limits, and planned schedule transitions. Add opt-in execution only after EX-04.

**Acceptance criteria:**

- Show optimistic/typical/conservative completion scenarios and an unknown outcome when observations are insufficient. No deadline is guaranteed from current speed alone.
- Preview changes in torrent priorities or existing throttle assignments, queue impact, and conflicts with disk reserves, bandwidth allowances, or manual holds.
- Re-evaluate when throughput, peer availability, selection, or limits materially change; explain why a formerly plausible deadline is now at risk.
- Execution changes only supported rTorrent controls through EX-04 and expires its intent at completion/cancellation. Forecast tests include stalls, schedule changes, and resumed downloads.

**Implementation anchors:** Per-torrent speed history, `internal/schedule/`, `internal/rtorrent/models.go`, and a new planning service. Verify per-torrent controls on the supported daemon before promising a specific allocation strategy.

**Validation signal:** Compare forecast ranges with actual completion in recorded trials and report coverage; measure displacement of other downloads too.

### PL-02 — Transfer allowance forecast

**User outcome:** See “At this rate, Blackbird will use its remaining allowance before the reset; reserve this much upload capacity for these seeds.”

**Initial scope:** User-defined accounting periods and upload/download allowances, projected consumption, and explicit reserve allocations. This extends existing traffic charts; basic capacity limits themselves are not a novel feature.

**Acceptance criteria:**

- Define reset date/timezone, counted directions, and optional manually entered external usage. Label totals as observed daemon traffic rather than ISP billing or all household traffic.
- Forecast a range from recent usage and known schedule windows; show missing history, counter resets, and uncertainty from traffic not measured by Blackbird.
- Explain reserve allocations and the effect of candidate downloads. Respect PR-01 preservation choices and distinguish an advisory target from an enforced cap.
- Enforcement, when added through EX-04, has explicit behavior at exhaustion and reset, including restart recovery. Zero-valued rTorrent rate limits must not be used to mean “stop” where zero means unlimited.

**Implementation anchors:** `internal/traffic/traffic.go`, `internal/schedule/`, seeding policies, and Stats. Preserve existing UTC accounting buckets; boundary precision may require additional samples.

**Validation signal:** Period rollover and restart fixtures reconcile observed usage without double counting; projections display external-usage limitations clearly.

## Preserve and reproduce collections

### PR-01 — Preservation watchlist

**Implementation:** Added on `experimental`: bounded durable cached observations, coverage-aware availability bands, tracker provenance, reviewable manual pins, and removal/archive-cleanup guards. See `docs/preservation-watchlist.md` for limits, persistence, API, and validation. Automated stop overrides remain dependent on EX-04.

**User outcome:** Identify torrents that repeatedly appear poorly seeded and choose which ones deserve scarce storage and upload capacity.

**Initial scope:** Record bounded samples of tracker-reported and locally connected seed counts, last observed activity, and completion state for explicitly watched torrents. Provide explainable risk bands and manual preservation pins.

**Acceptance criteria:**

- Keep connected-peer observations distinct from tracker reports, with source, timestamp, and observation coverage. Unknown values and stale reports never become zero.
- Rank sustained observations rather than one scrape. The UI says “few seeds observed,” not “you are the last copy”; the current local bitfield cannot prove swarm-wide rarity.
- Pins can carry a reason and review date. They constrain cleanup; automated overrides of seeding stops require EX-04 and explicit operator policy.
- Use cached observations and a bounded slow sampling budget. Respect tracker intervals and private-torrent settings; do not add trackers or enable discovery to improve the score.

**Implementation anchors:** `internal/rtorrent/models.go`, focused tracker detail retrieval, EX-02 storage, seeding policy, and table filters.

**Validation signal:** Recorded sessions rank sustained low observed availability above transient outages without claiming complete swarm knowledge.

### PR-02 — Reproducible collection manifests

**User outcome:** Export a research dataset, Linux-image archive, or public-domain collection as a portable description that can be reconstructed later.

**Initial scope:** A versioned manifest of torrent identities, selected relative files, operator notes, expected sizes, and optional provenance. Import first produces a reconciliation plan: already present, missing, conflicting, or unsupported.

**Acceptance criteria:**

- Record identifier type explicitly; only accept torrent formats supported by the daemon and parser. Preserve original metainfo bytes when included rather than silently rebuilding identity-bearing content.
- Exclude local absolute paths, tracker passkeys, RSS credentials, and secrets from the portable export. Private sources can be represented as operator-supplied references requiring local resolution.
- Import maps logical destinations into configured roots, previews additions and file selections, handles duplicates deterministically, and never overwrites existing payloads automatically.
- Verification reports unavailable files and mismatches. Publisher provenance is an annotation unless separately authenticated; matching a torrent hash establishes identity/integrity, not publisher trust.

**Implementation anchors:** `internal/torrentfile/`, `internal/api/utilities.go`, `internal/api/history.go`, add APIs, and file-selection controls. This is collection portability, distinct from the existing operations backup/restore backlog.

**Validation signal:** Export/import round trips reconstruct a supported test collection's identities and selections across different local directory layouts.

### PR-03 — Integrity patrol

**User outcome:** Schedule periodic integrity checks for long-lived seed data without unexpectedly saturating disks or disrupting the entire queue.

**Initial scope:** Track last successful verification and file-change evidence, then propose bounded maintenance windows for whole-torrent rTorrent rechecks. Piece-sampling verification is a later capability spike, not assumed supported.

**Acceptance criteria:**

- Show last verification time, scope, outcome, and data changes since verification. Changed modification times indicate possible drift, not proof of corruption.
- Initial scheduling limits concurrent checks by filesystem and honors operator windows/holds through EX-04. Explain expected interruption; only offer pause/cancel behavior the daemon supports.
- Preserve prior operator intent and restore it only if still valid when checking finishes. Interrupted or failed checks never receive a successful verification stamp.
- Record failures in EX-02 and EX-05 with affected data when known; require a separate explicit action for repair/redownload rather than silently changing a preserved collection.

**Implementation anchors:** Existing recheck actions, `internal/rtorrent/models.go`, filesystem inspection, and scheduler. Recheck timestamps and durable maintenance jobs are new data.

**Validation signal:** A corrupted fixture is detected and reported, while concurrent check limits and manual holds remain effective across restart.

### ST-03 — Verified local data reuse

**User outcome:** Before downloading a torrent, discover compatible data already on disk and reuse verified content without downloading it again.

**Positioning:** BiglyBT already documents swarm merging. This proposal is a narrower local-data workflow with explicit ownership and verification, not a claim to invent cross-torrent reuse.

**Research gate:** Establish supported rTorrent recheck behavior, metadata availability, piece boundaries spanning files, and safe copy/reflink semantics on target filesystems. Produce a small demonstrated fixture before scheduling implementation.

**Acceptance criteria for a later MVP:**

- Candidate discovery by name/size only suggests matches; torrent piece verification is the authority before reused bytes count as complete. Missing metadata leaves a candidate unverified.
- Start with complete local files copied or safely reflinked into a staged destination, followed by daemon verification. Do not share writable hardlinks between active downloads in the initial version.
- ST-02 records ownership and ST-01 checks temporary capacity. Cancellation never removes the original source, and verification failure cannot overwrite it.
- Produce a receipt of source, verified destination, reused bytes, remaining network bytes, and failures. Tests include identical names with different content, cross-file pieces, and partial/cancelled preparation.

**Implementation anchors:** `internal/torrentfile/`, `internal/api/move.go`, `internal/rtorrent/`, and ST-02. No DHT search, tracker discovery, or swarm-merging protocol is needed for this initial scope.

**Validation signal:** Verified reuse measurably reduces transferred bytes on the fixture while preserving the original files and torrent correctness.

## Suggested delivery sequence

1. **First useful slice: EX-01.** Add the read-only Why view for evidence already available. Demo scheduler limits, seeding stops, skipped files, and unknown external changes.
2. **Foundations: EX-02 and ST-01.** Establish bounded durable evidence and capacity planning. These support later ideas while delivering useful standalone views.
3. **First differentiated release: EX-03.** Combine explanations, consequences, and storage estimates into a reviewable rule-editing workflow. Start with current snapshots; add replay once sufficient historical inputs exist.
4. **Operational follow-through: EX-05 and manual PR-01.** Turn evidence into an attention inbox and preservation decisions without introducing competing automation writers.
5. **Automatic decisions: EX-04, then PL-01/PL-02/PR-03.** Stabilize arbitration before enabling automatic planners. Advisory versions can validate demand earlier.
6. **Collection depth: ST-02 and PR-02; ST-03 only after its research gate.** Shared-file accounting and verified reuse deserve their own storage correctness work.

## Completion and validation policy

- A story is done when its scoped acceptance criteria, relevant automated tests, user documentation, and persistence/API compatibility work are complete. New API fields follow the project's versioning policy.
- Use the existing fake daemon for deterministic scenarios and real-rTorrent integration checks for new RPC assumptions. Do not infer daemon capability from a UI control or an existing test double alone.
- Measure new background work against existing 5,000-torrent fixtures. Record added RPCs, retained bytes, poll latency, and frontend cost; explanations and planners must not make the main console wait for long scans.
- Keep forecast assumptions, evidence coverage, and unknowns visible. Treat the validation signals above as proposed product experiments, not measured benefits.
- Reassess P2/P3 after operators try the first slice. Favor ideas that change a real decision over additional charts that merely restate existing counters.
