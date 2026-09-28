# Metro live acceptance audit — 2026-09-28

This dated audit compares the accepted Metro popup specification with performed checks. It does not
change the historical measurements or certify provider completeness and physical station timing.
See [current behavior](../metro-live-popups.md) and the [transport measurements](metro-live-release-2026-09-28.md).

## Evidence and remaining work

| Requirement | Available evidence | Remaining work |
|---|---|---|
| Dedicated serialized 500 ms collection and shared attempt budget | Local HTTP/race collector fixtures, existing shared-budget tests and the earlier bounded source probe | No new upstream calls are required for recovery testing; distributed quota coordination is not implemented |
| Original-clock evidence, conflicts, missing waits and separate inferred events | Backend parser/transition/progress tests and archive recovery/corruption/TTL tests | Independent physical reference and frozen movement calibration remain unavailable |
| Vehicle timeline, directional station inventory, countdown and camera intent | Six dedicated Metro browser cases | Those cases mock EventSource; they do not themselves demonstrate the actual proxy recovery path |
| Thirty-two clients, fifteen minutes, actual Go/Caddy/built frontend | Final synthetic checkpoint: P95 906 ms, 58,715 visible changes, no cursor reorders or JavaScript errors | This checkpoint has stationary coordinates and positive waits; it is a throughput/latency checkpoint rather than the full mixed replay scenario |
| Expiry, corrections and controlled reconnects during the mixed load replay | Component tests plus one unplanned native SSE error/snapshot read in the checkpoint | Add deterministic replay phases and controlled delivery interruption through the actual transport path; assert the displayed state after each phase |
| Recorded mobile and network profile | Sixteen 390 px clients; CDP latency 100 ms, 10 MB/s in each direction | CDP's latency parameter describes request-to-response-header delay; it does not establish a measured 100 ms RTT for continuing SSE frames |
| Payload, memory and transport limits | Encoded-frame cap tests, 29 runtime samples, decoded UTF-8 counters, slow socket deadline and auth/admission tests | Compressed wire bytes and per-client recovery timelines were not recorded in the checkpoint |
| Durable committed history and restart limitations | Archive component tests and explicit partial-history recovery | An integrated controlled recovery scenario should demonstrate the UI result; source acquisition must not be described as exercised by the synthetic publisher |
| Complete database-backed application suite | Targeted real-database Metro subset passes | Broader existing history/reporting SQL waits remain unresolved; the full suite is not a passing check |

## Next validation increment

Extend the existing opt-in synthetic fixture and browser harness with an explicitly selected mixed replay
mode. Keep the existing positive-only checkpoint reproducible. Use unique source clocks for supported
positive-to-zero arrival, correction, stale evidence and recovery phases. Keep the selected journey pinned
through loss of support; switching to a newly supported episode must require explicit selection.

Record phase boundaries and each client's accepted revisions, reset/fallback/error events and DOM outcomes.
Exercise stream unavailability, conditional fallback and full reset with controlled faults rather than relying
on incidental connection errors. Healthy-stage latency and failure/recovery behavior must be reported
separately; missing frames cannot improve the latency result by disappearing from the denominator.

Specify any additional network delay as the mechanism actually applied. A viewport and CDP settings are
emulation evidence, not observations of mobile hardware or a measured packet RTT. Any new long run must
write a separate dated raw report and retain the earlier checkpoint's original limitations.

Departure calibration is a separate increment. It needs a source-qualified movement transform, frozen
geometry/noise/resolution inputs and independent reference observations before the live adapter can emit
model departures. Synthetic coordinates cannot supply that calibration or establish physical accuracy.

## Checks performed for this audit

Read the accepted specification, fixture publisher, browser harness, dedicated Metro tests and existing
measurement reports. Confirmed the CDP latency description in the installed Playwright protocol types.
No new provider probe, application execution, deployment or commit is included in this audit.


## Completed follow-up evidence

The subsequent [failure/recovery report](metro-live-recovery-2026-09-28.md) records the completed 32-client,
15-minute mixed replay and all ten recovery assertions, with healthy-stream DOM P95 969 ms. It adds original
correction withdrawal before progress suspension and a five-second snapshot budget across repeated reconnect
failures. Nine dedicated Metro browser tests passed. The final measurement resets setup counters and records
native revision/snapshot timelines plus actual replay-control boundaries.

The additional [calibration preparation report](metro-departure-calibration-preparation-2026-09-28.md) records
an offline template, independent-reference contract, whole-journey splits and a deterministic candidate/holdout
command. No independent physical reference exists yet; live departures remain unavailable. The 100 ms profile
is a declared application relay, not measured packet RTT. Compressed wire bytes and the broader database-suite
waits also remain outside the completed evidence. This follow-up does not change the earlier checkpoint's scope.
