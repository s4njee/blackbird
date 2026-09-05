# Blackbird performance report (PERF-6.6)

How fast the console is, where the time goes, and what guards it. The Epic 6
target: a 5,000-torrent session with 20 active downloads on a 2-core VM keeps
the list poll under 150ms, WebSocket delta payloads under 20 KB per tick at
steady state, and Blackbird under 150 MB resident.

## Fixtures

`internal/fakertorrent` generates deterministic synthetic sessions instead of
the canned 3–4 rows (`Options.SessionSize/ActiveFraction/Seed`): same seed
and size always yield the same columns, states, labels, and trackers, and
live rows advance per served list poll so sequential polls observe steady
change. Benchmark fixtures use **500, 5,000, and 20,000** torrents at 4%
activity (20/200/800 live rows). Unknown XML-RPC methods now fault
(`-501 unknown method …`) instead of returning an empty string, so a typo'd
daemon command fails tests rather than silently passing.

## Benchmarks (`make bench`)

| Benchmark | What it measures |
|---|---|
| `BenchmarkPollCycle/{500,5000,20000}` | Full poll through the real stack: fakertorrent → SCGI → typed client → poller (includes the fake's XML encoding) |
| `BenchmarkListDecode/{500,5000,20000}` | Transport + decode + mapping alone (one `ListAndGlobals` round trip) |
| `BenchmarkComputeDelta` | Pure 5,000-row diff, 200 changed |
| `BenchmarkDeltaEncoding/{v1,v2}` | v1 vs v2 wire marshaling of a 200-change tick |

`make bench` runs all of them with `-benchmem` (`-benchtime=500ms`). The
`/20000` variants run for the report only — too slow and noisy to gate
merges. `BenchmarkPollCycle` minus `BenchmarkListDecode` is roughly the
poller's own overhead.

## Numbers (2026-09-03)

Reference machines: Apple M1 Max (darwin/arm64, go1.26.5) and Docker
linux/arm64 (go1.26.8). Allocs/op are identical across platforms; timings
agree within ~15%.

### Poll latency per fixture (full stack)

| Fixture | PollCycle | ListDecode | Poller overhead (same-run diff) |
|---|---|---|---|
| 500 | 25 ms | 25 ms | ~0.5 ms |
| 5,000 | 229 ms | 224 ms | ~5 ms |
| 20,000 | 962 ms | 921 ms | ~41 ms (single-iteration, approximate) |

The 5,000-torrent poll **exceeds the 150ms Epic 6 budget**. Profiling shows
the cost is the XML codec (`encoding/xml` decode client-side plus the XML
encoding that rides along in-process from the fake daemon), not poller
logic: the pure 5,000-row diff runs in ~1ms with **zero** allocations, and a
fully idle 5k cycle costs 15 allocs. Real rTorrent encodes in C++ (faster
than the Go fake), so production pays mostly the decode side — but decode
alone still dominates. Meeting 150ms needs a follow-up story: streaming XML
decode (or a leaner codec path) for the list response. Nothing else in the
poll path justifies optimization work first.

### Delta payload per tick at steady state

200/200/800 live rows per fixture; raw JSON and the permessage-deflate
proxy (raw flate, same codec gorilla negotiates):

| Fixture | v1 | v2 | v1+deflate | v2+deflate |
|---|---|---|---|---|
| 500 (20 live) | 19,701 B | 4,406 B (22%) | 963 B | 484 B |
| 5,000 (200 live) | 193,942 B | 41,307 B (21%) | 3,324 B | 2,097 B |
| 20,000 (800 live) | 774,743 B | 164,308 B (21%) | 10,802 B | 7,597 B |

The 20 KB Epic 6 budget is met with deflate on (2.0 KB at 5k), not by
patches alone (41 KB). Encoding cost for the 5k tick: v1 427µs/607 allocs,
v2 255µs/3,407 allocs — v2 marshals faster but allocates more (interface
dispatch over patch maps); per flush per client every 2s, negligible.

### Memory and goroutines (20,000-torrent session, 8 polls, post-GC)

Heap **18.2 MB**, 180k live objects, **3 goroutines** — two orders of
magnitude under the 150 MB budget (`TestPerfFootprint` in
`internal/poller/bench_test.go`; `sys` virtual-arenas excluded).

### Frontend bundle and startup (PERF-7.5, 2026-09)

Route chunks (`web/dist/assets`, `npm run size` gate: entry 80 KB, total
120 KB gzip):

| Chunk | Contents | gzip |
|---|---|---|
| `index-*.js` (entry) | Console shell, table, detail, stores | 55.4 KB |
| `SettingsPanel-*.js` | Settings route (lazy) | 21.0 KB |
| `StatsView-*.js` | Stats route (lazy) | 4.2 KB |
| `RssView-*.js` | RSS route (lazy) | 2.2 KB |
| `HistoryView-*.js` | History route (lazy) | 2.0 KB |
| Total | | 84.9 KB |

First-snapshot data path on the 5,000-torrent fixture
(`web/test/startup-perf.test.mjs`, part of `npm test`): snapshot apply
19.3ms + ordered-view rebuild 22.4ms + virtual-window slice 0.1ms =
**~42ms total, budget 300ms**. Skeleton rows paint during `connecting`
before this, so the visible sequence is skeleton-to-data in well under the
budget on the reference machine; browser paint time on reference hardware
still wants the Playwright harness (POL-8.1).

## CI guard

`TestPerfRegression` (`internal/perf/`) re-runs the guarded benchmarks
(everything above except the `/20000` variants), compares ns/op and
allocs/op against `docs/performance-baselines.json` for the current
`GOOS/GOARCH`, and fails over **20%** regression on either metric — but only
when the breach reproduces on an immediate re-run, since shared-runner
timing spikes (observed 2–3x under load with identical alloc counts) are
machine noise, not regressions. It runs as an isolated CI step
(`make bench-guard`, i.e. `PERF_GUARD=1`) — never folded into
`go test ./...`, whose parallel packages skew timings through CPU
contention. `go test -short` always skips it. Allocations are additionally
guarded deterministically and cross-platform by `TestPollCycleAllocBudget`
(idle ≤100, busy ≤4000 allocs/op on the synthetic 5k session).

Platform policy: baselines are keyed by platform. A platform with no entry
logs the measured numbers and skips (CI on a new platform never
false-positives). Record one with `make bench-update` on a quiet reference
machine and commit the file. Benchmark timing is inherently noisy: re-run a
failing guard on a quiet machine before bisecting, and suspect the change
only when the regression is sustained.

## Regenerating this report

```sh
make bench                                        # human-readable numbers
go test -run TestPerfDeltaSizes ./internal/api/ -v        # wire table
go test -run TestPerfFootprint ./internal/poller/ -v      # memory/goroutines
make bench-update                                 # re-record baselines
make bench-guard                                  # the CI gate, isolated
```
