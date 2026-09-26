# CP service details, readable plates and stopped vehicles

Status: accepted planning after independent plan and final reviews; main confirms the plan meets the requested scope. Planning only, 26 September 2026. Main owns research, planning, implementation, testing and fixes; existing real `quasar-alpha` reviewers at `xhigh` perform independent reviews only. No implementation, commit, publication or deployment is included in this planning session. Preserve the accepted, uncommitted popup work as the implementation baseline.

## Outcome and scope

1. Display road vehicle plates as `CE-29-PV`, consistently in vehicle details and fleet tables, while preserving the original upstream value and identity. Users can search with or without separators.
2. Show a normal stopped-at-stop/station/terminal state when the provider publishes that state. Keep operational state separate from data freshness: a parked vehicle is not necessarily out of service, and missing updates do not establish a stop.
3. Add verified CP journey origin/destination, service information, delay and station calls from independent official sources. Evaluate direct position coverage at Oriente without fabricating GPS markers.

Do not add a general provider framework, a state-machine library, nearest-station guesses, path interpolation, a new vehicle inventory, new history charts, longer retention or new infrastructure. Do not consume Mover Lisboa APIs, reuse CP website credentials, disable TLS verification, or enlarge the Lisbon map/network to retain national journeys.

## Evidence and existing behavior

