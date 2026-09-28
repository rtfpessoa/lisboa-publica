# Lisboa Pública

A responsive Lisbon public-transport dashboard using independent operator and TML data. The application uses Go, React/TypeScript, Vite, MapLibre, TanStack Query and Recharts. One [OpenAPI contract](api/openapi.yaml) generates the Go server interface and TypeScript client. The deployed Go process serves both API and built frontend assets.

Start with the [application documentation](docs/README.md): [architecture](docs/architecture.md), [data catalogue](docs/data/README.md), [data associations](docs/data/associations.md), [history and metrics](docs/data/history.md), and [consumed integrations](docs/integrations/README.md). Code, documentation and comments use English; the UI supports Portuguese.

## Run locally

Use Go 1.26 or newer, Node 24, and Postgres or CockroachDB. Create a database, then:

```sh
make build
export DATABASE_URL='postgresql://localhost/lisboapublica?sslmode=disable'
export PUBLIC_ORIGIN='http://127.0.0.1:8080'
./bin/server
```

Open http://127.0.0.1:8080. Schema initialization and persisted-cache restoration run at startup. Initial source collection can take tens of seconds; source panels distinguish loading, errors and stale observations. Static schedules and selected live state are cached durably; history begins with collection by this installation. The large Carris archive can require roughly 1–2 GiB of memory; `GOMEMLIMIT=1GiB` can reduce garbage-collection headroom.

For direct Metro data, unlock the 1Password CLI and use the existing secret-reference file:

```sh
op run --env-file=config/metro.op.env -- ./bin/server
```

It contains `op://` references, not secret values. Credentials enter the server environment. [The Metro reference](docs/integrations/metro.md) explains OAuth, station matching and predictions. Run one ingestion instance for the selected provider subscription; per-process budgets are not shared across replicas.

## Develop and validate

```sh
make generate
make check-generated
TEST_DATABASE_URL='postgresql://localhost/lisboapublica_test?sslmode=disable' make test
cd frontend
npm test
```

`make check-generated` regenerates and compares the Go/TypeScript interfaces. Application tests exercise temporary database schemas, pagination, calendars/DST, authentication, source policies, persistence and history. Browser tests require a populated server, normally `127.0.0.1:8080`; `UI_BASE_URL` selects another server. Use a dedicated test database.

For frontend hot reload, run `npm run dev` from `frontend` and configure backend `PUBLIC_ORIGIN` to the browser's Vite origin. `DEV_AUTH=true` enables local test login in development; login requires a loopback peer and matching origin/nonce. Production rejects it.

