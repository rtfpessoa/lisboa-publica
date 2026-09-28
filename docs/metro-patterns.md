# Experimental transport patterns and forecast comparison

The **Padrões** view shows retained hourly proxy signals and two forecast sources. Collection consumes the existing authenticated Metro refresh, with no additional upstream calls. Official predictions remain independent of historical collection failures. Waiting means the first admissible future service in published slot order at the station; onward calls refer to one currently supported association. Neither published train IDs nor a supported association identify a physical fleet unit.

Open `/api/v1/metro/patterns?stop_id=metro:...` for a station. An optional `episode` adds the remaining calls for that association. The [OpenAPI contract](../api/openapi.yaml) defines parameters and fields. Public-read policy applies; API keys require both `read:transit` and `read:history`. Raw retained responses and detailed historical evaluations are server-only and have no download endpoint.

Three positive-to-zero first-slot transitions are needed before a chain becomes supported. The planned destination/route must be unique and each next retained visit unanimously adjacent, with preserved sequence numbers. Distinct station windows must be strictly ordered and disjoint; duplicates or multiple associations remain unknown. Presence in any valid slot must continue through receipts. Clock regressions/conflicts, source gaps over 60 seconds, receipt gaps over 60 seconds or source age above 90 seconds reject support. Clocks with ambiguous/nonexistent Lisbon local times are rejected. Initial zero, repeated zero or disappearance alone never means a physical event or cancellation. Known intermediate losses observed at the existing faster refresh also revoke support, without creating faster training samples.

An admitted adjacent pair contributes a duration window and its explicitly modeled midpoint. A window crossing an origin local hour is excluded from hour-specific duration training. Its component includes origin dwell once and excludes target dwell. A future official anchor for the same association starts the own forecast. Subsequent components use compatible route/destination/platform, profile, source condition, local hour/offset and day type, evaluated at each predicted origin time. Completed travel is never added. Missing support stops the affected point and later points while official values remain available. The model has no measured speed, dwell or residual progress input.

Forecast evaluation preserves sampled issuance values and later bounded proxy references. First-eligible calibration excludes repeat updates but retains first cases without a reference. The nominal arrival interval targets 80%, uses a corrected order-statistic rank and conservative histogram outer edge, and is unavailable if the rank exceeds sample count. Paired error strata use the official horizon at issuance. These scores and bands are conditional on inferred windows from the same source, without physical validation or an independence guarantee. Sampled cases and the two functionalities can repeat a journey association.

Hourly cells display supported signal counts, not probabilities or complete operational frequencies. Missing signals do not establish absence of service. Wilson 95% physical occurrence intervals remain unavailable without an admissible detection/coverage denominator. Dwell and speed remain null. Data can be shown from the first admissible samples, with the number of dates and a sparse-data warning; 30 days is not a display gate. Civil-day grouping distinguishes weekdays, Saturdays, Sundays and mandatory Portuguese national holidays plus Lisbon’s 13 June holiday. This is not an operator service calendar. Offsets, sampling/evidence identities and service conditions remain separate; compatible bin widths can be combined conservatively.

The archive keeps configurable target retention of seven days of detail and 12 months of hourly aggregates. Initial sampling/bin widths are independently 30 seconds; checkpoint/evaluation intervals are 60 seconds; training/calibration windows are 30 days. A 10 GB server archive budget, FIFO and bounded hot state may reduce effective retention. See [history](data/history.md), [runtime/recovery](architecture.md) and [deployment configuration](../deploy/README.md).

Experimental adapters also cover Carris Metropolitana, Carris, CP, Fertagus, Transtejo/Soflusa, TCB and Mobi Cascais. Enable one additional collection stage at a time. Their own forecasts need compatible reported stop-state sequences and verified published paths; missing evidence keeps the own point unavailable. Live data collected after deployment will inform sizing, cadence, compression and model adjustments; it does not automatically establish improvement over the official forecast.

Waiting skips expired, missing, invalid or duplicate train slots while preserving the source clock. Physical proxy transitions still use the original first slot. Forecast mode `official+sequential-proxy-components/v3` separates this eligibility rule from previously issued calibration cases.

Station and onward queries use the shared bounded frontend retry policy. For `503 busy`, it permits up to four retries, respects `Retry-After` with backoff/jitter, and then exposes an error until the normal 30-second refresh. Retrying application reads does not call the Metro provider.

## Compatible history and context

Recent exact-context components are preferred. When absent, the model tries older exact-context data, then recent general-context data, then older general-context data, within retained history. Every forecast component discloses sample/date support, oldest/newest date, older-history use and general-context use. Missing target-platform uniqueness rejects the component. Inputs must have been known strictly before issuance.

