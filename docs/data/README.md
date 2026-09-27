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
| Positions | Admitted source reports and identity/continuity checks | Current or labelled last-known vehicles | Selected live state, without pending history samples | New admitted samples feed selected facts/aggregates, speed and partial distance |
| Requested-stop predictions | Validated CM/TML arrivals, source-specific clocks and bounded validity | Stop arrival API/popups | None | None; memory-only |
| CP predictions | Current publication/update clocks plus validated stop visits | CP prediction services and typed arrivals | None | None; memory-only |
| Metro direct data | Line state, waits and stations | Status and predicted arrivals | Separate direct cache | No direct prediction history or GPS conversion |
| Fleet specifications | Verified static/live identity enrichment | Fleet and vehicle details | Static/live metadata | Supported metadata/specification projection in future snapshots; old missing values remain absent |
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

Source verification uses a 90-second collection freshness window; current vehicle age is at most 180 seconds. Last-known reports can be retained for one hour, with bounded counts and truncation flags. The five-minute `inactive_at` display threshold does not prove a vehicle stopped operating. See [continuity](associations.md#continuity-and-last-known-state).

Normalized nullable output fields use null for unknown/unavailable values rather than zero. Raw input handling is source-specific: the current [Metro wait decoder](../integrations/metro.md#waiting-time-fields) treats an explicit JSON null as zero, a documented parser limitation. Zero delay, zero displacement and published capacity zero remain meaningful when valid. A successful empty source response differs from failure; failure can preserve old data but does not refresh its source timestamp. Availability/error/coverage fields distinguish unavailable collection from known empty coverage.

## Derived and unsupported conclusions

Sampled speed and partial observed distance use eligible consecutive reported observations. Detected trips establish a published trip's presence, not completion. Fleet views show vehicles detected in the selected window, not a complete registered inventory. Traffic colors describe transit observations, not general road congestion.

Exact commercial speed, completed-trip counts, operational headway, depot allocation, permanent vehicle assignment and current occupancy are not supported. Metro estimated positions are excluded from sampled speed/distance. History begins with collection by this installation; no unsupported upstream backfill is inferred. See [metric definitions and limitations](history.md).

## Consumers and evidence

The UI starts in the live view with Metro selected, vehicles/stops and official network overlays enabled. Operator cards reflect coverage for the selected view/window while remaining selectable for other supported data. Empty selection and missing observations are distinct states. CP services/station popups use typed prediction availability; other stop views use bounded CM/TML arrival availability with planned fallback where available. Unsupported completion/frequency/depot views explain their missing operational inputs.

[Frontend queries](../../frontend/src/App.tsx) consume operators, vehicles, routes/stops, shapes, schedules, predictions and historical views. [VehicleSpecifications](../../frontend/src/VehicleSpecifications.tsx) and [CP predictions](../../frontend/src/CPPredictions.tsx) preserve typed unavailable/optional fields. API operations and access scopes remain in [OpenAPI](../../api/openapi.yaml).

Implementation: [entities/cache](../../internal/app/data.go), [source ingestion](../../internal/app/ingest.go), [specifications](../../internal/app/metadata.go), [snapshot storage](../../internal/app/snapshots_store.go), [coverage queries](../../internal/app/coverage.go). Relevant existing evidence: [availability tests](../../internal/app/availability_test.go), [specification tests](../../internal/app/provider_specs_test.go), [CP reads](../../internal/app/cp_reads_test.go), [continuity tests](../../internal/app/continuity_test.go).

Stop popups group visible operators' stops within50metres of the initial stop. The group stays anchored while switching or zooming; buttons and focused left/right arrows select alternatives, Tab remains normal and Escape returns focus. Overlapping map stops/vehicles offer explicit targets. Expired or already-past arrivals leave the list even after a failed refetch. This selection does not prove that stops are operationally interchangeable.
