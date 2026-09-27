# Carris Metropolitana v2

The direct CM integration provides the line/stop catalogue and reported vehicle observations. Its base is `https://api.carrismetropolitana.pt/v2`. [TML Hub](tml-hub.md) separately supplies official geometry and optional fleet enrichment. The direct line catalogue remains authoritative for the app's CM routes.

## Access and collection

| Method | Response used | Collection |
|---|---|---|
| `GET /lines` | Bare JSON array | Static refresh when cache is not reusable |
| `GET /stops` | Bare JSON array | Same static refresh, after lines |
| `GET /vehicles` | Bare JSON array | Nominal five-second position loop |

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
| `lat`, `lon` | Finite local degrees | `lat`, `lon`; outside-bounds rows skipped |
| `timestamp` | Positive Unix milliseconds, at most 30 seconds ahead | UTC `observed_at` |
| `bearing` | Optional number, carried through | Optional `bearing` |
| `model`, `license_plate` | Optional strings | Direct attributes take precedence over missing-field metadata fallback |
| `capacity_seated`, `capacity_total` | Optional integers in 0–10,000 | `seated_capacity`, `total_capacity` specification |
| `wheelchair_accessible`, `contactless` | Optional booleans | Optional specification attributes |
| `current_status` | Optional string admitted by application enum | Published stop-progress status |
| `stop_id` | Optional nonempty string, at most 128 bytes | `source_stop_id`, optional exact static stop/name association |

The app adds `operator_id`, `collected_at`, `source_url` and `position_kind=reported`. Invalid required identity/time or duplicate IDs fail the snapshot; null/missing vehicle array fails, while an empty array succeeds. Missing specification values stay unknown; false and zero are preserved when valid. Capacity is published vehicle specification, not occupancy or free places.

`speed`, `route_id`, `pattern_id` and `capacity_standing` are ignored if present: the current vehicle decoder has no mapping for them. Application `speed_kmh` and partial distance come from [eligible consecutive reported positions](../data/history.md#sampled-speed-and-distance). Units of an ignored upstream speed field are not inferred.

## Hub geometry and specification enrichment

[CM geometry collection](../../internal/app/cm_shapes.go) discovers four active Hub plans (`LA77N`, `BNA17`, `YA15B`, `A2L1N`) and downloads their normalized GTFS archives. It reads route/trip/shape records and optional `vehicles.txt`; direct lines/stops remain the catalogue, and it does not populate a CM scheduled-trip engine.

GTFS `routes.txt.line_id` links shapes to the direct route ID. A successful fresh update includes only shapes for current catalogue lines. [Shared GTFS fields](tml-hub.md#gtfs-field-inventory) and [geometry rules](../data/associations.md#geometry-associations-and-fallback) cover validated vertices, variants and fallback. A failed geometry update can retain the previous CM geometry with an error marker while the catalogue remains available, including shapes exposed by the shape-list API whose route IDs are no longer in that catalogue.

Optional GTFS fleet keys preserve `[agency]vehicle_id`. Hub metadata adds only verified agency/numeric-prefix matches. [Fleet associations](../data/associations.md#fleet-metadata-crosswalks-and-precedence) explain source-ID lookup and direct-field precedence; unavailable identities are not invented.

## Usage and lifecycle

| Data | Consumers | Durable cache | History/derivation |
|---|---|---|---|
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
