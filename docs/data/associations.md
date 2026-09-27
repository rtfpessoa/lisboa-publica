# Data associations and provenance

This reference explains how the implemented application connects source records. [Integration references](../integrations/README.md) own raw-field mappings; [the catalogue](README.md) owns entity/time definitions; [history](history.md) owns metric calculations. Rules describe the actual selectors, including first-match behavior where the code does not reject ambiguity.

## Operator namespaces and Hub prefixes

[`qualify`](../../internal/app/data.go) constructs `operator:source_id`. Equal raw IDs from different operators remain different entities. Passenger-facing names are labels, not globally unique matching keys.

`verifiedHubID` removes exactly the expected leading `[agency]` when the remainder does not begin with another bracketed prefix. `verifiedHubTrip` removes `[plan][agency]` only when the plan is the currently selected plan and the agency prefix is verified. An unmatched prefix stays literal; a parsed mismatching plan remains attached as provenance. Enrichment clears a route association when the observation's plan differs from current static data.

Synthetic examples:

| Input/context | Result |
|---|---|
| Raw `3001` for Carris and MobiCascais | `carris:3001` and `mobi:3001` remain distinct |
| `[IA9T6]118_0` with expected agency `IA9T6` | Source route `118_0` |
| `[OTHER]118_0` with expected agency `IA9T6` | Prefix preserved |
| `[P][IA9T6]T` with active plan `P` | Source trip `T`, plan provenance `P` |
| `[OLD][IA9T6]T` with active plan `P` | Trip prefix preserved; plan mismatch prevents current route enrichment |

Evidence: [mapping/enrichment](../../internal/app/ingest.go), `TestVerifiedHubPrefixes` in [app tests](../../internal/app/app_test.go).

## Active plans and GTFS relationships

[`activeHubPlan`](../../internal/app/ingest.go) requires an exact agency, `is_active`, and inclusive `active_from`/`active_until` coverage of today's Lisbon date. Among eligible records it chooses the greatest `active_from`. Equal start-date ties retain the first catalogue row; there is no ID sort or ambiguity rejection. Download time is separate from service validity.

[GTFS parsing](../../internal/app/gtfs_parser.go) joins trip to an existing route, service/calendar to trip, and stop-time to an existing trip. Local stops use coordinates within latitude 38.3–39.3 and longitude −9.7–−8.4. Trips' retained visits are sorted by sequence; route/stop membership comes from those visits, with parent-station membership where available. Active services respect weekday calendars and `calendar_dates` exceptions. Service times may exceed 24:00 and use Lisbon service-day/DST handling.

CP preserves bounded nationwide endpoint names and first/last timing context while keeping the passenger network/visits local. [Endpoint extraction](../../internal/app/gtfs_endpoints.go) identifies origin/destination by minimum/maximum sequence. Identical duplicates can remain usable; different stops at the same endpoint sequence mark that endpoint ambiguous. An unavailable endpoint does not become an invented station or estimated service.

Evidence: [schedule logic](../../internal/app/gtfs.go), [CP timing](../../internal/app/cp_schedule.go), [endpoint tests](../../internal/app/gtfs_endpoints_test.go), [ambiguity tests](../../internal/app/gtfs_endpoint_ambiguity_test.go).

## Observation stop and scheduled-service enrichment

[Stop enrichment](../../internal/app/vehicle_service.go) captures a published source stop against eligible static data when admitting the observation. It uses exact source identity within the operator, with verified prefix normalization. Later plan changes must not retroactively invent a published stop association.

Hub observations require a verified current-plan trip transformation before static stop enrichment. CM uses the current direct static catalogue. Unknown status codes become null; bounded raw source stop IDs can remain present without a matched public stop.

CP attached scheduled endpoints additionally require published `operational_date`, matching observation/static plan, exact qualified trip and route, plan-date validity and an active calendar. Inferred prediction service dates are not observed vehicle operating dates.

Evidence: [service tests](../../internal/app/vehicle_service_test.go), `TestCPObservedServiceRequiresExplicitDatePlanRoute` in [endpoint tests](../../internal/app/gtfs_endpoints_test.go), [persistence tests](../../internal/app/service_persistence_test.go).

## Geometry associations and fallback

[Shape variants](../../internal/app/gtfs_shapes.go) join trip, route, shape, plan/agency and optional direction. Coordinates are emitted as `[longitude, latitude]`, simplified from published vertices with a two-metre tolerance. The application does not create a routing-service path between stops. Train/ferry variants require usable local service visits. The non-CM GTFS parser deduplicates repeated shape sequences with identical coordinates and invalidates shapes with contradictory coordinates at the same sequence.

Non-CM [geometry fallback](../../internal/app/gtfs_geometry.go) reuses prior shapes only for the same plan, a still-existing route and a still-referenced route/shape pair. Availability/error metadata remains explicit.