Route conditions come from the actual published line state at collection; a different line’s alert does not reset this route. Missing/conflicting line information is unknown. These snapshots do not establish when a physical disruption began. Sampling cadence remains part of the compatibility identity. A bin-width change preserves source detail and previously issued forecasts, and admits conservative mixed-width calibration edges. Cross-plan component reuse requires identical published segment geometry and endpoint coordinates; missing or ambiguous geometry keeps full-profile isolation. A topology/profile change still revokes live continuity.

The holiday rule `pt-national-lisbon-2026-v1` uses Gregorian Easter for Good Friday, Easter Sunday and Corpus Christi, fixed mandatory national dates and Lisbon’s 13 June municipal holiday. It supports civil years 2017–2199; other years are unknown. Optional Carnival is excluded. Rules were checked on 2026-09-27 against [Labour Code article 234](https://diariodarepublica.pt/dr/legislacao-consolidada/lei/2009-34546475-46746675) and the [Lisbon municipal calendar](https://informacoeseservicos.lisboa.pt/fileadmin/espacos/ficheiros/CDMAL_Feriados_2025_aviso.pdf). Future legal changes require a new rule version rather than relabeling existing evidence.

## Durable evaluation

The station report separates waiting/onward, route/destination, method, sampling/evidence profile, communicated condition, official issuance horizon and paired/official-only/own-only cohorts. It exposes issued/evaluated case counts, both point availabilities, bounded MAE and P90, dates and distinct hashed association identities. These are associations, not physical journeys; unavailable identity or bounded distinct-count capacity is disclosed. Legacy summaries without error histograms have no P90. Report-wide date entries, distinct associations and histogram cells are each bounded to 100,000; limits disclose partial support, and incomplete distributions suppress P90 while exact retained sums/counts remain usable. Neither unassociated forecasts nor cases losing support supply an error reference.

MAE uses exact retained error-bound sums; P90 uses separate conservative lower/upper histogram edges, including each row’s original bin width. Arrival-band coverage is reported as a lower/upper proportion: the whole inferred window inside the issued band versus any overlap. Missing references are excluded and availability remains visible. These results describe agreement with same-source proxies, not physical accuracy or demonstrated improvement. Distributions/support persist in daily aggregate blocks after detail expiry. Bounded hot state restores complete recent days; older cold blocks remain readable, while effective component training may be shorter than nominal retention.

## Other operator stages

`TRANSPORT_ARCHIVE_OPERATORS` defaults to `metro`. Enable prefixes only, in order: `metro,cm,carris,cp,fertagus,ttsl,tcb,mobi`. Each step adds one operator; disabling a stage does not immediately delete its retained history. `INGEST_ENABLED` and normal provider availability still determine whether snapshots arrive. Capture runs before last-known projection, samples at the configured archive cadence and stores normalized published identity/source URL, route/trip/plan/pattern/service-date/stop/status/position-kind, original observation clock, coordinates and optional reported speed. It preserves source error receipts and never renews observation clocks. This is normalized source evidence, not a copy of every provider’s original payload or an independently observed arrival. Metro continues using its direct wait payload path.

The runtime uses one bounded eight-receipt writer queue so compression/filesystem work does not block live publication. Queue overflow or a failed archive write marks the next admitted receipt with an explicit collection gap; gap notifications may be retained between scheduled samples. Shutdown can leave queued samples unwritten, and no uninterrupted capture is assumed across restart. Compressed hourly chunks use the same owner lock, verified immutable generations, seven-day detail expiry and global 10 GB FIFO. Each chunk has at most 16 MiB decoded data, each receipt at most 16 MiB/20,000 observation rows, and the manifest remains bounded to 1 MiB. Reported speed is retained privately with provenance; it does not populate Metro’s unavailable measured-speed metric. The API/UI list all stages and their capabilities without exposing vehicle records. Collection alone does not establish that the adapter can produce a supported own forecast.

## Evidence-backed maintenance

The local `cmd/patterns-maintenance` command requires an exclusive archive owner, a private correction file, original receipt time, SHA-256 of the currently corrected normalized row and a source-evidence description. It accepts at most 1,000 changes; stop/platform/destination changes require separate validation and are rejected. Original detail and issued forecasts remain immutable. A ledger preserves each revision. Retained inputs/topologies are replayed for affected route/destination/date scopes; original forecast values and first-case selection are reused for evaluation. Statistics from unaffected scopes are preserved.

Expired inputs, stale hashes, missing topology, affected aggregates older than reconstructable detail, corrupt files, cancellation, capacity/working-set limits or source retirement during reservation reject the revision. The decoded replay and prepared publication working sets are independently bounded to 64 MiB; larger revisions need a future streaming workflow. A single durable manifest commits revised daily blocks, ledger and checkpoint together. Maintenance resets live continuity and retains pending issued values for later explicit loss-of-support accounting. Corrected statistics are known only from the revision time, and no complete physical reconstruction is claimed. Retention and cadence flags must match the running service; see the deployment guide.

## Published-stop adapters for later stages

`/api/v1/transport/patterns?operator_id=cm&stop_id=cm:...` selects an operator;
`episode` adds its onward calls. Metro delegates to the original endpoint. The
same public/key scope policy and bounded read admission apply. The UI keeps
operator, stop and association in both query identities, lets users select a route/direction, labels sequence values
as published visits rather than physical platforms, and preserves official values.

CM requires its explicit native pattern/line and a verified full published path.
Other stages require exact published plan, trip, operating date, active calendar
and route. A longer journey may supply a uniquely selected contiguous retained
run containing the published stop; missing middle visits are never bridged.
Missing direction uses an explicitly labeled published pattern/destination context.
Coordinates, names and original visit sequence participate in the path digest.
Later-stage components remain isolated by full path/evidence/sampling profile;
Metro's unchanged-segment geometry compatibility does not authorize cross-plan
reuse for these adapters.

Eligible inputs are fresh reported positions, an exact verified stop context and
`INCOMING_AT` or `IN_TRANSIT_TO` transitioning to `STOPPED_AT` at that same visit.
The stopped position must be within 150 metres of its published stop. Source and
receipt gaps over 60 seconds, age over 90 seconds, conflicting equal-clock rows,
duplicates, ambiguous repeated visits and changed contexts cut support. Three
strictly adjacent disjoint windows admit a proxy chain. Windows crossing a local hour do not populate a single hourly signal cell or an origin-hour component. Intermediate losses cut
only the affected association and retain explicit per-identity cuts for replay, without adding faster training; sampling can miss short stops. These
published-state proxies are not physical arrivals, dwell or speed measurements.
A normal vehicle disappearance ends support without invalidating completed
components; conflicting duplicate identity revokes the affected training strata.

The model sums compatible arrival-to-arrival midpoint components at each
predicted origin hour, with origin dwell included once. A fresh official next-stop
anchor starts the remaining chain when it uniquely matches route/trip and any
published plan/date/vehicle/sequence. Repeated visits require explicit sequence.
Without an anchor, the last supported arrival-window midpoint can start the
remaining chain under a separate method/calibration identity. A modeled point
already in the past is unavailable, not clamped forward. Official/own points
remain on the same case, and both waiting/onward functions use the existing
first-eligible calibration and bounded evaluation reports. Official predictions
without an admissible vehicle association remain visible at cold start.

The patterns read also exposes recent official predictions from the existing
accepted cache when experimental history is disabled or unreadable. These
response-only rows retain the original source clock and validity, never enter
calibration/evaluation, and do not rewrite previously emitted pairs. Predictions
without a supported association appear separately from a selected journey or
direction. Reading this fallback adds no provider polling; stale cached values
are unavailable.

Normalized stop-arrival and shared-feed publications are retained independently
at the configured sample interval, with original validity and source clock (or
explicit collection-bounded validity where the provider omits that clock).
Source/collection time is never promoted into an independent reference. Shared
feed replacement clears only its namespace; direct-stop replacement clears only
that stop namespace. CM patterns reads register the same bounded stop interest
as the existing station board. No new position or shared-feed polling is added.

The asynchronous archive queue assigns receipt time under its lock so independent collectors cannot introduce arrival-order regressions. It separately retains the snapshot’s original collection time; row and prediction source clocks and validity are unchanged.

Each later-stage hot engine bounds aggregates/selections/report identities to
5,000, tracks/calls/prediction sample keys to 2,000 and active path definitions to
256 (at most 512 visits each). Both functions together retain at most 20,000 component references per live snapshot; unsupported points beyond this summary budget are explicitly unavailable. Capacity reductions are disclosed. Independently
retained detail chunks bootstrap a bounded dictionary and omit duplicate path
definitions within that chunk. Failed detail publication rolls back unarchived
statistics and cadence; already committed global FIFO deletions remain final.
Recovery restores complete newest daily generations, including empty withdrawal
generations, then replays records newer than the checkpoint across closed hours.
Additions covered by newer durable daily generations are suppressed. Every
restart revokes continuity, including degraded recovery. Later stages share
Metro's 10 GB allocated budget; configured retention is still a target.

## Corrections for later stages

`patterns-maintenance -operator cm` accepts the same correction envelope with
`row` as a retained normalized observation. The receipt time and canonical row
hash must match exactly. Evidence is required; journey, source, route, trip,
plan, service date and normalized stop context cannot change. A separate provider
ledger preserves revisions, while original observations and emitted forecasts
remain immutable. Replay uses recorded forecasts/selection, never revised
issuance values. Complete affected route/direction/date aggregates are replaced
in one manifest transaction together with the ledger and checkpoint. Inputs are
known from revision time afterward. Retired/stale/context-changing inputs,
unknown or earlier input provenance, partial reconstruction and bounded working
sets reject the request. The operator-specific command uses the same exclusive
lock and exact configuration flags as Metro maintenance.
