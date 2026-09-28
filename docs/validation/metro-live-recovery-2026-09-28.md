# Metro failure/recovery validation — 2026-09-28

This follow-up addresses the [acceptance audit](metro-live-acceptance-audit-2026-09-28.md) using the actual
Go handlers, Caddy and compiled frontend with synthetic source data. It preserves the earlier positive-only
checkpoints and does not claim physical calibration, provider completeness or new upstream source measurements.
The final 32-client mixed run completed and passed. It verifies the controlled application recovery path;
network packet RTT, physical timing and the complete database-backed suite remain explicit boundaries.

## Regressions and fixes

A newer positive wait contradicted an inferred arrival, but backwards-progress suspension returned before
withdrawal. The regression failed with the earlier inferred time still present. Withdrawal and its correction
proof now precede that suspension; earlier immutable frames remain unchanged. Targeted race tests passed,
and the native transport replay has verified that the committed correction restores without the earlier arrival.
Uncommitted corrections can still be absent after runtime loss; the current reference describes that boundary.

Repeated failed reconnects also triggered a new snapshot immediately, bypassing the five-second fallback
minimum. The browser regression reproduced three requests at intervals of 1.134 s and 2.227 s. Every actual
snapshot attempt now reserves the shared five-second interval for its interest, including successful/304
responses; failed responses can extend it with Retry-After. A stream reset aborts pending reads. The final
browser command passed all nine Metro cases, including repeated failures, duplicate/regressive cursors,
closed-scope events and a delayed GET arriving after an accepted reset.

## Synthetic replay and method

[Replay controls](../../internal/app/metro_release_controls_test.go) exist only in the opt-in test server.
[The publisher](../../internal/app/metro_release_fixture_test.go) supplies 50 stations, 96 references and
1,600 platform rows at 500 ms cadence, with moving synthetic Hub coordinates. No Metro API is contacted.
The [mixed browser harness](../../frontend/scripts/metro-live-mixed-release.mjs) keeps 32 scoped clients,
16 station popups at 1280 px and 16 vehicle popups at 390 px, using the real endpoints through Caddy.
Unrelated catalogue/API reads and map base tiles are mocked. This is desktop Chromium emulation, not mobile hardware.

Each incoming request and each response write receives 50 ms delay in a test-only application relay,
including continuing SSE writes. CDP request latency is zero; throughput is configured as 10 MB/s in each
direction per context. The [relay profile sample](metro-live-relay-profile-2026-09-28.json) measured five
application request/response pings at 104.701–125.806 ms. This is a declared nominal 100 ms request/response
floor, not measured packet RTT, netem emulation or a mobile radio performance claim.

The harness schedules positive-to-zero arrival, later incompatible positive correction, loss of runtime
tracks after journal flush, stream interruption, conditional snapshots, reconnection, a real freshness expiry,
source return and explicit vehicle reselection. Phase assertions examine native frames and displayed text;
client event/revision timelines and snapshot attempt times are retained. A [selected-file/asset manifest](metro-live-replay-manifest-2026-09-28.json)
records the working-tree and served-build inputs; it is not a complete reproducible binary build claim. Control schedule targets can slide
while browser assertions/clicks complete; assertion timestamps are not precise fault-start or recovery-SLA measurements.

Latency is backend frame-publication (`published_at`) to a changed popup revision in the DOM, excluding
changes while the latest native stream is unhealthy. Full visible latencies, per-client change counts and
native event timelines remain available rather than hiding outages/coalescing. The final harness resets counters after all clients finish setup; per-client reset-barrier timestamps and
actual control request/completion times are retained. Earlier smoke/checkpoint counters included setup and
are not directly comparable to the final sustained-interval counters. Decoded UTF-8 event
payload bytes are neither compressed wire bytes nor protocol framing. Caddy compression is configured;
compressed transfer bytes are not measured.

## Performed checks

- Default `go test -race ./... -count=1 -timeout 5m`: passed, app 96.139 s and patterns 21.943 s;
  database-specific tests skipped without `TEST_DATABASE_URL`.
- Targeted replay-control/correction/clock/progress race tests: passed in 2.252 s. Relay tests verify stream
  cancellation/slot cleanup, 503/Retry-After, cancellation-aware writes and ResponseController unwrapping.
- `UI_BASE_URL=http://127.0.0.1:18080 npx playwright test tests/metro-live.spec.ts`, run in `frontend/`:
  all nine cases passed in 1.1 min after final fixes and fixture timing adaptation.
- Frontend production build, `go vet ./...` and `make check-generated`: passed.
- Initial [two-client mixed smoke](metro-live-mixed-smoke-2026-09-28.json): arrival, withdrawal, restoration,
  interruption/reconnect, expiry and reselection assertions passed, but its recorded snapshot intervals exposed
  the pre-fix fallback acceleration. It is not a passing fallback-rate acceptance run.

The earlier isolated database-backed Metro/budget checks remain dated evidence for unchanged database/auth
paths. The broader pre-existing history/reporting SQL waits remain unresolved; a complete database-backed
application suite is not reported as passing. See [calibration preparation validation](metro-departure-calibration-preparation-2026-09-28.md)
for the separate offline increment and independent-reference boundary.

## Final sustained mixed run

The [raw report](metro-live-mixed-release-2026-09-28.json) records 2026-09-28 16:14:58.436–16:30:01.758 UTC,
with a 900-second sustained interval after all 32 clients completed setup. The harness exited successfully.
All ten phase assertions passed, including the visible withdrawal/restoration, conditional fallback, full
reset/pin preservation, source expiry/recovery and explicit reselection of reference 1 into a fresh journey.

| Measurement | Result |
|---|---:|
| Changed popup revisions after measurement reset | 45,243 |
| Visible changes per client | 1,193–1,467 |
| Healthy-stream DOM latency P50 / P95 / P99 | 259 / 969 / 1,323 ms |
| Largest recorded healthy-stream DOM latency | 2,909 ms |
| JavaScript errors / connection-local cursor reorders | 0 / 0 |
| Conditional snapshot attempts | 160, five per client |
| Smallest recorded snapshot-attempt interval | 5,002 ms |
| Native SSE error events / resets | 128 / 80 |
| Decoded UTF-8 event payload bytes | 7,587,208,725 |
| Runtime samples | 30 |
| Largest heap / sys values in those samples | 240,275,696 / 359,823,704 bytes |
| Active streams in sampled states | 0–32, including the controlled interruption |

Native error counts include the intentionally canceled streams and refused reconnects during the fault;
they are not JavaScript errors. Resets include reconnects and explicit stop/vehicle/pin scope changes.
The P95 is below the one-second healthy-stream DOM objective under this synthetic application-delay profile;
P99 and the maximum retain slower updates and do not establish a hard one-second guarantee. Event counters
include coalescing/visible-change differences; per-client native revision timelines are preserved in the report.
The sampled heap/sys maxima do not measure peak RSS or all allocation peaks between samples.

The first 32-client attempt passed arrival, withdrawal, durable restore, transport interruption/reconnect
and expiry/recovery, but aborted on a reselection locator before completing 15 minutes. Backend inspection
then showed 96 supported/linked station trains. The original locator assumed that the first DOM row was
already the refreshed linked row. The harness now waits for the station-scoped reset and selects the linked
reference 1 explicitly. The separate [two-client reselection smoke](metro-live-reselection-smoke-2026-09-28.json)
passed, followed by the complete run above. No production behavior was changed for this locator adaptation.

Recovery behavior and reproducible calibration preparation are completed locally. No independent physical
observations were supplied, so the live departure adapter remains unavailable. No provider probe, commit,
push or deployment was performed in this follow-up.