CM reads four Hub plans and uses GTFS `routes.txt.line_id` to link shapes to the direct line catalogue. A successful fresh update includes only shapes for existing CM lines. Its parser validates individual shape points, then sorts and simplifies them without the non-CM duplicate-sequence conflict rejection. Its [fallback](../../internal/app/cm_shapes.go) can retain the previous CM network with an error marker, including shapes whose route IDs are absent from the current line catalogue; the shape-list API can still expose them. This is not the non-CM same-plan rule. Geometry and metadata still have bounded write admission.

Evidence: [route-shape tests](../../internal/app/route_shapes_test.go), [rail/ferry tests](../../internal/app/rail_ferry_test.go), [geometry-upgrade tests](../../internal/app/geometry_upgrade_test.go).

## Fleet metadata crosswalks and precedence

[Hub metadata](../../internal/app/metadata.go) accepts these explicit agency/code pairs: `HF16N→21`, `LA77N→41`, `BNA17→42`, `YA15B→43`, `A2L1N→44`. The vehicle ID must carry the corresponding numeric prefix. Unknown agencies and conflicting prefixes are ignored.

Synthetic examples:

| Published identity | Metadata key and association |
|---|---|
| `HF16N`, `21-3001` | MobiCascais key `3001`, matched by exact vehicle `source_id` |
| `BNA17`, `42-7` | CM key `[BNA17]7`, preserving agency identity |
| `HF16N`, `42-conflict` | Ignored; prefix contradicts the agency |

Optional GTFS `vehicles.txt` is another metadata input. MobiCascais strips the verified numeric code; CM keeps its plan agency in the key. Observations use exact `source_id` lookup. Direct non-null model/plate/capacity/accessibility/contactless fields take precedence over missing-field fallback. Nonempty incoming static attributes replace previous static attributes. Typology/propulsion remain published values, not a unified classification.

These joins do not establish full inventory, permanent operator allocation, depots or Metro/Fertagus physical-unit identities. Published capacities are specifications, not occupancy.

Evidence: [specification tests](../../internal/app/provider_specs_test.go), [availability tests](../../internal/app/availability_test.go), [fleet research](../research/FLEET-FIELDS.md) (dated external evidence).

## CP prediction matching

[The CP index](../../internal/app/cp_mapping.go) requires `[active_plan][N18KL]` on the trip identifier and an existing GTFS trip. A supplied route must agree with the GTFS route, raw or `[N18KL]`-prefixed. Ordinary rows require absent or `SCHEDULED` relationships.

Stop matching uses a unique sequence when supplied; otherwise it requires a demonstrated Hub-stop-to-GTFS crosswalk and a unique occurrence of that stop in the trip. The crosswalk is rebuilt per update batch from exact sequence evidence. One Hub stop mapping to multiple GTFS stops, or multiple Hub IDs mapping to the same GTFS stop, invalidates affected associations. Duplicate visit sequences also fail uniqueness.

[Service-instance resolution](../../internal/app/cp_instance.go) validates explicit start dates against plan/calendar and optional start time against the first published departure. Without a date, it searches bounded candidate service days and requires a unique coherent instance based on chronology, calendars and available event times/delays. Frequencies or insufficient/contradictory timing can prevent planned association. A trip ID's date-like suffix is not treated as today's operating date.

An independently valid absolute ETA can still be exposed with unknown service date. Delay-only ETA needs resolved planned time; null delay is not zero. When an instance is matched, absolute ETA and signed delay must agree with planned time. Fresh publication and original trip-update timestamps are checked separately. Upcoming predictions are bounded to two hours. A newer candidate supersedes an older one; conflicting candidates at the same timestamp are excluded. Applicable later cancellation/skipped updates suppress predictions.

Synthetic cases: a unique sequence demonstrates `hub-S→S` and can support another update in that batch; contradictory mappings to `S` and `T` are rejected. An absolute event may survive absent date, while the same record containing only delay cannot manufacture planned time.

The [frontend CP helpers](../../frontend/src/cp.ts) apply separate display joins. A prediction replaces a planned station arrival only when both have scheduled times and exactly matching plan, operator, source trip, service date, stop, stop sequence and scheduled instant. The prediction must have a known service date. [Station arrival rendering](../../frontend/src/CPPredictions.tsx) keeps other upcoming planned arrivals alongside current predictions. Vehicle prediction lists require a CP vehicle with published `plan_id`, `trip_id` and `operational_date`, then match prediction plan/source trip/service date exactly; they do not substitute an inferred date for the vehicle's operating date.

Evidence: [normalization](../../internal/app/cp_predictions.go), [prediction tests](../../internal/app/cp_predictions_test.go), [read/plan-race tests](../../internal/app/cp_reads_test.go), [sanitized fixtures](../../internal/app/testdata/cp/README.md).

