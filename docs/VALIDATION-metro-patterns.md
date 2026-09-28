# First experimental Metro delivery

Scope was authorized on 2026-09-27: finish implementation and improve the setup with live collection after deployment. The representative week and physical validation are still pending operational work.

Implemented: ingestion from the existing direct refresh without additional provider requests, preservation of unknown wait fields, positive-to-zero signals supported through three consecutive planned stations, continuity revocation including known losses between detail samples, and arrival-to-arrival components. The profile separates topology, sampling and bin resolution. The own forecast adds only future components at their predicted origin hours while preserving the official forecast and the dependency on its anchor. Waiting and onward calls share point calculations without claiming duplicate records are independent physical journeys.

The persistent archive uses JSON+Zstd and Parquet+Zstd, checksummed immutable generations and a single-owner file lock. The local manifest/checkpoint replace the preliminary database-metadata proposal: publication and recovery stay on the same server volume without expanding operational database writes. Issued forecasts and later outcomes preserve knowledge times. Evaluation samples every minute; calibration selects the first eligible case per episode/target/horizon/function/mode/profile. Intervals use the corrected 80% rank and the histogram outer edge. Insufficient scores leave the point visible without a finite interval.

Automated checks cover:

- repeated clocks, DST, duplicate IDs/contexts/slots, missing presence and insufficient association;
- ordered windows, ambiguous topology, component bounds, strictly prior training and sequential future hours;
- official visibility when our point is unavailable, first calibration selection and bounded proxy errors;
- unknown-field and Parquet roundtrips, restart/deduplication, corruption, owner exclusion, global FIFO, allocated bytes, orphans and failed publication;
- public API, invalid stations/parameters, scopes and UI with sparse data, collection pause, onward calls and mobile layout.

A regression reproduced failed initialization overwriting an invalid manifest. Cleanup now releases resources without publishing into an archive whose initialization failed. The regression preserves the original bytes. See the [debug record](/Users/rodrigo.fernandes/docs/plans/debug/20260927-metro-archive-init/DEBUG.md).

Before integrating the newer deployed baseline, all Go packages passed Postgres integration with race detection, generated contracts matched, go vet and frontend build passed, and 57 browser checks passed with two existing geometry-fixture checks skipped. Final baseline checks and deployment evidence are recorded separately; these earlier checks do not certify the final integrated revision by themselves.

Limits: no physical arrival/departure validation, measured dwell, speed or progress; no admissible physical occurrence denominator/Wilson interval; no automatic recent adjustment. Components use model midpoints of proxy windows, not observed physical durations. Weekday grouping does not classify holidays and the selector says so. Incompatible historical experiments are not imported. FIFO and memory/buffer bounds can reduce effective retention/support, with explicit gaps and warnings. Live operation should measure cost, availability, association stability and calibration before any method promotion.

