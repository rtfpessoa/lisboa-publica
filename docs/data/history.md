# History and derived metrics

History contains selected observations collected by this installation. It does not reconstruct missing movement, prove that scheduled trips occurred or import a provider's historical archive. CP predictions are memory-only; direct Metro predictions have a durable last-state cache but are not retained vehicle history.

## Collection modes

[History configuration](../../cmd/server/config.go) accepts `HISTORY_INTERVAL_SECONDS=0` or `300`. `0` is the code default; [production configuration](../../deploy/README.md) explicitly selects `300`.

| Mode | Records and persistence |
|---|---|
| Raw (`0`) | New admitted observation samples become individual history records; live persistence is attempted each update |
| Aggregate (`300`) | Intermediate samples stage into in-memory five-minute buckets; durable live/history writes are normally attempted at most every 30 seconds |

[Buckets](../../internal/app/snapshots.go) are keyed by qualified vehicle ID, route ID, trip ID, position kind and truncated observation interval. Route/trip/kind changes split buckets. Each bucket retains the last observation and first observation time, valid speed sum/count and valid distance sum/count. Repeated observation times do not add new samples. Pending identity/bucket capacity is bounded to 20,000. Late observations outside the admission window do not reopen a finalized bucket.

A bucket closes after its interval plus 90 seconds of grace. It becomes durable on a subsequent successful save, normally adding up to the 30-second write cadence. Finalization is not an exact wall-clock promise under failure or paused writes. Collector proposals are acknowledged after the transaction commits. There is no final shutdown flush; unfinished buckets are lost on restart.

## Retained facts

[Snapshot storage](../../internal/app/snapshots_store.go) writes these typed facts to `snapshots`:

| Facts | Interpretation |
|---|---|
| `operator_id`, `vehicle_id`, `route_id`, `trip_id`, `position_kind` | Scoped observation identities, not proof of completion |
| `observed_at`, `first_observed_at`, `generation` | Last/first underlying observation and ingestion generation for stable reads |
| `lat`, `lon` | Representative retained position; an aggregate is not a complete trajectory |
| `speed_kmh`, `speed_sample_count` | Mean of valid sampled speeds in a bucket and count used to weight it |
| `distance_km` | Sum of eligible observed displacement contributions; null when none exist |
| Metadata payload | Source ID, model, plate, typology, propulsion and supported capacity/accessibility/contactless fields |

Bearing, published stop status and attached scheduled-service details are available in live state but are not copied into this current historical projection. Specification projections appear in subsequent collected records; absent older values remain unknown. [Fleet/history queries](../../internal/app/history_queries.go) and [read queries](../../internal/app/read_queries.go) define the available historical views.

## Sampled speed and distance

[`sampledDistance`](../../internal/app/data.go) uses two eligible consecutive reported positions:

```text
distance_km = Haversine(previous_position, next_position, earth_radius_km=6371)
elapsed_seconds = next.observed_at - previous.observed_at
speed_kmh = distance_km / elapsed_seconds * 3600
```

Both positions must be reported, elapsed time must be 5–180 seconds, and speed must be at most 130 km/h. Admission also requires valid local coordinates, genuine newer observations, verified continuity and replay bounds. Zero displacement can yield speed zero. Invalid/unavailable pairs produce null, not zero.

Omission, source failure, restart and ledger eviction can break continuity. The first recovery report does not automatically create a valid movement pair. Identical reports preserve original clocks and do not create history samples; regressions are rejected. Estimated Metro positions and last-known display projections do not supply valid sampled speed/distance.

The distance is straight-line displacement between admitted samples, not travelled route distance. It is partial, misses unobserved movement and is not a completed ride's distance. The speed includes whatever movement/stopping occurred between eligible reports but is not exact commercial speed or general road speed.

## Aggregation and views

