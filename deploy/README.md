# Production deployment

The experimental Metro patterns collector consumes the existing direct refresh;
it adds no upstream requests. Known support losses between archived samples are
checked at the existing refresh rate without creating faster training samples. `TRANSPORT_ARCHIVE_DIR=/app/transport-history` uses
the persistent `transport_history` volume owned by UID 10001. Keep one collector.
The archive has its own server-only 10,000,000,000-byte budget, separate from the
existing database guard. It counts allocated file/directory bytes and reserves
space for replacements and metadata. An independent worker performs bounded cleanup
even when no receipts arrive. Without pressure it applies configured expiry; under
pressure it retires the oldest eligible data across kinds while protecting seven days.
An explicitly shorter normal retention still applies when not under pressure.
Unverifiable clocks and correction ledgers are retained, so admission can pause
before the byte limit. Reads close their files while holding the same lock as FIFO.
A file lock prevents two archive owners.

Defaults: `TRANSPORT_SAMPLE_SECONDS=30`, `TRANSPORT_BIN_SECONDS=30`,
`TRANSPORT_CHECKPOINT_SECONDS=60`, `TRANSPORT_EVALUATION_SECONDS=60`,
`TRANSPORT_DETAIL_DAYS=7`, `TRANSPORT_AGGREGATE_MONTHS=12`,
`TRANSPORT_TRAINING_DAYS=30`, `TRANSPORT_CALIBRATION_DAYS=30`.
All are configurable; sampling never increases the existing provider request rate.
Sampling intervals above 60 seconds intentionally prevent continuous Metro proxy
associations under the initial 60-second gap rule, while still collecting detail.
Unset `TRANSPORT_ARCHIVE_DIR` to disable this collector outside Compose.

`TRANSPORT_ARCHIVE_LIMIT_BYTES` defaults to `10000000000` and must be at least
1,048,576. `TRANSPORT_ARCHIVE_MIN_FREE_BYTES` and
`TRANSPORT_ARCHIVE_MIN_FREE_INODES` are nonnegative integers, each defaulting to
`0` (no extra reserve for that resource). When either is enabled, the archive
probes the filesystem containing its own volume and blocks writes if measurement
fails or available bytes or inodes cannot cover the prospective write and reserves.
`TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS` defaults to
`60` and accepts 10–3600; bounded-progress retries use one second. For example,
the following settings choose a decimal 1 GB archive cap and reserve 2 GiB plus
10,000 inodes on its filesystem:

```dotenv
TRANSPORT_ARCHIVE_LIMIT_BYTES=1000000000
TRANSPORT_ARCHIVE_MIN_FREE_BYTES=2147483648
TRANSPORT_ARCHIVE_MIN_FREE_INODES=10000
TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS=60
```

These are operator choices, not new defaults or a guarantee that all seven days
fit. Verify that the archive mount shares the filesystem intended by the reserve,
and account separately for database, WAL and other services. The probe cannot
prevent other writers from consuming space. When lowering the cap below current
usage, cleanup can take several passes and must be able to create and sync a
temporary manifest before deleting a block, even if this briefly consumes reserve.
If cleanup cannot make safe progress, history stays paused. A directory with more
than 32,768 entries, unmanaged files or nested directories can block maintenance.
Checks and unlink are count-bounded, but a large decode or filesystem call can
still hold the archive owner lock for an unbounded duration. Archive status reports
storage pauses generically; check the archive limit, filesystem headroom and
directory contents when diagnosing them.

At `info` level the server emits `transport archive maintenance heartbeat` at
startup and every minute while the archive is enabled. It reports the worker's
start and pass clocks, normal interval, `in_progress`, `stopped` and last result
(`never_started`, `completed`, `pending` or `error`); startup cleanup is separate.
A `warn` heartbeat indicates a hard error, stopped worker, pass in progress for at
least two minutes, or no completed pass within the configured interval plus two
minutes. Its `stale_reason` is `in_progress`, `overdue`, `never_started` or
`stopped`, or empty for a hard error. The log never waits for the archive mutex
or includes filesystem paths or raw errors. A stale pass can be waiting on the
mutex or filesystem; the heartbeat does not establish the cause, successful
reclamation or completeness of seven days of history. State and clocks reset
on restart. A missing heartbeat alone can also mean the server or logging stopped.
On shutdown the server cancels the heartbeat and waits up to 250 ms for it to
exit before closing the archive. A blocked log sink can retain that one heartbeat
goroutine until it unblocks; the later process logger sync can also block.