## Requested-stop arrival matching

The five additional TML operators use exact active plan/agency/trip identity and a unique published visit sequence. A batch builds both directions of the source-stop/GTFS-stop crosswalk, including unrequested visits that reveal conflicts. Contradictory mappings, cancelled trips, duplicate sequence identities and equal-clock conflicting predictions exclude affected associations. Parent-station requests collect validated child visits without replacing their identities. The latest original update wins for a visit; admission does not depend on GPS.

Explicit service dates require plan/calendar validity, compatible absolute events and the correct optional start time. Without a date, a unique active day must satisfy published absolute-time/delay equations. Absolute time remains authoritative when delay contradicts it, but only within a uniquely compatible nonoverlapping daily instance; unknown date leaves schedule/date/vehicle fields absent. Frequency descriptors lacking sufficient instance context are rejected. GTFS timing extrema include national visits, remain compact in the static cache, and respect Lisbon DST and clocks beyond24:00.

A prediction replaces a planned visit only when plan, normalized source trip, service date and visit sequence are proven equal. Unmatched planned rows remain separate. A vehicle link additionally requires exact normalized physical identity, operator, plan, trip, published operating date, one usable fresh observation and current context. The API and UI revalidate the link as live context changes without discarding the ETA. CM direct arrivals do not establish such a link.

Synthetic example: `[P][IA9T6]T` with sequence2 can prove source stop `H` corresponds to GTFS visit `S`. If another current record maps `H` to a different stop, affected ETAs cannot replace scheduled visits. A future absolute ETA with inconsistent delay can still be shown without an inferred service date.

Implementation: [visits](../../internal/app/arrivals_tml_visits.go), [date proof](../../internal/app/arrivals_tml_day.go), [snapshot selection](../../internal/app/arrivals_tml_snapshot.go), [merge](../../internal/app/arrivals_merge.go), [vehicle proof](../../internal/app/arrivals_vehicle.go). Evidence: [HTTP/date/identity tests](../../internal/app/arrivals_test.go).

## Metro station and line matching

[`metroStationID`](../../internal/app/metro.go) first accepts an exact source station code. Otherwise it finds the requested GTFS stop and returns the first published station whose normalized name is a prefix of the GTFS normalized name and whose absolute latitude and longitude differences are each less than 0.005 degrees. Normalization ignores accents/case. This is a coordinate tolerance, not a metric-distance threshold or a uniqueness check. Candidate ordering can affect the fallback.

Line identity comes from a recognized destination terminal code or a station listing exactly one line. GTFS route abbreviations `Az`, `Am`, `Vd`, `Vm` map to line identities; unknown line stays unknown. Predicted arrival time is published `hora` plus decoded wait seconds bounded to 0–7,200. The current integer decoder accepts explicit JSON null as zero, while omitted/malformed waits and numeric strings are rejected. A fresh original source time and nonempty train reference are required; an empty or unknown destination can still produce a prediction with a published-code fallback label. Predictions do not establish GPS or confirm a physical arrival.

Evidence: [Metro implementation](../../internal/app/metro.go), Metro cases in [app tests](../../internal/app/app_test.go).

## Continuity and last-known state

[Continuity admission](../../internal/app/continuity_publication.go) rejects regressing timestamps. Repeated timestamps retain the complete original report, including collection time, without a new history sample. Missing membership, unverified source, restart or bounded-ledger eviction breaks continuity. A later report can be admitted while its speed/distance remains null until an eligible consecutive pair exists.

The display projects current reports only when the source was verified within 90 seconds and observation age is at most 180 seconds. Omitted/stale/unverified reports can be shown as last-known for up to one hour, capped at 500 per operator. The continuity ledger is separately capped at 2,000 identities and preserves a replay floor. Last-known rows use original observation clocks, have null speed and do not prove stationary/inactive/out-of-service status. The five-minute inactivity timestamp is a display threshold.

Restoration marks persisted positions unverified, breaks continuity and advances a replay floor; it does not create new movement history. See [history calculations](history.md#sampled-speed-and-distance) for the additional position/time/plausibility conditions.

## Provenance flow

```mermaid
flowchart LR
    Raw["Source records and original clocks"] --> Validate["Parse and validate"]
    Validate --> IDs["Eligible scoped identities"]
    Static["Eligible plan and metadata"] --> Match["Implemented matching rules"]
    IDs --> Match
    Match --> Output["Normalized fields and provenance"]
    Match --> Unknown["Unmatched, rejected or unknown association"]
    Output --> Display["API and UI projection"]
    Output --> Facts["Selected retained facts"]
```

This is a conceptual flow, not one universal pipeline. CP predictions and direct Metro waits do not enter retained vehicle history. Source URL, source/plan identities and original clocks remain explicit where the API representation supports them. Unknown associations stay unavailable rather than being guessed.