| Output | Calculation/meaning | Limitation |
|---|---|---|
| Average sampled speed | Combine valid bucket speeds weighted by `speed_sample_count` | Does not invent samples for nulls, missing periods or estimates |
| Observed distance | Sum valid retained distance contributions | Partial movement only |
| Detected trips | Distinct observed scoped trip identities in the selected query | Presence, not operated/completed-trip count |
| Fleet/volume | Detected vehicle identities in the requested period and buckets | Not complete registered inventory or permanent allocation |
| Traffic/rankings | Aggregate retained transit samples using selected filters | Not road congestion, exact headway or completeness statistics |

Historical pagination freezes the selected time range and SQL generation. Current live counts added to metrics remain separately labelled and are not historical reconstruction. Query support is bounded by the installation's retained data and configured filters; the [API contract](../../api/openapi.yaml) owns exact result schemas.

The current [query implementation](../../internal/app/history_queries.go) also makes these presentation choices:

- `HistoryPoint` groups records into five-minute chart intervals even when collection stores raw samples. Chart grouping and storage mode are separate.
- Detected-trip counts use distinct reported `trip_id` values and return null when none are available. Reuse of one source trip ID across service instances is not automatically counted as multiple completed journeys.
- Historical fleet rows group by operator/vehicle, combine first/last observation times, sum distance and collect distinct route IDs. Source/model/plate/typology/propulsion use SQL `max` over the selected records, not necessarily metadata from the latest observation or one atomic metadata record.
- Traffic bins use 0.001-degree latitude/longitude cells and valid reported speed samples. Hour/day filters use Europe/Lisbon; the weekdays option means Monday–Friday, not a public-holiday calendar.

## Retention and storage guards

`SNAPSHOT_RETENTION_DAYS` defaults to 30 and accepts 1–30. Public history queries honor that configured window. Physical cleanup retains an extra hour for pagination grace and removes at most 10,000 eligible rows per table per cleanup run. It is scheduled every five minutes when general ingestion runs; disabling it also disables that loop's cleanup.

With `STORAGE_GUARD=true`, [the application budget](../../internal/app/storage_budget.go) measures storage, estimates/reserves writes and blocks history admission around 4,000,000,000 bytes and operational writes around 4,500,000,000 bytes. Measurement failure blocks guarded writes. Config reports resolution, retention, storage limit and collection status. These are application admission thresholds, not a hard cap on internal database storage or other writers. Use one collector and a dedicated database; exact deployment settings are in [the deployment guide](../../deploy/README.md).

Live publication may advance despite failed persistence. History can have gaps during errors/pauses and durable live state can lag. Restart restores persisted data with original clocks and broken continuity. It cannot recover unfinished in-memory buckets or memory-only CP predictions.

## Payload archival is separate from aggregation

[Optional compaction](../../internal/app/snapshot_compaction.go) can archive legacy PostgreSQL metadata payloads when compression saves sufficient space. [The archive format](../../internal/app/snapshot_archive.go) preserves exact JSON text as bounded `LPH1` gzip data, with a small interactive projection. It does not change typed SQL observation facts or five-minute aggregation. CockroachDB does not use this optional rewrite path. Archival is bounded, guarded, and can be skipped without blocking live reads.

## Evidence

Implementation: [history collector](../../internal/app/snapshots.go), [storage](../../internal/app/snapshots_store.go), [query/revisions](../../internal/app/history.go), [budget](../../internal/app/storage_budget.go), [continuity](../../internal/app/continuity_publication.go).

Existing behavior tests: [aggregation](../../internal/app/snapshots_test.go), [retention](../../internal/app/retention_test.go), [continuity](../../internal/app/continuity_test.go), [stationary/repeated reports](../../internal/app/vehicle_service_test.go), [budget](../../internal/app/storage_budget_test.go), [archive](../../internal/app/snapshot_archive_test.go). External definition research is dated in [source evidence](../research/SOURCES.md); it does not turn these sampled metrics into the provider's completed-ride commercial speed.
