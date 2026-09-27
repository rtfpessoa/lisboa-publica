# Application architecture

Lisboa Pública runs as one Go process that serves the generated HTTP API and the built React frontend. Collection, normalization, cache publication and historical persistence are responsibilities within that process. Postgres or CockroachDB stores durable state. There are no independently deployed services per operator.

## Runtime boundaries

```mermaid
flowchart LR
    Browser["Browser: React, TanStack Query, MapLibre"]
    Proxy["Configured HTTPS reverse proxy"]
    subgraph App["Single Go process"]
        HTTP["API and frontend assets"]
        Reads["Read handlers and access controls"]
        Collect["Static, position, shared ETA, CM arrivals and Metro collection"]
        Cache["Revisioned published state"]
        Store["Persistence and history collection"]
    end
    Sources["TML Hub, GTFS archives, CM v2, Metro"]
    DB[("Postgres or CockroachDB")]
    Identity["Google Identity"]
    Assets["OpenFreeMap and Google Fonts"]
    Browser --> Proxy --> HTTP --> Reads
    Browser --> Assets
    Browser --> Identity
    Reads --> Identity
    Collect --> Sources
    Collect --> Cache
    Collect --> Store
    Store --> DB
    Store -->|restore on startup| Cache
    Reads --> Cache
    Reads --> Store
```

The proxy represents the configured production deployment; local development can access Go directly. Vite can serve the frontend during development. Google verification and browser resources are outside the transport-provider HTTP budget. Diagram arrows describe dependencies, not a universal database-and-memory transaction.

## Startup and shutdown

[The entry point](../cmd/server/main.go) requires `DATABASE_URL`, opens the store, initializes the schema, applies [history settings](../cmd/server/config.go), creates a cache and restores persisted state. Configuration, initialization or restoration errors stop startup. The API validates origin/environment configuration and constructs its generated handler before serving requests.

One shared `http.Client` has a 45-second timeout and a `BudgetTransport` capped at 900 provider attempts per rolling minute. General ingestion starts when `INGEST_ENABLED=true` (the default). The direct Metro loop starts when both Metro credentials exist, independently of `INGEST_ENABLED`. CP predictions wait for static CP data; additional TML arrivals require a requested stop with eligible static data. A cold start can show loading/unavailable data until collection succeeds.

SIGINT/SIGTERM cancels the shared context and initiates HTTP shutdown with a ten-second timeout. The entry point does not explicitly join every collector or perform a final flush of unfinished history buckets. Restart can lose transient predictions, unpublished writes and unfinished aggregates.

## Collection and normalization

| Path | Cadence and prerequisites | Result |
|---|---|---|
| Static | Initial collection, then five-minute loop; reusable successful static cache lasts six hours | Active Hub plan discovery, bounded normalized GTFS parsing, CM catalogue/geometry and fleet metadata |
| Positions | Initial collection, then nominal five-second loop | Hub positions for seven operators, direct CM positions, observation admission and continuity |
| Shared ETA | Nominal five-second loop when CP static data or additional-stop demand exists; serialized five-second attempt | CP predictions plus demanded TML stop arrivals from one decode |
| CM arrivals | Separate nominal five-second loop; bounded requested-stop demand | Ephemeral direct arrivals independent of CP latency |
| Direct Metro | Initial collection, then nominal five-second loop with credentials; refresh serialized and at most once per five seconds | OAuth token reuse, line status, waits and station metadata |
| Cleanup/archival | Five-minute ticker in the position loop | Bounded retention cleanup and optional PostgreSQL payload archival |

These are local schedules, not guarantees of exact completion intervals or new source observations. Filters in the UI affect enabled queries and returned entities, not which operators the position/static collectors ingest. Stop arrival reads additionally register bounded30-second demand; they never perform an upstream fetch in the HTTP reader.

[Request policy](../internal/app/upstream.go) caps actual attempts, including OAuth and followed redirects. In addition to the shared 900/minute ceiling, TML uses a local 120/minute cap and CM uses 40/second. These are implementation policies, not assertions of current provider quotas. Network failures and HTTP 429/503 introduce source cooldowns; a valid `Retry-After` is respected. Attempts rejected by a budget wait for a later scheduled refresh. Redirects retain HTTPS hostname and effective port and have a bounded chain.

Parsers validate identity, time, coordinates and bounded payloads. [Source references](integrations/README.md) explain raw inputs; [associations](data/associations.md) explain crosswalks and matching. No Mover Lisboa API is consumed.

## Publication, persistence and recovery

| Data path | Memory publication | Durability |
|---|---|---|
| Static data and metadata | Replace the static state after a successful save | Compressed, chunked static cache and source health |
| Vehicle positions | Advance in-memory state independently of successful writes | Live cache/health and selected history; normally at most once per 30 seconds in aggregate mode, each update in raw mode |
| Requested-stop arrivals | Publish only against the exact static generation used; independent bounded store | None; no ETA rows in64 archived network revisions |
| CP predictions | Publish only if the static state used for normalization is still current | Memory only; a static CP update invalidates predictions |
| Direct Metro | Advance in-memory direct state independently of successful writes | Separate `cache_parts` kind `direct`, with writes limited separately to 30-second cadence |

