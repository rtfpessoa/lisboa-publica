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

`SNAPSHOT_RETENTION_DAYS` defaults to 30 and accepts 1–30. Public history queries honor that configured window. Permanent `vehicle_facts` and latest `vehicle_reporting` rows are independent of this retention and are never removed by historical cleanup. Reporting state has no retained transition history and does not add movement samples. Physical cleanup retains an extra hour for pagination grace and removes at most 10,000 eligible rows per table per cleanup run. A store-owned worker runs one pass before background writers start and retries each minute independently of provider availability and `INGEST_ENABLED`. Optional compaction is attempted separately at most every five minutes.

With `STORAGE_GUARD=true`, [the application budget](../../internal/app/storage_budget.go) measures storage and estimates/reserves writes. Ordinary history admission stops around 4,000,000,000 bytes and operational writes around 4,500,000,000 bytes; measurement failure blocks guarded writes. Config reports resolution, retention, storage limit and collection status. These are application admission thresholds, not a hard cap on internal database storage or other writers. Use one collector and a dedicated database; exact deployment settings are in [the deployment guide](../../deploy/README.md).

On PostgreSQL 17 or later, when the role can maintain the snapshots table, [pressure cleanup](../../internal/app/history_pressure.go) can shorten snapshot history before the configured retention target. The independent worker attempts an oldest-first batch of at most 10,000 snapshots strictly older than seven days; the seven-day floor applies only to this pressure path, and an explicitly shorter normal retention still applies. The persistent allowance starts at 300,000 deletions and is replenished by actual committed new recent snapshots. Each deleted row also grants one replacement insertion credit; another pressure batch waits until at most 1,000 credits remain. Duplicate or rolled-back insertions do not replenish the allowance. This path does not pressure-delete stop events, permanent facts, sessions or API keys.

After a batch, an ordinary PostgreSQL vacuum runs asynchronously with a 15-minute deadline independent of the cleanup pass; a pending or failed vacuum prevents pressure replacement writes and is retried on a later cleanup, including after restart. Eligible recent replacement snapshots can be admitted with a fresh size check and reservations below 4,200,000,000 bytes. On operator-verified direct PostgreSQL, maintenance reconciles completed reservations under the write lock with no vacuum in progress; a resumed pending vacuum retains its cleanup reservation. Before a guarded update near the history limit claims its own reservation, completed earlier reservations can also be reconciled so stale estimates do not unnecessarily block history. A failed measurement retains reservations and blocks the operation. An uncertain rollback, commit or vacuum retains reservations and blocks guarded writes. Its connection is discarded and maintenance waits for that backend PID to disappear on the same verified server before retrying; a serialized check reusing the same physical connection can prove earlier work has ended. An inaccessible activity view or mismatched server fails closed.

Direct mode also checks for prior same-role client sessions, including idle and not-yet-identified sessions, before migrations or new reservations. It assumes a direct, stable PostgreSQL server, a dedicated database and role, and one collector; an activity snapshot is not a distributed fencing protocol. Proxied or other unverified connections retain the existing periodic guard and do not use the extra reconciliation. Their uncertainty marker is in memory, so before restarting after an ambiguous write an operator must establish that the old work has ended; the application cannot prove that through an unverified proxy. See [deployment and recovery](../../deploy/README.md). Structured maintenance logs report outcomes, skip reasons and committed cutoff/epoch. When history batches are rejected or partially admitted, the next maintenance log also reports aggregate admission-attempt counts and, for the last rejection, the measured database size, estimated reservation and batch sizes, and the headroom or write-size category. Attempts can include a subsequent failed transaction. Current insertion credit and pending vacuum are sampled for that log when available; they may have changed since rejection. Storage measurement failures, insufficient headroom or missing maintenance privilege leave history paused. Vacuum generally makes space reusable within relations; it does not promise that the physical database or filesystem shrinks, and indexes, WAL and other writers can still grow. Retention is a target, not a guarantee of continuous observations.

An optional PostgreSQL filesystem probe checks available blocks and inodes on an operator-verified PGDATA mount before guarded writes and pressure vacuum. A failed measurement or insufficient reserve for the admitted write fails closed. If known free space fits the operational portion but not its history, the application drops the history and rechecks the operational portion. The shared-server Compose deployment enables it with a read-only, inaccessible PGDATA mount; external databases without a configured probe retain the database-size guard but their host capacity is unverified. The probe cannot reserve disk against other processes, and WAL or tablespaces on other filesystems require separate monitoring. See [deployment and recovery](../../deploy/README.md).

