# Maat cleanup

Current implementation revision `cd68383e9d07baad6bf832977f8ccbed1d5be080` passes the normal pinned Go gate at **98/100**, delta **+1**, with no critical regressions and zero suppressions. The [gate result](validation/metro-live-completion-2026-09-28-maat.json) records the analyzed command/app/patterns/generated-API scopes and bundle. This scope differs from the earlier app-only96 result. TypeScript remains outside Maat; the compiler, affected browser suite and native EventSource checks are recorded in the [completion validation](validation/metro-live-completion-2026-09-28.md). Maintainability and implementation-simplicity coverage remains incomplete. No gate rules or thresholds were changed.

Earlier code revision `0d69c10300bbd86950c1a54079d7f95c95426dae` passes the normal pinned Go gate for `internal/app` at **96/100**, delta **0**, no critical regressions and **zero suppressions**. The [scoped gate result](validation/metro-live-station-maat-2026-09-28.json) compares the same app scope in the feature baseline. The preceding whole-project feature result was97; these scopes differ. TypeScript is outside Maat coverage and passed its production build and nine Metro browser fixtures separately in the feature release. The gate reports incomplete maintainability and implementation-simplicity metric coverage; acceptance does not mean zero advisory findings. The [rollout record](validation/metro-live-main-rollout-2026-09-28.md) records production verification and the station-family correction. Earlier dated scores below retain their original scope.

The pinned Maat bundle and normal global Git hooks remain enabled. No thresholds were lowered and no rules were ignored. Maat analyzes the Go source in this repository; TypeScript is validated separately by its compiler and browser suite.

The initial Go score was **47/100**. After extracting GTFS archive/table handling, authorization, verified Google claims, Metro platform predictions, ingestion normalization/enrichment, schedule rendering, filter parsing and null-safe fleet sorting, the score is **68/100**. Exported API documentation and policy constant names were improved as well. The configured absolute threshold is **95**; that absolute scan **does not pass**.

Remaining findings include complexity/maintainability, return-count limits on Go error guards, parameter counts at existing transport interfaces, module-cohesion heuristics, and exported-method test-name heuristics despite HTTP integration coverage. These are recorded as unresolved advisories, not represented as a clean Maat result. Further structural changes should be weighed against the requested small scope and avoidance of unnecessary abstractions.

Validation after cleanup: Postgres race tests, Cockroach race tests, Go vet, deterministic OpenAPI generators, TypeScript production build, and four Playwright dashboard checks. Deployment-specific proxy and configurable-retention behavior have focused HTTP/database regression checks. See VALIDATION.md for the latest results.

The first normal commit attempt encountered a gate implementation error: an existing empty initial Git tree was sent to Maat as a diff baseline, which produced “no modules found.” The gate's `_base_commit` now recognizes an empty tree and uses its existing first-snapshot policy, still analyzing the complete staged candidate. The normal hook remains installed. This narrow repair and a regression test are local changes in code-factory's `git/scripts/maat_gate.py` and `scripts/test_maat_gate.py`; all22 gate tests pass. Neither Maat's pinned binary nor thresholds were changed.

The storage amendment's normal commit gate passed with scoped Go score70 (delta+5 versus its analyzed baseline), no critical regressions and zero suppressions. A preceding attempt was blocked for new return-count, startup complexity and file-cohesion findings; main separated database-pool configuration, guarded initialization and storage-budget decisions by their actual responsibilities, then retried through the normal hook. The absolute95 target is still not claimed to pass. TypeScript remains separately checked.

The CP/metrics/route-overlay amendment's normal gate passed at scoped Go score **74**, delta **+3** against its analyzed baseline, with no critical regressions and zero suppressions (commit adfa4c6). Two preceding attempts correctly blocked new shape parsing/simplification return-count and maintainability findings. Main extracted the actual parsing, geometry deviation/mask, response-cache admission and CM refresh responsibilities, preserving behavior under Postgres/Cockroach and browser checks. The configured absolute95 target remains unresolved; this is acceptance by the normal regression gate. The Caddy-only compression commit has no supported source-language changes and the normal hook skips analysis for that commit.


