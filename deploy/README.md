# Production deployment

Deploy one collector on the existing `server_web` network. The dashboard is public; production development login is disabled. The shared upstream budget allows at most 900 requests per rolling minute, including Metro OAuth and redirects.

Production uses PostgreSQL 17 in the same Compose project, on its internal `private` network, without a published database port. Keep credentials in mode-600 `deploy/.env`, excluded from Git; [`.env.example`](.env.example) lists the required settings. Use a long URL-safe `POSTGRES_PASSWORD`. The Cloud configuration remains an optional recovery path using protected `deploy/.cloud.env` with `sslmode=verify-full`. Set `VERSION` to the deployed Git SHA, `SNAPSHOT_RETENTION_DAYS=30`, `HISTORY_INTERVAL_SECONDS=300`, `STORAGE_GUARD=true`, and `TRUSTED_PROXY_CIDRS` to the existing Caddy network subnet. Metro consumer key/secret are server environment variables.

```sh
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml config --quiet
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml build dashboard
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d --wait database dashboard
```

Before replacing an existing container, inspect its active Compose inputs (`com.docker.compose.project.config_files`) and preserve any runtime overrides that are still required. A previous override may pin a different image even when `VERSION` is set. Append a final image-only override setting `services.dashboard.image` to `lisboa-publica:${VERSION:?set VERSION}` after those inputs, and verify the resolved image before starting the dashboard with `--no-deps`. Compare the effective environment, limits, isolation and artifact hashes against the saved baseline; keep credentials and rollback inspections protected on the host. See the [dated station rollout](../docs/research/station-popup-stability-2026-09-27/VALIDATION.md) for the performed checks and the user's exact-main selection.

The local override restores the healthy-database dependency even when appended after an existing external override. It selects `lisboa-publica_postgres_data` (configurable with `POSTGRES_VOLUME_NAME`) instead of the older `lisboa-publica_database_data` volume. Docker initializes this volume only when empty; changing the environment password does not change credentials in an existing initialized volume. Preserve previous volumes and the Cloud database. Add `Caddyfile.fragment` to the existing Caddyfile, validate and reload the running Caddy container. It overwrites the trusted client-IP header. Other services and domains retain their existing configuration.

History retains 30 days plus one hour of pruning grace, with immutable five-minute aggregates closed after 90 seconds. Live frontend queries use the configured `live_refresh_seconds` interval (five seconds by default); historical queries refresh every 30 seconds. History begins with collection in this database; unfinished buckets can be lost during restart, and storage pauses leave explicit gaps.

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
