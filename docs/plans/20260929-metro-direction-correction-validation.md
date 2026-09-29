# Metro direction correction validation — 2026-09-29

This is dated implementation evidence, not the current application manual. Implemented semantics are documented in [Metro live behavior](../metro-live-popups.md), [associations](../data/associations.md), [integration reference](../integrations/metro.md) and the authoritative [OpenAPI contract](../../api/openapi.yaml).

## Changes verified

- Retained Red-line inputs reproduce the minimal two-row/three-pattern direction loss and the five-reference forecast-retention failure. Four initial regression tests failed before the correction, then passed. Original source/static fixture bytes, clocks and SHA-256 values are recorded in the [fixture reference](../../internal/app/testdata/metro-20260928/README.md).
- Uniquely embedded contiguous path variants share downstream geometry, with uncertain origin disclosed. Exact equivalent station forecasts combine while retaining contributing platform clocks. A rejected alternative cannot establish the opposite current direction by exclusion.
- Platform-specific revisions, same-clock differences and missing optional values preserve usable local predictions independently of movement/event support. Anonymous live forecasts remain visible without an invented train identity. Display row IDs remain stable across ordinary ETA revisions; original revised instants are not clamped to force a decreasing countdown.
- Fresh confirmed qualified direction gates current vehicle links, station counts, headings, navigation, follow and local modeled coordinates. Forecast-only priors cannot resolve competing directions. Original source gaps over 60 seconds withdraw direction continuity without fabricating completion.
- Mandatory lifecycle barriers and unavailable/cancelled mandatory staging retain committed history while withholding unsupported current links. Pinned records without qualified current support retain identity under historical recovery. Existing terminal/reversal and checkpoint generation rules remain in effect.
- Both popup forecast views retain official-only, own-only and both-source values. Supported own-only contexts retain model provenance and do not become current vehicle links; qualified own-only station rows are not duplicated as unassociated forecasts. Existing own-model collection/admission/comparison behavior remains unchanged.

## Checks actually performed

| Check | Result |
|---|---|
| Initial four new regressions against unmodified implementation | Failed on the reported direction/platform/link defects, as expected |
| `make test` (`go test -race ./...`, `go vet ./...`) | Passed after the main implementation |
| `go test -race ./internal/app -run TestMetro -count=1` | Passed after source-gap, recovery and mandatory barrier updates |
| Focused race tests for mandatory checkpoint failure, terminal/reversal, own-only deduplication and source gaps | Passed after the final mandatory staging changes |
| Focused race tests for retained Red inputs, source-clock/platform cases, anonymous/cohort cases, own-only contexts and stable forecast identity | Passed after the final row-identity changes |
| `make check-generated` | Passed after final OpenAPI generation |
| Frontend production build | Passed; existing bundle-size warning remains |
| `metro-live.spec.ts` | 16 passed, including desktop/mobile native EventSource delivery, own-only forecasts, uncertainty, countdowns, pin/successor and fallback cases |
| `metro-patterns.spec.ts` | 8 passed, including both forecast sources, own unavailability, archive failure, mobile presentation and comparison reports |
| Local documentation links/references and new fixture hashes | 249 links/references and both retained-byte hashes verified |
| `git diff --check` | Passed |

The full repository suite ran before the final isolated lifecycle/identity review changes; the affected seams were rechecked with targeted race tests afterward. No production observation, independent physical calibration or improved forecast-accuracy measurement is claimed by these results.

## Commit-gate preparation

The first normal Maat candidate was blocked at Go score 97/delta 0 with 18 structural regressions and zero suppressions. Platform revision selection/station equivalence, forecast canonicalization and unique order matching were separated into focused helpers. Local call construction and source-gap handling were extracted without changing source clocks, evidence admission or provider budgets. `go test ./internal/app -run TestMetro -count=1` passed after these repairs. The next normal gate verdict and release checks are recorded separately after they occur.

## Remaining evidence limits and operational scope

No current source admits a complete continuous cohort for live order-based ownership. The bounded unique monotone matcher is checked with explicitly synthetic complete/incomplete cohorts; anonymous live rows remain unidentified. This is a source-evidence limitation, not a license to infer missing trains from an incomplete top-three board.

No new direction adapter or production model configuration was qualified or enabled. When current direction lacks admitted evidence, the interface preserves usable forecasts under “Viagem por confirmar”. Opaque Hub coordinates and ETA-derived motion remain modeled context; no physical direction, platform, arrival or departure accuracy is asserted.

No provider polling, cadence, shared budget, collector, authentication or deployment configuration changed. Pre-existing edits to `CONTEXT.md` and `frontend/src/MetroPopupFollow.prototype.html` were preserved. No commit, push or deployment was performed in this implementation step.
