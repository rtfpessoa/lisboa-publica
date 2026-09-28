# Metro live transport assessment — 2026-09-28

This is dated implementation evidence, not a provider guarantee or physical timing validation.
Current behavior is documented in [Metro live popups](../metro-live-popups.md).

## Fifteen-minute assessment

[Raw measurements](metro-live-release-2026-09-28.json) cover 32 independent Chromium contexts through
Go HTTP, Caddy 2.11.4 (`encode zstd gzip`) and the built frontend, from 14:19:14 to 14:34:14 UTC.
Sixteen desktop contexts showed station inventory; sixteen 390 px contexts showed vehicle journeys.
The Chrome CDP profile specified 100 ms network latency and 10 MB/s each direction. This records the
emulation settings; it does not certify real mobile hardware performance or per-frame physical RTT.

The synthetic source contained 50 ordered stations, 96 references and 1600 platform rows, published once
per second. Static/API setup reads and tiles were mocked. Metro live snapshots/streams used the actual Go
handlers through the proxy. No Metro upstream calls were made. Train coordinates were stationary and waits
stayed positive: conflict, event, recovery and movement logic require their separate behavior tests.

| Measurement | Result |
|---|---:|
| Observed popup frame changes | 30,674 |
| Publication-to-DOM P50 | 36 ms |
| Publication-to-DOM P95 | 303 ms |
| Publication-to-DOM P99 | 512 ms |
| Maximum observed delay | 2,570 ms |
| Client frame counts | 901–997 |
| Detected cursor reorders / browser errors | 0 / 0 |
| Peak sampled Go heap / runtime allocation | 237,349,472 / 304,105,800 bytes |
| Sampled active streams | 30–32 |

Publication is the scoped frame's `published_at` creation time; the observer timestamps its revision becoming
visible in the popup DOM. This excludes upstream acquisition delay and waiting for the next projection tick.
Setup observations are included in the counters. A single Go process snapshot showed approximately 53% of
one CPU and 273 MiB RSS; CPU was not sampled continuously. The Go fixture used `GOMAXPROCS=2` and
`GOMEMLIMIT=768MiB` on a shared development workstation, not a production resource limit.

The original raw report's `encoded_event_bytes` counter counts JavaScript event-data string code units.
It is neither compressed wire bytes nor a measured UTF-8 byte count. Transient reconnects/fallback reads were
not counted by that first harness; active-stream samples below 32 must not be represented as uninterrupted
connections. The one-second P95 objective passed for the measured publication stage.

This assessment used the implementation checkpoint compiled before the final conflict/capacity/durability
review changes. Final-checkpoint assessments and targeted checks are recorded below.

## Reproduction and boundaries

The opt-in [Go fixture](../../internal/app/metro_release_fixture_test.go) serves port 18082. The
[browser harness](../../frontend/scripts/metro-live-release.mjs) defaults to 32 clients and 900 seconds,
accepting `METRO_RELEASE_URL`, `METRO_RELEASE_SECONDS`, `METRO_RELEASE_CLIENTS` and
`METRO_RELEASE_REPORT`. Run a disposable local Caddy proxy to the fixture; its test-only trusted client IP
headers allow independent admission identities. Production proxy headers must remain overwritten by Caddy.

The final harness counts UTF-8 event payload bytes, resets, stream errors and fallback reads separately.
These remain application payload measurements rather than compressed network accounting. Test fixtures
and synthetic movement parameters do not establish physical station arrival/departure accuracy, source
completeness, sustainable real upstream 500 ms latency or distributed quota safety.


## Contention correction

The [initial final-checkpoint two-minute smoke](metro-live-final-smoke-2026-09-28.json) recorded
6385 frame changes, P95 520 ms, 86 fallback snapshot reads and 181 resets. A minimal regression reproduced
healthy streams closing when both finite projection read slots were occupied. The stream now coalesces the
next tick for this transient `busy` condition; other projection/write failures still close it.

The [corrected two-minute smoke](metro-live-final-contention-smoke-2026-09-28.json) recorded 6520 frame
changes, P95 456 ms, zero fallback reads, zero cursor reorders/browser errors and 32 active streams at every
sample. Its 96 resets were initial/background/station scopes plus vehicle selection/pinning setup, not replay.
Both smokes used one-second synthetic publications. The final 15-minute assessment additionally exercises
500 ms source publications.

## Behavioral and contract checks

- `go test -race ./...` passed on the final implementation (97.919 s for the application package after the receipt-clock correction).
  Database-dependent tests skip when `TEST_DATABASE_URL` is absent.