Codec and format dependencies: [klauspost/compress](https://github.com/klauspost/compress) and [parquet-go](https://github.com/parquet-go/parquet-go), pinned in go.mod.


## Integrated deployed baseline checks

The worktree was fast-forwarded to deployed commit `cd78d80065b2fc63c9e3d4f4c0c89cbecfb11cd8`, preserving the existing glossary changes in a safety stash. Shared API/UI/startup conflicts were resolved by combining the new collector with current navigation/reporting; generated clients were regenerated. Postgres race tests pass on this baseline; frontend build, go vet, generated parity and 272 affected-document local links passed.

The broad browser run passed 108 checks and skipped two existing geometry-fixture checks. Two failures were investigated: an older assertion expected a Metro popup warning intentionally removed in the deployed baseline, and a concurrent targeted run reused the trace directory, causing ENOENT at context cleanup. The old assertion was aligned with the unchanged deployed popup; targeted rechecks use an isolated directory. These failures did not justify changing the deployed popup behavior.

The compiled ARM64 archive suite passed on the server in a network-disabled, read-only runtime with 384 MiB memory, 0.5 CPU, a 192 MiB Go memory limit and an isolated ext4 fixture directory. All 11 top-level tests and six subcases passed; no provider/database calls were made.

The isolated browser recheck passed three checks, covering the corrected popup assertion and both overlay layouts. A final synthetic regression reproduced a zero first slot hiding the valid next official slot. Waiting now selects the first admissible future slot; physical signal inference remains unchanged. The regression and archive race suite pass.

## Live deployment verification

Final image `lisboa-publica:metro-patterns-6b935d193e5d` was deployed on 2026-09-27. Source fingerprint `6b935d193e5dabed78f8e070c1d21d80417be3600fce92940923b5f83e9600a3` identifies the uncommitted feature artifact based on deployed Git commit `cd78d80065b2fc63c9e3d4f4c0c89cbecfb11cd8`; checksummed binaries and the source patch remain in the server release directory. See [deployment and rollback](../deploy/README.md#release-policy-and-earlier-experimental-evidence).

The final ARM64 suite passed all 12 top-level tests and six subcases under the same network-disabled constrained Linux runtime. The final local archive race suite, targeted go vet and three patterns browser checks passed.

At 21:56:51 UTC, the container was healthy with no OOM kill. Public health, patterns API and root page returned HTTP 200. Collection was active, with 249,856 allocated archive bytes under the configured 10 GB limit, 30-second sampling/bin resolution and four explicitly recorded continuity gaps. No physical validation is asserted. The detail, aggregate and checkpoint blocks survived the release restart. At 21:57:18 UTC, the verified checkpoint retained 17 current-hour receipts, 19 planned patterns and 223 hot aggregate rows. These rows include receipt coverage, not confirmed train arrivals. There were zero supported live associations and zero own live points at that check; cold-start unavailability is expected and does not certify future model availability. Detailed real payloads remained on the server; only scalar verification summaries were copied locally.

Live collection now continues without extra provider requests. After sufficient operating coverage, review archive growth, continuity losses, association availability and proxy-calibration results before tuning configurable cadence, retention or model rules. A representative week, physical reference and further operators remain subsequent work.

The public browser smoke test loaded the patterns page with 74 station choices and the few-data warning, without exporting screenshots, traces or real train records. Initial patterns reads encountered the existing shared admission guard (`503 busy`); the scheduled refresh recovered with HTTP 200. The successful verification included two transient 503 responses before recovery, so immediate first-load availability is not guaranteed under concurrent reads. All 274 affected-document local links passed, and `git diff --check` passed after the final documentation updates.

## Admission recovery follow-up, 2026-09-27

Live follow-up at 21:59:55 UTC confirmed a healthy collector and 319,488 allocated archive bytes. The early dataset does not support accuracy or annual capacity conclusions. A practical UI issue was confirmed: station and onward queries used `retry:false`, overriding the shared application policy for `503 busy`. The new synthetic browser regression failed before the fix because the onward table was absent after 15 seconds. Removing both overrides restores the existing bounded retries, Retry-After and backoff/jitter; polling, provider calls and collection settings are unchanged.

All four patterns browser checks passed, including independent transient admission failures for station and onward reads. The frontend build passed with the existing map chunk-size warning. No server code or contract changed; the server binary checksum matches the preceding release, so the already-passed backend suite was not rerun.

The update was published as `lisboa-publica:metro-patterns-4940d7b91b51`, with source fingerprint `4940d7b91b513fa555f6a8ea6cf9d110f9e87100f8e172f095e86c9cfc2dc040` and a reviewable uncommitted source patch. At 22:02:42 UTC, the container was healthy without an OOM kill and collection remained active under the unchanged 10 GB budget and 30-second settings. Eight continuity gaps were explicitly reported, including release restarts; they are not train cancellations. The public browser saw two `503 busy` responses followed by HTTP 200 and displayed the page with 74 station choices and the few-data warning approximately two seconds after opening the tab. This verifies recovery in that case, not guaranteed availability under sustained overload. No real train records, screenshots or traces were exported.

The [debug record](/Users/rodrigo.fernandes/docs/plans/debug/20260927-metro-patterns-busy/DEBUG.md) records the reproduction and fix. [Deployment instructions](../deploy/README.md#release-policy-and-earlier-experimental-evidence) point to the current release; previous artifacts remain available for rollback.

## Local-only rebase onto main, 2026-09-27

The subsequent user instruction requires rebasing onto current `main`, testing locally and deploying only commits already on `main`. This supersedes the earlier deployment authorization. The earlier release records above remain historical observations; they do not describe the current production revision or authorize reuse of those artifacts. No server access, packaging, deployment or publication was performed during this rebase.

The branch was rebased from `cd78d80065b2fc63c9e3d4f4c0c89cbecfb11cd8` onto `origin/main` at `53fbe4c347032f770622896cd4273af515665760`. There were no feature commits to replay; the implementation and pre-existing glossary changes were retained in a named safety stash and reapplied as local changes. The safety stash remains available. Four stash conflicts were resolved: the documentation index retains both sections; App retains the newer station popup/navigation and adds only the patterns tab; the availability regression preserves both assertion sets; the Go API client was regenerated from the merged authoritative contract. Upstream station coverage schema changes and all existing API operations/schemas were verified unchanged.

Completed local checks: frontend typecheck/production build, all Go packages with race detection against the isolated local PostgreSQL test database, go vet, generated Go/TypeScript parity, affected Go formatting and 284 local documentation links. The build retains the existing map chunk-size warning. Browser integration checks cover patterns, availability, station refresh, stop navigation and transit popups; their final result is recorded below. The [deployment guide](../deploy/README.md#release-policy-and-earlier-experimental-evidence) now requires a clean reviewed `main` commit and no longer offers the uncommitted experimental overlay as an active release command.

Final local browser result: **53 passed** in 3.4 minutes, with no failures or skips. The command used `UI_BASE_URL=http://127.0.0.1:5173` and an isolated `/tmp/lisboa-metro-main-rebase-ui` output directory. It covered `metro-patterns.spec.ts`, `station-refresh.spec.ts`, `stop-navigation.spec.ts`, `transit-popups.spec.ts` and `availability.spec.ts`. All provider/API responses in these browser scenarios were synthetic fixtures. The local safety stash is named `metro-patterns-before-main-rebase-20260927-53fbe4c`; feature changes remain uncommitted and unstaged for review. No rebase or conflict resolution remains in progress.

## Local gap-completion checks, 2026-09-27

Work remains based on `53fbe4c347032f770622896cd4273af515665760`, local, unstaged and uncommitted. No SSH, live application/provider calls, image packaging, push or deployment occurred. Existing glossary edits were preserved. Dockerfile changes include the maintenance binary for a future reviewed release; a container build was not performed in this follow-up.

Implemented locally: durable bounded comparison reports and association support, labeled older/general-context fallback, conservative mixed-bin calibration, identical-segment compatibility across plan changes, actual-line conditions, versioned Lisbon civil holidays, evidence-backed retained-input revisions, and staged normalized observation capture for every existing operator under shared archive accounting. Other-operator disk work runs through one bounded writer queue; overflow/write failure preserves collection-gap provenance. These stages do not yet have own-arrival forecast adapters. Physical probability/dwell/speed and independent accuracy/capacity conclusions remain unsupported.

All Go packages passed `go test -race ./...` with `TEST_DATABASE_URL=postgresql://localhost/lisboapublica_metro_implementation_test?sslmode=disable`. Subsequent changes were checked with race tests for the affected archive/app packages, including bounded queues, late gap notifications, source-clock preservation, staged configuration/restart, calendar dates, historic/general fallback, sampling isolation, mixed-resolution rank, complete-day restoration without resurrecting withdrawals, durable report support, summary limits, cancellation and stale-correction rejection. Maintenance compiled in the Go package checks.

Frontend typecheck/production build, go vet, generated Go/TypeScript parity and diff whitespace checks passed. The existing large map-chunk warning remains. The broad local browser suite passed **55 tests** in 3.5 minutes, covering patterns, station refresh, stop navigation, transit popups and availability with synthetic responses. It includes the added comparison/fallback/operator-capability and holiday selection scenarios. Its isolated output directory is `/tmp/lisboa-patterns-gaps-ui-20260927`. A final patterns-only recheck follows the last limited-summary disclosure change; its result is recorded below.

Source-rule provenance and current semantics are in [the application manual](metro-patterns.md). The [audit follow-up](GAPS-metro-patterns.md#local-implementation-follow-up-2026-09-27) separates completed local work from outstanding operator adapters and measurement/reference work. No map ticket requiring representative live or independent physical evidence was closed on synthetic tests.

Final UI recheck: **6 patterns tests passed** in 16.1 seconds after the limited-summary disclosure change, using `/tmp/lisboa-patterns-final-ui-20260927`. The final frontend build and generated-client parity passed. The local Vite process was stopped after checks. Global FIFO coverage also includes other-operator detail; queue-gap notifications retained between sampling instants remain distinct from new provider requests or renewed observation clocks.

Final affected-document verification: **391 local links** across 16 Markdown files passed; affected Go formatting and `git diff --check` passed. No staged changes, merge conflicts or rebase operation remain. The four physical/reference/capacity map tickets and subsequent own-forecast adapters remain open.

## Local operator-adapter completion, 2026-09-28

The work remains based on `53fbe4c347032f770622896cd4273af515665760`, unstaged and uncommitted. Existing glossary edits are preserved. No SSH, live application/provider requests, image packaging, publication, push or deployment occurred. Historical release records above remain dated evidence and do not authorize a release outside main.

All eight existing operators now have staged experimental adapters and operator-scoped API/UI selection. Later stages require exact published journey/service paths and coherent reported stop-state transitions; they do not infer physical identity. Tests cover remaining adjacent components, both waiting/onward functions, fresh official cold-start values, incompatible/stale/ambiguous evidence rejection, independent prediction namespaces, per-identity unsampled continuity cuts, durable report/calibration, dictionary deduplication, bounded live component references, queue clocks/capacity disclosure, publication rollback, complete-day recovery, and retained evidence-backed revisions preserving original issuance. Official values from the existing accepted cache also remain response-only when history is disabled or unreadable; they never alter sampled evaluation cases or renew source clocks.

Final backend check passed: `TEST_DATABASE_URL='postgresql://localhost/lisboapublica_metro_implementation_test?sslmode=disable' go test -race ./...` (app 40.925 s, patterns 6.415 s). `go vet ./...`, generated Go/TypeScript parity, frontend typecheck/production build and formatting for 49 affected Go files passed. The build retains the existing large map-chunk warning. Local tests use synthetic fixtures and the isolated local PostgreSQL database.

Initial checks exposed two midnight-dependent backend fixtures and a browser selector that also matched concatenated row/time text; the fixes preserve production validation and use explicit synthetic times/identity. A browser run was interrupted by client-generation hot reload. Final browser checks use the built static preview without source/build changes. Two mobile gesture preconditions then raced the closing sidebar transition; polling the same canvas-hit condition preserves the original gesture assertion, and all three targeted desktop/mobile drag/pinch tests passed. See [the local investigation](/Users/rodrigo.fernandes/docs/plans/debug/20260928-transport-fixture-clock/DEBUG.md).

The [current manual](metro-patterns.md), canonical architecture/data/integration/deployment documents and [dated gap follow-up](GAPS-metro-patterns.md#local-operator-adapter-follow-up-2026-09-28) describe the implementation and limits. A representative live week, allocated capacity measurement and independent physical reference remain outstanding. Physical probability/Wilson, dwell, measured Metro speed, residual progress and physical accuracy improvement are not certified by these tests. The four evidence-dependent map tickets remain open.

Final browser result: **57 passed** in 2.4 minutes, no failures or skips. The static local preview served the final build at `http://127.0.0.1:5173`; the suite covered patterns, station refresh, stop navigation, transit popups and availability. Output is isolated in `/tmp/lisboapublica-patterns-completion-ui-20260928`. All API/provider data in these scenarios were synthetic fixtures. The preview was stopped after validation. Final local documentation-link, conflict-marker, formatting and whitespace checks passed; no merge/rebase operation or staged change remains.

## Authorized release preparation, 2026-09-28

A subsequent instruction authorizes completing implementation, repairing Maat issues, committing, pushing to main and deploying the committed revision. This supersedes the local-only instruction for subsequent work; earlier observations above retain their dates. Origin main was fetched and remains `53fbe4c`. The first normal commit gate blocked the candidate at Go 68/delta −22, zero suppressions; no commit or release was created. Structural repair is in progress, preserving the tested source/continuity/forecast/archive rules. See [Maat preparation](MAAT.md#transport-patterns-candidate-2026-09-28).

### Recovery and read correctness, 2026-09-28

Synthetic red/green regressions reproduced complete cold-day erasure under a full provider hot-cache budget, stale/past Metro display points, and incompatible histogram precision becoming available again. Recovery now marks a day dirty only after accepting an update and remembers durable recovery coverage even when a complete day cannot enter memory. Metro live reads withdraw expired model points using their original anchor freshness bound, independently expire official points, and leave original issued forecasts unchanged. Unknown common resolution remains unavailable throughout the summary. Focused race tests for patterns and app passed after these repairs. These checks do not establish physical arrival accuracy.

Additional synthetic red/green checks reproduced unpublished-day eviction, partial reopening of an evicted day, and acceptance of an oversized Metro pending-forecast replay. Hot eviction now preserves pending days and prevents partial cold-day replacement; Metro maintenance enforces replay and replacement capacity and restores affected days in a private engine. These are durability/bounded-work checks, not physical transport validation.

### Prepared release checks, 2026-09-28

Full PostgreSQL-backed `go test -race ./...` passed again after ownership and HTTP/filter separation (app 41.467s, patterns 12.424s). The deterministic OpenAPI check and `go vet ./...` passed. The TypeScript production build passed; the existing large map chunk warning remains. A static production preview passed all 57 relevant Playwright cases in 2.4 minutes, including operator patterns, station refresh, mobile navigation, transit popups and availability. The preview was stopped afterward. All 401 local Markdown targets checked in the candidate exist; whitespace and conflict-marker checks passed.

The normal pinned Maat gate accepted candidate five at Go94/delta+4 but Git signing failed in the configured 1Password agent. Candidate six reached Go97/delta+7 and was blocked for two filter return-count regressions, which were subsequently separated into window, range and hour validation. No commit, push or production replacement is implied by these prepared-candidate results. TypeScript is independently compiled/tested and remains outside Maat's language coverage.

Candidate seven passed the normal pinned Go gate at97/delta+7 with no structural regressions and zero suppressions, exceeding the absolute95 score target. Its Git signing operation failed (`1Password: agent returned an error`). A subsequent read-only SSH inventory attempt also failed at agent authentication; no remote inventory commands executed and no server change occurred. The GitHub API independently confirmed `main` remained `53fbe4c347032f770622896cd4273af515665760`. The active implementation is still local; commit, push and clean-main deployment await working authentication.

A focused regression additionally verifies that a fresh target-station source clock cannot renew an expired model anchor: the originally issued own point exists, current reads withhold it, the independent official target point remains, and the retained issuance digest does not change.

Further synthetic regressions cover fresh official cold starts retaining the association-unavailable explanation and a published day remaining available while a current association can still withdraw its statistics across midnight. Both failed before their respective corrections. Display expiry now preserves an existing unavailable reason when no own point was issued; capacity eviction protects referenced signal days. The final PostgreSQL race run before these narrow corrections passed (app36.023s, patterns10.730s); focused app/patterns race checks were repeated for the corrections.

One unauthenticated read-only request to the public `/api/v1/metro/patterns` returned404 before release. This is a pre-release endpoint availability observation, not a live validation of the new implementation or evidence of collected volume. No raw provider records were exported.

## Release resumed after local PostgreSQL cutover, 2026-09-28

The user explicitly authorized completion, normal Maat repair, commit, push to
main and deployment again after the database incident. The prepared branch is
rebased onto `61c5f3249c526119c6872ac403e0d662532365f4`. The transport Go/TypeScript
implementation is unchanged from the previously checked tree; the database
configuration and canonical documentation now select the clean local PostgreSQL
volume. The preceding signing failures remain historical evidence; normal signed
configuration commits and server SSH access subsequently succeeded.

Repeated full local PostgreSQL-backed `go test -race ./...` passed (app 49.137s;
patterns cached). `go vet ./...`, deterministic generated Go/TypeScript parity
and the TypeScript production build passed. The existing large map-chunk warning
remains. All 57 browser release checks passed in 2.5 minutes against a static production
preview, using synthetic API/provider fixtures. Output is isolated in
`/tmp/lisboapublica-patterns-release-ui-20260928`. Affected Markdown checks found
404 existing local targets; whitespace and conflict-marker checks passed.

Before publication, the host configuration was rendered with the candidate base
Compose and every current runtime override, including the final local-database
override and a final image-only override. It preserved the local database URL,
`lisboa-publica_postgres_20260928` volume and existing dashboard environment;
only the explicit first-stage `TRANSPORT_ARCHIVE_OPERATORS=metro` default was
added. The transport archive volume owner was UID 10001, with 335,872 allocated
bytes and a version-1 manifest containing four existing blocks. No archive was
cleared. Protected runtime inspections, Compose/environment copies, a transport
archive copy and PostgreSQL dump were saved before rollout under
`/root/lisboa-patterns-main-release/20260928`.

These preparations are not a deployment result or an independent physical
validation. The four representative-volume/capacity/reference map tickets remain
open. No inferred event is promoted to a physical occurrence, dwell measurement,
measured Metro speed or accuracy improvement by these checks.

## Committed main release and production verification, 2026-09-28

The normal signed code commit succeeded as
`044af1487488d50d16bf481c14f79be9461877c3`. The pinned Maat gate passed at
Go97/delta+7 against `61c5f32`, with no structural regressions and zero
suppressions. TypeScript remains outside Maat coverage and was validated by
its compiler/build and the 57 passing browser fixtures. The commit was pushed
as a fast-forward to main; the GitHub API confirmed that exact revision before
building and again before rollout. Both local main and the clean host checkout
fast-forwarded to this revision.

### Build and preserved runtime

Docker Compose built `lisboa-publica:044af1487488d50d16bf481c14f79be9461877c3`
from the clean committed main checkout. The build completed successfully,
including frontend, server, maintenance executable and the verified Metro TLS
intermediate. The image revision label matched the commit and its user was
`dashboard`. The server and frontend were built together from that same source.
The image config digest was
`sha256:2a4c9521153cde7f83985bc44ea12421e2914a5f5254d54daba4c1cea687d75a`.

The existing protected environment's `VERSION` was updated to that SHA.
Every active runtime override was preserved, including the final local-database
override; an image-only override was appended last to prevent an earlier image
pin winning. Only dashboard was recreated with `--no-deps --no-build` after
image verification. Dashboard and PostgreSQL remained healthy. No database
container, other service or persistent volume was replaced or deleted.

Post-rollout inspection confirmed the same local database connection,
`lisboa-publica_postgres_20260928` data, archive mount, other environment values,
CPU/memory limits, read-only filesystem, capabilities, security options, tmpfs,
logging/restart policies and networks. `TRANSPORT_ARCHIVE_OPERATORS=metro` was
the only added dashboard setting. Seven later operator stages remain disabled;
their implemented APIs expose that state rather than claiming active collection.
The backend, maintenance and all frontend artifact hashes in the running
container matched the built image.

| Artifact | SHA-256 |
|---|---|
| `/app/server` | `f1a1e633181e13e92fe6fc65035324bb1b53fa4da8e1f41f7ec63045dc463096` |
| `/app/patterns-maintenance` | `6b1d0e0b18e63c7f5f2fd0b5f2aafff6935b8def7289d95271101602aca6119f` |
| `/app/frontend/dist/index.html` | `0ee7c7d2dfcdb41a01222c65cb891ec194cf4b26da08d2068cc281230b93f764` |
| `index-DJsXXKm3.js` | `3593cbb9207b931c460c5b7b0afb4c6110e4c34ad870b8f03cd3036770454d57` |
| `MetroPatterns-D3UsZ1by.js` | `569576f463c490a98e71283deed9c8c4f853e3352a1d83eba2ce58acd48412d9` |

### Real application and persistence checks

Public health returned200 with `database=ok`, `status=ok`. The Metro patterns
endpoint and all eight operator-scoped patterns endpoints returned200. Metro
reported `collecting`; later stages reported `disabled`. Responses retained
30-second sampling, seven-day detail, 12-month aggregate targets and the
10,000,000,000-byte archive limit. Physical-validation flags were false and
physical dwell/speed values remained null.

A real Chromium session opened the production Padrões tab without fixtures,
verified its heading, all eight operator options, populated station selector,
24-hour grid, both forecast sections and few-data disclosure. No JavaScript
page error or pattern error panel occurred. Alameda selection
`metro:ML11060001` returned four fresh official forecasts and zero own points,
with insufficient association support disclosed. This verifies the independent
official fallback, not successful own-forecast accuracy. The synthetic browser
suite separately exercises supported own points and onward calls.
The live screenshot and scalar result are in
`/tmp/lisboapublica-patterns-live-release-20260928.png` and the adjacent `.json`
on the local workstation.

The archive published current detail, aggregate and checkpoint generations.
Six compressed published blocks had SHA-256 checksums matching the manifest;
a checkpoint was durably published at `2026-09-28T08:43:36.925859293Z`.
An earlier post-rollout sample counted352,256 allocated archive bytes.
Existing archive evidence was preserved subject to normal reconciliation,
rather than cleared. These short samples are not a representative-week growth
measurement or a guarantee of 12-month retention.

PostgreSQL generation advanced from706 to760 while retaining10,057 historical
snapshots; its measured database size was44,742,323bytes. Startup logs contained
no warning/error records during the performed check. One transient resource
sample measured556MiB dashboard memory under its1,280MiB limit and60MiB
PostgreSQL memory under256MiB; dashboard CPU reached its configured1.5-CPU
limit during warmup. This does not establish steady-state resource capacity.

Protected release files, image hashes, build log, inspections, manifest snapshot,
Compose command, archive copy and database backup remain under
`/root/lisboa-patterns-main-release/20260928` on the server. To roll back, use
the protected prior configuration and main image while retaining the local
PostgreSQL/archive volumes. Subsequent documentation commits record this
verification; they do not change the deployed executable revision above.

The four independent-reference/capacity map tickets remain open. Live receipt
and checkpoint persistence do not establish physical arrivals, dwell, velocity,
Wilson occurrence denominators, nominal physical interval coverage or accuracy
improvement over official predictions. Collection can now accumulate evidence
for later setup improvements without additional capture polling.
