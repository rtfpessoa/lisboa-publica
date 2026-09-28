# Metro live completion checks, 2026-09-28

This follows the [association/checkpoint slice](metro-association-checkpoints-2026-09-28.md).
The user authorized completing implementation, repairing Maat, committing, pushing to main and deploying.
Existing user glossary edits and the standalone prototype are preserved outside the release.

## Implemented boundaries

Three original-source station-axis positions with two consistent above-envelope steps confirm movement
direction. ETA interpolation cannot independently confirm its assumed sign. Supported terminal arrival
stages inferred completion; countdown, isolated zero, proximity and intermediate stop cannot close a run.
Mandatory lifecycle revisions freeze until one complete archive generation commits. Opposite candidates
remain private until qualified confirmation; old pins preserve history and require explicit reselection.

The reviewed startup model allowlist binds evidence checksums, profile/direction, frozen station geometry,
segment durations and parameters. Admission requires original-input model-consistency training movement,
whole-journey holdout and prohibited-input controls. Same-visit correction withdraws the main departure
and retains revisions. Complete checkpoints retain frozen model and source evidence without restoring
live direction/detector continuity. The browser evaluates qualified absolute model intervals every 500 ms
without source requests, inference events or source-clock renewal.

No qualifying actual-source configuration was supplied. Production departure/model-coordinate activation
therefore remains unavailable. The real 24C/5B fixtures are unchanged and do not supply a qualified
stop-to-movement chain. Synthetic declarations in admission tests exercise the contract only; they
cannot authenticate external evidence or establish physical accuracy.

## Performed checks

- Full `make test` with the dedicated Cockroach test database passed: app race156.530s,
  patterns race13.809s, followed by `go vet ./...`.
- Targeted lifecycle/model/direction/admission race tests passed: app3.457s and patterns1.857s.
  The actual adapter test detects first movement, corrects back to zero progress, withdraws the time
  and restores both revisions. Mandatory failed commits retain the previously visible active state.
- Synthetic healthy baseline receipt-to-selectable admission measured1.032205958s. This single fixture
  measurement is separate from source freshness and backend frame-to-DOM delivery; it is not a fleet P95.
- Existing Metro Playwright suite passed12/12 in52.7s. Two new model/withdrawal checks passed2/2 in4.7s.
  Final affected browser rerun after rendering optimization passed4/4 in11.6s.
- TypeScript/Vite production build, deterministic generated-contract check and current Go vet passed.
  The existing large map bundle warning remains.
- Local Markdown targets/fragments and OpenAPI references passed; no diagrams changed.

The first narrow rerun and native fixture launch failed to compile after a test import was removed;
restoring the required `context` import fixed that test-only failure. A native attempt made before that
fixture launched failed its initial fetch; it did not exercise application delivery. These are execution
failures, not new provider observations or production incidents.

Final native, latest affected race, Maat and deployment results are recorded below as they complete.
Physical timing error and production upstream source cadence/duplicate fraction remain unmeasured.

Final current-code native Go/compiled-frontend/EventSource check passed:60 changed DOM samples, P95597ms, zero fallback reads/browser errors, and successful pinned50-call checkpoint recovery. The [JSON evidence](metro-live-completion-2026-09-28.json) retains scope and clocks. This is two synthetic clients with no Caddy/network-delay/provider freshness profile. Heap239,874,392 bytes, system361,040,232 bytes,11 goroutines and2 streams were measured.

Latest database-enabled affected Metro/Hub/positions/budget race checks passed: app78.180s and patterns3.815s after the final direction-gap/correction/immutable-revision changes.
