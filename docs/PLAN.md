# Lisbon transit dashboard implementation plan

Status: complete; independently accepted by quasar-alpha at xhigh, with final requirements confirmed by the main agent.

Build the requested map dashboard and its live, history, traffic and detected-fleet views using independently sourced operator data.
Keep eight reference providers visible; capability and freshness determine which data is shown.
Do not issue application requests to Mover Lisboa.

## Scope and boundaries

Recreate the responsive map, operator controls, route/stop search, four tabs, metric strip, trend charts, route/stop details, detected vehicles and journeys tables.
The traffic view reports observed transit speeds and explicitly does not imply comprehensive road traffic coverage.
The fleet view is a detected fleet, with unavailable model/registration fields labeled.
Historical charts begin when this installation collects snapshots; no invented historical series.
No journey planner, payment flow, prediction pipeline, general plugin system, or administrative dashboard.

## Architecture

React + Vite + strict TypeScript, MapLibre GL with attributed OpenFreeMap tiles, Recharts, TanStack Query, plain responsive CSS.
Go net/http, generated strict oapi-codegen v2 interfaces, Uber Zap logging, pgx SQL.
One OpenAPI 3.0 document in api/openapi.yaml defines all /api/v1 operations, schemas, pagination, errors, and authentication; oazapfts generates frontend/src/api.ts.
Pinned generators and a regeneration check prevent contract drift.
Retry CockroachDB SQLSTATE40001 transaction failures with bounded context-aware backoff; bound both compressed downloads (64MiB) and total expanded GTFS size (512MiB, verified Carris normalized archive is383MiB with4.65M stop-time rows; bounded5M rows/file).
Use CockroachDB/Postgres-compatible JSONB entity cache and append-only vehicle snapshots, source health, sessions, and hashed API key tables.
The Go server serves the built frontend and API from one origin; Vite proxies /api in development.

## Verified sources and polling

Use the official TML GO hub JSON vehicle positions, shared across Carris, TCB, MobiCascais, Metro, Fertagus and TTSL, with provider-specific agency IDs from its plans catalog.
CP positions appeared in a later probe; the official parser copies upstream coordinates, so support reported CP positions while treating availability as dynamic.
Select current, active normalized GTFS plans from the hub catalog, retaining plan dates and matching IDs to observations; discover fresh URLs rather than hardcoding signed object-storage URLs.
Use Carris Metropolitana v2 lines, stops and vehicles endpoints independently; geometry will only be supplied where verified.
Direct Carris GTFS/GTFS-RT and Metro/CP/Fertagus GTFS are documented alternatives; a fallback with incompatible IDs must not silently join observations to routes.
The direct TTSL feed currently has an expired TLS certificate: keep TLS verification enabled and use the verified TML source.
Poll shared hub and CM vehicles every 5 seconds (durable publication every30 seconds), successful static feeds every 6 hours, retry failed static feeds every5 minutes, with backoff on errors and conditional fetches where supported.
Feed age, collection time, source status, service validity and missing data are separate UI states.
Metro hub positions are confirmed inferred from waiting-time predictions and GTFS shape, not GPS; label them estimated and exclude them from speed/distance estimates.
Each vehicle carries its source URL, original observation time and position_kind (reported/estimated); scheduled journeys and arrivals carry kind=scheduled.
Hub realtime routes have an [AGENCY] prefix, while normalized GTFS retains raw route IDs; remove only verified provider/plan prefixes before joining and retain provider identity separately.
API key operation scopes are documented as x-required-scopes because OpenAPI apiKey security requirements cannot carry OAuth scopes; the spec router drives enforced middleware requirements.
Use kin-openapi request validation in addition to generated strict interfaces; strict generation alone does not enforce authorization or parameter constraints.

## Data flow

