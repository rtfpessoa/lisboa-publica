# Lisboa Pública

Responsive Lisbon transit dashboard inspired by Mover Lisboa, using independent operator and TML data. Go, React/TypeScript, Vite, MapLibre, TanStack Query and Recharts. One [OpenAPI contract](api/openapi.yaml) generates the strict Go server interface and TypeScript client.

The dashboard is public. Optional Google sign-in manages personal scoped API keys. Supplied keys are always validated, including expiry, revocation and scopes; `PUBLIC_READS=false` requires a session or scoped key for reads.

## Run

Install Go 1.26 or newer, Node 24 and Postgres or CockroachDB. Create a database, then:

```sh
make build
export DATABASE_URL='postgresql://localhost/lisboapublica?sslmode=disable'
export PUBLIC_ORIGIN='http://127.0.0.1:8080'
./bin/server
```

Open http://127.0.0.1:8080. Migrations run at startup. The first feed refresh can take tens of seconds; source panels show loading/error/stale status and retain last good data after failures. Static GTFS and schedules are cached in the database; retained observations power history from the start of collection. The large Carris schedule needs roughly 1–2GiB of memory; `GOMEMLIMIT=1GiB` can reduce garbage-collection headroom.

For Metro, install/unlock the 1Password CLI and use the existing consumer key/secret item:

```sh
op run --env-file=config/metro.op.env -- ./bin/server
```

The reference file contains only `op://` paths. Secrets enter the server environment, never the frontend or repository. The server obtains client-credentials tokens on demand and caches them to their actual expiry. This grant does not use the registered browser callback `https://lisboapublica.rtfpessoa.xyz/auth/metro/callback`. Status and arrivals show unavailable when credentials or subscription access are missing.

Use **one ingestion server instance** for the provider subscription. One shared transport caps all provider requests, redirects and Metro token calls at **900 in any rolling 60 seconds**, below the 1,000 quota. Browser traffic uses cached feeds; shared Metro refresh is at most once per 5 seconds. Additional instances would each have a separate budget.

For production, set `ENVIRONMENT=production`, `PUBLIC_ORIGIN=https://lisboapublica.rtfpessoa.xyz`, `LISTEN_ADDR=0.0.0.0:8080`, a TLS-secured `DATABASE_URL`, and configure an HTTPS reverse proxy. Set `GOOGLE_CLIENT_ID` to your Google web client ID and register that exact origin in Google Identity Services. Tokens are verified for signature/audience/issuer/expiry/email and login nonce; sessions use HttpOnly cookies. Do not enable `DEV_AUTH` in production (startup refuses it). `DEV_AUTH=true` enables a nonce-protected local test session only for an explicit loopback development origin.

Other configuration: `RATE_LIMIT_PER_MINUTE` (incoming requests per IP/principal, default300), `FRONTEND_DIR` (defaultfrontend/dist), `INGEST_ENABLED` (defaulttrue), `METRO_CLIENT_ID`, `METRO_CLIENT_SECRET`. No CLI application existed in the initial tracked repository; every application API operation is represented in the UI, including key management and source health.

## Develop and validate

```sh
make generate
make check-generated
TEST_DATABASE_URL='postgresql://localhost/lisboapublica_test?sslmode=disable' make test
cd frontend
npm test                 # running populated server on 127.0.0.1:8080
```

`UI_BASE_URL` selects another server for browser checks. Enable local development login to exercise browser key creation/revocation. Tests use separate temporary database schemas and exercise pagination consistency, calendars/DST, scope enforcement, token caching, restart persistence and request budgets. Both Postgres and CockroachDB are supported and tested. For frontend hot reload, run `npm run dev` and set the backend `PUBLIC_ORIGIN` to the Vite origin so login origin/nonce checks match.

See [plan](docs/PLAN.md), [source-to-feature evidence](docs/research/SOURCES.md), [validation](docs/VALIDATION.md), and [reference inventory](docs/reference/INVENTORY.md).

## Data limits

Metro map positions are estimated and excluded from sampled speed and distance. GTFS departures are planned; direct Metro predictions have separate labels and observation timestamps. Completed trips, exact commercial speed, operational headway, depots and permanent fleet allocations are unavailable. Distance is partial observed movement; detected trips establish presence, not completion. Traffic colors summarize observed transit speeds, not general road congestion. Fleet metadata uses verified agency/vehicle crosswalks and marks missing attributes unavailable. Historical views cover the configured retention window, up to30 days of observations collected by this installation.

External map tiles/styles/fonts are supplied by OpenFreeMap with displayed attribution. Provider availability can vary. There are no Mover Lisboa API dependencies.

Production history uses five-minute aggregates and compact fleet metadata; live feeds refresh every 5 seconds, with durable cache/history writes at most every 30 seconds. Weighted speeds and summed observed distances preserve the underlying valid samples. Finalized buckets appear after a 90-second grace period plus up to30 seconds for the durable flush. Restart may lose the unfinished bucket; late data and storage pauses can leave gaps, which the UI labels.

Deployment policy: `SNAPSHOT_RETENTION_DAYS` defaults to30 and accepts1–30. The API and UI report the configured retention; physical cleanup retains one additional hour for stable pagination. `TRUSTED_PROXY_CIDRS` defaults empty. A configured Caddy proxy must overwrite `X-Lisboa-Client-IP`; only configured proxy peers can supply it. `HISTORY_INTERVAL_SECONDS` selects raw local history (`0`) or compact history (`300`). `STORAGE_GUARD=true` measures database storage and reserves writes: history pauses at 4 GB, operational writes at 4.5 GB. The user selected code-only enforcement because the Cloud cap is unavailable. Only this app may write to the selected database; CockroachDB internal storage and external writers are outside its control. Measurement failures block writes; the API reports collection status and resolution. See [Maat findings](docs/MAAT.md) and [deployment](deploy/README.md).

The initial selection is Metro only; vehicles, stops and both official network overlays start enabled and follow selected operators. Operator cards gray out by the current view and historical window, while remaining selectable for schedules/other views. Historical coverage is available through paginated `/api/v1/operator-coverage` (`read:history` for supplied keys). Unsupported completion/frequency controls explain the missing operational evidence.

Fleet fields use exact published identities, including optional CM `vehicles.txt` in the four existing GTFS downloads. Typology/propulsion are raw published codes/values, not unified classifications. New fields are recorded in future snapshots; old missing fields remain unavailable. Metro estimated entities and Fertagus observations have no verified physical-unit crosswalk, and depot allocations are not published. See [verified fleet fields](docs/research/FLEET-FIELDS.md) and [source limits](docs/research/POLLING-LIMITS.md).

CM requests additionally respect a local40/rolling-second cap below the official50/s; TML uses a conservative local120/rolling-minute policy because its quota is undocumented. HTTP429/503 and network failures introduce scheduled cooldowns; valid Retry-After seconds/dates are honored without shortening provider waits. Heavy historical queries refresh every30 seconds. Five-second collection is not a promise of five-second source observations.
