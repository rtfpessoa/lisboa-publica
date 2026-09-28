# Production deployment

The experimental Metro patterns collector consumes the existing direct refresh;
it adds no upstream requests. Known support losses between archived samples are
checked at the existing refresh rate without creating faster training samples. `TRANSPORT_ARCHIVE_DIR=/app/transport-history` uses
the persistent `transport_history` volume owned by UID 10001. Keep one collector.
The archive has its own server-only 10,000,000,000-byte budget, separate from the
existing database guard. It counts allocated file/directory bytes and reserves
space for replacements and metadata. Closed blocks expire or are removed globally
by data age; target retention can therefore shrink. Reads close their files while
holding the same lock as FIFO. A file lock prevents two archive owners.

Defaults: `TRANSPORT_SAMPLE_SECONDS=30`, `TRANSPORT_BIN_SECONDS=30`,
`TRANSPORT_CHECKPOINT_SECONDS=60`, `TRANSPORT_EVALUATION_SECONDS=60`,
`TRANSPORT_DETAIL_DAYS=7`, `TRANSPORT_AGGREGATE_MONTHS=12`,
`TRANSPORT_TRAINING_DAYS=30`, `TRANSPORT_CALIBRATION_DAYS=30`.
All are configurable; sampling never increases the existing provider request rate.
Sampling intervals above 60 seconds intentionally prevent continuous Metro proxy
associations under the initial 60-second gap rule, while still collecting detail.
Unset `TRANSPORT_ARCHIVE_DIR` to disable this collector outside Compose.

Detail is JSON lines compressed with Zstd, retaining the original wait response,
issued forecasts and later evaluation outcomes. Sparse daily hourly aggregates
use Parquet+Zstd. Checksummed immutable generations and an atomic fsynced manifest
are stored on this volume, avoiding additional database writes. A checkpoint is
published last; a verified retained hour can replay a crash between publications.
A restart always reinitializes live association support, showing a gap rather than
assuming continuous presence during downtime. Unknown/corrupt data stay unavailable.
Hourly detail and checkpoint buffers, row counts, query duration and output are bounded;
if a buffer or budget is exhausted, historical collection pauses and official live
data remain independent. The source collection and compression costs still need
weekday/weekend measurements after deployment.

`GET /api/v1/metro/patterns?stop_id=metro:...` returns collection health,
hourly proxy counts and both forecast sources. Optional `episode` includes remaining
calls for one currently supported association. Public-read policy applies; API keys
need both `read:transit` and `read:history`. The UI labels the proxy model experimental,
uses samples immediately and preserves unknown dwell/speed/probability as null.
Arrival intervals target 80% nominal coverage against bounded proxy references;
they are not physical coverage guarantees. No automatic recent adjustment or
speed/residual method is enabled without admissible inputs and further evaluation.

Deploy one collector on the existing `server_web` network. The dashboard is public; production development login is disabled. The shared upstream budget allows at most 900 requests per rolling minute, including Metro OAuth and redirects.

Production uses PostgreSQL 17 in the same Compose project, on its internal `private` network, without a published database port. Keep credentials in mode-600 `deploy/.env`, excluded from Git; [`.env.example`](.env.example) lists the required settings. Use a long URL-safe `POSTGRES_PASSWORD`. The Cloud configuration remains an optional recovery path using protected `deploy/.cloud.env` with `sslmode=verify-full`. Set `VERSION` to the deployed Git SHA, `SNAPSHOT_RETENTION_DAYS=30`, `HISTORY_INTERVAL_SECONDS=300`, `STORAGE_GUARD=true`, and `TRUSTED_PROXY_CIDRS` to the existing Caddy network subnet. Metro consumer key/secret are server environment variables.

```sh
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml config --quiet
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml build dashboard
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d --wait database dashboard
```

Before replacing an existing container, inspect its active Compose inputs (`com.docker.compose.project.config_files`) and preserve any runtime overrides that are still required. A previous override may pin a different image even when `VERSION` is set. Append a final image-only override setting `services.dashboard.image` to `lisboa-publica:${VERSION:?set VERSION}` after those inputs, and verify the resolved image before starting the dashboard with `--no-deps`. Compare the effective environment, limits, isolation and artifact hashes against the saved baseline; keep credentials and rollback inspections protected on the host. See the [dated station rollout](../docs/research/station-popup-stability-2026-09-27/VALIDATION.md) for the performed checks and the user's exact-main selection.

The local override restores the healthy-database dependency even when appended after an existing external override. It selects `lisboa-publica_postgres_data` (configurable with `POSTGRES_VOLUME_NAME`) instead of the older `lisboa-publica_database_data` volume. Docker initializes this volume only when empty; changing the environment password does not change credentials in an existing initialized volume. Preserve previous volumes and the Cloud database. Add `Caddyfile.fragment` to the existing Caddyfile, validate and reload the running Caddy container. It overwrites the trusted client-IP header. Other services and domains retain their existing configuration.