If a pending snapshot batch exceeds the available replacement credits, only an eligible subset is stored; the rest is dropped and collection reports paused. A filesystem shortage can likewise drop a batch while retaining an operational update. Those observations leave a gap and are not recovered later. After a no-op maintenance pass, a completed pressure vacuum or an eligible save, the collector can report collecting again when fresh storage headroom for a minimal replacement, replacement credit and maintenance checks indicate readiness. During subsequent maintenance, a still-valid readiness result can remain visible while its checks run. Status reflects the latest verified minimal readiness, not a guarantee that a future batch fits; its actual estimated size and eligibility are checked on admission. Exhausted credit, pending vacuum, insufficient headroom, interrupted work and failed checks invalidate that pressure readiness. Below the ordinary history limit, admissible history remains independent of replacement credit and vacuum pending state.

Live samples are staged only while collection is reported ready. A measurement can briefly delay staging while the bounded storage check holds its state lock, but unrelated cleanup transactions do not hold that lock. Samples missed while collection is paused or unavailable are not reconstructed; the five-minute buckets and 90-second closing grace still apply after collection resumes.

Pressure deletion persists an inclusive snapshot availability cutoff and advances the historical revision epoch. `/api/v1/config` exposes the cutoff as nullable `history_available_from`; it does not assert that observations exist at that time. Historical and coverage queries beginning before it return HTTP 410, and older revisions must restart pagination. The dashboard shortens overlapping ranges and explains when an entire selected day is unavailable. See [runtime recovery](../architecture.md) for rollback constraints.

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

The initial server-only cap is 10,000,000,000 bytes across managed detail, aggregates, manifest, state, temporary/replacement generations and filesystem allocation. Normal detail retention defaults to seven days, aggregate retention to 12 months. With no space pressure, configured expiry applies, including an explicitly shorter normal detail retention. Under pressure, the oldest verified eligible block is retired across kinds and the newest seven days are protected. Point clocks at the seven-day boundary are retained; an exclusive bucket end may equal the boundary. Unverifiable clocks and correction ledgers are retained and can cause earlier admission failure. Hour and five-minute blocks start with their exclusive bucket end; checkpoints start with their original source clock; daily aggregates use the next Lisbon midnight. Before retirement, checksum-verified records are scanned for later embedded clocks, including provider publication and correction evidence. A predicted target or deadline can conservatively extend storage but is not treated as an observed event. Ambiguous offset-free Lisbon clocks use the later valid instant. The archive rejects new writes if it cannot make safe room, so a seven-day floor is not a guarantee that all history was collected or remains available. Retiring an aggregate day removes cached training for that day; a prepared generation must not resurrect it. Exact allocated bytes are counted after serialized readers close and unlink completes.

Startup checks storage before normal expiry. An owner-held maintenance worker then runs independently of incoming records, normally every minute. Under bounded progress it retries after one second; hard failures use the configured normal cadence. Each pass performs at most two generation checks and one retirement; manifest metadata, a single decode and filesystem calls can still take significant time while holding the owner lock. Lowering the archive budget can require several passes. The job needs real space and an inode for a temporary manifest before unlinking; this narrowly bounded metadata write can exceed a lowered archive budget or consume filesystem reserve while recovering. The archive directory is limited to 32,768 direct entries, and unmanaged files or nested directories can prevent cleanup or reopening.

Optional `TRANSPORT_ARCHIVE_MIN_FREE_BYTES` and `TRANSPORT_ARCHIVE_MIN_FREE_INODES` reserve available space on the filesystem hosting the archive volume. Each defaults to zero, meaning no extra reserve for that resource. If either reserve is enabled, both available bytes and inodes must cover the prospective write; failed measurement or insufficient headroom fails closed. Other processes can still consume the measured space. The independent archive cap is not a quota on the shared filesystem. See [configuration and recovery](../../deploy/README.md).

The JSON v1 manifest is bounded to 8 MiB for reading and writing. Admission reserves the block-rounded temporary candidate manifest and new generations while counting the old manifest; at the ceiling only normal expiry can reduce the candidate, otherwise writes fail visibly. The manifest and its serialization require bounded extra memory. Legacy daily aggregate generations are checksum-verified and scanned in batches of 128 rows, up to 100,000 rows, to establish their newest correction clock. Other candidate generations are checksum-verified and decoded within their existing size bounds; recognized and timestamp-shaped fields are scanned conservatively, and results are cached for each unchanged generation. A selected generation is freshly checksum-verified before deletion; missing, malformed or unverifiable blocks are retained. A directory-sync failure after a manifest rename fences further archive writes until reopen and keeps both possible generations. Reopen syncs the selected manifest before removing unreferenced files and then syncs the cleanup.

