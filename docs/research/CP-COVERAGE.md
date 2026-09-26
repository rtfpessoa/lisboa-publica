# CP coverage and zero-position periods

Research completed on 2026-09-26 using public read-only requests, without credentials or Mover APIs.
The observations below record the actual public responses and inspected source, rather than inferred train operations.
Repository reference: commit `1d137873476e501bb532a7e67b649c26e9989e58` plus the current working files linked below.

## Finding

The reproduced zero-position period originates in the upstream TML feed before this application's normalization.
At **2026-09-26 11:18:01 UTC**, the [official positions endpoint](https://go.tmlmobilidade.pt/hub/api/v1/vehicles/positions)
returned HTTP 200 and 798 entities, with **zero** entities whose `agency_id` was `N18KL`.
A second observation at **11:18:47 UTC** returned 781 entities and again **zero CP entities** from that same endpoint.
These successful responses establish absence of published CP observations at those instants;
they do not establish that no CP trains were operating.

At **11:18:14 UTC**, the [deployed operators endpoint](https://lisboapublica.rtfpessoa.xyz/api/v1/operators)
reported CP `status=ok`, `static_status=ok`, `plan_id=76XA2`, `reported_positions=0`, `observed_at=null`,
and `live_updated_at=2026-09-26T11:17:56.592784837Z`.
At **11:18:37 UTC**, the [deployed CP vehicles endpoint](https://lisboapublica.rtfpessoa.xyz/api/v1/vehicles?operators=cp&limit=100)
returned HTTP 200, an empty `data` array, and `page.total=0`.
These app observations agree with the contemporaneous raw feed's absence of CP records.
They provide no evidence of a CP agency-ID normalization failure in this period.

The earlier research recorded CP positions appearing after an initial absence;
the existing [source note](SOURCES.md) records nine positions in a later probe.
That earlier note does not retain an exact timestamp or raw payload, so it cannot identify the cause of a particular screenshot's zero count.
The current diagnosis is limited to the timestamped reproduction above.

## Current official static coverage

At **11:18:01 UTC**, the [official plan catalogue](https://go.tmlmobilidade.pt/hub/api/v1/plans)
reported agency `N18KL`, name `Comboios de Portugal`, numeric agency code `3`, and active plan `76XA2`.
Its declared plan dates are `20260616` through `20261231`, inclusive, and `is_active=true`.
The older plan `FBM8J` is inactive and ends `20260615`.
The normalized ZIP dynamically linked by the active plan downloaded with ordinary TLS validation and HTTP 200.
Do not pin its rotating object-storage access URL; discover it from the catalogue.

The inspected normalized ZIP contains 123 routes, 454 stops, and 2,226 trips.
Its files are `agency`, `attributions`, `calendar`, `calendar_dates`, `feed_info`, `routes`, `shapes`,
`stop_times`, `stops`, and `trips`, all as GTFS text tables.
It includes national service names `AP`, `IC`, `IR`, `R`, `U`, and suburban names including `Sintra`,
`Cascais`, `Azambuja`, `Sado`, `Aveiro`, `Braga`, `Guimarães`, `Leixões`, and `Marco`.
The ZIP's `feed_info.feed_version` is `76XA2`; its publisher is Transportes Metropolitanos de Lisboa.
These are direct observations of the ZIP selected by the [official plan catalogue](https://go.tmlmobilidade.pt/hub/api/v1/plans).

Exactly 94 of its stop coordinates satisfy the repository's geographic bounds,
latitude 38.3–39.3 and longitude -9.7–-8.4.
The application applies those bounds to static stops and live positions;
routes/trips survive when they retain at least one local stop time.
Thus its CP coverage is services passing through the configured Lisbon region, rather than all national CP services
or only the four named Lisbon suburban lines.
Sources: [bounds and position predicate](../../internal/app/data.go),
[bound constants](../../internal/app/limits.go), [GTFS filtering and route assembly](../../internal/app/gtfs_parser.go).

The separately verified [direct CP public GTFS](https://publico.cp.pt/gtfs/gtfs.zip) is an alternative static source.
Earlier first-party ZIP inspection documented its national stops/calendar and lack of `shapes.txt`;
the currently selected TML normalized ZIP does contain shapes, so switching source blindly would reduce map geometry coverage.
Sources: [earlier inspected source record](SOURCES.md), [official current plan catalogue](https://go.tmlmobilidade.pt/hub/api/v1/plans).

## Provenance and access requirements

The [official TML CP fetcher](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/apps/pt-tml-cp-api-fetch/src/index.ts)
assigns agency `N18KL`, reads `entity.vehicle.timestamp` as Unix seconds, and stores normalized milliseconds.
It discards source entities without vehicle, trip, or position, and polls its partner integration every second.
The [official CP parser](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/packages/parsers/src/pt/tml/cp/v1.ts)
copies published latitude/longitude, source trip ID, and vehicle ID; it sets speed and stop ID to null.
Unlike the Metro adapter, that code does not synthesize positions along a timetable shape.
Therefore CP markers can be labelled positions reported by the provider, while speed remains unavailable
until this application obtains valid pairs of observations for its explicitly sampled speed calculation.
Sources: the official fetcher/parser above and [local sampled-speed calculation](../../internal/app/data.go).

The [official TML CP client](https://github.com/tmlmobilidade/go/blob/prd/packages/external/src/clients/cp/index.ts)
documents partner-relative paths `/schedule/gtfs.zip`, `/realtime/TripUpdates.pb`,
and `/realtime/VehiclePositions.pb`.
It requires an OAuth bearer plus `x-cp-connect-id` and `x-cp-connect-secret`, with its base URL configured privately.
This evidence establishes that a credentialed CP partner integration exists;
it does not establish a publicly accessible partner base URL, developer entitlement, uptime guarantee, or unrestricted reuse policy.
No such contract was independently established in this pass.
The public TML hub is the verified source currently usable without new access requirements.

## Normalization and freshness audit

| Boundary | Inspected behavior | Implication |
|---|---|---|
| Agency | CP provider is exactly `N18KL`; raw rows must match that agency | Matches official fetcher/catalogue; no alternate-ID fix is supported |
| Vehicle/route prefix | Verified `[N18KL]` prefix is removed; other prefixes are preserved | Does not drop a vehicle merely because its route ID cannot be resolved |
| Trip prefix | `[plan][N18KL]trip` is unwrapped only when its plan matches active static plan | Protects cross-plan joins; unmatched trip text remains visible |
| Plan mismatch | Enrichment clears `route_id` when a vehicle's plan differs from static plan | Can remove a route link, but cannot explain operator-wide zero vehicles |
| Geography | Invalid/out-of-region coordinates are skipped | National CP rows may intentionally lie outside displayed coverage |
| Invalid observations | Missing ID/timestamp or timestamp beyond clock-skew bound makes refresh error | Would set provider error, unlike the observed successful empty snapshot |
| Observation freshness | Older than 180 seconds is stale and excluded from reported active count | Stale rows may still appear in vehicles with an explicit stale flag |
| Empty successful response | Empty per-agency slice replaces current live slice; status remains `ok`, count becomes 0, observation time null | Current `ok` describes successful collection, not confirmed complete operational coverage |
| Collection freshness | Operators endpoint marks collection stale after 90 seconds | Independent of the 180-second age of individual observations |

Sources: [provider mapping/prefix logic](../../internal/app/data.go),
[hub conversion, enrichment, and publication](../../internal/app/ingest.go),
[operator/vehicle response freshness](../../internal/app/server.go), [limits](../../internal/app/limits.go).
The frontend currently renders `status=ok` as a numeric count in the provider selector,
and labels it “Disponível” in the sources panel.
That creates ambiguity when successful collection yields no CP observations.
Source: [dashboard provider selector and source panel](../../frontend/src/App.tsx).

No CP raw rows were available in this reproduction, so their current vehicle/route/trip prefixes,
per-row age distribution, and exact static-trip matching could not be checked empirically.
The code audit establishes how those boundaries behave, not proof that every future upstream prefix will match.
The matching agency/active plan and empty raw feed are sufficient to rule out those filters as the cause of this reproduced absence.
Sources: timestamped public endpoint observations above and the linked normalization implementation.

## Minimal source-to-fix map

| Evidence | Minimal main-agent action | Scope boundary |
|---|---|---|
| Raw feed has no CP rows while successful app refresh has count 0 | Show “Sem observações atuais” / “Cobertura parcial” for a successful empty CP snapshot; say count is observed positions | Do not say zero trains are operating or that the CP service is stopped |
| Static plan is active and accessible | Preserve stops, routes, published geometry, scheduled trips/arrivals during live gaps | Never turn timetable trips into measured live vehicles |
| Plan mismatch only clears a route association | Keep existing agency/prefix/plan checks; change only if a captured nonempty raw payload demonstrates mismatch | No guessed prefix rewrite or global relaxation of validation |
| CP partner client requires multiple credentials | Retain public TML source; document partner access as a possible future option requiring an actual contract | No guessed API, bypass, scraping dependency, or new OAuth integration in this fix |
| Source absence may be intermittent | Retain historical observations as historical and expose collection versus last observation age | Do not present vanished vehicles as fresh or backfill missing operation |

The likely gap is **coverage signalling**, not ingestion repair, for the reproduced interval.
The specific reason TML published no CP observations remains unknown:
an upstream CP/TML outage, filtering, or genuinely absent eligible source entities cannot be distinguished from these public responses.
No SLA or operator-wide completeness guarantee was found in the inspected primary source.
Main-agent acceptance should compare a future nonempty raw CP snapshot with normalized API results,
including geographic exclusion and age thresholds, while separately checking the empty-feed label.
These are research-derived validation recommendations, not tests performed by this research agent.

## Request budget and limits

This pass used ten bounded read requests: two raw positions snapshots, one plan catalogue,
one catalogue-selected normalized ZIP, three application API reads, and three official GitHub source reads.
One application vehicles request used singular `operator=cp`, which the API did not treat as the documented filter;
that response was excluded from CP coverage evidence and replaced with the correct `operators=cp` request.
No feed was polled in a loop, and no private CP credentials, Metro secrets, or Mover API were accessed.
