# Metro association, checkpoint and acquisition follow-up

Date: 2026-09-28. Local implementation evidence; no commit, push, deployment or upstream load probe was performed.

## Implemented scope

The shared context classifier replaces blanket reference rejection and the second raw vehicle-destination matcher. It preserves scoped conflicts, coherent prior continuity, usable local forecasts and explicit ambiguity. Vehicle links require one fresh selected line/reference context. Ambiguous vehicle forecasts are grouped by direction; pinned history remains visible through explicit unavailable recovery.

Complete latest checkpoints now retain event-free journeys under the existing archive ownership, allocated-byte cap and seven-day original-source-age policy. New selectable identities require a committed baseline. Subsequent progress coalesces within the bounded journal; a late acknowledgement does not discard a newer pending revision. Verified cold recovery returns suspended history and original clocks; missing selections never reference absent records. Legacy event-only recovery remains explicitly partial.

Hub positions have a dedicated one-second minimum-start target. Faster publication is limited to Metro; other consumers use the shared latest response at their existing cadence. Positions preserve 20 Hub/100 global safety slots plus bounded active protected chains, back off to two/five seconds under pressure and honor provider cooldowns. Actual source production freshness was not measured.

## Captured evidence

[24C/5B fixtures](../../internal/app/testdata/metro-20260928/README.md) preserve original cache/publication clocks, bytes and provenance hashes. Eight deterministic row permutations retain the same classified forecast result. Their possible contexts are not forced into a physical allocation. Repeated cache reads are not new original source observations or departure qualification evidence.

## Performed checks

| Check | Result |
|---|---|
| Targeted checkpoint archive tests | Passed: event-free restart/latest revision, atomic pair/cancellation, corruption, TTL, stale revision, record/count/storage limits |
| Targeted shared Metro tests | Passed: captured permutations, optional/unmapped context, committed baseline before selection, missing pin, persistence failure, original source clocks |
| Targeted budget/pacer tests | Passed: bounded redirect chain, due-work headroom, 120 Hub/900 global hard rolling caps and minute-based recovery |
| `make test` with isolated Cockroach database | Passed: `go test -race ./...` and `go vet ./...`; app package 148.785 s, patterns 16.469 s |
| Later targeted Metro/positions race tests with database | Passed: app 65.405 s, patterns 3.421 s, including slow acquisition/no catch-up/other-operator isolation/shared-response reuse |
| `make generate` / `make check-generated` | Passed; schema remains authoritative |
| Frontend build | Passed; existing large-chunk warning remains |
| Metro browser suite, first run | 11 passed; explicit-current-episode test added later passed separately |
| Native EventSource smoke | Passed; details below |
| Full browser suite, final isolated-output run | 135 passed, 5 skipped; 140 cases, 5.9 minutes. Earlier failed run retained below |
| Local documentation references / `git diff --check` | Passed for the first slice: 14 documents and 396 local targets; no diagram changes were made |

Database checks used `lisboapublica_metro_followup_test`, separate from the running application's database; each integration test used its existing isolated schema helper. The opt-in long-running transport fixture is skipped in ordinary Go runs and was explicitly started for the native smoke.

## Native browser evidence

[Initial machine-readable result](metro-association-checkpoints-2026-09-28-initial.json): compiled frontend, actual Go projection/archive/SSE and native browser EventSource, two clients at 1280 px and 390 px. The source workload is explicitly synthetic: 96 references, 50 visits and 1600 platform rows. Unrelated setup endpoints were mocked; the Metro live endpoint was not mocked. No proxy/network-delay profile or provider calls were used.

The 56 changed DOM revisions had local publication-to-DOM P95 **415 ms**. Both clients used zero fallback snapshot reads and had no browser errors. The selected vehicle's 50-call checkpoint survived removal of live runtime tracks; recovery retained the pin with suspended association and null current/next indices. The runtime sample was 257,655,904 heap bytes, 357,103,960 system bytes, 11 goroutines and two streams. These are one sample under this small smoke scope, not a capacity bound or a production result.

[Recovery-cache repeat](metro-association-checkpoints-2026-09-28-recovery.json) repeated the same scope after the recovery-cache changes: 58 changed DOM revisions, P95 **325 ms**, zero fallback reads and browser errors, and successful 50-call pinned recovery. That runtime sample was 258,196,640 heap bytes, 365,558,104 system bytes, 11 goroutines and two streams. Both local runs met the one-second delivery objective for this workload.

Baseline admission latency was not measured: browser setup followed the initial checkpoint flush. This result does not repeat the earlier 32-client/15-minute proxy assessment, measure live provider cadence, or establish physical timing accuracy.

## Remaining accepted-contract work

This is the acquisition/shared-association/recovery implementation slice. The entire accepted follow-up contract is **not complete**. The qualified movement adapter, three-original-position direction confirmation, durable terminal/reversal lifecycle transitions, production model admission/allowlist, experimental departure withdrawal/revision UI and labeled 500 ms local modeled coordinates remain to implement and qualify. The separate offline model-consistency assessment is now implemented; production admission/allowlisting is still pending. Departures remain explicitly unavailable; synthetic or repeated captured data cannot activate them. The archive supports atomic multi-record commits, but no new terminal/successor lifecycle is claimed by this slice.

No deployment result, rollback execution or full new admission-latency measurement is claimed. The additive checkpoint archive kind requires a compatible rollback binary or verified backup; existing archive/database volumes must be preserved.