Detail is JSON lines compressed with Zstd, retaining the original wait response,
issued forecasts and later evaluation outcomes. Sparse daily hourly aggregates
use Parquet+Zstd. Checksummed immutable generations and an atomic fsynced manifest
are stored on this volume, avoiding additional database writes. A checkpoint is
published last; a Metro detail tail beyond that checkpoint whose order cannot be proved is preserved and fenced rather than replayed after a crash.
A restart always reinitializes live association support, showing a gap rather than
assuming continuous presence during downtime. Unknown/corrupt data stay unavailable.
Hourly detail and checkpoint buffers, row counts, query duration and output are bounded;
if a buffer or budget is exhausted, historical collection pauses and official live
data remain independent. The source collection and compression costs still need
weekday/weekend measurements after deployment.

The JSON v1 manifest is limited to 8 MiB, separately from the allocated-byte budget;
candidate metadata and replacement files must fit concurrently. A 1 MiB archive
configuration remains valid but can pause without enough temporary headroom. If a
manifest rename succeeds but its directory sync fails, writes are fenced until the
owner is restarted; both generations are retained. On reopen, the selected manifest
is directory-synced before orphan cleanup and the cleanup is synced afterwards.
Buffered Metro detail blocked by pressure is retried before other admissions.
Rejected receipts are not recovered, and unacknowledged detail can be lost on restart.
The Metro hour buffer holds at most 1,200 receipts and 16 MiB of encoded JSON lines;
ordinary write failures retain it for retry. If a sampled inference would overflow
the buffer, its isolated candidate is discarded and collection records a gap for
the rest of that UTC hour. The next hour resumes only after the retained detail and
checkpoint flush; failed or uncertain publication retains the existing fences.
Missing referenced, corrupt, oversized, conflicting or non-round-trippable detail
or checkpoint data, and multiple later hours after a checkpoint, also fence
collection and preserve identified evidence. The
shared checkpoint fence may interrupt other enabled operators after their detail
already committed; their next receipt records a gap. Investigate a persistent pause
by stopping the owner and preserving a complete, verified copy of the volume; inspect
or restore evidence on a separate copy before resuming. Do not delete the affected
generations as a shortcut. Reopening verifies evidence again but cannot repair it or
recover observations already rejected or lost from memory.
Older binaries reject manifests
over 1 MiB: roll forward to a compatible binary, not back against that archive.
For disaster recovery, stop the sole owner, copy the complete volume and verify
file hashes and manifest generations before resuming. Verify restoration in a
separate directory; restoring an older backup loses later commits and is not a
routine rollback. Previously unacknowledged or evicted evidence cannot be recovered
by this change.

`GET /api/v1/metro/patterns?stop_id=metro:...` returns collection health,
hourly proxy counts and both forecast sources. Optional `episode` includes remaining
calls for one currently supported association. Public-read policy applies; API keys
need both `read:transit` and `read:history`. The UI labels the proxy model experimental,
uses samples immediately and preserves unknown dwell/speed/probability as null.
Arrival intervals target 80% nominal coverage against bounded proxy references;
they are not physical coverage guarantees. No automatic recent adjustment or
speed/residual method is enabled without admissible inputs and further evaluation.

Deploy one collector on the existing `server_web` network. The dashboard is public; production development login is disabled. The shared upstream budget allows at most 900 requests per rolling minute, including Metro OAuth and redirects.

## Shared server Compose project