Provider-specific fetch functions with request deadlines, response size limits, bounded polling, retries on the next cycle and last-known-good caching.
Persist normalized provider data and raw observation timestamps; restart loads the retained cache.
An outage does not erase previous good data or renew its freshness.
Deduplicate snapshots and compute metrics from valid observations only; reject impossible coordinates/speeds, out-of-order or future observations and long sampling gaps.
Snapshot identity is (operator, vehicle_id, provider observation timestamp); duplicate polling never adds distance or samples.
An atomic refresh transaction writes entity cache, source health and new snapshots together; malformed/partial payloads fail without clearing good data.
Expose 30 days of history and physically retain30 days plus one hour of pruning grace, with explicit bounded query windows and pagination; history times use Europe/Lisbon service dates including DST and GTFS times beyond 24:00.
Static GTFS contributes stops, routes, shapes and scheduled trips only where a verified feed is accessible.
Unavailable providers remain visible with an explanation and official source links.

## API and authentication

User update: dashboard is open without sign-in for now. Health/config and all transit/history reads are public and rate-limited. Supplied API keys are still authenticated and their operation scopes enforced; key management remains session-only. PUBLIC_READS=false can restrict reads for deployments that need it.
Google Identity Services obtains an ID token; server verifies signature, audience, issuer and expiry using Google's supported Go verification library before creating an HttpOnly same-origin session.
UI offers optional Google sign-in for API-key management; a clearly labeled development session is enabled only by an explicit nonproduction switch for local testing.
Session endpoints include current user and logout.
GET config returns a short-lived random login nonce and sets a matching HttpOnly SameSite=Strict login-binding cookie.
Pass this nonce to GIS; the server requires a signed nonce claim matching the cookie, and an exact configured Origin, on Google session creation.
The explicit local development session requires the same cookie/body binding and origin, loopback remote address, and ENVIRONMENT=development with DEV_AUTH=true; startup rejects that combination in production.
Missing/mismatched binding and cross-origin login fail; the login cookie is cleared on successful use.
API key creation/list/revocation is session-only; generated secrets are shown once, only hashes are stored, read:transit and read:history are enforced per operation.
Apply request limits per authenticated principal plus bounded unauthenticated IP limits; 429 carries Retry-After.
Verify the Google email_verified claim, bind the token audience to configured Google client ID, and avoid token/secret logging.
Session and key authentication use hashed opaque tokens, persisted expiry and revocation, and explicit cookie/path/samesite settings.
Check Origin on login and cookie-authenticated mutations, secure production cookies, expiry/revocation, and prevent key privilege escalation.
Entity IDs are operator-qualified (operator:raw_id); joins also retain static plan provenance and exact prefix normalization.
All lists use bounded limit/offset pagination with total and has_more metadata; filters apply before pagination.
Live entity lists return an immutable collection revision: subsequent pages supply it, deterministic ID ordering is retained, and unavailable/expired revisions return410.
Retain revision references for5 minutes (bounded to24 versions); frontend retries the whole collection on410, never mixes revisions.
Historical/fleet/traffic/ranking pages also bind to a committed ingestion generation read from app_state, returned as a snapshot revision.
Every refresh transaction locks and increments that generation and writes it onto snapshots; all pages exclude later generations, including delayed observations with earlier event timestamps.
History read range is30 days while physical retention is30 days plus one hour, preventing boundary cleanup from shifting an in-progress page sequence; expired query windows fail clearly.
All offsets use deterministic secondary ID ordering.
API features have matching UI controls, including API key management, arrivals, source status, fleet and history.
No existing CLI was found; the only command will be the Go server, configured by environment.

## Main-agent execution order

1. Save cited source-to-feature evidence and reference screenshots; resolve the research tickets.
2. Finalize this plan with verified endpoints, supported features and explicit limitations; request quasar-alpha/xhigh independent plan review and fix blockers.
3. Write contract, generators, storage migration and strict Go implementation.
4. Implement provider ingest, authentication, rate limits, query filtering/pagination, history and metric calculations.
5. Build the responsive reference-inspired dashboard using the generated client throughout.
6. Run regeneration checks, Go race tests, TypeScript/build checks, database integration tests against Postgres and CockroachDB, and browser tests on desktop and mobile.
7. Give a separate quasar-alpha/xhigh final reviewer the evidence, change inventory and results; fix blockers and repeat until acceptance recommended.
8. Main agent confirms requirements, documents run instructions and remaining external configuration, and reports completion candidly.

