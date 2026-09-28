# Metro completion main rollout, 2026-09-29

Lisbon date2026-09-29; verification occurred at23:07–23:09 UTC on2026-09-28.
The user authorized completing implementation, repairing Maat, committing, pushing main and deployment,
and confirmed `root@roodle.rtfpessoa.xyz` as the SSH destination.

## Release and preserved state

Signed implementation commit `cd68383e9d07baad6bf832977f8ccbed1d5be080` passed the
[normal Maat gate](metro-live-completion-2026-09-28-maat.json), score98, delta+1,
no critical regressions or suppressions. It was pushed to `main` before the clean server checkout
was fast-forwarded and built. Frontend and backend were built together with the same revision.
The user's existing glossary edits and standalone prototype were excluded.
[Completion validation](metro-live-completion-2026-09-28.md) records race/vet, browser,
generated-interface, build, native delivery and local documentation checks.

Production image: `lisboa-publica:cd68383e9d07baad6bf832977f8ccbed1d5be080`.
Its OCI revision matches the commit. Running server/frontend hashes match the packaged image.
The [safe verification JSON](metro-live-main-rollout-2026-09-29.json) records both hashes,
image ID, health, preserved configuration, backup checks and browser reset timings.

All nine active Compose inputs were preserved, followed by one image-only override.
An earlier external override required `DATABASE_URL` interpolation despite the later local override.
The protected existing local value supplied that interpolation; resolved and running environments
match the previous container exactly. An initial preparation assertion compared a string memory
limit to its integer inspection value; normalizing that representation fixed the verification.
Neither failed preparation stopped the service. No application patch was needed.

The dashboard retains its1280MiB limit,1.5CPU quota, read-only filesystem, dropped capabilities,
security settings and existing networks. One collector is running. PostgreSQL17 remained running,
healthy and private, using `lisboa-publica_postgres_20260928`. The unchanged archive volume is
`lisboa-publica_transport_history`. Other services were not recreated.

## Backups and recovery boundary

Protected release material is in `/root/lisboa-metro-live-main-release/cd68383e9d07`,
with mode700 directory and mode600 configuration/inspection/backup files. A consistent PostgreSQL
custom dump contains41,486,529 bytes; its catalog was successfully listed with32 entries.
The collector was stopped before the quiescent archive copy:33 tar members,12,738,560 bytes,
SHA-256 `ed1d0d462fc2151275ab772165bcad463fb03703ec0ccce6cefc2a87a1177fed`.
Tar listing and checksum passed; an actual restore/rollback was not executed.
The previous image `lisboa-publica:0d69c10300bbd86950c1a54079d7f95c95426dae` is retained.
Older executables may reject the new checkpoint archive kind, so recovery requires a compatible
executable or this verified pre-change backup. No volume or current evidence was deleted.

## Performed production checks

- Dashboard and database container health passed; public health reports both status and database `ok`.
- Actual Metro snapshot reported service `ok`, archive `collecting`,23 vehicles and18 train records.
  A later sample retained18 supported associations:15 latest checkpoints committed,3 pending.
  Latest pending revisions are separate from the mandatory durable baseline admission rule.
- Production UI rendered at1280px and390px with no page errors. Native EventSource received a
  complete scoped reset through Caddy in320.8ms and380.4ms respectively, with7 scoped trains/vehicles.
  These are two initial reset measurements, not a delivery P95 or reconnect/failure-load profile.
- An initial Metro snapshot returned503 `busy` under the existing bounded read admission.
  Subsequent reads and native resets succeeded. Startup log inspection found no error/fatal/panic messages.
- Startup resource sample was737.9MiB/1.25GiB and151.86%CPU; this is not a steady-state capacity claim.

A supplementary local Python HTTPS probe reported a local issuer-chain verification failure. Standard verified curl, Node fetch and Chromium checks succeeded; TLS verification was
not disabled. Its first scalar summary also used the wrong shape for the association enum and was
corrected without changing application code.

Direct Metro's configured target remains500ms; dedicated Hub acquisition targets1s within shared
900-global/120-Hub rolling attempt budgets and protected headroom. Production actual upstream
cadence/duplicate fraction was not measured here. No qualified actual-source model allowlist was
supplied: model projections and experimental departure activation remain unavailable.
No physical arrival/departure precision or500ms provider-coordinate guarantee is claimed.

Final documentation verification checked18 affected Markdown files,459 local targets,36 fragments and257 OpenAPI references, with no failures. `git diff --check` and the staged equivalent passed. No diagrams changed.