- The Metro, frozen-prediction and shared-budget subset passed against an isolated CockroachDB 26.2.6
  with the race detector: `GOMAXPROCS=2 TEST_DATABASE_URL=<local fixture> go test -race ./internal/app
  -run '^TestMetro|^TestFrozenPrediction|^TestUpstreamBudget' -timeout 3m -count=1 -v` (88.687 s after the receipt-clock correction; an earlier run took 137.126 s).
  This includes actual key revocation, principal expiry, scoped/conditional frames, stream admission,
  cancellation, slow socket deadlines, original-clock conflicts and newest missing/elapsed waits.
  A preceding 90-second attempt exhausted its suite timeout; individual Metro behavior checks passed
  in the subsequent three-minute run. Its opt-in release server test correctly skips in normal runs.
- A separate race regression for serialized 500 ms collection passed (3.540 s). A synthetic slow first
  request cannot overlap the next cycle or cause a catch-up burst; the configured interval floor remains
  500 ms. This is a local HTTP fixture, not a sustainable-rate measurement of the external provider.
- The six dedicated Metro Playwright cases passed (53.1 s), covering desktop/390 px inventory,
  countdowns, pinning, explicit same-ID reselection, manual follow pause, direction preservation,
  five-second fallback/reset cancellation, inferred-time presentation and plan-safe station navigation.
  Legacy browser checks passed across the full run and targeted fixture follow-ups; this is not a claim
  that one uninterrupted full-suite command passed. Three populated/live-backend dashboard acceptance
  cases require `UI_LIVE_DATA=1`; two geometry cases require their optional official fixture.
- `npm run build`, `go vet ./...` and `make check-generated` passed. The existing large Map bundle
  warning remains. OpenAPI local references resolve; generated clients match the authoritative contract.
- Local link-target checks and `git diff --check` passed. No changed diagrams required rendering.

Two attempts at the broader Cockroach-backed application suite did not complete: one stack waited in an
existing reporting/vehicle-facts SQL read; another hit its five-minute timeout in the existing history
rollback/revision test. The reporting case passed alone. These broader waits have no established Metro
root cause and remain a validation limitation; the full database-backed suite is not reported as passing.

Physical station timing, real train completeness/identity, live departure calibration and distributed upstream
quota coordination remain outside these synthetic measurements. Current live departures stay unavailable.


The external acquisition path is omitted from the transport fixture. A late collector-only correction keeps
completed-response receipt separate from cycle-start scheduling, so a zero published during acquisition can
support an inferred transition. Its regression failed before the correction; the receipt and serial-cycle
regressions passed together under race in 3.519 s afterward. The running transport checkpoint's handlers,
projections and frontend are unchanged by this acquisition-clock correction. The final default race suite,
real-database Metro subset and vet passed after that correction, rather than claiming the load fixture
exercised acquisition.

## Final fifteen-minute transport checkpoint

[Final raw measurements](metro-live-final-release-2026-09-28.json) cover the final transport/projection and
frontend checkpoint with **500 ms** synthetic publications, from 2026-09-28T15:16:13.895Z to 2026-09-28T15:31:14.721Z.
The same 32-client Go/Caddy/Chromium setup and CDP profile described above were used.

| Measurement | Result |
|---|---:|
| Observed popup frame changes | 58,715 |
| Publication-to-DOM P50 | 151 ms |
| Publication-to-DOM P95 | 906 ms |
| Publication-to-DOM P99 | 1475 ms |
| Maximum observed delay | 3,414 ms |
| Client frame counts (including setup) | 1,216–2,128 |
| Cursor reorders / JavaScript browser errors | 0 / 0 |
| Native SSE errors / fallback snapshot reads | 1 / 1 |
| Reset events (including scope setup) | 96 |
| Active streams in all 29 runtime samples | 32 |
| Peak sampled Go heap / runtime allocation | 264,974,552 / 346,847,576 bytes |
| Decoded UTF-8 event payload total | 10,916,232,332 bytes |

The one-second P95 objective passed for the measured frame-creation-to-popup stage. P99 and the maximum
exceeded one second; this is a percentile objective, not a maximum-delay guarantee. A native stream error
and a snapshot read occurred, so uninterrupted connections and zero fallback are not claimed. Reset/error
counters include setup; the harness does not retain a per-event causal timeline for that single recovery.
The UTF-8 total counts event payloads processed by the browser, including setup/reconnection, and does not
measure compressed wire traffic. All runtime samples showed 32 streams, but discrete sampling cannot prove
continuous connectivity between samples.

The transport fixture bypasses upstream acquisition. Its final handlers/projections/frontend are unchanged
by the independently verified late collector receipt-clock fix. Shared workstation activity, including final
collector race checks, overlapped part of this run. These measurements do not certify a dedicated production
capacity or sustainable real-provider cadence. Temporary local fixture, proxy and database services were
removed after the checks; no deployment is included in this assessment.
