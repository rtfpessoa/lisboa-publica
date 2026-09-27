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
