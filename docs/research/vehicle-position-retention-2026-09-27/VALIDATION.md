# Vehicle retention and permanent-fact validation — 2026-09-27

This records local implementation validation on `rodrigo/fix-vehicles`. It does not certify external provider uptime or the private CP collection pipeline. No extra upstream polling was introduced for these tests.

The implementation retains one latest position per exact operator/source identity for 24 hours from the original source clock. CP's old-position detail begins at ten minutes; other operators at five. Marker appearance and evidence rules do not use that detail threshold. Stable validated attributes persist independently, with field provenance and confirmation times, including compatibility guards for registration changes. The CM optional service-date join now compares millisecond clocks.

## Completed checks

- [Go race/integration log](go.txt): `TEST_DATABASE_URL='postgresql:///postgres?host=/tmp' go test -race ./...` passed, including isolated PostgreSQL schemas.
- `go vet ./...`, `make check-generated`, frontend `npm run build` and `git diff --check` passed.
- [Browser fixture log](browser.txt): 100 passed, two optional geometry scenarios skipped; three API-server-dependent scenarios excluded as described below. The final command was `UI_BASE_URL=http://127.0.0.1:5177 npx playwright test --grep-invert 'desktop public dashboard|optional local account|Metro station board advances'`.
- Local link checking verified 279 targets across ten affected documents before adding these evidence links; the completed report's local links were also checked.

## Behavioral evidence

- [Continuity tests](../../../internal/app/continuity_test.go): original clocks, all eight operators, repeats, restart, regressions, expiry and traversal of a 1,001-position retained inventory across every frozen page without duplication/truncation.
- [Permanent-fact tests](../../../internal/app/vehicle_facts_test.go): missing/invalid optional fields, zero/false, per-field confirmation, direct/catalogue precedence, first registration, conflicting registration, independent metadata projection, immutable prior revisions/history samples, durable restoration after position/history deletion, initial-read failure followed by an X-only response, transaction failure/retry, and low-precedence recovery of legacy attributes without renewing clocks.
- [CM join tests](../../../internal/app/popup_reads_test.go): original millisecond precision, exact identity/trip/time and rejection of ambiguity or mismatch; optional enrichment never gates positions.
- Browser fixtures cover original-clock 24-hour expiry, an open expired detail, CP's ten-minute boundary with unchanged rendered marker pixels, all eight informational thresholds, station/vehicle navigation and mobile gestures. Gesture coordinates are selected from exposed canvas below the additional count row, with an explicit hit-target check.

## Test environment and exclusions

PostgreSQL integration uses isolated temporary schemas through `TEST_DATABASE_URL`. Browser fixtures use Vite at port 5177. The initial unrestricted browser run also attempted three existing dashboard scenarios that require a populated API server at port 8080; that server was absent (connection refused). Those scenarios were not evidence of a feature regression and are excluded from the final fixture regression. They remain unvalidated against a populated server in this task. Two optional browser geometry scenarios require `GEOMETRY_BROWSER_FIXTURE` and are skipped when that fixture is not supplied.

## Resource measurement

[The resource log](resource.txt) uses previously explicitly downloaded official static fixtures for all eight operators, including the complete CM pattern index. The revised synthetic workload models 10,000 stable IDs per operator, observed in rotating groups of 1,000 over one hour; all 80,000 positions remain retained. The previous workload relied on the 500-position cap while generating unbounded new identities; it is not a valid memory guarantee after removing that cap.

The macOS run passed functionally in 105.19 seconds, retained 64 network revisions and 20,000 pending history entries, and reported 1,631,990,424 heap bytes and 2,648,890,488 runtime system bytes. It had no container memory limit. This does **not** certify operation under a one-GB Linux limit, a peak RSS ceiling, a real provider's daily identity inventory, or arbitrary ID churn. Preserve older resource reports as evidence for their original capped workload only.

## Commit gate and production rollout

The normal pinned Maat regression gate passed: CM join commit `f25e88d` at Go89/delta0; vehicle commit `a18a774` at Go90/delta0, no critical regressions and zero suppressions. Structural regressions in two prior candidates were repaired by separating the underlying responsibilities. TypeScript is unchecked by Maat; the absolute95 target remains unresolved. [Post-extraction full PostgreSQL race log](go-after-maat.txt) passed in36.142s, followed by focused fact/database race checks, vet, deterministic generation and the frontend production build.

[Deployment evidence](deployment.json) identifies executable revision `a18a7747aca38323b917cc50e8ddb0ab7c81aaf2` and verifies the running binary/assets. The first packaging attempt omitted the server executable permission; the deployment script automatically restored the previous healthy image. Correcting the package permission and retrying succeeded. The prior runtime image remains available for rollback. Container environment, isolation, limits, Cockroach configuration and six other containers were preserved.

[Production API traversal](production-api.json) retrieved all923 vehicles in one frozen revision, with no duplicate IDs, total equal to the page inventory and every original position deadline exactly24 hours after observation. All eight operator status records were OK with no reported error. Seven operators had positions; CP published and retained zero throughout the observed post-rollout cycles. This upstream absence is unresolved and is not represented as recovered CP service.

[Production browser validation](production-browser.txt) passed desktop public search/navigation/history/fleet, mobile navigation/layout and Metro board refresh (three passes,1.1 minutes). The optional development-login scenario correctly skipped because production disables development authentication. This supplies populated-backend evidence for the two previously excluded public dashboard scenarios; local key-management remains outside production validation.

A single [direct public Hub response](production-source.json) also contained zero raw CP agency `N18KL` rows with a null envelope error, confirming the observed CP absence precedes local normalization/joins/pagination. No conclusion about the private upstream CP collector is possible from this response.

[Bounded production runtime measurement](production-runtime.json) recorded a792,231,936-byte cgroup peak (755.5MiB) below the unchanged1,342,177,280-byte cap (1280MiB), healthy status, zero restarts, no OOM kill and all cgroup memory events zero. This observation does not certify arbitrary daily identity cardinality or future static-refresh peaks.

The additional [same-workload run with production Go settings](resource-production-gomemlimit.txt), `GOMEMLIMIT=768MiB GOMAXPROCS=2`, passed functionally in486.36 seconds but reached a1,538,490,368-byte macOS maximum RSS (1467.2MiB), above the1280MiB production container cap. Final live heap was877,887,496bytes and runtime system memory1,539,366,520bytes. The Go soft limit is not an OS limit. This80,000-position synthetic scenario therefore **does not pass the production memory ceiling**; the successful bounded production observation above is distinct and must not be generalized to that inventory or arbitrary24-hour churn. No display cap or workload reduction was added.