[The server fragment](compose.server.yaml) is included by the main Compose file
of the shared `/root/dev/server` project. It defines `lisboa-dashboard` and
`lisboa-database` with a dedicated internal network. The database retains its
`database` network alias, so the existing connection URL need not change. The
dashboard retains the `lisboa-publica` alias on `server_web` for Caddy. Both
existing volumes are external and pinned by name; this fragment must not be used
to initialize a replacement database. The shared project owns the running Lisboa
services following the 2026-10-22 cutover.

The main Compose file includes the protected installed fragment as follows.
Keep the include in the main file: existing BuFin scripts pass that file
explicitly.

```yaml
include:
  - path: /etc/lisboa-publica/compose.server.yaml
    env_file: /etc/lisboa-publica/compose.env
```

The root-owned, mode-600 `compose.env` sets `LISBOA_IMAGE` to an image already
loaded on the host, built from a reviewed commit on `main`. Verify the image ID
and revision label before changing it. The fragment does not build or pull
images. Its mode-600 `/etc/lisboa-publica/dashboard.env` and
`/etc/lisboa-publica/database.env` contain the respective current container
environment values, including the existing database URL and credentials. They
use Compose's `raw` env-file format: write literal `KEY=value` records without
shell quoting or interpolation, preserving each value exactly. Do not commit,
print or embed these files in Compose output or logs. Compare the resolved
environment against protected container inspections before cutover. Include
interpolation can also be overridden by the calling shell or shared project's
environment; check those sources and the resolved image without exposing secrets.
For the shared project, put archive settings in the protected `dashboard.env`;
the standalone [Compose file](compose.yaml) accepts the same variables through
its interpolation environment. Neither form changes the existing volumes.

The shared fragment also mounts the existing PostgreSQL data volume read-only at
`/app/postgres-filesystem` and enables its filesystem probe. `STORAGE_GUARD=true`
is required in the protected dashboard environment. Before deployment, verify
that PGDATA has mode `0700`, is owned by a different UID from the non-root
dashboard, and cannot be opened by the dashboard user; verify that the mount is
the live PGDATA volume. The probe rejects readable directories and paths whose
filesystem cannot be measured, and fails guarded writes closed. Confirm that
`pg_wal` and every tablespace use the
same filesystem, or monitor their separate filesystems independently. Compose
variables `LISBOA_DATABASE_FS_MIN_FREE_BYTES` and
`LISBOA_DATABASE_FS_MIN_FREE_INODES` default to 1,073,741,824 bytes and 10,000
inodes. The reserve is operational headroom, not a quota or a guarantee against
other processes filling the filesystem.

The fragment sets `STORAGE_POSTGRES_DIRECT=true`. Verify that the dashboard's
database URL resolves to the dedicated PostgreSQL server directly, without a
proxy, connection pooler or load balancer; verify the role and database are
dedicated to this single dashboard. This setting enables activity-based
recovery and a startup check before migrations. A still-present session using
that role, even an idle session, blocks startup. The check is a snapshot, not a
distributed fence; run only one collector and stop the previous owner before
starting a replacement. Do not enable it for an unverified endpoint. The
dashboard has a 60-second stop grace period for maintenance and database
cancellation before Docker forces termination.

Plain `docker compose` in `/root/dev/server` loads the shared project's existing
settings through `.env`, a symlink to `/etc/bufin/sandbox.env`, and resolves both
Lisboa services. Preserve the protected target and symlink. The Caddy image is
pinned by the shared override; explicit `-f` invocations must include that
override when required. Compare secret values without exposing them, accounting
for Compose's escaped-dollar representation. A broad `docker compose up` may
recreate unrelated services because of pre-existing Compose image-identity drift
and may run the BuFin initialization service. Use targeted service commands for
Lisboa releases.

For another ownership change, save protected copies of the active Compose inputs, container
inspections, image identities and rollback settings. Verify sufficient space in
a protected backup destination for a PostgreSQL logical backup, the quiesced
archive and database/WAL growth; verify backup integrity and restoration. Do not
assume the database's current size bounds backup or temporary space. Compare the
new resolved service configuration with the running environment, mounts, image,
health checks, networking, logging and resource/security settings. Confirm the
two external volumes already exist and identify their current owners.