When pressure blocks a Metro flush, its bounded detail buffer is retained and a later worker pass retries the flush before allowing competing publications to use the reclaimed room. Rejected incoming receipts still create a continuity gap; hard failure remains paused. Unowned space reservations expire after five seconds or two maintenance passes; an unowned manifest-limit request releases after one expiry pass. Unacknowledged in-memory changes can be lost on crash or shutdown. Cleanup and recovery do not restore missing observations.

The active Metro hour keeps at most 1,200 receipts and 16 MiB of encoded JSON lines, including newline bytes. Reaching either limit rejects further samples without discarding the retained buffer; a later hour can flush it when publication is safe. An overflow discovered after inference first attempts to publish the earlier buffer, then fences further checkpoint and detail writes until verified reopen, because the rejected sample may have changed the in-memory engine. Such a fence clears the patterns service's live historical support and can affect other operators sharing the checkpoint. Provider detail already committed before a checkpoint failure remains committed; the next collection records a gap.

Recovery verifies the selected checkpoint and latest Metro detail and accepts their sequences together only when one is an identical prefix of the other. A missing checkpoint entry is normal, but a referenced missing file is not. More than one later detail hour is conservatively fenced because replaying only the newest would omit the intermediate tail. Unknown, malformed, oversized, conflicting or otherwise non-round-trippable evidence is preserved and fences detail and shared-checkpoint replacement until it can be verified after repair. Identified conflicting generations are excluded from retirement even when their recorded kind is wrong. A fence immediately clears derived support in the patterns service, including when another operator's flush discovers it. Already copied Metro runtime forecasts and in-flight projections may remain visible until refreshed or expired; independent official predictions remain available. Replacement and deletion recheck source checksums, and a corrupt or unidentifiable detail/state orphan is retained for investigation. These checks cannot recover previously dropped samples or guarantee that an in-memory buffer survives a crash.

Sampling and histogram resolution are independently configurable (30 seconds initially); checkpoints/evaluation use 60 seconds; training/calibration target available 30-day windows without a minimum display age. The bounded hot-state cache can shorten the effective training window without deleting published long-term aggregates. A checkpoint whose marshaled payload would exceed the single-block archive limit trims the oldest aggregate days until it fits, never dropping the current day, a dirty day that still owes a publication, or a day with a live association; the reduced window is disclosed through the existing limited/cold-day reporting and published daily files are untouched. If the current day alone cannot fit, the flush fails explicitly instead of emptying the window. An emptied day is not republished as an empty replacement, and aggregate round-trip verification compares the row multiset so empty and repeated rows verify consistently. Buffer/budget failures pause history with explicit gaps. Startup recovery uses only retained verified data, and changing sampling/bin configuration creates a distinct model profile. Full details: [Metro patterns](../metro-patterns.md), [recovery](../architecture.md) and [deployment](../../deploy/README.md).

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

For each operator, an overflow gap follows the dropped queue entry, while a write
failure gap precedes the next processed receipt, even if it was already queued.
The worker retries the gap on further failed writes; successful gap receipts are
sampled outside the normal cadence. Pending markers are in memory and shutdown
may discard them along with queued receipts. Restart cuts live continuity. A
checkpoint error can occur after detail publication, making a later gap conservative.

Daily generations are authoritative during recovery, including empty revised
days; a cached row cannot resurrect a retired or withdrawn day. Replay across
closed hours uses recorded issuance and suppresses contributions already covered
by a newer daily generation. Failed detail publication restores uncommitted hot
state and cadence. Every restart cuts continuity. Evidence-backed maintenance
supports normalized observations with `-operator`; context-changing/stale or
unreconstructable inputs fail explicitly. Aggregates retain the earliest input
instant as well as latest knowledge time, so revisions cannot silently replace
summaries whose input provenance predates retained detail. See [full rules](../metro-patterns.md).

Schedule-prior Metro forecasts carry the contributing anchor's source clock, when known, and cannot outlive its expiry or the current episode. Inferred occurrence records remain retained independently of whether their source clocks are still fresh enough to anchor a forecast. A newer zero wait at the same station can support a still-current stop; another station cannot renew it. Historical forecast work is rejected if its visit evidence or station points changed while the work was in flight, even when archive pressure prevented a checkpoint revision. Episode and individual estimate expiry are checked again when work is applied, including time spent waiting for the runtime lock.

