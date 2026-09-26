# Shared Caddy TLS patch: prepared, not deployed

F5 remains open in production. The audit's SSH permission was read-only. This runbook and Dockerfile are a reviewable replacement proposal, not authorization to build on or restart the shared production server. Application fixes likewise have not been committed, pushed or deployed in this task.

## Candidate and evidence

`Dockerfile.caddy-security` rebuilds Caddy **v2.11.4**, preserving standard modules, with **Go 1.27.1**. Both official base-image manifest digests are pinned. It inherits the official image's paths, command, certificate/config locations and file binding capability. It does not introduce plugins or alter the active shared Caddyfile, ports, networks or mounts. The custom version suffix identifies this rebuild; build-info remains the authority for source/toolchain versions.

Main locally compiled the Linux/ARM64 executable using the same install command and Go version. Its binary scan no longer contains GO-2026-6090 or affected standard-library advisory IDs. Native build configuration/TLS/proxy checks are recorded in `docs/SECURITY_FIXES.md`. These are executable checks: **no Docker daemon is running locally**, so the final container/image has not been built or validated. Do not label it a validated production image.

The scan still reports **16 library advisory IDs / 102 finding records**, including feature-dependent gRPC/SSH/OpenPGP/Chi/JSON/DNS/normalization paths. Source scan traces are conservative possible-call paths, not proof of active shared-host configuration or exploitation. Keep these residual advisories visible. Reconcile version ranges with maintainers and assess active modules/configuration before rollout; this rebuild specifically addresses F5, not every library warning.

## Build and validate on an authorized Docker runner

Run from this repository with an ARM64 Docker builder. The following writes only to that builder; it is not approved for the production server by the previous audit permission.

```sh
docker build --platform linux/arm64 -f deploy/Dockerfile.caddy-security -t lisboa-caddy:2.11.4-go1.27.1 .
docker image inspect lisboa-caddy:2.11.4-go1.27.1 --format '{{.Id}}'
docker run --rm --network none lisboa-caddy:2.11.4-go1.27.1 caddy version
docker run --rm --network none lisboa-caddy:2.11.4-go1.27.1 caddy build-info
```

Verify Go 1.27.1, Caddy source v2.11.4, expected standard modules, architecture, image package scan and digest. Extract `/usr/bin/caddy` from a temporary nonrunning container on the builder, scan that exact executable and retain its hash. Remove the temporary container. A native executable hash will differ across build environments; use the actual image artifact's hash/digest for rollout.

Validate a **protected read-only copy of the complete active shared Caddyfile**, its imports and any required assets with the new image. Keep secrets out of logs/reports and never print expanded shared Compose environment values. Validate all configured host routes, certificate storage/binding permissions, plugin requirements and local TLS/proxy/compression behavior. Inventory every affected hostname and choose an ordinary health/TLS request per host; no load/exploit testing.

## Separately authorized rollout

1. Confirm explicit approval for shared-proxy production changes. Record current image ID/digest, Compose project name and files, protected active configuration and data/config mount paths. Check whether automated image replacement is enabled; the override sets `wud.watch=false` to prevent resetting this pinned rebuild.
2. Build/import the validated image. Set `CADDY_SECURITY_IMAGE` to its immutable digest or local image ID. Apply `compose.caddy-security.yaml` to the existing **shared server** Compose invocation, using its actual base files/project settings. The audited container name is `server-caddy-1`; the override assumes that project's service is `caddy`. Verify this service name before applying. Do not apply it to `deploy/compose.yaml`, which is the dashboard project.
3. Inspect a filtered Compose diff: only the Caddy image and watch label should change. Retain all ports, networks, commands and volumes. Validate the full configuration with the candidate image and the existing assets mounted read-only. A validation command does not start public listeners or request production certificates.
4. Recreate only Caddy with `--no-deps`, preserving `/data` and `/config`. Never run Compose down/remove volumes. Expect a short interruption and confirm its acceptability before the rollout.
5. Read running `build-info`, image identity, healthy state and logs. Verify HTTPS/certificate chains and ordinary dashboard/API/HTTP redirects for every inventoried site. Confirm no new admin-port binding and no TLS-verification bypass. Record timings/errors with secret redaction.
6. If validation fails, restore the recorded old image/config and recreate only Caddy. Rollback to the affected Go1.26.3 artifact is temporary recovery and reopens F5; schedule a corrected patch promptly. Never delete certificate volumes during rollback.

Production F5 closes only after these exact artifact/container and all-site checks pass. Do not combine unrelated firewall/network/identity changes with this rollout.


## Subsequent authorization and constrained host build

The user explicitly requested commit, push and deployment after accepting the local remediation. This supersedes the original read-only restriction above. The original candidate evidence remains historical; final image and rollout evidence belongs in docs/SECURITY_FIXES.md.

The shared host has limited free RAM. Main compiles Linux/ARM64 off-host and uses `Dockerfile.caddy-security-runtime` with a context containing only the verified `caddy-security` binary. Verify its checksum before transfer and again after extraction from the final image, alongside Go/Caddy build info and standard modules. The source-build recipe remains `Dockerfile.caddy-security`; the runtime-only packaging preserves its pinned official runtime and binding capability. No Go or npm compiler runs on the production host. The app update similarly uses `Dockerfile.security-prebuilt` with the recorded existing runtime image, verified off-host server binary and matching compiled frontend assets. Do not use this runtime shortcut when runtime/CA dependencies change.
