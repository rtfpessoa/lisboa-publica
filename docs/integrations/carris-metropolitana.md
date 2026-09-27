# Carris Metropolitana v2

The direct CM integration provides the line/stop catalogue, reported vehicle observations and requested-stop arrivals. Its base is `https://api.carrismetropolitana.pt/v2`. [TML Hub](tml-hub.md) separately supplies official geometry and optional fleet enrichment. The direct line catalogue remains authoritative for the app's CM routes.

## Access and collection

| Method | Response used | Collection |
|---|---|---|
| `GET /lines` | Bare JSON array | Static refresh when cache is not reusable |
| `GET /stops` | Bare JSON array | Same static refresh, after lines |
| `GET /vehicles` | Bare JSON array | Nominal five-second position loop |
| `GET /arrivals/by_stop/{id}` | Bare JSON array | Separate nominal five-second collector, only while the known stop is requested |

The app supplies no source credentials. General ingestion requires `INGEST_ENABLED=true`. Static checks run every five minutes; successfully cached static data can be reused for six hours. The shared client enforces local 40/rolling-second CM and 900/rolling-minute global budgets. These are local limits; dated quota research is in [polling evidence](../research/POLLING-LIMITS.md). ETags/cooldowns and bounded JSON fetching follow [shared policy](README.md#shared-collection-policy).

## Line and stop fields

The [static parser](../../internal/app/ingest.go) reads the following. Nonidentity strings are carried as published; this table does not assert stricter upstream requirements than the parser.

| Endpoint/field | Input and validation | Application target |
|---|---|---|
| Lines `id` | Nonempty string | Route `id=cm:<id>`, `source_id`, `operator_id=cm` |
| Lines `long_name`, `short_name`, `color` | Strings | Route names/color |
| Lines `stop_ids` | String array | Qualified `stop_ids` |
| Stops `id`, `long_name` | Strings | Stop scoped ID/source ID/name |
| Stops `lat`, `lon` | Finite local coordinates | Stop latitude/longitude; outside-bounds rows skipped |
| Stops `line_ids` | String array | Qualified stop `route_ids` |

Empty line or stop collections fail static refresh. Routes are line IDs; the app does not substitute `route_id` from vehicle payloads for this catalogue. Static data is sorted by public ID before publication.

## Vehicle fields

| Raw field | Input/validation | Normalized target |
|---|---|---|
| `id` | Nonempty string | `id=cm:<id>`, `source_id` |
| `line_id` | String | Optional qualified `route_id`, published `route_name` |
| `trip_id` | String | Optional qualified `trip_id` |
| `pattern_id` | Explicit `[plan][agency]pattern` with ASCII letters, digits, allowed punctuation and at most 253 source bytes | Optional `cm:`-qualified `pattern_id`; original namespace preserved, no derivation from trip ID or date |
| `lat`, `lon` | Finite local degrees | `lat`, `lon`; outside-bounds rows skipped |
| `timestamp` | Positive Unix milliseconds, at most 30 seconds ahead | UTC `observed_at` |
| `bearing` | Optional number, carried through | Optional `bearing` |
| `model`, `license_plate` | Optional strings | Direct attributes take precedence over missing-field metadata fallback |
| `capacity_seated`, `capacity_total` | Optional integers in 0–10,000 | `seated_capacity`, `total_capacity` specification |
| `wheelchair_accessible`, `contactless` | Optional booleans | Optional specification attributes |
| `current_status` | Optional string admitted by application enum | Published stop-progress status |
| `stop_id` | Optional nonempty string, at most 128 bytes | `source_stop_id`, optional exact static stop/name association |

The app adds `operator_id`, `collected_at`, `source_url` and `position_kind=reported`. Invalid required identity/time or duplicate IDs fail the snapshot; null/missing vehicle array fails, while an empty array succeeds. Missing specification values stay unknown; false and zero are preserved when valid. Capacity is published vehicle specification, not occupancy or free places.

`speed`, `route_id` and `capacity_standing` are ignored if present: the current vehicle decoder has no mapping for them. Application `speed_kmh` and partial distance come from [eligible consecutive reported positions](../data/history.md#sampled-speed-and-distance). Units of an ignored upstream speed field are not inferred.

## Hub geometry and specification enrichment

[CM geometry collection](../../internal/app/cm_shapes.go) discovers four active Hub plans (`LA77N`, `BNA17`, `YA15B`, `A2L1N`) and downloads their normalized GTFS archives. It reads route/trip/shape records and optional `vehicles.txt`, plus required GTFS stop identities and stop-time visit sequences for complete-path validation. Direct lines/stops remain the catalogue; GTFS stop-time clocks and calendars do not populate a CM scheduled-trip engine.

GTFS `routes.txt.line_id` links shapes to the direct route ID. A successful fresh update includes only shapes for current catalogue lines. [Shared GTFS fields](tml-hub.md#gtfs-field-inventory) and [geometry rules](../data/associations.md#geometry-associations-and-fallback) cover validated vertices, variants and fallback. A failed geometry update can retain the previous CM geometry with an error marker while the catalogue remains available, including shapes exposed by the shape-list API whose route IDs are no longer in that catalogue.

Optional GTFS fleet keys preserve `[agency]vehicle_id`. Hub metadata adds only verified agency/numeric-prefix matches. [Fleet associations](../data/associations.md#fleet-metadata-crosswalks-and-precedence) explain source-ID lookup and direct-field precedence; unavailable identities are not invented.

## Requested-stop arrivals

[Collection](../../internal/app/arrivals_collect.go) escapes the raw direct stop ID and streams a response of at most512KiB through the [decoder](../../internal/app/arrivals_cm_decode.go). A null/malformed response fails; `[]` is a successful empty publication. At most32 CM stops have30-second interest, with two requests in flight, five seconds between attempts per stop and48 admitted attempts per rolling minute, within the shared transport policy. Nearby alternatives are not prefetched.

| Source field | Validation and mapping |
|---|---|
| `line_id`, `trip_id`, `headsign` | Bounded strings; line is required; scoped route/trip and destination label |
| `scheduled_arrival_unix` | Unix seconds in2000–2100, required; `scheduled_at` and visit identity |
| `estimated_arrival_unix` | Optional Unix seconds; valid absolute ETA becomes `expected_at` and kind `prediction`; an invalid estimate can leave a valid scheduled row |
| `observed_arrival_unix` | Nonzero means already observed and excludes the passage; malformed/future observation marks partial coverage |

An ETA can remain upcoming when its scheduled time is past. Admission is bounded to the next24hours and256rows; duplicate visit identities mark partial coverage. Published coverage is limited to the next Lisbon midnight. No source update clock or vehicle identity is invented. The30-second local validity is a collection bound, not proof of source freshness. Failure can preserve unexpired future scheduled times from the previous response, with their original expiry and no prediction/vehicle fields. There is no CM GTFS schedule fallback.

The [arrival API](../../internal/app/arrivals_reads.go) distinguishes loading, empty publication, error, stale and partial coverage. These rows are ephemeral and do not enter vehicle history. [HTTP and identity tests](../../internal/app/arrivals_test.go) cover expiry, failure, milliseconds and cancellation.

## Usage and lifecycle

| Data | Consumers | Durable cache | History/derivation |
|---|---|---|---|
| Requested-stop arrivals | Stop popup and arrival API | None | None |
| Lines/stops and verified shapes | Route/stop search, map/network overlays | Static normalized state | Static context only |
| Observation IDs, coordinates and clocks | Vehicle API/map and detected fleet | Live state | Selected new facts/aggregates, sampled speed and partial distance |
| Model/plate/type/propulsion | Vehicle popups and fleet details | Static/live enrichment | Supported metadata in subsequent snapshots |
| Capacity/accessibility/contactless | Live vehicle specifications; not fields on current historical FleetVehicle | Static/live enrichment | Retained snapshot metadata when available |
| Bearing/status/stop reference | Live vehicle details | Live state | Not in current historical projection |
| Repeated/last-known observations | Labelled continuity display | Bounded state | No new movement/history sample solely from display retention |

Live memory can advance when durable writes fail. Static publication waits for successful persistence. Source errors and successful empty responses preserve different availability semantics. Original source clocks are not refreshed by failures or identical reports.

## Examples and evidence

Synthetic example: `id=7`, `line_id=1001` produces `cm:7` on route `cm:1001`. A valid direct `capacity_total=0` remains zero; null can receive verified metadata fallback. A Hub metadata row with inconsistent agency prefix is ignored. A successful `[]` vehicles response means no current membership, not an upstream error.

Implementation: [CM decoding](../../internal/app/ingest.go), [geometry/enrichment](../../internal/app/cm_shapes.go), [metadata](../../internal/app/metadata.go), [stop/service capture](../../internal/app/vehicle_service.go). Existing tests: [specification precedence](../../internal/app/provider_specs_test.go), [CM line/shape joins](../../internal/app/route_shapes_test.go), [stationary/current-status behavior](../../internal/app/vehicle_service_test.go), [continuity](../../internal/app/continuity_test.go), [request budgets](../../internal/app/upstream_policy_test.go). Dated external field evidence: [fleet research](../research/FLEET-FIELDS.md), [source research](../research/SOURCES.md).

## Selective complete published paths

`/v2/vehicles.pattern_id` is preserved when it has a bounded explicit `[plan][agency]pattern` identity, qualified as `cm:`. Cached official GTFS archives already fetched for geometry supply `trips.txt.pattern_id`, shape/direction, `routes.txt.line_id`, and ordered `stop_times.txt` visits. The parser validates every trip before sharing one sequence per pattern. Geometry and paths remain in the same immutable static revision. The source's direct `/patterns/:id` endpoint is not consumed: the September 27, 2026 limited check returned a different plan for the unqualified ID and 404 for the explicitly qualified ID. No extra network loop or per-popup upstream request was added.

The opened CM vehicle popup can expose complete published stops and opt into the exact variant's existing geometry. Missing pattern, mismatched namespace/line, inconsistent archive or incomplete native stop catalogue falls back explicitly to the published next stop. Neither progress, ETA nor an operating date is inferred. [Implementation and validation evidence](../research/cm-selective-paths/VALIDATION.md) is dated local evidence; combined deployment verification is recorded separately.

### Synthetic exact-pattern and fallback example

This example is synthetic, not a reported vehicle or current plan. A vehicle declares native line `1001` and pattern `[P][LA77N]1001_0_1`. The cached archive for exactly plan `P` / agency `LA77N` has that pattern, a route with `line_id=1001`, one matching published shape/direction, and every trip has visits `S1, S2, S1` with sequences `1, 2, 3`. If the native catalogue contains both stops, calls can return all three visits as `complete_published_route`, retaining the repeated visit, with unknown progress and no times inferred. The opted-in map geometry is the exact shape referenced by that verified pattern.

If the vehicle instead declares `[Q][LA77N]1001_0_1`, the line differs, any trip has inconsistent visits, or native stop `S2` is absent, that complete association is rejected. Calls retain only a separately published next-stop reference when available, otherwise explicit unavailability. They never substitute plan `P`, another line variant, an operating date or a guessed GPS position.

## Timed journey schedules and station directions

The four archives already collected for CM geometry also supply complete stop visits, calendars and directions. Trips/services use their explicit `[plan][agency]` namespace; `routes.txt.line_id` maps archive routes to the native line. Published pattern paths remain independent of dated journey identification. The matching Hub position can supply an explicit operational date only when qualified vehicle, qualified trip and original observation instant coincide exactly.

The native requested-stop arrival cache still serves forecast evidence through the direction board's bounded frozen lease. It does not provide a guessed operating date or departure time. Native scheduled/estimated times and original available clocks retain their source provenance. Actual arrivals/starts are not fabricated from the native observed field, ETA or GPS; no actual-event adapter is enabled. See [popup behavior](../VEHICLE-POPUPS.md).

CM operational-date enrichment requires exactly one shared Hub row with the same fully qualified vehicle ID, trip ID and original millisecond source instant. Both native `timestamp` and Hub `created_at` are decoded with `UnixMilli`; a missing, ambiguous or mismatched join cannot suppress the position and cannot borrow a service date/plan. [Join tests](../../internal/app/popup_reads_test.go) include decoder-realistic clocks and duplicate/identity mismatch cases. Stable attributes follow the [permanent-fact policy](../data/associations.md#fleet-metadata-crosswalks-and-precedence); only explicitly supplied catalogue fields are staged, so merging an older catalogue does not reconfirm omitted fields.
