# History and derived metrics

History contains selected observations collected by this installation. It does not reconstruct missing movement, prove that scheduled trips occurred or import a provider's historical archive. CP predictions are memory-only; direct Metro predictions have a durable last-state cache but are not retained vehicle history.

A clean database cutover deliberately starts a new database history without importing Cloud rows. Existing filesystem transport archives are separate and are preserved. See the [deployment procedure](../../deploy/README.md#switch-from-cloud-to-a-clean-local-database).

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

Omission, source failure, restart and ledger eviction can break continuity. The first recovery report does not automatically create a valid movement pair. Identical position reports preserve original clocks and do not create history samples; stable metadata may be independently confirmed at that same position clock; regressions are rejected. Estimated Metro positions and last-known display projections do not supply valid sampled speed/distance.

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

`SNAPSHOT_RETENTION_DAYS` defaults to 30 and accepts 1–30. Public history queries honor that configured window. Permanent `vehicle_facts` and latest `vehicle_reporting` rows are independent of this retention and are never removed by historical cleanup. Reporting state has no retained transition history and does not add movement samples. Physical cleanup retains an extra hour for pagination grace and removes at most 10,000 eligible rows per table per cleanup run. It is scheduled every five minutes when general ingestion runs; disabling it also disables that loop's cleanup.

With `STORAGE_GUARD=true`, [the application budget](../../internal/app/storage_budget.go) measures storage, estimates/reserves writes and blocks history admission around 4,000,000,000 bytes and operational writes around 4,500,000,000 bytes. Measurement failure blocks guarded writes. Config reports resolution, retention, storage limit and collection status. These are application admission thresholds, not a hard cap on internal database storage or other writers. Use one collector and a dedicated database; exact deployment settings are in [the deployment guide](../../deploy/README.md).

Live publication may advance despite failed persistence. History can have gaps during errors/pauses and durable live state can lag. Restart restores persisted data with original clocks and broken continuity. It cannot recover unfinished in-memory buckets or memory-only CP predictions.

## Payload archival is separate from aggregation

[Optional compaction](../../internal/app/snapshot_compaction.go) can archive legacy PostgreSQL metadata payloads when compression saves sufficient space. [The archive format](../../internal/app/snapshot_archive.go) preserves exact JSON text as bounded `LPH1` gzip data, with a small interactive projection. It does not change typed SQL observation facts or five-minute aggregation. CockroachDB does not use this optional rewrite path. Archival is bounded, guarded, and can be skipped without blocking live reads.

## Evidence

Implementation: [history collector](../../internal/app/snapshots.go), [storage](../../internal/app/snapshots_store.go), [query/revisions](../../internal/app/history.go), [budget](../../internal/app/storage_budget.go), [continuity](../../internal/app/continuity_publication.go).

Existing behavior tests: [aggregation](../../internal/app/snapshots_test.go), [retention](../../internal/app/retention_test.go), [continuity](../../internal/app/continuity_test.go), [stationary/repeated reports](../../internal/app/vehicle_service_test.go), [budget](../../internal/app/storage_budget_test.go), [archive](../../internal/app/snapshot_archive_test.go). External definition research is dated in [source evidence](../research/SOURCES.md); it does not turn these sampled metrics into the provider's completed-ride commercial speed.

## Explicit stop occurrences

The append-only `stop_events` table stores verified journey/visit arrival and departure occurrences, their original evidence and committed generation. These records are independent of position snapshots, aggregates and forecasts. The internal `saveReportedStopEvents` boundary deduplicates matching occurrences and preserves previous versions for frozen journey readers across later corrections and restart. No public write endpoint exists, and no current adapter is certified to populate this table. Missing past times remain unavailable.

Stop-event writes reserve the existing history budget before their transaction. Configured 1–30-day retention and bounded pruning apply; a storage pause preserves committed events and is exposed in popups as “Recolha de tempos reais em pausa”. The original provider clock is never replaced with polling time. See [popup behavior](../VEHICLE-POPUPS.md).

## Independent experimental Metro archive

`internal/patterns` consumes existing direct Metro responses, separate from the vehicle snapshot history and its database guard. JSON lines compressed with Zstd retain sampled wait payloads (including unknown fields), normalized inputs, sampled forecasts and later outcomes by operator/UTC hour. Sparse Parquet+Zstd files retain additive hourly aggregates by operator/local date, including route/destination, stop/platform, ordered component, local hour/UTC offset, day type, reported condition, profile/reference, resolution and histogram bin. Counts/sums and bounded-duration/error sums remain distinct from model midpoint samples. No physical occurrence denominator is fabricated.

The initial server-only cap is 10,000,000,000 bytes across managed detail, aggregates, manifest, state, temporary/replacement generations and filesystem allocation. Retention targets are seven days and 12 months, subordinate to space. After expiry, FIFO removes the oldest closed data globally without a type/operator preference. Retiring an aggregate day removes cached training for that day; a prepared generation must not resurrect it. Exact allocated bytes are counted after serialized readers close and unlink completes.

Sampling and histogram resolution are independently configurable (30 seconds initially); checkpoints/evaluation use 60 seconds; training/calibration target available 30-day windows without a minimum display age. The bounded hot-state cache can shorten the effective training window without deleting published long-term aggregates. Buffer/budget failures pause history with explicit gaps. Startup recovery uses only retained verified data, and changing sampling/bin configuration creates a distinct model profile. Full details: [Metro patterns](../metro-patterns.md), [recovery](../architecture.md) and [deployment](../../deploy/README.md).

The local completion follow-up adds route-specific conditions, versioned Lisbon holiday grouping, labeled older/general-context component fallback, unchanged-segment compatibility, conservative mixed-bin calibration and durable bounded MAE/P90/availability/band-support reports. Evidence-backed maintenance revises retained inputs atomically while keeping issued values. Staged normalized observation/prediction capture and experimental own-forecast adapters cover all eight existing operators under the same archive budget. Metro uses ETA transitions; later stages require verified published paths and coherent reported stop-state transitions. Forecast availability depends on actual compatible inputs, and physical validation remains unavailable. See [current behavior](../metro-patterns.md) and [remaining live evidence](../GAPS-metro-patterns.md).

## Later-stage inference and revisions

Later-stage detail also retains normalized official predictions, their original
source clock/validity and verified published path dictionaries. Full position
and partial prediction sources sample independently at the configured cadence;
receipt counts can therefore include multiple sources and are not event counts.
Path definitions are deduplicated within bounded independently retained chunks.
Each later-stage engine has 5,000 hot aggregates and 2,000 tracks/calls/prediction
sampling keys, separate from Metro's larger hot limit but inside the same global
archive budget. API summaries filter archived daily blocks by operator.

Daily generations are authoritative during recovery, including empty revised
days; a cached row cannot resurrect a retired or withdrawn day. Replay across
closed hours uses recorded issuance and suppresses contributions already covered
by a newer daily generation. Failed detail publication restores uncommitted hot
state and cadence. Every restart cuts continuity. Evidence-backed maintenance
supports normalized observations with `-operator`; context-changing/stale or
unreconstructable inputs fail explicitly. Aggregates retain the earliest input
instant as well as latest knowledge time, so revisions cannot silently replace
summaries whose input provenance predates retained detail. See [full rules](../metro-patterns.md).

Read-time Metro display validity is separate from immutable issuance evidence. The retained forecast stores an optional `own_valid_until` bound derived from the original official anchor source clock plus 90 seconds. A legacy forecast without that bound is unavailable for current model display. A past point, inactive association, expired anchor or expired collector receipt withdraws the model point and band; official display uses its own original source clock and future point. Reads never modify the original issuance used for evaluation. When incompatible retained bin widths cannot yield a supported common resolution, the summary returns `resolution_seconds: 0` and the UI identifies the resolution as unavailable.

Provider recovery tracks the durable `AsOf` coverage of every verified daily generation even when that complete day cannot fit in the hot cache. Capacity-rejected replay updates do not mark an untouched retained day dirty, preventing publication of an empty replacement. The retained day remains readable from its verified Parquet generation; limited hot training support is disclosed separately.

Hot-day eviction is allowed only after pending daily changes have been durably published. A complete day excluded from hot memory is marked cold and cannot be reopened from a partial update; its full retained generation remains the read authority, with limited training support disclosed. Cold-day markers expire with the configured aggregate window. Maintenance reads complete affected days into a private working copy, rejects a capacity-limited replay, and replaces live state only after the atomic manifest commit.

An admitted hot day also remains in memory while a current association references a signal window in that day, including windows spanning local midnight. This preserves the complete input needed for conservative withdrawal after a later contradiction. Under capacity pressure, newer training contributions may be unavailable until publication and association cleanup make eviction safe. A fresh official cold-start point keeps its association-unavailable explanation; read-time expiry only replaces that explanation when an own point actually expires.

## Metro popup inference journal

The [inferred popup journal](../../internal/patterns/metro_event_journal.go) retains event revisions and minimal
contributing original samples in the existing allocated-byte archive budget. The target is seven days, independently
of the sampled raw-response archive; global FIFO can shorten it. Proofs are capped at 64 KiB, pending writes at
1024 proof records and a shared 8 MiB pending budget with checkpoint work; healthy durable batches flush every second. Manifest admission/checksums govern recovery;
corruption, eviction or write failure leaves partial history, and restored evidence cannot restore live continuity.
These records are not certified SQL actual events or physical accuracy metrics. See [runtime/UI semantics](../metro-live-popups.md).

The [offline Metro departure calibration command](../metro-departure-calibration.md) reads a separately
collected frozen dataset and writes a candidate/holdout report to stdout. It neither imports the input into
the shared archive nor adds SQL actual events or retained physical metrics. Original evidence bundles and
reference windows are operator-managed; existing sampled history and popup proofs are insufficient. Physical-reference comparison requires independent references; the separate model-consistency path uses explicitly labeled model-support intervals and never measures physical accuracy or enables live departures.

## Metro latest journey checkpoints

[Complete checkpoints](../../internal/patterns/metro_checkpoint.go) retain the latest journey state independently of event proofs. The additive `popup-checkpoint` kind shares the existing manifest owner, compressed-block checksums, atomic multi-record transactions, seven-day original-source-age retention and global allocated-byte FIFO. A baseline commits before the runtime exposes a selectable identity. Coalesced later progress carries pending revision metadata and may be lost in a crash; it cannot overwrite a later queued revision when an earlier commit finishes.

Each stored record, including metadata, is at most 256 KiB; the runtime reserves 1 KiB of that limit for metadata. Checkpoint work has at most 1024 dirty identities and shares an 8 MiB pending budget with pending event proofs, including a 1 KiB reservation per checkpoint for encoded metadata. Hot runtime history has at most 1024 episodes; cold recovery reads the latest manifest key without scanning all event payloads. Reads do not renew source age; the same original-age TTL is checked for hot records even while ingestion is stopped. Recovery returns suspended historical state and explicitly missing/expired/corrupt or legacy partial outcomes. Missing records cannot be attributed definitively to eviction. Archive write or capacity failure leaves forecasts usable, without an unrecoverable new selectable identity.

Verified cold checkpoint recovery can populate the bounded hot historical cache without restoring continuity or creating writes. Failed recovery consultation has at most 1024 negative entries: unavailable/expired results are memoized for 60 seconds, other failed results for five seconds. This avoids repeated legacy event scans on every stream tick; these timers do not alter source clocks or retained evidence lifetime.

Lifecycle completion/handoff revisions freeze rather than coalescing while their complete multi-record generation commits. Model departure withdrawal removes the main time but retains bounded per-visit revision evidence (256 revisions maximum; excess makes the field unavailable). Qualified checkpoints retain frozen calibration, axis, segment durations, evidence hash/reviewer and latest source model samples. These are historical audit inputs, never restored live detector state. A later commit acknowledgement marks only matching departure/revision evidence as committed.