The store's `PublishMu` coordinates collector publication; cache updates publish new states under a cache lock. Published display revisions do not carry pending history samples, and archived revisions do not retain the full continuity replay ledger.

[Store initialization and restoration](../internal/app/store.go) restore static/live data and source health; direct Metro has its own restored cache. Restored positions are marked unverified and continuity is broken, preserving original clocks instead of inventing a movement pair. CP waits for recollection. Detailed retained facts and history failure semantics are in [History and derivation](data/history.md).

## Read consistency and frontend queries

```mermaid
sequenceDiagram
    participant UI as Browser query
    participant API as Go API
    participant State as Cache or history store
    UI->>API: First page with filters
    API->>State: Select revision
    State-->>API: Rows and revision
    API-->>UI: Page and revision
    loop Remaining pages
        UI->>API: Next page with same revision
        API->>State: Read pinned revision
        State-->>API: Rows
        API-->>UI: Page
    end
    Note over API,State: Live cache revision differs from historical generation and time window
    Note over UI,API: Expiry may require restarting pagination
```

[Live cache revisions](../internal/app/data.go) expire by age (five minutes) and capacity (64 versions); validity for the entire five minutes is not guaranteed. [Vehicle revisions](../internal/app/vehicle_revision.go) also freeze the clock used to project freshness. [Historical revisions](../internal/app/history.go) freeze a SQL generation and time window. CP reads preserve a coherent prediction/schedule view. These mechanisms do not create one transaction covering all dashboard queries.

[The frontend pagination helper](../frontend/src/data.ts) carries the returned revision through subsequent pages and restarts on HTTP 410, up to three attempts. [CP queries](../frontend/src/cp.ts) have their own coherent collection logic. TanStack Query uses the [generated client](../frontend/src/api.ts), same-origin credentials and query keys incorporating selections. Live queries generally refresh at the reported live interval; historical windows advance on a 30-second cadence. CP, Metro and ordinary live API reads consume published cache. Additional stop arrival reads register demand for the asynchronous collectors; popup cancellation does not cancel shared collection.

## API, authentication and trust

[OpenAPI](../api/openapi.yaml) generates the strict Go interface and TypeScript client through the [generation workflow](../scripts/generate.sh). [The server](../internal/app/server.go) applies request validation, identity/scopes, per-IP/principal rate limits and generated dispatch. Heavy reads are bounded to two concurrent operations with 15-second contexts and result limits. The frontend distinguishes `busy` responses and uses bounded retry/backoff.

Reads are public by default. `PUBLIC_READS=false` requires credentials for operations marked public reads. Supplied API keys are still checked for expiry, revocation and scopes on public reads. Key management requires a browser session. [Google sign-in](integrations/google-identity.md) verifies ID tokens and binds login to origin/nonce, then creates an HttpOnly session. Session/key secrets are stored as hashes. `DEV_AUTH` requires a development environment; its login endpoint additionally requires a loopback peer and origin/nonce binding. Production refuses it.

Metro secrets stay in the server environment. `PUBLIC_ORIGIN` establishes the allowed browser origin. [Proxy trust](../internal/app/proxy.go) accepts the client-IP header only from configured peers; [deployment instructions](../deploy/README.md) require Caddy to overwrite it. Exact operational settings belong in that guide.

## Failure and recovery

| Condition | Application behavior |
|---|---|
| Upstream failure/throttling | Mark source error, preserve prior usable data and original observation clocks, retry on scheduled refresh after applicable cooldown |
| Successful empty positions | Known successful source coverage with no new membership; omitted vehicles may remain separately labelled last-known |
| Source not recently verified | Current counts can become unknown; age and last-known projection do not turn old reports into fresh observations |
| Missing Metro credentials | Direct status is unconfigured; estimated Hub positions and planned GTFS remain distinct paths |
| Static/geometry failure | Preserve eligible prior data; geometry availability may differ from schedule availability; fallback is source-specific |
| Database write failure/storage pause | Live memory can continue while durability/history lags or has gaps; collection status explains the interruption |
| Expired revision | HTTP 410; callers restart pagination |
| Restart/shutdown | Restore durable state with original clocks; CP is transient; unfinished history and changes since successful writes may be lost |

## Runtime configuration

These defaults come from [main.go](../cmd/server/main.go), [config.go](../cmd/server/config.go) and [limits](../internal/app/limits.go); production overrides are documented in [deployment](../deploy/README.md).

