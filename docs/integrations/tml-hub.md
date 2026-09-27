# TML Hub

The public Hub is the shared integration for seven operators' positions and static plans, additional CM geometry/fleet records, selected fleet metadata and CP predictions. The base is `https://go.tmlmobilidade.pt/hub/api/v1`. General collection runs when `INGEST_ENABLED=true`; provider availability and actual observations are not guaranteed by that setting.

## Consumed contracts

| Method/resource | Input contract used by the app | Consumers |
|---|---|---|
| `GET /vehicles/positions` | JSON envelope with `data` array and nullable `error` | Seven operators' vehicle positions |
| `GET /plans` | JSON envelope with plan records and nullable `error` | Active static plan selection, GTFS discovery |
| `GET /vehicles/metadata` | Bare JSON array | MobiCascais and CM metadata enrichment |
| `GET /realtime/eta/gtfs` | JSON wrapper containing GTFS-RT-style `data.header` and `data.entity`; nullable `error` | CP predictions |
| Discovered normalized GTFS ZIP | Plan `operation_gtfs_normalized_url` | Static network/schedule and optional fleet/shape parsing |

Public source calls do not use a source token. The shared local TML cap is 120 attempts/minute; see [collection policy](README.md#shared-collection-policy). Position and CP loops are nominally five seconds. Static plans/metadata are checked on the five-minute loop, with reusable static cache up to six hours. Source timestamps retain their original meaning.

## Position fields

The [position decoder and normalizer](../../internal/app/ingest.go) process these fields:

| Raw field | App input/validation | Normalized field or use |
|---|---|---|
| `agency_id` | String matching the configured operator agency | Select operator; other agencies skipped |
| `vehicle_id` | Nonempty string; verified agency-prefix transformation | Scoped `id`, `source_id` |
| `route_id` | String; verified agency prefix when present | Optional qualified `route_id` |
| `route_short_name` | String | `route_name` |
| `trip_id` | String; verified current-plan/agency transformation | Optional qualified `trip_id`, parsed `plan_id` |
| `latitude`, `longitude` | Finite degrees within the application's Lisbon bounds | `lat`, `lon`; out-of-bounds rows skipped |
| `created_at` | Positive Unix milliseconds, at most 30 seconds ahead of collection | UTC `observed_at` |
| `bearing` | Optional numeric value, carried through without unit conversion | Optional `bearing` |
| `license_plate` | Optional string | Optional `license_plate`, with missing-field metadata fallback |
| `current_status` | Optional string admitted only if it matches the application enum | Optional published stop-progress status |
| `stop_id` | Optional nonempty string, at most 128 bytes | `source_stop_id`; verified static match can add `stop_id`/`stop_name` |
| `operational_date` | Integer parsed as valid `YYYYMMDD` | Optional ISO service date; CP scheduled-service prerequisite |

The app supplies `operator_id`, `collected_at`, `source_url` and `position_kind`; Metro Hub positions are `estimated`, others `reported`. Missing/invalid identity or future timestamp can fail the operator's conversion; duplicate vehicle IDs fail its snapshot. A missing `data` array or non-null envelope `error` is failure. A valid empty array is successful empty coverage.

`received_at`, upstream `speed`, `pattern_id` and `shape_id` have no field in this decoder and are ignored if present. The app's `speed_kmh` is [derived from eligible observations](../data/history.md#sampled-speed-and-distance), never an implicit conversion of upstream speed.

## Plans and archive discovery

| Plan field | Expected input/use | Application result |
|---|---|---|
| `_id` | Plan identity string | Static `plan_id`, prefix/association provenance |
| `agency_id` | Exact agency string | Operator/CM-plan selection |
| `is_active` | Boolean | Eligibility |
| `active_from`, `active_until` | Numeric `YYYYMMDD` bounds | Inclusive Lisbon-date selection and static validity |
| `operation_gtfs_normalized_url` | HTTPS URL on authorized host | Bounded archive download |

[Selection](../data/associations.md#active-plans-and-gtfs-relationships) chooses the latest eligible start date and keeps the first equal-date catalogue record. It does not choose by `updated_at`, which this plan decoder does not map.

[Archive admission](../../internal/app/cm_shapes.go) permits only `objectstorage.eu-frankfurt-1.oraclecloud.com` over HTTPS without userinfo, using default/443 port. URLs rotate and must be discovered, not hard-coded. [ZIP parsing](../../internal/app/gtfs_archive.go) bounds compressed data to 64 MiB, expansion to 512 MiB, entries to 256 and rows per file to five million. Parsing streams CSV. Integrity/resource failures reject the archive; geometry-specific handling can leave schedules available where supported.

## GTFS field inventory

The following are fields read by [the GTFS parser](../../internal/app/gtfs_parser.go), [geometry parser](../../internal/app/gtfs_shapes.go), [metadata parser](../../internal/app/metadata.go) and [CP context extraction](../../internal/app/cp_schedule.go). This is the app's inventory, not a complete GTFS schema. Strings are CSV values; empty optional attributes remain unknown.

| Table | Fields used | Normalization/decisions |
|---|---|---|
| `routes.txt` | `route_id`, `route_short_name`, `route_long_name`, `route_color` | Route identity/names; missing short name falls back to long name; six-character color or operator fallback |
| `stops.txt` | `stop_id`, `stop_name`, `stop_lat`, `stop_lon`, `parent_station` | Parsed coordinates/local stop admission, scoped identity/name/parent; CP additionally remembers bounded endpoint names |
| `calendar.txt` | `service_id`, `start_date`, `end_date`, `sunday`, `monday`, `tuesday`, `wednesday`, `thursday`, `friday`, `saturday` | Service calendar; weekday active only when value is `1` |
| `calendar_dates.txt` | `service_id`, `date`, `exception_type` | Exceptions require numeric 1/add or 2/remove |
| `trips.txt` | `trip_id`, `route_id`, `service_id`, `trip_headsign`, `shape_id`, `trip_short_name`, `direction_id` | Existing-route relationship, service/headsign/label/shape; optional direction must be 0 or 1 |
| `stop_times.txt` | `trip_id`, `stop_id`, `stop_sequence`, `arrival_time`, `departure_time` | Existing trip, nonnegative sequence, local visits, bounded service-day clocks; CP endpoint/timing extrema also use nonlocal rows |
| `shapes.txt` | `shape_id`, `shape_pt_lat`, `shape_pt_lon`, `shape_pt_sequence` | Finite world-bounded coordinates and nonnegative sequence; published variant geometry, `[lon,lat]` output |
| `vehicles.txt` | `vehicle_id`, `make`, `model`, `license_plate`, `typology`, `propulsion`, `total_capacity`, `wheelchair_accessible` | Optional exact-identity specifications; capacity is bounded integer; wheelchair codes 1/2 become true/false, others unknown |
| `frequencies.txt` (CP) | File presence only | Marks frequency-based schedules to restrict CP planned-instance joins; rows are not a frequency scheduling engine |

CM reads `routes.txt.line_id` in addition to route fields and uses only route/trip/shape/optional-vehicle tables for enrichment. It does not load CM GTFS calendars/stop-times into a scheduling engine. [The CM reference](carris-metropolitana.md) owns its direct catalogue contract. MobiCascais GTFS vehicle IDs strip the configured `21-` prefix; CM metadata keys retain plan agency.

GTFS clocks accept hours through the implementation bound of 72 and can represent times after 24:00. Actual dates respect Lisbon calendars/DST. Download/parse timestamps describe cache freshness, not new observations or proof of operated trips. [Geometry and association rules](../data/associations.md) explain optional shapes, invalid sequences and constrained fallback.

## Hub fleet metadata fields

| Raw field | Input and treatment | Result |
|---|---|---|
| `agency_id`, `vehicle_id` | Strings requiring a known agency/numeric-prefix pair | Exact Mobi/CM metadata key |
| `make`, `model` | Strings trimmed/combined; duplicate model text normalized | Optional `model` |
| `license_plate` | Published string | Optional plate enrichment |
| `propulsion` | Trimmed string bounded to 128 bytes | Published propulsion value |
| `available_seats` | Optional integer admitted in 0–10,000 | Published `seated_capacity` specification, not live availability |
| `wheelchair`, `contactless` | Optional booleans | Accessibility/contactless specification fallback |

Only MobiCascais and CM currently merge this endpoint. Exact crosswalks and attribute precedence are in [fleet associations](../data/associations.md#fleet-metadata-crosswalks-and-precedence). Optional absence stays null; zero capacity is preserved. GTFS and Hub capacity/accessibility encodings are distinct.

## CP prediction contract

The [CP collector](../../internal/app/cp_collect.go) waits for static CP data, accepts HTTP 200 with the wrapper above or HTTP 204 as successful no predictions, and bounds input to 16 MiB. [The streaming decoder](../../internal/app/cp_decode.go) consumes entities individually; it does not retain the complete multi-agency feed in published state. Other-agency entities are not normalized as CP.

| Raw path inside wrapper | Input/validation | Result or decision |
|---|---|---|
| `error` | Absent/null accepted; non-null rejected | Source error |
| `data` | Exactly one object | Feed container |
| `data.header.gtfs_realtime_version` | Must be `2.0` | Supported contract |
| `data.header.incrementality` | Must be `FULL_DATASET` | No differential-state merging |
| `data.header.timestamp` | Positive Unix seconds; fresh publication | Availability `published_at`, not trip age |
| `data.entity[].is_deleted` | True rejected | Unsupported deletion envelope |
| `data.entity[].trip_update.trip.trip_id` | Bounded string; active `[plan][N18KL]` prefix and known trip | Scoped source trip, plan and service identity |
| `...trip.route_id` | Optional string; if supplied, must agree with GTFS route | Route association |
| `...trip.start_date` | Optional `YYYYMMDD`; active plan/calendar when present | Published service date or rejection |
| `...trip.start_time` | Optional GTFS clock; must agree with first departure for resolved service | Descriptor validation |
| `...trip.schedule_relationship` | Absent/`SCHEDULED` for ordinary rows; applicable cancellation suppresses older rows | Admission/suppression |
| `...trip_update.timestamp` | Unix seconds, original update freshness | `source_updated_at`, validity |
| `...stop_time_update[].stop_id` | Bounded source ID | Batch-scoped stop crosswalk/visit matching |
| `...stop_time_update[].stop_sequence` | Optional nonnegative integer, bounded to 100,000 | Unique visit sequence |
| `...stop_time_update[].schedule_relationship` | Scheduled for ordinary prediction; applicable skipped/cancelled semantics suppress | Visit admission/suppression |
| `...stop_time_update[].arrival.time` | Optional positive Unix seconds with bounds | Absolute `expected_at` |
| `...stop_time_update[].arrival.delay` | Optional signed seconds; validation depends on inference/ETA context | Nullable `delay_seconds`; planned time plus delay when resolvable |

Trip/update fields in abbreviated rows belong beneath `data.entity[].trip_update`; stop fields belong beneath its `stop_time_update` array. Other entity attributes/departure events have no application mapping. String/depth/entity/row/output limits reject amplification, unsupported headers and malformed envelopes. Normalized output is bounded to 1,024 rows, 256 services and 256 KiB; exact input/string bounds are in [cp_feed.go](../../internal/app/cp_feed.go) and [JSON bounds](../../internal/app/cp_json_bounds.go).

[Matching rules](../data/associations.md#cp-prediction-matching) distinguish absolute ETA from delay-only inference, signed/absent deviation, current plan, service date, unique visits and conflicting updates. Names and planned endpoints come from verified static context, not an inferred physical vehicle. ETA is upcoming within two hours; original updates/publication use a 90-second freshness window with 30-second future-clock allowance.

Failed fetches can retain previous same-plan prediction rows with error availability and unchanged source clocks. Successful empty responses clear current predictions. Static CP replacement invalidates the prediction snapshot. No CP prediction cache/history survives restart.

## Usage and storage

| Data | API/UI use | Durable cache | Retained history/derivation |
|---|---|---|---|
| Position identity, coordinates, original time | Vehicles/map, detected fleet | Live normalized state | Selected new observation facts and eligible speed/distance |
| Bearing, stop status/reference, scheduled-service context | Live vehicle details | Live state | Not in current historical fact projection |
| GTFS routes/stops/trips/calendars/shapes | Search, schedules, route/network overlays | Static normalized state | Not proof of operation; matching context |
| Fleet metadata | Vehicle popups/specifications and fleet views | Static/live metadata | Supported metadata in subsequent snapshots |
| CP normalized predictions | CP services/station views and typed arrivals | None | None |
| Plan selection/error fields | Source health and matching decisions | Static/health state | Not historical observations |

Fleet exposure is field-specific: live vehicles can expose the additional capacity/accessibility/contactless specifications; historical FleetVehicle exposes model/plate/typology/propulsion. Retaining specification metadata in a snapshot does not imply a corresponding historical API field. See [catalogue lifecycle](../data/README.md#lifecycle-and-availability).

## Examples and evidence

Synthetic example: a Hub report with `created_at=T` maps that millisecond source time to `observed_at`; collecting it again does not advance `observed_at` or create a new sample. `[P][N18KL]trip` associates only with the active plan `P`. A stop update with delay but no provable planned instance remains unavailable; an independently valid absolute ETA can have unknown service date.

Implementation: [ingestion](../../internal/app/ingest.go), [GTFS parser](../../internal/app/gtfs_parser.go), [metadata](../../internal/app/metadata.go), [CP mapping](../../internal/app/cp_mapping.go), [CP normalization](../../internal/app/cp_predictions.go). Existing tests: [prefix/Metro app tests](../../internal/app/app_test.go), [route shapes](../../internal/app/route_shapes_test.go), [specifications](../../internal/app/provider_specs_test.go), [CP predictions](../../internal/app/cp_predictions_test.go), [CP reads](../../internal/app/cp_reads_test.go), [sanitized fixture provenance](../../internal/app/testdata/cp/README.md).

[Source research](../research/SOURCES.md), [CP prediction research](../research/CP-PUBLIC-PREDICTIONS.md) and [fleet research](../research/FLEET-FIELDS.md) hold dated external evidence. Current raw provider completeness/availability is not asserted by this code inventory.