The ownership change requires downtime. Stop the old dashboard first and verify
that it released the archive and the `lisboa-publica` web alias; stop the old
database gracefully and verify that it released its volume. Prevent the old
containers from restarting automatically during the migration and retain their
previous restart policies for rollback. Then, from `/root/dev/server`, start only
the new database and dashboard, checking health and authenticated database
access between steps:

```sh
docker compose up -d --wait --no-deps --no-build lisboa-database
docker compose up -d --wait --no-deps --no-build lisboa-dashboard
```

Verify that exactly one dashboard owns the archive and one PostgreSQL process
owns the database volume, Caddy reaches the intended dashboard, and the other
shared services remain unchanged. Check restart behavior before relying on the
new setup after a host reboot. Never use `down -v` or start both old and new
projects against these volumes. To roll back, stop the new dashboard and then
the new database, restore the old configuration and restart policies, and start
the old database followed by the old dashboard. If a later release changes
storage compatibility, use a compatible image or a verified matching backup.

For subsequent releases, update `LISBOA_IMAGE` in the protected
`/etc/lisboa-publica/compose.env` only after loading and verifying the reviewed
image, then recreate only `lisboa-dashboard` with the targeted command above.
Check the effective configuration and owner state first.

For this sandbox, set `SNAPSHOT_RETENTION_DAYS=24` in the protected
`/etc/lisboa-publica/dashboard.env` and recreate only the dashboard. Public
history queries use that window. The independent cleanup worker removes expired
snapshots in bounded batches with one hour of grace; pressure cleanup can shorten
the window but protects the newest seven days. Deleting old rows can make space
reusable after vacuum; it does not guarantee smaller database files.

## Separate application Compose project

The separate application Compose project is a recovery and deployment option;
the running services use the shared server project described above. This setup
uses PostgreSQL 17 on its internal `private` network, without a published database port. Keep credentials in mode-600 `deploy/.env`, excluded from Git; [`.env.example`](.env.example) lists the required settings. Use a long URL-safe `POSTGRES_PASSWORD`. The Cloud configuration remains an optional recovery path using protected `deploy/.cloud.env` with `sslmode=verify-full`. Set `VERSION` to the deployed Git SHA, `SNAPSHOT_RETENTION_DAYS=30`, `HISTORY_INTERVAL_SECONDS=300`, `STORAGE_GUARD=true`, and `TRUSTED_PROXY_CIDRS` to the existing Caddy network subnet. Metro consumer key/secret are server environment variables.

```sh
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml config --quiet
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml build dashboard
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d --wait database dashboard
```

Before replacing an existing container, inspect its active Compose inputs (`com.docker.compose.project.config_files`) and preserve any runtime overrides that are still required. A previous override may pin a different image even when `VERSION` is set. Append a final image-only override setting `services.dashboard.image` to `lisboa-publica:${VERSION:?set VERSION}` after those inputs, and verify the resolved image before starting the dashboard with `--no-deps`. Compare the effective environment, limits, isolation and artifact hashes against the saved baseline; keep credentials and rollback inspections protected on the host. See the [dated station rollout](../docs/research/station-popup-stability-2026-09-27/VALIDATION.md) for the performed checks and the user's exact-main selection.

An earlier external override may still require `DATABASE_URL` during interpolation even when the later local override replaces it. Supply the existing verified local URL through protected process configuration when resolving those preserved inputs; verify that the final dashboard environment still selects the local database. Do not print credentials or substitute a new database target.

The local override restores the healthy-database dependency even when appended after an existing external override. It selects `lisboa-publica_postgres_data` (configurable with `POSTGRES_VOLUME_NAME`) instead of the older `lisboa-publica_database_data` volume. Docker initializes this volume only when empty; changing the environment password does not change credentials in an existing initialized volume. Preserve previous volumes and the Cloud database. Add `Caddyfile.fragment` to the existing Caddyfile, validate and reload the running Caddy container. It overwrites the trusted client-IP header. Other services and domains retain their existing configuration.