| Setting | Code default or requirement | Role |
|---|---|---|
| `DATABASE_URL` | Required | Postgres/CockroachDB connection |
| `PUBLIC_ORIGIN` | `http://localhost:8080` | Browser origin and auth binding |
| `ENVIRONMENT` | `development` at the entry point | Production origin/development-login restrictions |
| `LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listener |
| `FRONTEND_DIR` | `frontend/dist` | Built frontend location |
| `INGEST_ENABLED` | `true` | General static/position/CP collection |
| `PUBLIC_READS` | `true` | Anonymous access to marked reads |
| `DEV_AUTH` | `false` | Restricted local development login |
| `GOOGLE_CLIENT_ID` | Unconfigured | Optional Google sign-in |
| `METRO_CLIENT_ID`, `METRO_CLIENT_SECRET` | Unconfigured | Separate direct Metro collection |
| `RATE_LIMIT_PER_MINUTE` | 300 | Incoming per-IP/principal limit |
| `TRUSTED_PROXY_CIDRS` | Empty | Peers trusted to supply the overwritten client-IP header |
| `SNAPSHOT_RETENTION_DAYS` | 30; accepts 1–30 | Public history window |
| `HISTORY_INTERVAL_SECONDS` | 0; accepts 0 or 300 | Raw versus aggregate collection |
| `STORAGE_GUARD` | `false` | Guarded startup/writes; enabled in production |

## Code navigation and evidence

| Responsibility | Entry points and behavior evidence |
|---|---|
| Startup/settings | [main.go](../cmd/server/main.go), [config.go](../cmd/server/config.go) |
| Collection/policy | [ingest.go](../internal/app/ingest.go), [upstream.go](../internal/app/upstream.go), [policy tests](../internal/app/upstream_policy_test.go) |
| Publication/continuity | [data.go](../internal/app/data.go), [live_publication.go](../internal/app/live_publication.go), [continuity tests](../internal/app/continuity_test.go) |
| Persistence/history | [store.go](../internal/app/store.go), [snapshots_store.go](../internal/app/snapshots_store.go), [storage budget](../internal/app/storage_budget.go) |
| CP/Metro | [CP collection](../internal/app/cp_collect.go), [CP read tests](../internal/app/cp_reads_test.go), [metro.go](../internal/app/metro.go) |
| Auth/HTTP | [auth.go](../internal/app/auth.go), [middleware](../internal/app/http_middleware.go), [security tests](../internal/app/security_fixes_test.go) |
| Frontend | [App.tsx](../frontend/src/App.tsx), [main.tsx](../frontend/src/main.tsx), [data.ts](../frontend/src/data.ts) |

## Arrival result leases

[Arrival reads](../internal/app/arrivals_reads.go) default to a one-hour interval. An `a:` revision pins the first page's normalized result, selector and bounds with a random process epoch and bounded retention; expiry, eviction, restart or static-generation replacement returns410. Selector changes return400. Source clocks and validity survive pagination. Pure GTFS pages and existing CP/Metro paths retain their previous revision behavior.

[Retention](../internal/app/arrivals_store.go) is independent of the64 network revisions:32 demand keys per source,3MiB TML/1MiB CM normalized retention,1MiB results and at most64 leases. No neighbour prefetch is added; complete popup schedules now use a separate lazy index tied to each immutable static generation. [Measured resource validation](VALIDATION-stop-arrivals.md) preserves the original full-network workload and limits. These application limits are separate from upstream provider guarantees and container settings.

## Frozen vehicle navigation and selective paths

`GET /api/v1/vehicles/{vehicle_id}/calls` reads existing immutable cache revisions under the same admission, scope and rate limits as other heavy reads. Response-only vehicle references bind operator, observation, source service, operating date and optional published pattern identity; they are not credentials. Pagination freezes the result, and CP predictions expire at the earliest source expiry. A missing or evicted reference fails closed.

CM paths share one validated stop sequence per published pattern and reference existing geometry, rather than retaining an index per trip. Paths and shapes publish/restore/retain atomically in the static cache. Legacy CM caches lacking a recorded path attempt use the normal scheduled refresh once before the reuse TTL; completed empty or rejected attempts retain normal reuse behavior after restart. The selected vehicle popup requests 20 calls per page; optional exact geometry is returned only on page zero. Later-page overlay reactivation requests page zero with the same frozen reference/revision. Closing, switching, or disabling the overlay cancels the browser fetch and clears the highlight. Heartbeats do not reposition the map or resend identical geometry. No provider request is made by the calls endpoint. Ordinary station arrival reads retain their existing bounded asynchronous demand registration.

## Direction boards and complete journey times

[Direction boards and journeys](VEHICLE-POPUPS.md) add cached reads alongside the existing calls/navigation operations. GTFS schedules retain full visits separately from local map visits and build an immutable lazy trip/stop/direction index once per static generation. TML ETA decoding is shared between CP, bounded per-operator publications and requested-stop arrival collection; the native CM collector keeps its existing demand and request budgets. A board `b|` revision combines the frozen network with an existing bounded arrival-result lease. Journey `j:` revisions pin network/observation, read time and committed stop-event generation. Actual events are served only after persistence; no current upstream adapter produces certified occurrences.

The complete timed journey appears only with a safe plan/route/trip/date association. The existing CM published-pattern path and next-stop fallback remain separately available when a timed journey cannot be identified. UI polling does not change the provenance, observation clock or highlighted path.

The popup schedule retains a single visit sequence when the local and complete journeys coincide.
Cache restoration streams individual trips and interns stop identities,
so decoding does not buffer the whole expanded timetable.
The station-to-trip lookup is lazy and bounded to 32 selected stops and 4 MiB per immutable network revision;
a cache miss scans the retained local visits without dropping calls.