History retains 30 days plus one hour of pruning grace, with immutable five-minute aggregates closed after 90 seconds. Non-Metro live frontend queries use the configured `live_refresh_seconds` interval (five seconds by default); Metro uses scoped SSE with a five-second fallback while unavailable. Historical queries refresh every 30 seconds. History begins with collection in this database; unfinished buckets can be lost during restart, and storage pauses leave explicit gaps.

Vehicle position display uses a fixed ten-minute lifetime from the original source clock for all operators, with an immediate marker warning for backend `not_reporting` state and an independent age warning at five minutes. This is independent of historical retention and the live request interval. The frontend ages cached positions every second even when requests fail; restoring positions or polling the same report cannot renew the deadline.

The app measures all tables in its database using table-scoped CockroachDB range statistics (Postgres uses database size). Writes reserve conservative byte estimates between measurements. History stops at 4,000,000,000 bytes, operational writes at 4,500,000,000; measurement failures block writes. Cleanup deletes at most10,000 expired rows every five minutes. Startup budgets schema work before DDL and rejects large unbounded backfills. Reservations remain locked through each write; cache/history share one measurement, and unchanged cache parts are retained. These application thresholds remain enabled locally. They do not impose a filesystem quota on PostgreSQL WAL, logs, temporary files or the host. Monitor free disk space and keep protected backups on the server. Run one collector and use a dedicated database. Local PostgreSQL has no Cloud monthly RU quota; no Cloud spending caps are changed.

The image uses a 1280 MiB memory limit, bounded Go memory and serialized frontend/backend compilation. Logs rotate at 10 MB × 3 files. Metro currently omits its TLS intermediate; the public Sectigo OV R36 intermediate is verified against the standard CA bundle during the image build. TLS chain and hostname verification stay enabled. Certificate provenance: http://crt.sectigo.com/SectigoPublicServerAuthenticationCAOVR36.crt; DER SHA-256 `6542d176bed50f193c0ce297ae44ecd8a0a86bec2ede682769344059b4e78530`.

See [validation](../docs/VALIDATION.md) for measured capacity, tests, independent review and deployment results, and [Maat findings](../docs/MAAT.md) for remaining advisories.

Complete journey times require the upgraded static cache structure. Old caches become eligible for the normal collector refresh; no manual cache clearing or retrospective event generation is required. The `stop_events` schema is created through the existing guarded initialization. No current actual-event adapter is enabled; position collection continues independently. See [popup behavior](../docs/VEHICLE-POPUPS.md).