History targets the configured retention, 30 days by default, with one hour of pruning grace and immutable five-minute aggregates closed after 90 seconds. Storage-pressure cleanup can shorten that target. Non-Metro live frontend queries use the configured `live_refresh_seconds` interval (five seconds by default); Metro uses scoped SSE with a five-second fallback while unavailable. Historical queries refresh every 30 seconds. History begins with collection in this database; unfinished buckets can be lost during restart, and storage pauses leave explicit gaps.

Vehicle position display uses a fixed ten-minute lifetime from the original source clock for all operators, with an immediate marker warning for backend `not_reporting` state and an independent age warning at five minutes. This is independent of historical retention and the live request interval. The frontend ages cached positions every second even when requests fail; restoring positions or polling the same report cannot renew the deadline.

The app measures all tables in its database using table-scoped CockroachDB range statistics (Postgres uses database size). Writes reserve conservative byte estimates between measurements. History normally stops at 4,000,000,000 bytes and operational writes at 4,500,000,000; measurement failures block writes. Cleanup deletes at most 10,000 expired rows per table at startup and per independent one-minute pass. Startup budgets schema work before DDL and rejects large unbounded backfills. Reservations remain locked through each write; cache/history share one measurement, and unchanged cache parts are retained. On verified direct PostgreSQL, maintenance can reconcile completed reservations after a fresh measurement while holding the write lock and only when no pressure vacuum is running.