New features must update affected documentation in the same change. See [project instructions](AGENTS.md) and [documentation maintenance](docs/README.md#evidence-and-maintenance).

## Access and production

Reads are public by default; optional Google sign-in manages personal scoped API keys. Supplied keys are always validated, including expiry, revocation and scopes. Set `PUBLIC_READS=false` to require a session or scoped key for protected reads. [Google Identity](docs/integrations/google-identity.md) explains login and session/key data.

For production, set `ENVIRONMENT=production`, an HTTPS `PUBLIC_ORIGIN`, the intended `LISTEN_ADDR`, and `DATABASE_URL` pointing to the private PostgreSQL service in Compose. The local database is persistent and has no published host port; external database connections require verified TLS. Configure `GOOGLE_CLIENT_ID` and register the matching origin when sign-in is needed. Follow the [deployment guide](deploy/README.md) for reverse-proxy trust, credentials, one-collector topology, retention, aggregate history and storage guards. [Runtime configuration](docs/architecture.md#runtime-configuration) distinguishes defaults from production settings.

## Data interpretation

[The catalogue](docs/data/README.md) distinguishes schedules, predictions, reported positions, estimates and last-known states. Metro map positions are estimated and excluded from sampled speed/distance. Detected trips do not establish completion; fleet views are detected vehicles, not complete inventory. For every operator, positions stay on the map for ten minutes using their original source clocks and show a marker warning immediately when the backend detects missing reporting, with an independent age warning after five minutes. The backend persists latest reporting state separately from movement and labels source failures as unconfirmed. Popups show the last vehicle update and a separate application receipt line when its formatted time or age differs; repeated polling does not renew either clock. Confirmed stable vehicle attributes persist independently of positions and historical retention. Capacity is a published specification, not occupancy. Exact commercial speed, operational headway and depot assignments are unsupported.

[History](docs/data/history.md) is partial observed movement over the configured 1–30-day retention window. Aggregate mode preserves valid sample weights and summed displacement, with explicit gaps and unfinished-bucket loss on restart. Background cartography uses [OpenFreeMap](docs/integrations/openfreemap.md) with attribution; typography uses [Google Fonts](docs/integrations/google-fonts.md). There are no Mover Lisboa API dependencies.

## Supporting evidence

The [source research](docs/research/SOURCES.md), [implementation plan](docs/PLAN.md), [validation](docs/VALIDATION.md), [Maat findings](docs/MAAT.md) and [reference inventory](docs/reference/INVENTORY.md) preserve dated research and delivery evidence. Use the canonical application references above for current behavior.

Station popups expose line/direction selection and independent arrival/departure evidence. The [Metro live map and popups](docs/metro-live-popups.md) share coherent SSE frames, five-second fallback during stream failure, locally decreasing countdowns, identified line/direction trains and pinned inferred journeys with ordered visits. Direct Metro collection defaults to 500 ms; Hub positions have a separate one-second target under the shared attempt budget, with pressure/cooldown backoff. New selectable journeys have durable complete checkpoints, including event-free recovery; ambiguous references retain direction-grouped forecasts. Inferred arrival publication evidence remains separate from actual events; departure times require unavailable frozen movement calibration and remain unknown. Other operators retain [their popup behavior](docs/VEHICLE-POPUPS.md).

## Experimental Metro patterns

The **Padrões** view compares official and own forecasts for waiting and following stations, with hourly proxy counts and sparse-data warnings. These are experimental estimates from the same source, without physical arrival validation. Configure `TRANSPORT_ARCHIVE_DIR` to collect server-only detail/aggregates; Compose mounts its persistent volume with a separate configurable 10 GB budget. Existing database storage limits and upstream request rates remain unchanged. See [Metro patterns](docs/metro-patterns.md) and [deployment](deploy/README.md).

The local completion follow-up adds route-specific conditions, versioned Lisbon holiday grouping, labeled older/general-context component fallback, unchanged-segment compatibility, conservative mixed-bin calibration and durable bounded MAE/P90/availability/band-support reports. Evidence-backed maintenance revises retained inputs atomically while keeping issued values. Staged normalized observation/prediction capture and experimental own-forecast adapters cover all eight existing operators under the same archive budget. Metro uses ETA transitions; later stages require verified published paths and coherent reported stop-state transitions. Forecast availability depends on actual compatible inputs, and physical validation remains unavailable. See [current behavior](docs/metro-patterns.md) and [remaining live evidence](docs/GAPS-metro-patterns.md).

Select an operator in **Padrões**. The operator-scoped `/api/v1/transport/patterns` read exposes both waiting and onward forecasts; the original Metro endpoint remains supported. No additional position/feed polling is introduced.

Metro departure model evidence can be prepared with the [offline calibration workflow](docs/metro-departure-calibration.md).
It separates model-consistency evidence from independent physical references, makes no upstream calls and does not enable live departures.

Metro live popups support durable inferred terminal completion, explicit successor selection and guarded experimental departure revisions. The optional reviewed model allowlist and 500 ms local station-axis rendering are implemented; no qualifying production profile is bundled. See [live behavior](docs/metro-live-popups.md), [model admission](docs/metro-departure-calibration.md) and [completion validation](docs/validation/metro-live-completion-2026-09-28.md).
