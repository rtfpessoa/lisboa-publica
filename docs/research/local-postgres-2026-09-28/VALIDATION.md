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

Runtime cutover checks are pending. The existing executable and frontend will
be retained; only the database target and local-service configuration change.
A new volume must be verified empty before starting the dashboard.
Cloud history and credentials will not exist in the new database; this is an
intentional reset, not evidence of copied data or historical continuity.