Latest vehicle reporting state is initialized in the guarded `vehicle_reporting` table alongside existing schema setup; no manual backfill or new environment setting is required. Reporting transitions share the operational write budget. A one-second worker runs even with ingestion disabled; database-only rows reconcile in bounded 128-row batches every 30 seconds. Pending/failed writes expose `persisted=false`; rolling back the executable can leave the additive latest-state table in place. This table has no automatic history retention cleanup or transition archive. See [runtime/recovery](../docs/architecture.md#durable-reporting-state).

## Switch from Cloud to a clean local database

Inspect the running dashboard's Compose labels and save its protected inspection,
active Compose files and environment files before changing configuration.
Preserve the dashboard image already on `main`, provider settings, networks,
resource limits and transport archive mount.
Append `compose.local.yaml` after **all** active overrides so that a previous
Cloud URL and database profile cannot win. An existing image-only override may
remain in place. Validate the resolved connection target, healthy dependency,
volume, absence of published database ports and exact dashboard image.

For a clean start, select a new, nonexistent `POSTGRES_VOLUME_NAME`; do not delete
or reuse a previous volume. Start `database` with `up -d --wait --no-deps database`,
verify a password-authenticated TCP query and an empty application schema, then
recreate only `dashboard` with `up -d --wait --no-deps --no-build dashboard`.
The existing executable initializes its PostgreSQL schema and collects feeds;
no Cloud data is imported. Sessions, API keys and database history from Cloud
are absent; users must sign in again and recreate keys. Static and live data
return as providers refresh. Original clocks still govern freshness.
The separate transport-history filesystem is preserved independently.

Verify health, public reads, database-backed history, continuing writes and the
same frontend/server image. Save a logical PostgreSQL backup after schema setup.
Keep backup files mode 600 inside a mode-700 server directory; database dumps
contain credential hashes and application data. Never run `down -v` during a
cutover or rollback. To revert, use the saved Compose inputs and image; Cloud
must first accept connections again. Data collected locally is not automatically
merged back into Cloud. Use the base plus local override for subsequent releases,
or append the local override last when preserving earlier runtime overrides.

The 2026-09-28 user instruction requests a clean PostgreSQL database following
Cloud monthly RU exhaustion. This is a deliberate history/authentication reset,
not a completed migration of Cloud data. See [cutover validation](../docs/research/local-postgres-2026-09-28/VALIDATION.md).

## Release policy and earlier experimental evidence

Production deployments must use a reviewed commit already on `main`.
Build the server and frontend from that same clean commit and set `VERSION` to
its Git SHA. Do not deploy worktree files, uncommitted patches or feature branch
artifacts. The Metro patterns release must follow this same clean-main workflow.
The 2026-09-28 instruction authorizes commit, push to main and deployment after
implementation checks and the normal Maat gate; it does not authorize uncommitted artifacts.

Earlier on 2026-09-27, experimental artifact
`lisboa-publica:metro-patterns-4940d7b91b51` was built from Git base
`cd78d80065b2fc63c9e3d4f4c0c89cbecfb11cd8` plus an uncommitted feature patch.
Its fingerprint was
`4940d7b91b513fa555f6a8ea6cf9d110f9e87100f8e172f095e86c9cfc2dc040`.
These artifacts and their verification are historical evidence, not approved
release inputs under the current policy. The earlier source patches, metadata
and binaries remain in `/root/lisboa-publica-releases/` for traceability.
See [dated delivery validation](../docs/VALIDATION-metro-patterns.md).

Preserve the named `lisboa-publica_transport_history` volume across a subsequent
compatible deployment. Disabling the collector or changing the executable does
not require deleting its archive. Follow the normal `main` deployment workflow
above rather than applying the earlier experimental overlays.

## Local completion and maintenance configuration

`TRANSPORT_ARCHIVE_OPERATORS=metro` remains the first-stage default. Add one stage at a time using the exact prefix `metro,cm,carris,cp,fertagus,ttsl,tcb,mobi`. Capture reuses existing fetches and shares the archive’s allocated-byte/FIFO budget; no new polling endpoint is introduced. All eight stages have experimental adapters. Later stages require coherent reported stop transitions and exact published paths; their own points remain unavailable until those inputs and historical components are supported. Both official and own forecasts use the operator-scoped patterns API/UI. Disabling stages keeps existing retained evidence subject to normal expiry/FIFO.

For evidence-backed revisions, build/run `cmd/patterns-maintenance` locally against a private archive fixture. The Dockerfile also includes `/app/patterns-maintenance` for a future authorized maintenance window; no image was packaged or deployed in this follow-up. It requires exclusive ownership; attempting access while the service holds the lock fails. `-operator` defaults to `metro`; other stages accept a normalized observation in `row`. The private correction JSON contains `received_at`, `expected_hash`, `row` and `evidence`; the hash refers to the currently corrected normalized row using its canonical Go JSON encoding. Match all actual service settings with `-sample`, `-bin`, `-limit`, `-detail-days`, `-aggregate-months`, `-training-days`, `-calibration-days`, `-checkpoint` and `-evaluation`. Mismatched retention settings can retire data on open. The command reads no provider endpoint and exposes only scalar revision results. Expired/stale/ambiguous inputs and oversized revisions fail explicitly. See [maintenance semantics](../docs/metro-patterns.md#evidence-backed-maintenance).

The earlier local-only follow-up remains dated evidence in the validation log. The subsequent 2026-09-28 instruction authorizes commit, push to main and deployment after the normal gates. Operational collection must preserve existing provider request limits and explicitly separate inferred signals from physical validation.

## Metro live popup configuration

`METRO_REFRESH_MILLISECONDS` defaults to 500 and accepts 500–60000. It affects only serialized direct Metro
collection; existing live JSON intervals and other providers are unchanged. Keep one subscribed collector and
shared attempt budget; multiple independently budgeted instances or external consumers can exceed an account quota.
The inferred-event lane uses the existing archive volume and global allocated-byte budget, with a seven-day target
subject to TTL/FIFO. Do not lower `TRANSPORT_SAMPLE_SECONDS` to enable per-update event processing.

[Metro streaming](../docs/metro-live-popups.md) requires unbuffered event-stream flushes through Go/Caddy. Stream
writes use separate bounded deadlines; ordinary API timeout/rate/auth policy remains in effect. Initial stream limits
are 256 KiB/frame, 64/process, 16/IP and 4/principal, configured through `Options.MetroStreamLimits`. Native
EventSource uses same-origin cookies/public reads; API-key clients need header-authenticated streaming fetch.
Calibrated departure input is unavailable and its passenger times remain unknown. Changing sampling/storage or
stream limits does not supply calibration or prove the P95 delivery objective. No deployment follows from local tests.