The availability/fleet/polling amendment's normal commit gate passed at scoped Go score **80**, delta **+6** against baseline12d25a4, with no critical regressions and zero suppressions (commitd1289a6). The preceding attempt blocked five return-count/maintainability regressions. Main extracted committed-coverage querying/scanning, Metro publication, verified metadata merging and fleet normalization/search responsibilities, then repeated Postgres full race35.748s, affected Cockroach race40.187s and vet successfully. Independent quasar-alpha/xhigh review confirmed behavior and code acceptance were preserved. Normal hooks, pinned bundle and thresholds remain unchanged; the absolute95 target is not claimed. TypeScript/compiler/browser checks remain separate.


The search/unsupported-state amendment (72c8a63) changes TypeScript/CSS/docs only. The normal pinned Git guard correctly skipped supported-language structural analysis; no Go source/spec or thresholds changed. TypeScript production compilation and21 serialized browser checks passed, with the affected post-cleanup depot/error regression passing separately. No absolute-score or TypeScript Maat-pass claim is made. The first signing attempt failed because the1Password agent was locked; after the user unlocked it, the normal signed commit and SSH push succeeded without bypasses.


The provider-continuity/rail-ferry overlay commit `be07fc3` passes the normal pinned regression gate at scoped Go score **84**, delta **+3**, no critical regressions, zero suppressions. The first attempt correctly blocked31 regressions; main split publication/projection/revision, GTFS table/integrity/geometry, cache serialization and test responsibilities without changing behavior or workload. Full Postgres/Cockroach race, generation/vet/build, official fixtures,26browser checks and1,011.4MiB retained-network resource validation pass after refactoring. TypeScript remains separately validated and unchecked by Maat; the absolute95 target is unresolved.

The narrow rollout correction `9f1668c` passes the normal Go gate at83/delta+1, no structural regressions/zero suppressions. It detects unattempted legacy train/ferry geometry without changing normal TTL or upstream limits. Production checks verify the corrected image, all3 overlays and real CP last-known continuity; detailed evidence remains in VALIDATION-provider-continuity-overlays.md.

The vehicle-state/CP-scheduled-service implementation `4cfe355` passes the normal pinned Go gate at84/delta0, no critical regressions and zero suppressions. Six preceding construction/join/GTFS stop regressions were corrected in main by separating existing responsibilities, without changing source clocks, identity joins, workload, policy or thresholds. Full Postgres race and official cache checks pass after those extractions; TypeScript/compiler/43browser checks remain separate. The absolute95 target remains unresolved.

The narrow ambiguous-CP-endpoint correction `7d6e9a5` passes the normal Go gate at83/delta0, no structural regressions and zero suppressions. The first correction attempt blocked a worsened logical condition; main extracted endpoint-local completeness, repeated full Postgres race14.263s/affected Cockroach race7.403s and vet, then obtained independent re-acceptance and the normal signed commit. Thresholds and hooks remain unchanged.


A integração de chegadas e seleção de paragens passa o gate normal Go: a candidatura completa inicial obteve88/delta+2; o commit final corrigido, no âmbito `internal/app`, obteve **87/delta0**, sem regressões críticas e com **zero supressões**. A extração separou leitura CM, identidade/visitas TML, datas, snapshots, veículos e composição HTTP; limites e workload mantiveram-se. PostgreSQL full race, vet, geração/build e o ensaio de redes completas (1002,09MiB) passaram após a extração. TypeScript continua validado pelo compilador e navegador, sem análise Maat; o threshold absoluto95 permanece um advisory não resolvido. Evidência em VALIDATION-stop-arrivals.md.

The 24-hour vehicle retention/permanent-fact commits pass the normal pinned Go regression gate: CM clock correction `f25e88d` scored **89/delta0**; the complete vehicle change `a18a774` scored **90/delta0**, with no critical regressions and **zero suppressions**. Two blocked vehicle candidates were repaired by separating fact acceptance, registration transitions, field projection, database scanning/recovery and publication responsibilities, without changing clocks, workload or thresholds. Postgres full race, focused fact race, vet, deterministic generation and the TypeScript production build pass after extraction. TypeScript remains unchecked by Maat; the absolute95 target is not claimed. See [dated validation](research/vehicle-position-retention-2026-09-27/VALIDATION.md).

The universal ten-minute position display policy commit `5d3ee7f` passes the normal pinned Go regression gate at **90/delta0**, with no critical regressions and **zero suppressions**. No structural repair, gate change or suppression was necessary. TypeScript remains unchecked by Maat; its production build and 40 distinct browser cases passed separately (two optional official-geometry cases skipped). Full Go race tests, vet, deterministic generation, 154 local documentation links and whitespace checks passed. This change uses a five-minute warning for all eight operators and ten-minute expiry from original source clocks, including disconnected clients; it replaces the earlier 24-hour display policy. The absolute95 target remains unresolved.


