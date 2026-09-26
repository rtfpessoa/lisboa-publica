# Verified CP, TTSL and Fertagus service paths

Main-agent research, 2026-09-26. No application edits or production changes.

One public [TML plans request](https://go.tmlmobilidade.pt/hub/api/v1/plans) identified the active normalized plans. Downloaded each of the three advertised official archives once, using HTTPS verification, the existing permitted storage host, a 64 MiB download bound and no redirects. The official [plan schema](https://github.com/tmlmobilidade/docs/blob/769674c0b1cbfbdd7f9b2413047e26634f2caec5/docs/reference/hub/v1/plans.mdx) describes active validity and the normalized archive URL. Discover URLs from that catalogue at runtime; do not hardcode temporary object-storage grants.

| Provider | Agency / active plan | Valid dates | Compressed / expanded bytes | Retained local routes / routes with shapes | Geometry variants |
| --- | --- | --- | ---: | ---: | ---: |
| CP | `N18KL` / `76XA2` | 2026-06-16–2026-12-31 | 4,500,254 / 16,184,128 | 44 / 44 | 77 |
| TTSL | `LTP61` / `YYS70` | 2026-07-11–2027-07-01 | 19,785 / 153,597 | 5 / 5 | 17 |
| Fertagus | `7NTB1` / `2XUL7` | 2026-07-02–2026-12-31 | 90,513 / 518,246 | 2 / 2 | 4 |

Local means trips retain at least one stop within the application's existing Lisbon rectangle. Fertagus's archive has three route rows, but only two routes are referenced by retained trips; this is not a missing-shape error. Each retained route has at least one usable published variant. Current coverage is complete at that route level; it does not establish that the shapes cover every physical railway track, every possible service variant or actual operation. Counts and SHA-256 archive checksums are in [archive evidence](rail-ferry-paths/archives.json).

The CP archive is national: 123 routes, 454 stops and 209 shapes with 305,436 raw points. The existing local stop/trip filter retains 44 routes, 94 stops and 1,244 trips. Referenced official shape coordinates span latitude 37.020462–42.024734 and longitude -9.417836–-7.232900; some retained services continue far beyond Lisbon. Retaining complete, locally referenced service variants preserves their actual geometry. Do not discard out-of-region vertices and connect the remaining points, or import every national route. Current measurements found no shape variants lacking a retained local trip; a regression fixture must enforce that constraint for the new modes as feeds evolve.

## Existing-pipeline measurements

A quarantined main-agent Go overlay enabled the existing geometry branch for these fixtures by setting only the fixture provider's mode to `bus`. It uses the real parser, variant generation, two-metre simplification, gzip cache and admission check. It demonstrates what removing the mode gate permits; it is not an implementation or a production memory benchmark. All three fixture subtests passed after fixing an integer-type mismatch in the diagnostic harness.

| Provider | Variants / simplified points summed across variants | Existing static gzip bytes | Candidate static gzip bytes | Guarded bytes (4× candidate) | Measured total allocation during parse |
| --- | ---: | ---: | ---: | ---: | ---: |
| CP | 77 / 89,419 | 579,656 | 1,070,505 | 4,282,020 | 59,559,976 |
| TTSL | 17 / 224 | 12,862 | 14,071 | 56,284 | 1,428,480 |
| Fertagus | 4 / 1,137 | 27,890 | 31,697 | 126,788 | 1,800,264 |

All candidates pass the existing geometry admission check; CP parsed in roughly 329 ms including candidate encoding in this local run. Allocation totals are not live heap or RSS, and cannot establish headroom on the deployment host. The eventual implementation must measure the real new-mode pipeline alongside existing feeds, retained-position churn and browser geometry. Evidence: [measurement output](rail-ferry-paths/measurements.txt), [quarantined measurement probe](rail-ferry-paths/measurement-probe.go.txt).

The normalized feeds identify TML as publisher and the respective provider in `agency.txt`; retain operator, plan and catalogue attribution in the existing geometry API. The inspected feed-info rows do not state a separate data redistribution licence. No additional licence grant is inferred from public access or a source-code licence. This plan adds the existing application’s service-path display using its established public feeds; it introduces no archive redistribution endpoint.

No fallback is needed for currently retained routes. Direct CP GTFS, the historically TLS-failing direct TTSL URL and alternative Fertagus data are therefore outside this implementation. Preserve existing TLS, provider budgets and static refresh lifetime. Geometry availability is independent of current vehicle reports.

## Related provider semantics

The official [CM v2 API documentation](https://github.com/carrismetropolitana/api/blob/v2/README.md#vehicles) describes an array for all vehicles and the timestamp of their last known position. This supports treating it as a snapshot, but does not prove that every listed vehicle is operating now or that omission means a completed trip. A generic bounded historical display can preserve an omitted report truthfully without making either inference. Source errors and explicit removals must be treated separately.

## Research limits

Four successful provider-data requests: catalogue plus three archives. Three additional successful public source-document requests: CM README, TML docs tree and pinned plan documentation. Web-tool attempts to reopen the GTFS reference and prior TML open-data path returned 403/404, and search supplied no new data-licence terms; these failures do not change the verified archive availability. No new live vehicle sampling, credentials, SSH, database writes or production configuration changes occurred. Current archive shape coverage is verified; fail-soft ingestion, API changes and UI behavior remain implementation work.

## Actual implementation validation (2026-09-26)

The earlier mode-substitution probe above measured candidates before validating conflicting shape sequences. The actual train/ferry parser now rejects an entire variant when the same sequence has different coordinates; identical duplicates are safely normalized. CP contains 54 raw shapes with conflicting sequences (44,310 conflicting sequence entries; 90,964 duplicate raw rows). Its accepted result is **36/44 locally retained routes, 57 variants, 36,348 simplified points**, explicitly `partial`. This supersedes the preliminary complete-coverage claim for usable CP geometry; all 44 static routes and their useful schedules remain available. No paths are fabricated for the rejected variants.

Actual TTSL and Fertagus results remain complete at retained-route level: **5/5 routes, 17 variants, 224 points** and **2/2 routes, 4 variants, 1,137 points**, respectively. Full current-provider measurements additionally use official Carris, TCB, MobiCascais, Metro and four CM area archives discovered from a fresh bounded catalogue response, plus the official CM line/stop catalogues. Downloaded archives stay outside the repository. Geometry data, archive integrity regressions, memory and browser results are recorded in [implementation validation](../VALIDATION-provider-continuity-overlays.md). These local tests do not guarantee future provider completeness or production RSS.