## Metric definitions

All metrics honor the selected operators and exact qualified route; timestamps are provider observation times.
Fresh active vehicles count unique reported positions no older than180 seconds; estimated-position count is separate and never added to GPS/reported totals.
Sampled speed uses haversine distance between increasing consecutive reported coordinates divided by elapsed seconds, only for intervals5..180 seconds and implied speeds<=130km/h.
Do not describe this as independently measured commercial speed: exact commercial speed, completed-trip count and operational headway remain unavailable.
Observed distance sums accepted consecutive distances in the selected window, explicitly partial; no sample means null.
Detected trips count distinct nonempty reported trip IDs; it does not establish completion.
Trend samples are5-minute buckets: distinct reported/estimated vehicle counts separately, arithmetic mean of accepted sampled speeds, and summed observed distance.
Fleet first/last seen and distance/trips come from retained snapshots; estimated rows are marked and excluded from measured travel metrics.
Traffic is a spatial aggregation of accepted sampled speeds over the selected hour/day/30-day window, optional Lisbon-local hour and weekday filters; bins are display thresholds, not upstream speed reinterpretation.
Empty and stale states never convert unavailable data to zero; a successful empty feed supports zero current reported vehicles only for that source.

## Reference feature acceptance inventory

| Feature | Behavior checked |
|---|---|
| Full-height map | Provider-colored vehicle markers, stop layer, route geometry when available, attribution and selected-entity details |
| Operators | All eight controls, multi-select and selected-provider source status |
| Search | Route/stop search with debouncing and selection synchronized to map and metrics |
| Live | Fresh/stale/estimated labels; active count and honest nullable speed/distance/trip/headway metrics |
| History | Date selection, retained-snapshot range, speed and fleet-volume charts, route ranking table/bar distributions for sampled speed/detected trips/partial distance, unavailable completion/headway tabs; empty-before-collection state |
| Traffic | Spatial view of valid derived transit speed estimates; no Metro inferred speeds or citywide traffic claim |
| Fleet | Overview/vehicles/models/depots/types tabs; metadata distributions only where published, depots/allocation unavailable; detected vehicles table, sorting/filtering/pagination, first/last seen, observed distance and detected trip IDs; unknown plate/model labels |
| Route/stop details | Route-specific vehicles and scheduled trips, stop-specific scheduled arrivals with kind labels |
| Charts | Expandable speed and detected-volume charts linked to current provider/route/date filters |
| Authentication and API keys | Google sign-in/current-user/logout, scoped key creation, one-time secret display, paginated list and revocation |
| Responsive layout | Sidebar/drawer and panels tested at 1440x900 and 390x844, keyboard navigation and accessible controls |

## Acceptance

- Recorded [reference inventory](reference/INVENTORY.md) and desktop/mobile screenshots guide visual checks.
- Reference features appear and work at desktop/mobile widths; map/operator/search/route selection and tab state are connected to actual API data.
- Eight provider coverage/access/freshness comparisons carry primary citations and endpoint probes.
- No production runtime dependency on moverlisboa.com endpoints.
- Go v2 strict interface and oazapfts client regenerate deterministically from one spec.
- API denies wrong-scope/expired/revoked supplied keys and missing credentials when PUBLIC_READS=false, rejects invalid Google tokens, enforces rate limits and validates pagination.
- Cache survives restart; failed refresh retains old timestamps and marks stale/error; snapshots power honest historical views.
- Both Postgres and CockroachDB pass migration/query/session/key/snapshot integration checks.
- Every API operation is used by the UI or, for health, exercised as the service readiness check.
- Unavailable metrics/arrivals/provider feeds/fleet attributes are clearly labeled.
- Required independent plan and final review recommend acceptance; main agent verifies all requirements.

## User updates during implementation