Read-time Metro display validity is separate from immutable issuance evidence. The retained forecast stores an optional `own_valid_until` bound derived from the original official anchor source clock plus 90 seconds. A legacy forecast without that bound is unavailable for current model display. A past point, inactive association, expired anchor or expired collector receipt withdraws the model point and band; official display uses its own original source clock and future point. Reads never modify the original issuance used for evaluation. When incompatible retained bin widths cannot yield a supported common resolution, the summary returns `resolution_seconds: 0` and the UI identifies the resolution as unavailable.

Provider recovery tracks the durable `AsOf` coverage of every verified daily generation even when that complete day cannot fit in the hot cache. Capacity-rejected replay updates do not mark an untouched retained day dirty, preventing publication of an empty replacement. The retained day remains readable from its verified Parquet generation; limited hot training support is disclosed separately.

Hot-day eviction is allowed only after pending daily changes have been durably published. A complete day excluded from hot memory is marked cold and cannot be reopened from a partial update; its full retained generation remains the read authority, with limited training support disclosed. Cold-day markers expire with the configured aggregate window. Maintenance reads complete affected days into a private working copy, rejects a capacity-limited replay, and replaces live state only after the atomic manifest commit.

An admitted hot day also remains in memory while a current association references a signal window in that day, including windows spanning local midnight. This preserves the complete input needed for conservative withdrawal after a later contradiction. Under capacity pressure, newer training contributions may be unavailable until publication and association cleanup make eviction safe. A fresh official cold-start point keeps its association-unavailable explanation; read-time expiry only replaces that explanation when an own point actually expires.

## Metro popup inference journal

The [inferred popup journal](../../internal/patterns/metro_event_journal.go) retains event revisions and minimal
contributing original samples in the existing allocated-byte archive budget. The target is seven days, independently
of the sampled raw-response archive; missing collections and earlier evictions can leave gaps. Proofs are capped at 64 KiB, pending writes at
1024 proof records and a shared 8 MiB pending budget with checkpoint work; healthy durable batches flush every second. Manifest admission/checksums govern recovery;
corruption, eviction or write failure leaves partial history, and restored evidence cannot restore live continuity.
These records are not certified SQL actual events or physical accuracy metrics. See [runtime/UI semantics](../metro-live-popups.md).

The [offline Metro departure calibration command](../metro-departure-calibration.md) reads a separately
collected frozen dataset and writes a candidate/holdout report to stdout. It neither imports the input into
the shared archive nor adds SQL actual events or retained physical metrics. Original evidence bundles and
reference windows are operator-managed; existing sampled history and popup proofs are insufficient. Physical-reference comparison requires independent references; the separate model-consistency path uses explicitly labeled model-support intervals and never measures physical accuracy or enables live departures.

## Metro latest journey checkpoints

[Complete checkpoints](../../internal/patterns/metro_checkpoint.go) retain the latest journey state independently of event proofs. The additive `popup-checkpoint` kind shares the existing manifest owner, compressed-block checksums, atomic multi-record transactions, seven-day original-source-age retention and global allocated-byte FIFO. A fresh supported identity is selectable before acknowledgement, with pending/unavailable persistence explicitly exposed. Coalesced later progress carries pending revision metadata and may be lost in a crash; it cannot overwrite a later queued revision when an earlier commit finishes.

Each stored record, including metadata, is at most 256 KiB; the runtime reserves 1 KiB of that limit for metadata. Checkpoint work has at most 1024 dirty identities and shares an 8 MiB pending budget with pending event proofs, including a 1 KiB reservation per checkpoint for encoded metadata. Hot runtime history has at most 1024 episodes; cold recovery reads the latest manifest key without scanning all event payloads. Reads do not renew source age; the same original-age TTL is checked for hot records even while ingestion is stopped. Recovery returns suspended historical state and explicitly missing/expired/corrupt or legacy partial outcomes. Missing records cannot be attributed definitively to eviction. Archive write or capacity failure leaves the live episode and forecasts usable, with unavailable persistence; a crash can lose those uncommitted identities.

Verified cold checkpoint recovery can populate the bounded hot historical cache without restoring continuity or creating writes. Failed recovery consultation has at most 1024 negative entries: unavailable/expired results are memoized for 60 seconds, other failed results for five seconds. This avoids repeated legacy event scans on every stream tick; these timers do not alter source clocks or retained evidence lifetime.

