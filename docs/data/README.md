# Data catalogue

The application combines static network/schedules, vehicle observations, predictions and published fleet specifications. It retains selected facts from observations for history. This catalogue describes application capability and storage, not guaranteed live provider coverage or an exhaustive upstream schema.

## Entities and relationships

| Entity | Meaning and relationships | API representation |
|---|---|---|
| Operator | One of eight transport organizations; uses one or more shared integrations | `Operator` |
| Route | Named service scoped to an operator; links passenger stops and scheduled trips | `Route`, `RouteDetail` |
| Stop | Passenger boarding/alighting location; can have a parent station and route membership | `Stop` |
| Scheduled trip | GTFS service/calendar and ordered visits; does not prove operation | `Trip`, `ScheduledEndpoints` |
| Arrival | Scheduled or predicted passenger-stop time, explicitly differentiated by kind | `Arrival` |
| Vehicle reporting state | Backend-owned latest publication membership/availability, independent of movement | Optional `Vehicle.reporting` |
| Vehicle observation | Reported or estimated position, source identity/time and optional operational/specification enrichment | `Vehicle` |
| CP prediction | Matched stop visit and ETA with source clocks and optional planned service/date context | `CPPrediction`, `CPPredictionAvailability` |
| Route shape | Published geometry variant tied to route, plan, shape and optional direction | `RouteShape`, `GeometryCoverage` |
| Snapshot/history row | Selected observation facts or aggregate, bounded by collection and retention | `HistoryPoint`, `FleetVehicle`, history/metric schemas |
| Fleet specification | Published model/plate/type/propulsion/capacity/accessibility metadata associated with verified vehicle identity | Optional vehicle/fleet fields |

Names above refer to [OpenAPI schemas](../../api/openapi.yaml); the contract is authoritative for exact fields and nullability. [CONTEXT.md](../../CONTEXT.md) defines domain terms. [Associations](associations.md) explain how records connect.

## Operator capability

| Operator | Position kind | Network/scheduled trips | Predictions | Specifications |
|---|---|---|---|---|
| Carris | Reported Hub positions | Selected normalized GTFS; geometry when usable | Requested-stop Hub ETA | Optional published GTFS metadata with exact identity |
| Carris Metropolitana | Reported direct CM observations | Direct line/stop catalogue; Hub GTFS geometry; no collected CM schedule engine | Direct requested-stop arrivals | Direct fields, optional GTFS records and Hub enrichment |
| TCB | Reported Hub positions | Selected normalized GTFS; geometry when usable | Requested-stop Hub ETA | Optional GTFS metadata with exact identity |
| MobiCascais | Reported Hub positions | Selected normalized GTFS; geometry when usable | Requested-stop Hub ETA | Optional GTFS plus verified Hub metadata |
| Metro | Estimated Hub positions | Selected normalized GTFS; geometry when usable | Direct waits with configured credentials | No verified physical-unit crosswalk for inferred entities |
| CP | Reported Hub positions, potentially with gaps | Lisbon-serving GTFS trips/local stops and published endpoint context | Public Hub CP ETA updates | Optional metadata only when exact identities match; absence is unknown |
| TTSL | Reported Hub positions | Selected normalized GTFS; geometry when usable | Requested-stop Hub ETA | Optional GTFS metadata; completeness not established |
| Fertagus | Reported Hub positions | Selected normalized GTFS; geometry when usable | Requested-stop Hub ETA | Published records require an exact observation crosswalk; no guaranteed physical-unit mapping |