## Browser reruns and infrastructure limits

The first full run had one chart-marker timeout (zero dots within five seconds) and one artifact-file collision while overlapping Playwright invocations used the same default output directory. The chart case passed on unchanged HEAD and five consecutive current-code reruns; no chart implementation was changed. The Metro fallback case passed when rerun with a separate output directory. A first isolated explicit-current-episode invocation used the wrong default port and failed connection before loading the application; rerunning with the configured Vite URL passed. These failed runs are not counted as passes. The final full run used its own output directory and stable source files: 135 passed, five skipped, with no failures.

Five full-suite skips require populated/live backend data or an explicitly downloaded official geometry fixture. Local link checks verified targets, not web availability or all section anchors. Existing diagrams were not changed or rerendered.

## Final review guards and offline preparation

Targeted current-code checks cover metadata-inclusive pending checkpoint accounting/coalescing/pruning, original-age hot TTL without ingestion, line-scoped station forecast deduplication and explicit incompatible vehicle context reasons. A bounded synthetic queue can commit its admitted archive batch; capacity rejection does not hide source forecasts. Browser-retained history removes active/own predictions when an explicitly unavailable pin has no record.

The offline command now accepts `model_consistency` with frozen qualification provenance and `model_support` intervals, distinct from `observed`/`independent` and synthetic controls. Matching results are labeled `within_model_support_interval`; physical accuracy remains `not_measured`, and every assessment retains `live_enabled: false` and `review_required: true`. This is preparation for the remaining live adapter/allowlist, not activation. The populated CLI control and unit tests are explicitly synthetic declarations of the new contract, not qualified actual-source evidence. The empty model template deliberately fails.

Performed after the first full checks: complete patterns race suite (12.643 s); database-enabled Metro/positions/budget race checks (67.316 s); final focused recovery/line-scope race checks (4.682 s); `go vet ./...`; frontend build and the 12-case Metro browser suite (55.0 s). The CLI binary produced identical output for identical synthetic input bytes with verified SHA-256, accepted the labeled model-contract control without enabling live use, and rejected the empty model template. One initially malformed synthetic gap control was rejected for nonmonotonic clocks; after shifting all later original clocks consistently, the intended gap-rejection case passed. A first frontend build caught missing required nullable fields in the history helper; the corrected build passed.

The earlier complete browser result precedes these narrow review refinements; current affected behavior was rechecked through the final Metro suite. No new source requests or physical measurements were made.

## Native admission investigation

A later native repeat failed the strict no-fallback smoke gate: [original failed result](metro-association-checkpoints-2026-09-28-failed-smoke.json), P95 438 ms, one mobile snapshot and otherwise successful pinned recovery. Ten instrumented pre-fix repeats had seven passes and three failures. [Captured busy response](metro-association-checkpoints-2026-09-28-busy.json) records HTTP 503 with code `busy` at initial station SSE admission, native error, and correctly spaced fallback reads. There were no visibility events or dangling selected records in that failure. Two existing projections could occupy the shared read slots; established stream ticks coalesced this state, but initial reset admission rejected immediately.

The initial reset now waits up to one second on the unchanged two-slot semaphore, bounded by the existing connection inventory. Three regression cases failed before implementation and passed under race afterward (stream suite 5.701 s): temporary 100 ms contention resolves to a complete reset, sustained contention times out, and cancellation releases stream admission without consuming/leaking a read slot. Regular snapshot admission and existing stream coalescing retain their behavior.

Repetition also exposed a harness selection fault: the arrival control affects reference 1, while immediate first-row selection could open reference 2 from the previous run's pending input mode. A captured timeout had reference 2, no HTTP/native errors and no fallback. The smoke now waits for positive input mode and explicitly selects/checks reference 1. This test-only correction does not change application matching or original source clocks.

Final database-enabled Metro/positions/budget race rerun after bounded initial stream admission passed (75.199 s). Generated-contract and vet checks passed after the OpenAPI description update. Identity-bound review confirmed the existing 240-character query constraint; the HTTP regression verifies oversized pins cannot populate the new negative recovery cache. A first proposed 128-character query test was rejected as an incorrect assumption: 128 is the stored checkpoint identity bound, while the existing query contract remains 240.

[Post-fix native repetition](metro-association-checkpoints-2026-09-28-native-repeat.json): **10/10 passed**, zero fallback snapshot reads, HTTP errors or browser errors, with successful 50-call pinned checkpoint recovery in every run. Per-run publication-to-DOM P95 ranged from **304 to 524 ms**; all met the one-second objective for this two-client synthetic scope. The first runs overlapped the final targeted Go/database checks. This does not measure production source freshness, physical accuracy, baseline persistence admission delay or full deployment capacity.

[Final current-code smoke](metro-association-checkpoints-2026-09-28.json), after the original-age guard also covered active inventory without ingestion: passed with 57 changed DOM revisions, P95 **542 ms**, zero fallback reads/HTTP/browser errors, and correct 50-call suspended pinned recovery. Runtime sample: 232,273,080 heap bytes, 356,972,888 system bytes, 11 goroutines and two streams. The focused age/baseline/checkpoint race rerun passed in 2.030 s; the oversized-query cache-bound check passed in 2.180 s. Final frontend build, generated and vet checks passed. Final local reference checking covered 14 documents and 407 targets, and `git diff --check` passed; web availability/section anchors and unchanged diagrams were not validated by those checks.
