# Why provider observations disappear

Investigated 2026-09-26. Planning research only; no application or production configuration changes.

## Confirmed CP disappearance

The reported mechanism was reproduced against the public source and application: CP stayed available as an operator while its vehicle collection went **16 → 12 → 0**. Both feeds agreed on CP counts in every paired sample. The CP observations had stopped advancing: the newest captured CP report stayed at **15:22:50 UTC**. At **15:24:35 UTC**, the sampled TML publication no longer contained CP; the application correctly exposed zero CP vehicles and reports. CP remained absent through the final sample at **15:27:32 UTC**. The TML publication continued updating other operators.

| Sample start (UTC) | TML CP rows | Application CP rows | Newest CP report |
| --- | ---: | ---: | --- |
| 15:23:05 | 16 | 16 | 15:22:50 |
| 15:23:50 | 12 | 12 | 15:22:50 |
| 15:24:35 | 0 | 0 | None |
| 15:25:20 | 0 | 0 | None |
| 15:26:02 | 0 | 0 | None |
| 15:26:47 | 0 | 0 | None |
| 15:27:32 | 0 | 0 | None |

The current public TML source explains the disappearing-publication mechanism. Its publisher selects events whose provider observation time is newer than **90 seconds**, then inner-joins matching rides within the scheduled-time bounds. Vehicles without eligible recent observations or matching rides do not enter the published list. Its GTFS-RT counterpart explicitly uses `FULL_DATASET`; the JSON endpoint serves the same publisher's complete cached position list. Sources are pinned to public repository revision `f90e9f91f3daa3fff60ba2582f3827f4ea4f1300`: [selection query](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/sql/publish-vehicles/select-vehicle-positions.sql), [publisher](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-vehicles/src/tasks/publish-vehicle-positions.ts), [JSON handler](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/api/src/endpoints/v1/vehicles/handlers/get-vehicle-positions-json.ts).

All 16 CP withdrawal witnesses had last-captured reports aged **98.459–135.459 seconds** when absent from the next sample. That agrees with the documented cutoff and publication delay. It confirms upstream absence and strongly supports aging out as the proximate cause. Sampling does not expose TML's private intermediate events/ride joins or prove the deployed publisher's exact source revision. The CP partner's reason for not supplying newer usable observations remains unknown; do not label it a confirmed CP outage or assert a fixed update interval.

The [CP adapter](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/tracker/apps/pt-tml-cp-api-fetch/src/index.ts) polls every second but preserves the provider's observation timestamp. Faster requests cannot make an unchanged report fresh. This application's five-second polling is therefore not the cause demonstrated here.

## Application behavior and reproduction

The actual path is `refreshLive → hubVehicles → saveLive → Cache.update → ListVehicles → allPages → App.liveData → Map.setData`.

- [refreshLive](../../internal/app/ingest.go:238) distinguishes transport/JSON failures, missing data and explicit source errors from a valid empty result.
- [saveLive](../../internal/app/ingest.go:275) replaces a provider collection with the new successful result. An empty agency subset clears its rows and sets current position counts to zero; `status=ok` means collection succeeded, not that vehicles were reported. CP's static plan stayed `76XA2` and static status stayed `ok` during the observed gap.
- [markError](../../internal/app/ingest.go:145) publishes an error without replacing live rows. Thus failures preserve the prior collection, while an empty successful snapshot does not. The distinction matters for any proposed continuity policy.
- [ListVehicles](../../internal/app/server.go:453) returns stale rows with a stale flag rather than deleting them after 180 seconds. [Frontend fresh counts](../../frontend/src/App.tsx:58) separately exclude old observations or old collection times. [Map rendering](../../frontend/src/Map.tsx:14) receives all returned rows and dims stale points. Raising the local observation-age threshold alone cannot recover rows omitted upstream.
- Region checks can intentionally exclude national CP positions outside the Lisbon bounds. None of the captured CP rows were outside those bounds. Their captured plan prefixes matched `76XA2`; a plan mismatch could remove a route link and therefore affect a route-filtered view, but did not explain this operator-wide gap.
- Every two-page vehicle result in the sampling used one immutable revision. The operator request and vehicle request are independent reads, so brief cross-response count differences are possible; they must not be mistaken for a pagination defect.

A quarantined [Go diagnostic probe](provider-continuity/continuity_probe_test.go.txt) exercises real normalization/publication/cache/API paths without a database or persistent code changes. It demonstrates nonempty → HTTP failure retaining rows → successful empty result clearing rows → recovery. Three characterization tests, including subtests for all eight providers, passed with the race detector. Separate checks cover stale rows versus current counts, plan/route filtering, region rejection, clock skew and pinned revisions. With `DIAGNOSTIC_REQUIRE_CONTINUITY=1`, the same probe fails explicitly on the selected-CP marker/count loss; this is an intentional reproduction signal, not a fix test that now passes.

