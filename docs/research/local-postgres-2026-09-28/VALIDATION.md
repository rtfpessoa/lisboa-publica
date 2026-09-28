# Clean local PostgreSQL cutover — 2026-09-28

## Authorization and baseline

The user requested PostgreSQL in the existing Docker Compose project after the
Cloud monthly 50-million-RU allowance was exhausted, then explicitly selected a
clean database. No Cloud data import, spending-limit change or volume deletion
is authorized by this procedure.

Read-only host inspection confirmed that `lisboa-publica-dashboard-1` used
`lisboa-publica:383bae74ff7856b04790482a2f17fcce83f64bcb`, a commit already on
`main`, and was unhealthy. Its logs explicitly reported SQLSTATE 53300:
Cloud disabled the cluster after reaching its monthly Request Unit limit.
The base Compose already defined PostgreSQL 17, but `compose.external.yaml`
selected Cloud and disabled the local service with a profile.

The host had approximately 24 GiB free disk and 1.3 GiB available memory.
The old `lisboa-publica_database_data` volume contained PostgreSQL 17 data
(172 MB allocated); `lisboa-publica_transport_history` was 328 KB.
Protected baseline inspection, environment/Compose copies and a cold archive
of the old database volume were saved under
`/root/lisboa-postgres-cutover/20260928`, with private directory/file permissions.
The PostgreSQL image was pulled with digest
`sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24`.

## Validation status

Docker Compose v5.5.1 rendered the base and every existing runtime override with
`compose.local.yaml` appended last. The resulting dashboard environment differed
only in `DATABASE_URL`; its image was identical. The database dependency required
health, the database had no active profile or published port, and selected volume
`lisboa-publica_postgres_20260928` did not yet exist.

## Performed cutover

The configuration was committed as `47af4c63b27ba7ff5727f11daa2a8f9b29e1893e`
and pushed to `main` through a normal signed Git commit. The Maat Git gate
reported `skipped` because no supported source or analysis inputs changed;
this is not a scored Go/TypeScript assessment. All 112 local Markdown targets
in the affected existing documents were verified to exist; diff whitespace
checks passed.

The host checkout fast-forwarded to that commit. `POSTGRES_VOLUME_NAME` was
persisted in the existing protected `.env`. The final local override was added
after the saved active Compose inputs. Only `database` was started initially.
A password-authenticated TCP SQL query confirmed the new database identity,
zero public-schema tables, and size 7,698,099 bytes before dashboard startup.
The PostgreSQL version was 17.11. No Cloud or old local rows were imported.

Only `dashboard` was then recreated, with `--no-build --no-deps`. Both services
became healthy. Inspections confirmed identical dashboard image ID, mounts,
environment key set, memory/CPU limits, read-only filesystem, dropped
capabilities, security options, tmpfs, rotating logs, restart policy and
networks. `DATABASE_URL` was the only changed dashboard environment value.
The existing `lisboa-publica_transport_history` mount was preserved. The local
database has no published host ports and uses the private internal network.
Other running services were left in place.

## Application and recovery checks

Public HTTPS requests returned 200 for `/`, both referenced frontend assets,
`/api/v1/health`, `/api/v1/config`, `/api/v1/operators`,
`/api/v1/vehicles?operator_id=cm&limit=1`, `/api/v1/history?limit=1` and
`/api/v1/metro/status`. Health reported `database=ok`, `status=ok`; configuration
reported history `collecting` with 300-second resolution and 30-day retention.
Vehicle reads returned current CM data and direct Metro status was `ok`.
Some static feeds were still loading during the initial cold-start check;
these checks do not imply complete provider coverage. At the subsequent check,
all eight operators reported live and static status `ok`. The history API then
returned a nonempty result and health/configuration remained successful.

The existing executable initialized nine application tables. An early SQL check
found 35 cache chunks, 1,996 vehicle-fact rows, 1,816 reporting-state rows,
eight source-health rows, zero sessions and zero API keys. Database generation
advanced from 15 to 47; later cache checks included all eight operators.
History was initially empty, as expected before five-minute buckets closed.
The first completed collection interval subsequently persisted 3,426 snapshots.
Their original source timestamps ranged from 08:21:38 to 08:24:59 UTC; some
source reports naturally predate the clean-start ingestion time. Sessions and
API keys remained empty. No historical continuity with Cloud is claimed.

A protected `pg_dump -Fc` backup (15,152,798 bytes at the early snapshot) was
created under the rollback directory. `pg_restore --exit-on-error` successfully
restored it into a separate temporary PostgreSQL database, with the same cache,
vehicle-fact and reporting-state counts. That test database was then dropped;
application tables were not replaced by the restore test. This was a one-time
backup/restore verification, not a configured periodic backup schedule.

At 08:24 UTC, logs since the cutover contained 129 informational records and
no warning/error records. A resource sample showed about 351 MiB dashboard
memory (1,280 MiB limit) and 59 MiB PostgreSQL memory (256 MiB limit).
These are short startup observations, not long-term capacity guarantees.

The older PostgreSQL volume and Cloud configuration remain preserved.
Cloud is still disabled by its monthly RU allowance; the application no longer
uses it. Reverting to Cloud would require Cloud access to recover first and
would not automatically merge newly collected local data.

## Artifact identity

SHA-256 hashes from the running, unchanged dashboard image:

| Artifact | SHA-256 |
|---|---|
| `/app/server` | `76788e9ead5eb28a0c80a5b0d0c29e5d4d923e9af2164230874fe7966e98cc4c` |
| `/app/frontend/dist/index.html` | `9d051fd258cae4c6841840c0cd815b386d89974069e37be5568cec016dc978d2` |
| `index-CsNwpzJl.js` | `e8dfb57d0793f945dcb8c7a7000e7a0527cff5725252fb08c1c97963a0aeddf3` |
| `index-CZG6j-4m.css` | `008504f0c979205b0bce934e6f599642ada69bcb2446c762a403fc1d7cd58c6f` |

The server and frontend were not rebuilt for this configuration-only cutover.
The deployed image's revision remains `383bae74ff7856b04790482a2f17fcce83f64bcb`;
repository configuration and validation commits are separate from that image.
The unpublished transport implementation was rebased onto the configuration
commit with its code unchanged; a documentation conflict was resolved by
retaining both the local-database instructions and the transport release policy.