On PostgreSQL 17 or later, a role with table `MAINTAIN` privilege can recover some history collection under pressure by deleting oldest snapshots in batches of up to 10,000, strictly older than seven days. The seven-day minimum applies only to pressure cleanup; a shorter configured retention still applies. A persistent initial allowance of 300,000 deletions is replenished only by committed recent snapshot inserts. Each deleted snapshot earns one insert credit, and another deletion batch waits until at most 1,000 credits remain. A pending ordinary `VACUUM` blocks pressure admissions and is retried by the one-minute maintenance job, including after restart. Vacuum has a separate 15-minute deadline and is canceled and joined at shutdown; pending work remains durable. Pressure admissions require a fresh database measurement and outstanding reservations below 4,200,000,000 bytes. Failures and insufficient headroom block admission. Other backends do not use this pressure cleanup. Vacuum makes space available for reuse but does not guarantee smaller database files. Once a pressure cutoff has advanced, an older executable cannot enforce it safely; plan rollback accordingly. See [history retention and limits](../docs/data/history.md#retention-and-storage-guards).

The optional filesystem probe uses `STORAGE_FILESYSTEM_PATH`, `STORAGE_FILESYSTEM_MIN_FREE_BYTES` and `STORAGE_FILESYSTEM_MIN_FREE_INODES`; the path must be an absolute, inaccessible read-only mount of PGDATA and the process must run as non-root. If enabled, a missing/unsafe mount, failed measurement, or insufficient available blocks/inodes fails guarded writes closed. The shared-server fragment enables it; deployments without it preserve existing behavior and log that PostgreSQL host free space is unverified. These application thresholds do not impose a filesystem quota on PostgreSQL WAL, logs, temporary files or the host. Monitor free disk space and keep protected backups. Run one collector and use a dedicated database. Local PostgreSQL has no Cloud monthly RU quota; no Cloud spending caps are changed.

For the shared server, an error mentioning prior sessions or an uncertain
write means the guard could not establish that earlier database work finished.
Stop the old dashboard and inspect same-role sessions on the verified server;
from `/root/dev/server`, the following query is read-only and excludes itself:

```sh
docker compose exec -T lisboa-database sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT pid,datname,usename,application_name,state,backend_type
FROM pg_catalog.pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND (datname=current_database() OR datname IS NULL)
  AND (usename=current_user OR usename IS NULL)
  AND (backend_type='client backend' OR backend_type IS NULL);
SQL
```

Wait for old sessions to end or investigate them with the database operator
before retrying startup. Do not assume an idle session has no queued command.
If the activity view or server identity is unavailable, correct access or the
endpoint before retrying. For unverified proxy/remote connections, an ambiguous
write fails guarded writes for the current process, but that in-memory reason
is lost at restart. Confirm through the provider that prior work has ended
before restarting; the application cannot verify it through an untrusted proxy.
The periodic reservation guard for those deployments retains its previous
behavior and does not gain a crash-safe fence.

If guarded startup refuses migrations near the 4,500,000,000-byte operational
ceiling, inspect the existing database without starting the application or
changing its schema. From `/root/dev/server`, the following reads the current
measurement, migration-guard prerequisites and pressure state in a read-only
transaction; keep its output private:

```sh
docker compose exec -T lisboa-database sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
BEGIN READ ONLY;
SELECT pg_database_size(current_database()) AS database_bytes;
SELECT (SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='snapshots' AND column_name IN ('first_observed_at','speed_sample_count'))=2
   AND (SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('snapshots_time','snapshots_route','keys_owner'))=3 AS migration_guard_schema_complete;
SELECT enabled,delete_credit,insert_credit,epoch,cutoff,vacuum_pending FROM history_pressure WHERE id=1;
ROLLBACK;
SQL
```

Also check free bytes and inodes on PGDATA and WAL. Above the guarded startup
ceiling, ordinary vacuum does not shrink relation files, and automatic recovery
may be impossible. Preserve a verified backup and use an independently reviewed
offline restore or rewrite procedure with sufficient temporary space, preserving
the most recent seven days and pressure metadata. Do not bypass pressure credits,
cutoffs or revision bookkeeping with direct deletes.

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

For evidence-backed revisions, build/run `cmd/patterns-maintenance` locally against a private archive fixture. The Dockerfile also includes `/app/patterns-maintenance` for a future authorized maintenance window; no image was packaged or deployed in this follow-up. `-operator` defaults to `metro`, for which archive reprocessing is temporarily unavailable: the command validates the correction JSON and count, then rejects before opening the archive. Supported later-stage providers accept a normalized observation in `row` and require exclusive ownership; attempting access while the service holds the lock fails. Other archive opens can perform normal recovery and reconciliation. The private correction JSON contains `received_at`, `expected_hash`, `row` and `evidence`; the hash refers to the currently corrected normalized row using its canonical Go JSON encoding. Match all actual service settings with `-sample`, `-bin`, `-limit`, `-min-free-bytes`, `-min-free-inodes`, `-maintenance-interval`, `-detail-days`, `-aggregate-months`, `-training-days`, `-calibration-days`, `-checkpoint` and `-evaluation`. The two reserve flags default to zero and do not inherit the server environment; explicitly pass its configured values, for example `-min-free-bytes 2147483648 -min-free-inodes 10000`. The interval uses Go duration syntax, such as `-maintenance-interval 60s`. Mismatched retention or reserve settings can retire data on open or leave insufficient filesystem headroom. The command reads no provider endpoint and exposes only scalar revision results. Expired/stale/ambiguous inputs and oversized revisions fail explicitly. See [maintenance semantics](../docs/metro-patterns.md#evidence-backed-maintenance).

## Offline Metro tail repair

`/app/patterns-recover` is a narrowly scoped repair for a single verified Metro detail receipt following an identical checkpoint prefix. It is not automatic recovery or general reprocessing. Stop every archive owner and preserve a verified full-volume backup first. Inspect the quiesced manifest and source generations, measure storage headroom and rehearse on a private copy. The tool takes the archive's exclusive lock itself, does not call normal archive startup or cleanup, and does not contact providers. This legacy-tail repair refuses any manifest containing a Metro detail segment kind or reserved segment key, including on an idempotent retry. Segmented archives require a separately verified recovery procedure. Use a private request file outside the archive and invoke:

```sh
/app/patterns-recover -archive /absolute/archive/path -request /private/request.json
```

The request is a JSON object with lowercase SHA-256 strings `manifest_sha256`, `checkpoint_sha256`, `detail_sha256` and `aggregate_sha256` for the exact quiesced files; UTC RFC3339 `detail_hour`, `checkpoint_at` and `tail_at`; Lisbon `affected_day` (`YYYY-MM-DD`); and integer `checkpoint_rows`, `tail_outcomes`, `sample_seconds`, `bin_seconds`, `archive_limit_bytes`, `min_free_bytes` and `min_free_inodes`. Pin the actual configured archive limit and sampling/bin values. This repair requires positive filesystem reserves even if the service is configured with zero; choose at least the configured values and independently allow for the candidate checkpoint and temporary manifest. Unknown request fields, mismatched identities, insufficient headroom and an existing owner fail closed.

The tool requires the exact full checkpoint prefix, one later receipt in the same hour and Lisbon day, no newly issued forecasts, and uniquely matching evaluated proxy outcomes. All potentially affected hot-state clocks must be confined to the verified Lisbon day, and its complete published aggregate must equal the checkpoint's aggregate fields and keys. It does not infer or backfill the tail: raw detail stays unchanged, matching pending cases are retired to avoid a duplicate outcome, and the tail's outcome and receipt-coverage contributions remain absent from aggregates. Other manifest references remain unchanged. A durable checkpoint marker and gap make this limitation visible in the Portuguese patterns view, including after later checkpoints and restarts. Compatible startup keeps the original checkpoint as an additional pinned archive allocation, verifies its digest and fences Metro writes if it is missing or corrupt. Normal detail retention and the seven-day pressure floor still apply; keep the backup independently. Older checkpoint decoders reject the marker, but an older server can still start with Metro paused and reconcile archive files. Prevent incompatible server startup externally; rollback requires a compatible binary or a separately verified pre-repair archive.

Success emits exactly one JSON object with `status` (`repaired` or `already_applied`), `old_manifest_sha256`, `new_manifest_sha256`, `old_state_sha256`, `new_state_sha256`, `detail_sha256`, `aggregate_sha256` and `tail_outcomes`. A repeat verifies the original evidence and the entire expected repaired checkpoint. Compare those hashes with the stopped archive before starting it. Failures produce a nonzero exit; after any uncertain manifest publication, keep the archive stopped and rerun the same request to prove and sync the selected state. Retain both source and candidate generations until the manifest is verified. Never delete evidence to force success.

The earlier local-only follow-up remains dated evidence in the validation log. The subsequent 2026-09-28 instruction authorizes commit, push to main and deployment after the normal gates. Operational collection must preserve existing provider request limits and explicitly separate inferred signals from physical validation.

## Metro live popup configuration

`LOG_LEVEL` defaults to `info`; `debug` enables the bounded per-sample Metro counters (rows,
first slots, zero-ETA observations, presence, rejections, signals, groups), a cumulative
own-forecast summary logged at most once per minute, and the existing per-request API path line.
At most once per minute an existing Metro sample entry also contains aggregate FIFO admission,
rejection, acknowledgement, queue and worker-stage diagnostics. Added fields are bounded to 2 KiB
per enhanced entry (at most 2.8125 MiB/day before log metadata). Receipt times are local collection
times; the counters reset at restart, and no entry is emitted by a stopped worker. Queue/cut
snapshots are approximate, archive record time includes lock wait, and projection totals lag the
current sample. See [history](../docs/data/history.md) for interpretation.
Keep `info` outside diagnosis.

`METRO_REFRESH_MILLISECONDS` defaults to 500 and accepts 500–60000. It affects only serialized direct Metro
collection; existing live JSON intervals and other providers are unchanged. Keep one subscribed collector and
shared attempt budget; multiple independently budgeted instances or external consumers can exceed an account quota.
The direct refresh is a target, not a promise of new provider data: a longer interval can reduce
the target request rate and delivery pressure but can delay or coarsen movement observations. The
2026-10-24 sandbox experiment used an explicit 1000 ms override in protected `dashboard.env`; the
application default remains 500 ms. For a rate change, compare the resolved configuration without exposing credentials,
then observe the debug `delivery` fields over time: queue depth and trend, rejection and failure
counters, cut state and worker timings. Counters reset on restart; missing diagnostics do not
prove health. Also check source clocks, both pattern statuses, newly hash-verified durable detail,
filesystem bytes/inodes and database pressure. A short healthy interval does not establish ongoing
headroom. See the [dated sandbox experiment](../docs/validation/metro-refresh-sandbox-2026-10-24.md).
The inferred-event lane uses the existing archive volume and global allocated-byte budget, with a seven-day target
and pressure floor; collection gaps remain possible. Do not lower `TRANSPORT_SAMPLE_SECONDS` to enable per-update event processing.

[Metro streaming](../docs/metro-live-popups.md) requires unbuffered event-stream flushes through Go/Caddy. Stream
writes use separate bounded deadlines; ordinary API timeout/rate/auth policy remains in effect. Initial stream limits
are 256 KiB/frame, 64/process, 16/IP and 4/principal, configured through `Options.MetroStreamLimits`. Native
EventSource uses same-origin cookies/public reads; API-key clients need header-authenticated streaming fetch.
The operational model exposes experimental estimates without asserting physical calibration; the optional legacy allowlist remains separately supported. Changing sampling/storage or
stream limits does not supply calibration or prove the P95 delivery objective. No deployment follows from local tests.

The dedicated Hub positions loop targets one-second starts, with no overlap/catch-up; it accelerates Metro publication while preserving other operator/ETA schedules. The shared local caps remain 120 Hub attempts/minute and 900 global attempts/minute. Position work protects 20/100 safety slots plus bounded active protected chains and backs off to two/five seconds or longer provider cooldowns. This does not guarantee new coordinates every second.

Latest Metro journey checkpoints add the `popup-checkpoint` archive kind under the existing persistent archive volume/cap. Preserve that volume with the database and run one archive/ingestion owner. Older binaries that reject unknown archive kinds cannot read this additive manifest: rollback requires a compatible binary or a verified pre-change archive backup, never deletion of current evidence. Without an archive, fresh supported journey identities and forecasts remain selectable with unavailable persistence. Archive latency does not gate their initial visibility. Lifecycle checkpoint groups reserve and commit together; retain the additive `metro-inputs` kind as well. Restart never restores live motion continuity.

The archive reader also recognizes historical `metro-detail-segment` blocks. This release still writes hourly Metro detail and does not activate segmented collection. If a retained archive contains the new kind, an older binary rejects its manifest: use a compatible binary or a verified backup for rollback and preserve the current archive. Segments without a recovered checkpoint or extending into its active hour fence Metro collection. See [history](../docs/data/history.md) for validation and retention.

`METRO_MODEL_ALLOWLIST` is optional and defaults unset. To enable a reviewed profile, mount its bounded JSON file read-only and pass its container path through an explicit Compose override. Startup rejects malformed/unreviewed/nonmatching replay evidence; there is no production allowlist bundled in this release. Preserve the persistent archive and run one owner. Lifecycle changes publish complete in-memory state immediately and reserve their durable generation together; restart recovers suspended history without motion continuity. The operational browser model evaluates source-bound published polylines every 500 ms without more provider requests.

The [Metro completion rollout, 2026-09-29 Lisbon time](../docs/validation/metro-live-main-rollout-2026-09-29.md) records the performed clean-main deployment, unchanged runtime environment/volumes, protected backups and production health/native reset verification. That dated rollout did not activate the legacy reviewed adapter; current operational estimates are described above.


The [Metro direction correction rollout, 2026-09-29](../docs/validation/metro-direction-correction-main-rollout-2026-09-29.md) records the deployed clean-main revision, ten preserved Compose inputs plus an image-only override, unchanged environment/resources/volumes, protected database/archive backups and actual public/native-stream checks. Existing polling budgets and the unset model allowlist were preserved; deployment does not qualify physical direction or departure timing.