- [CP source investigation](research/CP-REALTIME-COVERAGE.md): TML CP position rows are intermittent; legacy/current position endpoints had an identical paired dataset. CP travel APIs expose journey details and minute-valued delays. Train 574 had `AT_ORIGIN` and null train coordinates; stop-list coordinates locate stations. CP partner VehiclePositions/TripUpdates exist, but our endpoint/access, permissions and quotas remain unverified. TML ETA is public and experimental; original freshness and station-ID joins need verification.
- The official [TML Hub publisher](https://github.com/tmlmobilidade/go/blob/prd/modules/hub/apps/publish-vehicles/src/tasks/publish-vehicle-positions.ts) forwards `current_status`, `stop_id`, original `created_at`, and `operational_date` to JSON. Its feed-generation timestamp is distinct from the original vehicle timestamp; publishing again does not establish a newer observation.
- Both [Hub decoding](../internal/app/ingest.go#L83) and [CM decoding](../internal/app/ingest.go#L370) drop `current_status` and `stop_id` that upstream already publishes. This can be improved without additional upstream requests.
- A bounded two-feed read produced the [sanitized status capability evidence](research/cp-service-stopped/state-evidence.json). Correct operator/agency pairs come from [existing provider configuration](../internal/app/data.go#L20): Carris `IA9T6`, CM's direct multi-agency feed, TCB `A3H3M`, Mobi `HF16N`, Metro `IA2N9`, CP `N18KL`, TTSL `LTP61`, Fertagus `7NTB1`. CM had 110 `STOPPED_AT` rows; Mobi had 23. Carris, Metro, TTSL and Fertagus had `IN_TRANSIT_TO` only in this sample; TCB had null state; CP had no rows. These are upstream capability observations, not geographically filtered application fleet counts or guarantees of coverage.
- [GTFS-RT](https://gtfs.org/documentation/realtime/reference/#enum-vehiclestopstatus) distinguishes arrival approach, standing at a stop, and transit towards a stop. `IN_TRANSIT_TO` does not prove instantaneous motion. Raw GTFS-RT also constrains interpretation by stop sequence; do not assign defaults to absent fields and claim confirmation. Native CM/TML JSON status is retained as a published status, without inventing dropped GTFS context.
- [Projection](../internal/app/continuity_publication.go#L118) separates current from last-known rows. `inactive_at` already means no newer observation after five minutes, not physical inactivity ([OpenAPI](../api/openapi.yaml)). The UI's current “Inativo / sem atualização” wording and inactive marker language can misleadingly suggest a service failure. Last-known positions already last one hour from the original observation; omitted rows immediately leave current metrics.
- [Fleet search](../internal/app/history.go#L309) currently matches raw plate substrings; a displayed hyphenated plate needs an equivalent search path. Fleet/detail UI currently renders raw `license_plate` ([App.tsx](../frontend/src/App.tsx)).
- [IMT](https://www.imt-ip.pt/veiculos/identificacao-veiculos/) documents the four regular pair arrangements. The current physical plate series uses spaced groups; the user's requested hyphens are our UI presentation convention, not a change to the registration identity.

No source observation proves that every omitted position was caused by a stationary vehicle. Our wording must accept normal stops without claiming the reason for a provider's silence.

## Behavior decisions

### Plates

Use one small frontend display helper in `frontend/src/data.ts`, called by the two existing plate surfaces. Only transform recognized regular Portuguese road-vehicle plate values: `AA0000`, `0000AA`, `00AA00`, `AA00AA`, including already separated/spaced forms. Trim and case-normalize for recognition, then display three uppercase pairs separated by hyphens. Do not apply this to vehicle IDs, train numbers or boat/rail registrations; gate road plate formatting using the existing operator mode. Leave nonstandard/foreign/partial values as published and retain the existing unavailable label for null/empty values.

Keep API/database `license_plate` raw. Extend only the plate branch of `fleetMatches` to compare separator-insensitive plate text; leave source ID/model matching unchanged. Support full and partial queries (`CE29PV`, `CE-29-PV`, `CE 29 PV`, `CE-29`, `29-PV`), with mixed case. Empty-after-normalization queries must not match every row. No migration, new plate column, international plate parser or new endpoint.

### Stopped state and freshness

Preserve optional recognized source `current_status` and its stop reference on each observation. The ordinary normalized values are `STOPPED_AT`, `INCOMING_AT`, `IN_TRANSIT_TO`; missing/unknown values remain unknown. Retain an opaque source stop ID separately from a qualified application stop ID only when needed to make an unresolved reference explicit. Resolve an application station name only with an exact provider/plan/static-stop match or a verified crosswalk. Never show the next stop of an `IN_TRANSIT_TO` row as its present location.

| Evidence | Display | Freshness and metrics |
| --- | --- | --- |
| Fresh reported position, `STOPPED_AT` | `Parado` / `Parado em <paragem>` where the stop join is verified | A normal state. Remains a current reported observation under existing eligibility. Speed comes from valid consecutive observations; never force it to zero. |
| Provider publishes approach/transit | `A chegar à paragem` / `Entre paragens` | These describe progress relative to a stop, not measured motion or physical speed. |
| Last confirmed `STOPPED_AT`, then row omitted or old | `Último estado: parado em <paragem> · última observação <hora>` | Preserve the original state/position for up to one hour. After five minutes add `Sem atualização há …`; do not label the vehicle out of service. It is excluded from current metrics as before. |
| No state published; position disappears or repeats with old timestamp | `Posição anterior · estado de paragem não confirmado` | Missing data is not a provider failure by itself, nor proof of a stop. Keep original expiry; no synthetic samples, speed or location. |
| Metro/other estimated position with a published state | `Estado estimado: …`, retaining estimated-position styling | No onboard GPS or physical stop confirmation is claimed; remains excluded from reported speed/distance. |
| CP service `AT_ORIGIN`, with no vehicle coordinates | `Serviço à partida em <estação>`, in service/station details | This is a service status, not a newly located physical vehicle. No reported marker, count or speed is created from station coordinates. |

Replace “Inativo” with “Sem atualização” in vehicle details, retained-position notices, legends and accessibility text. Keep the compatible API field `inactive_at`, with its existing documented no-update meaning. Preserve five-minute warning, one-hour expiry, 90-second source verification, 180-second position eligibility, immediate omission exclusion, caps/replay floors and unknown-versus-zero semantics. A failed source still receives its separate source-error indication. Zero current observations from a successful feed must not be relabelled as an outage.

Map markers gain a small stopped indicator where supported; its status and age are independent. Do not reset marker opacity/expiry because a vehicle stopped, or change operator selection/overlays. Existing last-known markers remain inspectable. Fresh observations that omit a state reset it to unknown rather than inheriting an old stopped state. Same coordinates with a genuinely newer timestamp are valid stationary observations; the same old timestamp does not refresh any clock. Do not infer an actual five-minute stop duration from two observations or a five-minute data gap.

No proximity-only stopped inference in this change. It cannot distinguish a berth/platform stay from stale telemetry, a traffic queue near a bus stop, or an estimated Metro position.

### CP details and source choice

Reuse the prior evidence-backed CP plan, with explicit stages:

1. **Scheduled endpoints using existing GTFS:** capture bounded original first/last stop IDs and names for CP trips before geographic filtering. Existing retained `Times` are clipped to Lisbon and do not contain complete endpoints. Keep the retained network unchanged. Populate only exact current-plan/date trip joins. Retain and validate Hub’s explicit `operational_date`, currently discarded by `hubPosition`, as source service-date evidence; never infer it from a trip-ID suffix, collection day or the current calendar day. Missing/invalid/ambiguous dates cannot join to a scheduled service. Legacy caches show unavailable until the normal static refresh supplies endpoint metadata. Label these as scheduled, not as proof of operation.
2. **Source admission:** obtain application-authorized CP partner/travel endpoint/access details and permitted reuse, verified quotas, provider timestamp semantics, and bounded fixture samples covering Oriente, at-origin trains, running trains and delays. No credential probing, copying site keys or quota guesses. Prefer bulk VehiclePositions plus TripUpdates if access is available; use the travel API only for details those feeds lack. This prerequisite blocks live CP integration, not plate/status/GTFS work.
3. **Public TML ETA evaluation:** verify an explicit stop/trip/plan crosswalk and useful source freshness. Oriente IDs differed (`94-31039`, `94_31039`, `253542`) in inspected sources. No join by display name or punctuation substitution. Predictions are labelled experimental; `eta_at` is a predicted time, not an observation timestamp. If original freshness cannot be verified, display only appropriately qualified consulted predictions if the contract/UI can express that uncertainty, otherwise keep scheduled fallback. Do not promote them to authoritative CP delays or physical stop state.
4. **Minimal direct CP adapter once admitted:** fetch and cache a shared bulk feed, normalize verified positions/TripUpdates, and coalesce bounded station/train-detail reads on demand. Use train number plus operating date only as a service identity; retain separate physical vehicle identity where supplied. Match dates in Europe/Lisbon, exceptions, midnight and DST. CP travel delays are minutes; normalize once to `delay_seconds`. Preserve negative/zero/unknown according to the admitted source semantics. A supplied platform is labelled planned/published unless its live assignment is documented. Null coordinates remain absent.

Use a clear precedence: admitted direct official realtime information for that same verified service instance; otherwise qualified TML predictions; otherwise scheduled GTFS. Do not combine incompatible plans or an old direct delay with a fresh unrelated service. Never run two position streams for the same operator into distinct vehicle identities/counts. Choose one admitted authoritative position source, with explicit source-health fallback; switching/recovery breaks speed continuity unless identity/time/provenance continuity is proved, and must prevent double counting or resurrection of expired observations.

The exact backend source used by Mover Lisboa remains unverified; it is not an integration target. All actual upstreams must be independently official and admitted on their own evidence.

## Contract and implementation sequence

### 1. Plate presentation and search

Own `frontend/src/data.ts`, `frontend/src/App.tsx`, `internal/app/history.go` and focused tests. Keep raw history/identity data unchanged. This can ship independently after validation and independent review.

### 2. Stop status without adding upstream traffic

Update the single `api/openapi.yaml` first with nullable/optional observation state and stop-reference fields. Regenerate with the existing `github.com/oapi-codegen/oapi-codegen/v2` and `oazapfts` scripts; never edit generated clients/server types manually. Keep `/api/v1`, scoped access and public UI behavior.

Update `hubPosition`, the CM decoder, safe ID normalization and observation projection. Carry status with its original observation through current/last-known data, durable live cache, restart and immutable revisions. Keep status/stop fields out of historical `snapshotMetadata` in this change: historical state display is out of scope, and no extra snapshot payload or `recordBytes` reservation is needed. Verify serialized live/static cache byte accounting includes all added fields before writing. Do not recompute status using later static/source data inside a pinned page. No new SQL event table or polling loop. Decode old cache/payload fields as unknown and retain existing no-replay rules; old data is not backfilled with modern status.

Update marker data/styling and detail copy. Treat absent state as unavailable for each provider. Do not add a new historical fleet “current state” aggregation: historical `max(payload field)` is not the latest operational state. Do not add new state fields to historical snapshot payloads, synthetic stationary history or a new chart/count.

### 3. CP scheduled endpoint metadata

Own `gtfs.go`, `gtfs_parser.go`, static-cache records, API Trip/Vehicle enrichment and existing planned-trip/detail UI. Keep a lightweight bounded first/last endpoint record; do not retain every national stop-time merely to obtain two names. Resolve source endpoints before local filtering, preserving a valid complete trip instance. Add no guessed origin from a route's short name or first retained local stop.

Use optional service-detail fields where needed, keeping scheduled endpoint provenance explicit. Inspect generated contract and old-cache defaults before completing this stage.

### 4. CP realtime integration, conditional on admission

Add only a small concrete CP collector/service-details cache, using the current fetch/budget/security patterns. Finalize source-specific contract fields from the admitted fixtures. A compact optional CP service-detail object may hold service identity/date, origin/destination, delay seconds, published platform/status, source URL and separate source-observed/collected times; it must not replace Vehicle observation time or fabricate a fleet identity. No generic enrichment framework.

Expose details on existing vehicle, planned-service and station-arrival surfaces where verified. Trains with no GPS can still have visible station/service information without a new vehicle marker. `Arrival.kind` already distinguishes scheduled/prediction; update the UI's current Metro-only prediction label to use actual source provenance. Keep provider capabilities/unavailable labels precise rather than implying that every operator supports delay, platform or stopped state.

Capture enriched values in immutable read versions; do not enrich pinned pages from a mutable latest service cache. Cache capacity, response bytes, active-service/date range, station/trip demand and concurrency must be bounded using existing limits and the admitted provider quotas. No full per-visitor catalogue fan-out or unbounded per-train polling. Static service catalogues are cached separately from live status. Honour original/source clocks, HTTP validators and 429/503 Retry-After/backoff.

All actual HTTP attempts, including OAuth refresh, redirects and retries, must pass the existing shared **900/rolling-minute** ceiling, below the user's 1,000 limit, and stricter per-provider budgets. Record a concrete worst-case request calculation after endpoint/quotas are known; do not choose a new polling interval until then. A Metro credential does not grant CP access. Server-only credentials remain in the existing secret setup, never in the frontend, logs, fixtures or report.

## Validation and acceptance

| Area | Required checks |
| --- | --- |
| Plates | Four regular layouts; raw/spaced/hyphenated/mixed case; null, invalid, foreign and non-road IDs unchanged. Vehicle detail and fleet agree. Search full/partial with separators matches the same vehicles/pages as compact text; model/ID behavior unchanged. |
| Source state | CM and TML fixtures for stopped, approaching, transit, absent/unknown status; correct agency mapping; verified/missing/wrong-plan stop IDs; failed source separate from empty success. Missing GTFS context must not manufacture confirmed stop identity. |
| Continuity | Fresh stopped record → omitted → five-minute no-update notice → one-hour expiry; current count exclusion unchanged. Repeated old timestamp, restart/unverified cache, regression/recovery and pinned-page clocks remain correct. Resume/new unknown state clears the old stopped indication. |
| Metrics | Genuine newer stationary observations can yield sampled zero from valid pairs; no forced zero from status, no samples during silence, no bridge speed after a gap, no estimated Metro speed, no unchanged-timestamp refresh. |
| CP scheduled metadata | Journey beyond Lisbon retains actual full endpoints while local stop times/network stay bounded. Exact active-plan/date joins only; explicit valid source `operational_date`, including overnight services, is required for an observed-service join. Unknown/legacy/mismatched trip data remains unavailable; fetching after midnight must not reassign yesterday’s service. Resource test includes the extra metadata. |
| CP realtime | Admitted fixtures: at-origin with null coordinates, running positions, minute/second units, unknown/negative/zero delay, missing/stale timestamps, cancellations/skipped stops, platforms, midnight/DST, incompatible IDs/plans and operating dates. Predictions/service statuses never create GPS counts or completed-trip claims. Single-source switch/recovery does not duplicate vehicles or bridge metrics. |
| Budgets and security | Fake transport validates total and per-provider attempts, OAuth/redirect accounting, cooldown/Retry-After, demand coalescing, bounded caches and failure fallback. Existing URL/TLS policy, API scope/rate limits, logs/redaction and public UI remain intact. No new permitted hosts before source admission. |
| Storage and resource | Old static/live cache round trips and serialized byte reservations for all added cache fields; unchanged historical snapshot payload/`recordBytes`, compact snapshot eligibility, Postgres and Cockroach-compatible checks for touched persistence paths; no historical rewrite or extra high-frequency snapshots. Existing 30-day/5-GB application guard and caps remain. Run the existing bounded resource scenario before rollout; do not raise limits to hide a regression. |
| Browser/contract | Generated-code check, Go build/vet/race tests relevant to changes, TypeScript/Vite build; desktop/mobile fixtures for stopped and aged labels/markers, formatted/searchable plates, CP origin/delay/arrivals source labels, unavailable data, filter/layer settings and accepted popup behavior. |

Main fixes all blockers. Independent `quasar-alpha` / `xhigh` reviews cover this plan before implementation and final changed code/fixtures/test/resource results before release; repeat after blockers until acceptance is recommended and main confirms scope/requirements. Optional live source verification is bounded, read-only and uses admitted access. Commit/deploy are separate authorized steps; no production changes occur while planning.

## Planning completion and remaining prerequisites

Plate/status/CP scheduled metadata stages are actionable with existing sources. CP partner/travel integration remains conditional on our authorized access, verified numeric limits, source timestamps and exact identity crosswalk. These are source-admission prerequisites, not permission to substitute Mover APIs, treat website cache TTL as a quota, or add misleading location markers. The preceding evidence does not establish the precise cause of every position omission; it establishes how to display normal stops and data gaps honestly.

## Planning verification

- Main inspected the adapters, source-date fields, continuity/projection, plate surfaces/search, GTFS filtering, OpenAPI generation and cache/snapshot accounting. Bounded official-source reads and primary documentation support the linked evidence; no Mover API, credentials or production changes were used in this session.
- Both existing independent `quasar-alpha` reviewers at `xhigh` recommend planning acceptance with no remaining blockers. Main incorporated the explicit operational-date join requirement and live-cache-only state persistence clarification, then confirmed scope and requirements.
- Evidence JSON parses and `git diff --check` passes. No application tests were run for these planning-only additions. Prior accepted popup implementation/test evidence remains separate; future implementation must pass the acceptance matrix above and its independent final review before release.