The local [browser probe](provider-continuity/browser-probe.mjs) confirms CP remains `aria-pressed=true` throughout. MapLibre's vehicle source receives **1 → 1 → 0 → 1** features for nonempty, HTTP failure, empty success and recovery. The current metric follows the same sequence; the operator label changes to “Sem observações atuais” after its refresh. Final run had no page errors. Recovery is proven locally, not observed in the bounded production window. Evidence: [browser proof](provider-continuity/browser-proof.json), [Go output](provider-continuity/go-characterization.txt).

## Coverage across all eight providers

Counts are ranges across seven paired samples, not completeness guarantees. Withdrawal witnesses count transitions between adjacent snapshots; the same vehicle can contribute more than one witness. Their age is that of the last captured report, not proof that no intermediate report existed.

| Provider | Raw positions | API positions | Observed loss / implication |
| --- | ---: | ---: | --- |
| CP | 0–16 | 0–16 | Complete loss reproduced upstream and API; 16 withdrawal witnesses, all older than 90 seconds. |
| Carris | 237–253 | 237–253 | 49 individual withdrawal witnesses; last captured ages 94.455–139.456s. Same TML cutoff is applicable; no whole-provider loss observed. |
| TCB | 12–15 | 12–15 | Five individual withdrawals, ages 93.579–113.596s; no whole-provider loss observed. |
| MobiCascais | 37–44 | 37–44 | Eight individual withdrawals, ages 94.596–139.456s; no whole-provider loss observed. |
| TTSL | 2–4 | 2–4 | Two individual withdrawals, ages 116.579–130.455s, with new reports also appearing. Small fleets make such gaps conspicuous. |
| Fertagus | 7–8 | 7–8 | One individual withdrawal, age 123.031s; no whole-provider loss observed. |
| Metro | 22–24 | 22–24 | No individual withdrawal observed in this window. Uses the same TML publication, so is subject to its filters. Positions remain estimates, excluded from reported movement metrics. |
| Carris Metropolitana | 398–407 | 396–406 | Separate official v2 array endpoint, not the TML publication. Out-of-region rows explain part of the difference; non-simultaneous collection can explain small remaining differences. No provider-wide loss observed. Its source omission/removal contract was not independently verified here. |

The common local publication path accepts successful empty results for every provider; local probes confirm the same replacement/recovery behavior for all eight. That is application behavior, not proof that each provider currently suffers a total feed gap. Private Metro status/prediction APIs were not queried; Metro position provenance and exclusions remain unchanged.

## Source-to-fix boundary

The evidence supports addressing the experience of **missing observations**, rather than replacing CP agency IDs, changing its static plan, increasing polling or claiming trains stopped running.

The next decision is whether to retain a finite, explicitly last-known display when a complete snapshot omits a vehicle. Such display must keep the original report timestamp and distinguish omitted vehicles from current counts immediately, even if their timestamps are still recent. It must expire, respect any source removals, recover correctly and never generate new speed, distance, trips or historical evidence from retained rows. No retention duration or new API field is chosen in this research ticket.

Official static route paths should remain independent of live report availability. Their addition and actual coverage belong to the subsequent geometry research/integration tickets; no geometry archive was downloaded in this investigation.

## Checks, bounds and limits

Provider sampling ran **15:22:28–15:27:34 UTC**, approximately 305.6 seconds. It used **18 upstream attempts and 26 application attempts**, including four TLS-failed Python attempts in each category and one successful curl trust diagnostic against the application. The 14 successful upstream requests and 21 paired application requests produced seven paired samples; every vehicle collection had two pages. No automatic retry, privileged read, secret access, production mutation, polling/configuration change or load test occurred. All requests remained within the ticket's ten-minute / twenty-upstream / eighty-application ceilings. [Request ledger](provider-continuity/requests.json), [compact evidence and withdrawal witnesses](provider-continuity/evidence.json).

Five additional public GitHub requests fetched a tree and four pinned source files. Two web-tool GitHub opens failed; successful curl retrieval supplied the current source evidence. TLS verification stayed enabled. Initial Python trust failures were local and are not provider failures. The temporary browser fixture initially omitted required geometry coverage metadata; correcting the fixture removed that unrelated test-harness error.

Passed: three race-enabled diagnostic tests, eight provider subtests, five existing focused tests (sampled-speed provenance, upstream budget, verified prefixes, frozen publication and provider clock skew), and the browser characterization. No production recovery was observed, provider uptime/failure rates cannot be estimated from this window, and the partner-side reason for CP's unchanged reports is not established. No database integration suite or full application suite was needed for this read-only research; no executable application change is claimed validated.

Independent final `quasar-alpha` review at `xhigh` recommended research acceptance without blockers; the main agent accepts the investigation and closes its research ticket. The retained log is plain text, and the portable diagnostic commands were rerun successfully. Display policy and implementation remain undecided.