The collector can be disabled, feeds can fail or return no current observations, and static validity changes. “Optional metadata” states a parser/enrichment capability, not evidence that a provider publishes a complete populated fleet. Published capacity is not occupancy. See the [operator/source matrix](../integrations/README.md#operator-to-source-coverage).

## Lifecycle and availability

| Dataset | Received/used | Exposed | Durable cache | Retained history/derivation |
|---|---|---|---|---|
| Static GTFS | Active plan archives parsed into network, calendars, trips and optional metadata | Routes/stops/planned services/shapes | Compressed normalized static state | No proof of operated trips; context for associations |
| CM catalogue | Direct lines/stops; Hub geometry and metadata supplement it | CM routes/stops/shapes | Static state | No static CM scheduled trips; observed trip IDs may appear in positions |
| Reporting state | Accepted normalized snapshot membership, original observation age and source health | Vehicle popup signal and immediate marker warning | Latest `vehicle_reporting` row per exact operator/source ID | No transition history or movement sample; survives position/history expiry |
| Positions | Admitted source reports and identity/continuity checks | Vehicles with original clocks; age detail and update coverage | Selected live state, without pending history samples | New admitted samples feed selected facts/aggregates, speed and partial distance |
| Requested-stop predictions | Validated CM/TML arrivals, source-specific clocks and bounded validity | Stop arrival API/popups | None | None; memory-only |
| CP predictions | Current publication/update clocks plus validated stop visits | CP prediction services and typed arrivals | None | None; memory-only |
| Metro direct data | Line state, waits and stations | Status and predicted arrivals | Separate direct cache | No direct prediction history or GPS conversion |
| Fleet specifications | Verified static/live identity enrichment | Fleet and vehicle details | Independent permanent vehicle facts, plus static/live projections | Supported metadata/specification projection in future snapshots; old missing values remain absent |
| Derived metrics | Valid retained reported samples and observation identifiers | Speed, partial distance, detected trips, volume/fleet/rankings/traffic estimates | Not an upstream metric cache | Computed from retained facts; see [history](history.md) |
| Account data | Verified login identity, session/key lifecycle | Own account/key management | SQL identity and hashed session/key secrets | No transport-history role |
| Map/fonts | Browser presentation resources | Rendered cartography and typography | Browser-controlled resource caching | No application transport-history role |

Successful persistence depends on configuration, write admission and database availability. A durable cache is a last-restorable state, not a record of every collection or every raw provider field. Source references list [raw mappings and lifecycle](../integrations/README.md).

Exposure is also field-specific: live `Vehicle` includes capacity/accessibility/contactless specifications and the vehicle detail component can display them. Those values are retained in snapshot metadata when available, but the current historical `FleetVehicle` schema exposes model/plate/typology/propulsion rather than those additional specification fields. Storage does not imply an existing historical UI/API field.

## Time and availability

| Time/status | Interpretation |
|---|---|
| Vehicle `observed_at` | Original Hub `created_at` or CM `timestamp`, both Unix milliseconds converted to UTC |
| Vehicle `collected_at` | Local time that admitted source report was collected; an identical repeated report preserves its original report clocks |
| TML arrival `source_updated_at` | Original trip-update seconds; independent of collection/header time |
| Arrival `valid_until` | Original prediction expiry or bounded CM collection validity; retries do not extend retained rows |
| Arrival `date_basis` | Published date or uniquely matched GTFS date; absent when unproven |
| CP `source_updated_at` | Trip-update Unix seconds; drives prediction age |
| CP publication time | Feed-header Unix seconds; validates feed publication but does not make trip updates newer |
| Metro arrival `observed_at` | Published `hora` parsed in Europe/Lisbon |
| `scheduled_at` | Calendar/service-day GTFS time; times beyond 24:00 are allowed within parser bounds |
| `expected_at` | Validated prediction time, not observed arrival or position |
| Plan validity | Published service-date bounds plus active selection and calendars; archive fetch time is not validity |
| Cache revision | Published read state; historical revision additionally freezes generation/time range |
| Current observation | Source recently verified and position within the current observation-age window |
| Last-known state | Original report projected after omission, stale source or restart; not a new observation |

Source verification uses a 90-second collection freshness window; current vehicle age is at most 180 seconds. All operators use a ten-minute position display deadline from the original source clock. A `not_reporting` state immediately adds the marker warning. At five minutes without a newer position, the age warning also appears, including on disconnected pages. Popups label the original vehicle update and show the separate application receipt time only when its formatted time or age differs; both original clocks remain exposed by the API. Local one-second aging continues through request failures; cached deadlines from the former longer policy cannot extend display past ten minutes. These age indicators do not establish movement, inactivity or metric eligibility. Map totals distinguish updated positions from those awaiting a signal; current speed/history still require valid observation evidence. `inactive_at` is a legacy timestamp, not proof of stopped operation. Stable verified fleet attributes have no automatic expiry and are independent of position/history retention. See [continuity](associations.md#continuity-and-last-known-state).

Normalized nullable output fields use null for unknown/unavailable values rather than zero. Raw input handling is source-specific: the current [Metro wait decoder](../integrations/metro.md#waiting-time-fields) treats an explicit JSON null as zero, a documented parser limitation. Zero delay, zero displacement and published capacity zero remain meaningful when valid. A successful empty source response differs from failure; failure can preserve old data but does not refresh its source timestamp. Availability/error/coverage fields distinguish unavailable collection from known empty coverage.

## Derived and unsupported conclusions

Sampled speed and partial observed distance use eligible consecutive reported observations. Detected trips establish a published trip's presence, not completion. Fleet views show vehicles detected in the selected window, not a complete registered inventory. Traffic colors describe transit observations, not general road congestion.

Exact commercial speed, completed-trip counts, operational headway, depot allocation, permanent vehicle assignment and current occupancy are not supported. Metro estimated positions are excluded from sampled speed/distance. History begins with collection by this installation; no unsupported upstream backfill is inferred. See [metric definitions and limitations](history.md).

## Consumers and evidence

The UI starts in the live view with Metro selected, vehicles/stops and official network overlays enabled. Operator cards reflect coverage for the selected view/window while remaining selectable for other supported data. Empty selection and missing observations are distinct states. CP services/station popups use typed prediction availability; other stop views use bounded CM/TML arrival availability with planned fallback where available. Unsupported completion/frequency/depot views explain their missing operational inputs.

[Frontend queries](../../frontend/src/App.tsx) consume operators, vehicles, routes/stops, shapes, schedules, predictions and historical views. [VehicleSpecifications](../../frontend/src/VehicleSpecifications.tsx) and [CP predictions](../../frontend/src/CPPredictions.tsx) preserve typed unavailable/optional fields. API operations and access scopes remain in [OpenAPI](../../api/openapi.yaml).

Implementation: [entities/cache](../../internal/app/data.go), [source ingestion](../../internal/app/ingest.go), [specifications](../../internal/app/metadata.go), [snapshot storage](../../internal/app/snapshots_store.go), [coverage queries](../../internal/app/coverage.go). Relevant existing evidence: [availability tests](../../internal/app/availability_test.go), [specification tests](../../internal/app/provider_specs_test.go), [CP reads](../../internal/app/cp_reads_test.go), [continuity tests](../../internal/app/continuity_test.go).

Stop popups group visible operators' stops within50metres of the initial stop. The group stays anchored while switching or zooming; buttons and focused left/right arrows select alternatives, Tab remains normal and Escape returns focus. Overlapping map stops/vehicles offer explicit targets. Expired or already-past arrivals leave the list even after a failed refetch. This selection does not prove that stops are operationally interchangeable.

## Published vehicle paths and presentation

Vehicles may expose a published commercial `service_label`, a response-only `vehicle_ref`, and CM's explicitly published qualified `pattern_id`. These fields do not prove a physical allocation or an operating day. Calls distinguish predictions, schedules and published route visits; complete CM paths use `coverage=complete_published_route`, with unknown progress and no inferred ETA. Stop references carry static revision provenance and are revalidated before navigation. An incomplete catalogue or unsafe association leaves the existing next-stop fallback explicit. Path sequences and geometry are shared static-cache data; historical vehicle snapshots do not contain itineraries.

Passenger-facing popups omit missing optional specifications, preserve published zero/false values, group warnings at the bottom, expand known rail/Metro line names and show observation age. Metro can show an explicitly estimated association to the published line/direction stop sequence without a journey ID or train arrival/departure times; the routine unresolved Metro association warning is omitted. Marker displacement is in screen pixels with a connector to the reported position, solely to keep stations and vehicles clickable; it changes neither geographic coordinates nor history. Nearby station navigation remains anchored to the first stop and its original selected operators.

## Stop-visit times and direction boards

A stop visit identifies one occurrence within a complete planned journey; repeated stops remain separate visits. Arrival and departure independently expose actual, prediction and schedule evidence, or an explicit unavailable reason. Each evidence item retains its source URL and available original source/collection clocks; predictions also retain expiry. A collected GTFS schedule has a collection clock and does not invent a publication clock. Past visits select only certified actual occurrences. The generated API is authoritative for optional fields and enum values; see [direction boards](../VEHICLE-POPUPS.md), [matching](associations.md) and [history](history.md).

## Backend-owned reporting state

The optional `Vehicle.reporting` object describes whether the exact operator-scoped source identity is currently reporting through the accepted normalized feed. It is independent of `current_status=STOPPED_AT`, position retention and fleet attributes. Metro states describe inferred Hub entities, not identified physical trains. Exact fields and enums are defined in [OpenAPI](../../api/openapi.yaml).

| Evidence, in evaluation order | State | Reason |
|---|---|---|
| Startup/unverified source or no successful collection | `unknown` | `source_unverified` |
| Source error | `unknown` | `source_error` |
| Successful collection older than 90 seconds | `unknown` | `collection_old` |
| Identity omitted from the accepted successful normalized snapshot | `not_reporting` | `missing_from_snapshot` |
| Present identity with original observation older than 180 seconds | `not_reporting` | `observation_old` |
| Fresh collection membership and original observation | `reporting` | `current` |

An empty successful snapshot causes omission immediately for known visible identities. Filtered/rejected provider rows can also be absent from this normalized snapshot; the state is not proof of raw provider absence, service withdrawal or physical stopping. A later admitted fresh report recovers the state. Repeated membership advances `last_seen_at` using the application's collection clock, while `last_observed_at` remains the original source clock. `state_changed_at` changes only when the state or reason changes.

The backend saves latest state separately from expiring positions. `persisted=false` means the presented state or clocks still await a successful durable transaction, including normal batched clock updates; the popup notes pending storage. There is no reporting transition archive or public inventory endpoint. Reporting fields on retained vehicle responses do not create new historical samples or counts. See [runtime and recovery](../architecture.md) and [continuity](associations.md#continuity-and-last-known-state).