The station popup commit `383bae7` passes the normal pinned Go regression gate at **90/delta0**,
with no structural regressions and **zero suppressions**.
The first candidate was blocked for station-coverage complexity, logical operators and maintainability;
evidence presentation and collector availability were separated without changing coverage rules or clocks.
Full Go/race, vet, generation consistency and the TypeScript build passed;
49 distinct relevant browser cases passed across targeted runs.
TypeScript remains unchecked by Maat, and the absolute95 target remains unresolved.
See [dated verification](research/station-popup-stability-2026-09-27/VALIDATION.md).

## Transport patterns candidate, 2026-09-28

The first normal commit attempt was blocked: scoped Go score **68**, delta **−22** against main `53fbe4c`, with zero suppressions. TypeScript remains unchecked by Maat. New findings include excessive inference/archive/reprocessing complexity, maintainability and size, checkpoint/configuration logical conditions and oversized integration tests. No commit was created and no push or deployment followed this blocked candidate.

Repair is in progress. Official-cache selection, journey construction, geometry evidence, configuration/checkpoint checks, Metro receipt/signal/association/forecast/calibration/evaluation, provider observation/track/signal/prediction/case handling and archive ownership/recovery/FIFO/publication have been separated by responsibility. Source clocks, admissibility, remaining-component semantics, original issuance and archive transaction order remain covered by regression tests. This paragraph is not a clean gate verdict; further candidate checks and repairs are required before publication.

The second normal candidate was also blocked (**74**, delta **−16**, 141 blocking regressions, zero suppressions). Further repairs separate complete-day restoration, per-frame revision scope, recorded-forecast replay, durable revision publication, station summaries and read-time expiry. Integration assertions remain in smaller purpose-specific helpers; no gate rule, threshold or suppression was changed.

The third and fourth prepared candidates scored **78**, delta **−12**, with ten and one blocking structural findings respectively. Complete diagnostics from the verified pinned bundle identified additional ownership/cohesion penalties. Repairs group archive resources separately from the Metro publication cursor, group ingestion publication dependencies, and isolate Metro token/refresh state. Stateless cache helpers and scalar engine policy queries no longer masquerade as stateful methods. No suppression or threshold adjustment was used.

The fifth prepared candidate passed the normal pinned regression gate: Go **94**, delta **+4**, no structural regressions and zero suppressions. TypeScript remains unchecked by Maat. The subsequent Git signing operation failed in the configured 1Password agent (`failed to fill whole buffer`), so this attempt created no commit. Full PostgreSQL race tests, vet, deterministic generation, the TypeScript production build and 401 local Markdown targets passed. The absolute95 target and remaining advisory findings are still not claimed complete. Further cleanup separates HTTP dispatch, public configuration, transit queries and filter parsing, as well as Metro condition changes from segment compatibility.

Candidate seven passed the normal pinned gate at Go **97**, delta **+7**, no structural regressions and **zero suppressions**. This also exceeds the configured absolute95 score target. The language coverage remains Go only; passing scores do not assert zero heuristic advisory findings. The configured Git signing agent returned an error again, so no commit object, push or deployment was created by this attempt.

The resumed normal signed commit `044af14` succeeded after the local PostgreSQL
cutover. Its prepared tree `3e6724d2b8d9e0a97200e58ad515b1d20eb64bb6`
was accepted by pinned bundle `cb1c6a244e1972174357b029bd304d4a3c6fb2aa`
at97/delta+7, without hook overrides, threshold changes or suppressions.
The code was pushed to main and deployed from the same clean commit.
See [release verification](VALIDATION-metro-patterns.md#committed-main-release-and-production-verification-2026-09-28).

## Metro live popup candidate, 2026-09-28

The normal pinned Go gate accepted candidate five at **97/delta0**, with no
critical regressions and **zero suppressions**, after blocked candidates at91
and95 were repaired by separating acquisition, runtime ownership, transitions,
projection, stream delivery, event publication and offline calibration.
The subsequent configured 1Password signing operation failed; no commit or
release was created by that attempt. TypeScript remains outside Maat coverage
and its build and nine Metro browser fixtures passed separately. The absolute95
score target is met, without claiming zero advisories or complete metric coverage.
See the [dated release preparation](validation/metro-live-main-rollout-2026-09-28.md).
