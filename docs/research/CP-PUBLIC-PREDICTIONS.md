# Public CP predictions: identities, clocks and names

Read-only investigation, 26 September 2026. Main performed all research. No Mover endpoints, private CP endpoints, credentials or production mutations were used. Counts describe one changing upstream sample, not guaranteed coverage. [Sanitized sample summary](cp-public-predictions/source-summary.json).

## Sources and findings

| Source | Verified response / meaning | Use |
| --- | --- | --- |
| [TML simplified ETA](https://go.tmlmobilidade.pt/hub/api/v1/realtime/eta) | HTTP 200, 1,635,923 bytes; absolute ETA and stop sequence, but no original update timestamp or direct deviation | Investigation only; do not add a second realtime collector merely to recover dropped fields |
| [TML GTFS-RT JSON](https://go.tmlmobilidade.pt/hub/api/v1/realtime/eta/gtfs) | HTTP 200, 1,157,137 bytes; FULL_DATASET, original trip-update timestamps and signed stop-event delays | Preferred public CP source, subject to strict normalization |
| [TML active plans](https://go.tmlmobilidade.pt/hub/api/v1/plans) | HTTP 200; CP agency N18KL, active plan 76XA2, validity 20260616–20261231 | Exact qualification against the application's active static plan |
| Active CP normalized GTFS from the plan's published archive | Existing verified archive inspected locally; exact trips and original stop sequences | Human names, calendars, local station mapping and planned times |
| [Public network stops](https://go.tmlmobilidade.pt/hub/api/v1/network/stops) | HTTP 200, 10,444,364 bytes, but no CP agency flags in this sample; Oriente detail `/network/stops/253542` returned 404 | Not an admitted CP crosswalk; do not poll this large list in production |

The [official Hub introduction](https://go.tmlmobilidade.pt/reference/hub) describes public data access. Source availability is verified above; no published numeric TML quota was established. Keep the application's conservative TML ceiling, not an invented provider entitlement.

Official source inspected at repository tree commit `f90e9f91f3daa3fff60ba2582f3827f4ea4f1300`:

- [External feed registration](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-eta/src/tasks/external-feeds.ts) registers CP's partner TripUpdates through TML's own client. Our integration consumes only TML's public output.
- [External normalization](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-eta/src/tasks/gtfs/get-external-trip-updates.ts) qualifies trip IDs with plan and agency, preserves trip-update fields, and remaps original stop IDs to Hub infrastructure IDs. Stop sequences remain unchanged.
- [Publisher](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-eta/src/tasks/gtfs/publish-trip-updates.ts) combines internal and external updates. Its header timestamp is publication time, not each CP update's age.
- The internal [ETA SQL](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/sql/publish-eta/select-eta-gtfs.sql) produces absolute event times/sequences, calculates deviation from its ride schedule and timestamps the trip from the latest underlying position time. The public message also includes CP external updates. These two producers explain the mixed shapes, but the payload does not expose a per-entity producer field: attribute displayed predictions to the **TML public CP feed**, not uniformly to direct CP telemetry or a confirmed physical arrival.
- [Public JSON handler](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/api/src/endpoints/v1/realtime/handlers/get-trip-updates-gtfs-rt-json.ts) serves the cached GTFS-RT message with five-second cache age. An empty cache returns HTTP 204.
- [Simplified conversion](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-eta/src/tasks/simplified/trip-updates-to-etas.ts) drops source timestamps and deviations. Its [schedule helper](https://github.com/tmlmobilidade/go/blob/f90e9f91f3daa3fff60ba2582f3827f4ea4f1300/modules/hub/apps/publish-eta/src/tasks/simplified/trip-schedule-index.ts) uses a trip-ID date suffix as service date. Do not reproduce that assumption: sampled CP suffixes are historical dates, not today's operating date. Source code and deployed simplified payload also differ in negative ETA clamping; payload validation remains necessary.

## Sample validation and implications

The richer feed had 29 CP entities but only 19 distinct trip IDs; ten IDs occurred twice. Fifteen entities carried absolute event times and stop sequences, while fourteen carried delay-only updates, generally without sequences. None of the 29 supplied `start_date`. Never implement “last JSON row wins”.

Of 250 stop updates, 126 matched a unique exact GTFS `(trip_id, stop_sequence)`; 124 lacked a usable sequence. The exact matches established 47 Hub-to-GTFS stop relationships with zero conflicts. Applying only those verified relationships to delay-only rows, and requiring one visit to the corresponding stop in that exact trip, joined 104 additional rows; 20 remained unavailable. This is a same-batch, plan-scoped inference backed by the publisher's preserved sequences, not a name/proximity match or a permanent hand-written ID table. Conflicting relationships must fail closed.

Example: Hub stop `253542`, sequence 13 in CP trip `16044_20251214`, maps to GTFS `94_31039`, **Lisboa Oriente**. Journey headsign was `Lisboa Santa Apolonia`. All 126 absolute arrival times minus their supplied signed delays exactly matched the sampled date's GTFS scheduled arrival. That is a validation result, not permission to take every trip's date from the current clock.

Original update ages ranged from two seconds to 1,240 seconds relative to the feed header. No absolute-time CP arrival in this sample simultaneously had an update age at most 90 seconds and a future arrival within two hours. A collector admitting only those rows can honestly return no current arrivals even while the feed is HTTP 200. Fresh delay-only updates need conservative service-instance resolution; collecting again must not reset their original timestamps.

A local exploratory check of fresh, exactly mapped delay-only events against calendar weekdays/exceptions and a bounded two-hour upcoming window found 92 per-event unique candidates, including three at Oriente. This shows a useful integration path; it is not full admission proof. Whole-entity consistency, frequency/overlap rules and geographic filtering still need the implementation tests, rather than accepting every per-event candidate independently.

The [GTFS-RT reference](https://gtfs.org/documentation/realtime/reference/#message-tripdescriptor) permits omitted service dates for uniquely identifiable scheduled instances, but requires dates for frequency trips or overlapping instances. Admit a delay-derived ETA only when the exact active trip/calendar, mapped unique stop visit and source-clock window resolve exactly one instance. Reject ambiguous cases and expose partial coverage. Do not infer a current observed vehicle's operating date or fabricate GPS from this process.

## Passenger names

All operators need the same presentation rule: names for stops, destinations and routes, original IDs for joins. Preserve Unicode and meaningful abbreviations; never blindly title-case Portuguese names or turn raw ID segments into line labels. Prefer exact-associated named records and documented official display aliases. For example CP itself uses [Lisboa Santa Apolónia](https://www.cp.pt/info/pt/gabinetes-de-apoio-ao-cliente/), correcting the missing accent in that sampled headsign. A small documented alias is presentation only; it never joins records. Unknown names stay explicitly unavailable.

## Checks and limits

Verified TLS with the system CA bundle; bounded single public GETs, official GitHub source reads and local CSV joins. No load tests, credential requests, live exploit probes or service modifications. The bulk sources are multi-operator; scope normalized operational predictions to CP. No proof of complete CP coverage, real-time platforms, physical train inventory or every station's current arrivals. Original CP clock semantics are preserved as a provider update time, not renamed GPS time. Sample coverage and uniqueness need regression fixtures and repeated admission checks during implementation.
