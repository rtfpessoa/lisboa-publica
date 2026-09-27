# Production deployment

Deploy one collector on the existing `server_web` network. The dashboard is public; production development login is disabled. The shared upstream budget allows at most 900 requests per rolling minute, including Metro OAuth and redirects.

Keep credentials in mode-600 `deploy/.env` and `deploy/.cloud.env`, excluded from Git. The Cloud URL must use `sslmode=verify-full`. Set `VERSION` to the deployed Git SHA, `SNAPSHOT_RETENTION_DAYS=30`, `HISTORY_INTERVAL_SECONDS=300`, `STORAGE_GUARD=true`, and `TRUSTED_PROXY_CIDRS` to the existing Caddy network subnet. Metro consumer key/secret are server environment variables.

```sh
docker compose --env-file deploy/.env --env-file deploy/.cloud.env -f deploy/compose.yaml -f deploy/compose.external.yaml config --quiet
docker compose --env-file deploy/.env --env-file deploy/.cloud.env -f deploy/compose.yaml -f deploy/compose.external.yaml build dashboard
docker compose --env-file deploy/.env --env-file deploy/.cloud.env -f deploy/compose.yaml -f deploy/compose.external.yaml up -d --wait dashboard
```

The override removes the local database dependency. Preserve its volume and a backup, then stop the previous local database after verifying the Cloud cutover. Add `Caddyfile.fragment` to the existing Caddyfile, validate and reload the running Caddy container. It overwrites the trusted client-IP header. Other services and domains retain their existing configuration.

History retains 30 days plus one hour of pruning grace, with immutable five-minute aggregates closed after 90 seconds. Live frontend queries use the configured `live_refresh_seconds` interval (five seconds by default); historical queries refresh every 30 seconds. History begins with collection in this database; unfinished buckets can be lost during restart, and storage pauses leave explicit gaps.

The app measures all tables in its database using table-scoped CockroachDB range statistics (Postgres uses database size). Writes reserve conservative byte estimates between measurements. History stops at 4,000,000,000 bytes, operational writes at 4,500,000,000; measurement failures block writes. Cleanup deletes at most10,000 expired rows every five minutes. Startup budgets schema work before DDL and rejects large unbounded backfills. Reservations remain locked through each write; cache/history share one measurement, and unchanged cache parts are retained. The user selected application-only enforcement: the Cloud cap cannot be configured. These thresholds bound our application writes with 500 MB of final headroom; they cannot guarantee a hard provider-wide disk limit covering internal storage or another client. Run one collector and use a dedicated database. No Cloud spending caps are changed.

The image uses a 1280 MiB memory limit, bounded Go memory and serialized frontend/backend compilation. Logs rotate at 10 MB × 3 files. Metro currently omits its TLS intermediate; the public Sectigo OV R36 intermediate is verified against the standard CA bundle during the image build. TLS chain and hostname verification stay enabled. Certificate provenance: http://crt.sectigo.com/SectigoPublicServerAuthenticationCAOVR36.crt; DER SHA-256 `6542d176bed50f193c0ce297ae44ecd8a0a86bec2ede682769344059b4e78530`.

See [validation](../docs/VALIDATION.md) for measured capacity, tests, independent review and deployment results, and [Maat findings](../docs/MAAT.md) for remaining advisories.

Complete journey times require the upgraded static cache structure. Old caches become eligible for the normal collector refresh; no manual cache clearing or retrospective event generation is required. The `stop_events` schema is created through the existing guarded initialization. No current actual-event adapter is enabled; position collection continues independently. See [popup behavior](../docs/VEHICLE-POPUPS.md).