Lifecycle completion/handoff changes publish atomically in memory. All related checkpoints reserve queue capacity together and commit in one generation; capacity failure queues no partial transition. Model departure withdrawal removes the main time but retains bounded per-visit revision evidence (256 revisions maximum; excess makes the field unavailable). Qualified checkpoints retain frozen calibration, axis, segment durations, evidence hash/reviewer and latest source model samples. These are historical audit inputs, never restored live detector state. A later commit acknowledgement marks only matching departure/revision evidence as committed.

### Metro forecast provenance in live frames

Live-frame platform provenance and anonymous/estimated forecast identity are derived from the current direct publication; their API exposure does not create retained prediction history. The existing raw-wait cache and sampled archive keep their existing cadence, limits and original clocks. Journey checkpoints can retain the disclosed unknown-origin flag without treating a shared downstream axis as observed earlier visits. The direction correction adds no acquisition source, archive cadence or physical fleet identity. See [live behavior](../metro-live-popups.md).

## Changed Metro operational inputs

The `metro-inputs` archive kind retains bounded changed normalized waits and admitted Hub model positions, receipt order, original/model/receipt clock classes, source/profile/static context and source availability/errors. Processing-clock-only repeats do not produce fresh movement evidence. The runtime queue is at most 64 records/8 MiB with 256 KiB per record; five-minute compressed chunks are at most 32 MiB. Seven-day original-age retention shares the global allocated-byte budget and pressure floor. Queue gaps and exhausted writes stay explicit until a matching captured gap version is durably acknowledged. An older acknowledgement cannot clear a newer gap.

This changed-input lane is separate from sampled raw waits, learned aggregate history and SQL actual events. It supports reproducible model replay without inventing physical truth. The history delivery worker is separately bounded at 64 tasks/8 MiB using encoded payload size. Neither disk writes nor historical queries hold the operational runtime lock. Read-time historical projection uses revision/source/profile/expiry fences. See [current Metro behavior](../metro-live-popups.md) and [input writer](../../internal/patterns/metro_input_capture.go).

A pending lifecycle group protects its complete membership until acknowledgement or whole-group expiry. If one member expires after a prolonged outage, the complete pending generation is abandoned with a disclosed gap and bounded capacity is released. History delivery loss also advances an ordered generation; the worker resets prospective learning before the next post-loss receipt without erasing frozen issued comparisons or unrelated historical occurrences. Capture acknowledgement cannot clear that separate learning gap.

Post-loss sampled receipts persist a `delivery_gap` barrier and force one bounded receipt even inside the normal sampling interval. Historical reprocessing honors that barrier before rebuilding continuity. The marker is separate from changed-input capture, source error and normal raw-sampling cadence; it preserves frozen issuance while preventing later revisions from bridging dropped learning inputs.

Operational modeled-departure checkpoints retain bounded per-visit publication stamps for the qualifying direction positions, supported stop, first movement and confirmation (at most six clocks). These immutable clock/context/coordinate stamps permit scoped correction withdrawal independently of the current motion pointer. A bounded 64-clock model dictionary distinguishes actual replacements from delayed identical publications; unknown older input cuts prospective continuity without inventing a contradiction. Correction payloads and motion generations are retained in changed-input capture without advancing model movement. Historical-only correction writes do not reactivate a journey.

Correction barriers keep the maximum clock already seen within the motion lineage; descending delayed rows and repeated corrected clocks cannot lower it or become movement samples. Frozen occurrence support survives reference re-entry and historical recovery without restoring detector state.

The optional Metro predecessor frame section reads only one linked complete checkpoint or a current in-memory predecessor; it never scans event history or extends source age. Lookup is asynchronous, with at most four outstanding reads and 64 cached selection/link/profile entries. A five-second deadline fences results, and waiting for the archive owner lock respects cancellation. Once file I/O begins it cannot be interrupted; that read retains its slot until it returns. Closing a stream cancels the lookup it started; one-shot reads use the deadline because separate requests have no shared selection identity. Transient failures retry after 5, 15, 30 and then 60 seconds; absent, expired and corrupt records wait 60 seconds. A cached checkpoint is usable only through its original seven-day source-age limit. Pending correction evidence is retained in memory against eviction until a checkpoint containing the correction is acknowledged; if capacity is exhausted, new admissions can fail rather than resurrect the older event. Uncommitted corrections can still be lost on process failure.