Dashboard reads are open without authentication for now (2026-09-26). Google sessions remain optional for personal scoped-key management.
Metro credentials are consumer key/secret in 1Password item metro-lisboa-api-consumer-key-secret; generate client-credentials tokens server-side on demand, cache to verified expiry, and never disable TLS or expose secrets.
Configured Metro OAuth callback is https://lisboapublica.rtfpessoa.xyz/auth/metro/callback; client-credentials use does not require browser callback.
Initial access returned403/resource forbidden; subscribed endpoints subsequently verified200, with actual response schemas recorded in research/SOURCES.md.

User quota: shared outgoing transport caps all upstream requests at900 in any rolling60-second window per server instance, including Metro token calls; run one ingestion instance for the shared provider quota. Metro result cache5 seconds and token expiry reuse prevent browser visits multiplying provider calls.

## Final acceptance

2026-09-26: implemented and validated by main. Separate quasar-alpha/xhigh final review recommends acceptance after blockers and follow-up issues were fixed. Main confirms requirements against docs/VALIDATION.md and the actual public UI. All four final browser tests and both database race suites pass. Metro subscription credentials were injected via1Password and actual status/predictions verified. Shared upstream rolling quota is900/60s with one ingestion instance.

## External storage amendment — selected deployment

The latest selected deployment uses the `cockroach-lisboapublica` external cluster, **30 days of history**, and a **5 GB ceiling (5,000,000,000 bytes)**. This replaces raw-history/31-day physical-retention assumptions above for production. Default local compatibility remains raw observations and configurable1–30-day retention. Production history uses five-minute records; the live map refreshes every5 seconds, with durable writes every30 seconds.

Store only historical metadata actually read by fleet queries (source ID, model, plate and published typology/propulsion codes); coordinates, identities and metrics already have typed columns. Within each five-minute observation-time bucket, preserve speed sample sum/count, summed valid raw-pair distance, and first/last observation times. Separate vehicle/route/trip/position-kind keys retain observed route/trip presence. Locate traffic aggregates at their representative final observation and disclose this approximation.

Close buckets90 seconds after their end to tolerate documented provider freshness. Reject observations for already closed buckets; finalized rows are never updated. Bound pending state to20,000 aggregates, keep at most the open bucket and lateness window, and discard closed pending history when collection is paused. Stage one collector proposal before a database transaction; publish it only after a successful commit, including retries. Restart can lose at most7 minutes of pending history; all historical views are explicitly partial observations. Existing generation/window revisions exclude later inserts, and the one-hour physical pruning grace preserves valid pagination windows.

Measure all application database ranges in CockroachDB (or the database size in Postgres). Historical writes stop at4GB; operational writes stop at4.5GB with conservative reservations for every persistent write path, transaction byte bounds and fail-closed measurement behavior. Live-cache writes have their own reserved headroom; history is clearly paused/unavailable in API/UI when the budget prevents collection. Never accumulate a paused-history backlog or automatically raise a Cloud limit. The user explicitly selected code-only enforcement because a Cloud cap cannot be set. The application budgets its own writes, with final500MB headroom; provider internal growth and another writer remain outside its control. Do not claim a provider-enforced hard cap.

Before acceptance, measure compact rows/indexes, cache and route/trip splitting against representative ingestion and project30-day usage. A budget guard alone does not prove full30-day coverage. Verify weighted metrics, duplicates, late data, failed/retried transaction semantics, bounded paused state, stable revisions, measurement failure and threshold crossing. Run Go/Postgres/Cockroach checks, generated contract/TS/browser checks, and independent quasar-alpha/xhigh final review. Main owns all implementation, tests, fixes, deployment and acceptance.

The independent storage review's code blockers are fixed: guarded initialization precedes DDL; missing migrations/backfills are sized or rejected; bounded pruning runs every five minutes; persistent writes serialize through completion; cache/history reservations share one measurement; unchanged cache parts remain untouched. Main's tests verify migration rejection, retained startup reservation, cache shrink/overwrite and the measurement-boundary case. Deployment evidence is recorded in VALIDATION.md.
