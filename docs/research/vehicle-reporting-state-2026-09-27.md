# Vehicle reporting state investigation and validation

Date: 2026-09-27. This note preserves the delivery evidence; canonical current behavior belongs in the data catalogue and architecture references.

The user clarified that “stopped” meant no longer reporting, requested backend database updates, and authorized both implementation and publication alongside Metro/popup improvements.

## Source evidence

The inspected official Hub publisher replaces its cached position array, using original observations newer than 90 seconds and qualifying ride joins. Missing publication therefore does not establish a physical stop or raw telemetry withdrawal. Investigated revision `f90e9f91f3daa3fff60ba2582f3827f4ea4f1300` was not verified as the deployed provider version. [Hub publisher](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-vehicles/src/tasks/publish-vehicle-positions.ts), [selection query](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/sql/publish-vehicles/select-vehicle-positions.sql).

The inspected CM synchronizer derives its current complete array from Hub positions and can retain old publication after a failed replacement. Investigated revision `62fa16b7c3a4ca92c12be7d837a5fd88bcdda08c` likewise is source evidence, not a deployed-version guarantee. [CM synchronizer](https://github.com/carrismetropolitana/api/blob/62fa16b7c3a4ca92c12be7d837a5fd88bcdda08c/apps/sync-vehicles/src/tasks/sync-positions.ts).

These facts support keeping accepted normalized omission, old original observation clocks and unconfirmed source health as independent reasons. No source completeness or physical movement guarantee is claimed.

## Browser validation

The three targeted continuity checks passed: selected CP ages/expires offline, all eight operators age/expire offline, and stored reporting state warns immediately on omission, becomes unconfirmed on source error, and recovers on a fresh report without implying a physical stop. Existing no-reporting-field fixtures retain the age fallback. Frontend production compilation passed with the existing large-bundle warning. OpenAPI generated-file comparison and316 local link targets across 14 affected canonical documents passed; `git diff --check` passed.

The full PostgreSQL-backed Go race suite passed (`go test -race ./...`, app 36.174 seconds), and `go vet ./...` passed. Dedicated reporting integration/race checks passed on PostgreSQL (2.184 seconds including the revised availability regression) and native CockroachDB 26.2.6 (42.030 seconds). Tests cover immediate omission/recovery, unchanged membership batching, independent original clocks, source errors/restart, expired-position replay rejection, transaction rollback/retry, storage admission atomicity, stale-write rejection, bounded sweeps and visible clock advancement during database read failure. An older durability test was updated because first error/recovery reporting transitions now intentionally commit immediately, while repeated unchanged states remain batched.

A final focused capacity regression checks that failed-read placeholders can be evicted while uncommitted dirty versions remain protected; this correction follows the full-suite run. The final PostgreSQL-backed reporting race checks passed (2.856 seconds), final vet passed, and 327 local targets across 13 changed documents passed. Commit-gate and rollout results are recorded after their completion.

## Final commit checks and publication

Metro/popup commit `081a713` and reporting commit `cd78d80` each passed the pinned Go-only Maat gate at 90, delta 0, with no critical regressions and no suppressions. TypeScript is outside that gate; frontend build and browser tests were checked separately. Initial reporting candidates were rejected at 83: first for function complexity and then Store cohesion. The repair moved memory behavior into `reportingRegistry`, retained SQL/atomic publication orchestration in `Store`, and extracted focused publication/startup helpers. No policy bypass or scoring suppression was used.

Following the cohesion repair, the full PostgreSQL-backed race suite passed (app 43.358 seconds), and CockroachDB reporting race/integration checks passed (43.886 seconds). The final publication-helper extraction passed focused reporting/availability/staged-history race checks (2.558 seconds) and vet. Generated-file comparison passed after the structural changes; the API contract did not change during those repairs.

Executable commit `cd78d80065b2fc63c9e3d4f4c0c89cbecfb11cd8` was published to main and deployed. Go build metadata records that revision with `vcs.modified=false`. [Rollout metadata and hashes](vehicle-reporting-state-2026-09-27/deployment.json) identify the artifact, image and protected rollback backup. Both public health fields were `ok`; runtime environment values, limits and other container IDs were unchanged, and the dashboard had zero restarts at the final check. The first attempt started successfully but automatically rolled back because Docker environment ordering differed; comparing effective name/value mappings fixed that verifier error and the retry passed. The additive latest-state table remained in place during rollback.

The [bounded public API check](vehicle-reporting-state-2026-09-27/public-api.json) returned 447 retained vehicles, all carrying reporting state, with 126 exact latest values already committed at that instant; remaining clocks were pending their normal batch, not a durability claim. Five sampled Metro entities returned `published_route` with 18 visits each and null journey IDs. A subsequent 18-visit response check confirmed arrival/departure evidence remained unavailable with null actual/prediction/schedule/time values. The served frontend entry hash matched the local build.

A separate read-only SQL transaction confirmed [durable latest-state counts](vehicle-reporting-state-2026-09-27/database-state-counts.json). It used the dashboard's effective verified-TLS connection environment without publishing credentials; the temporary protected environment file was removed. Counts can differ between these independently timed samples and do not establish provider completeness or registered inventory. No production omission/error was injected to test behavior; controlled fixtures and database tests cover those transitions.
