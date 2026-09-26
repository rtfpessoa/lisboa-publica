# Existing-server deployment

Run this Compose project alongside the existing `/root/dev/server` project. It joins the existing `server_web` network; the current Caddy container owns ports80/443 and TLS. Append the supplied Caddyfile fragment once and reload that Caddy service. No application/database host ports are published.

On the server, keep this repository at `/root/dev/lisboa-publica`. Put a root-only (`chmod600`) `deploy/.env` beside compose.yaml with `VERSION` (Git SHA), a random alphanumeric `POSTGRES_PASSWORD`, `SNAPSHOT_RETENTION_DAYS=3`, `TRUSTED_PROXY_CIDRS` (existing Caddy network subnet), and the Metro consumer key/secret. `GOOGLE_CLIENT_ID` is optional; reads stay public. Secrets are never committed.

```sh
cd /root/dev/lisboa-publica
# Run after checking out the intended Git revision and updating deploy/.env VERSION.
docker compose --env-file deploy/.env -f deploy/compose.yaml config --quiet
docker compose --env-file deploy/.env -f deploy/compose.yaml build dashboard
docker compose --env-file deploy/.env -f deploy/compose.yaml up -d --wait
```

Validate the existing Caddy configuration before reloading:

```sh
cd /root/dev/server
docker exec server-caddy-1 caddy validate --config /etc/caddy/Caddyfile
docker exec server-caddy-1 caddy reload --config /etc/caddy/Caddyfile
```

Verify HTTPS health, all eight sources, direct Metro status, and actual arrivals; then run the browser suite against the public URL. Only one ingestion instance may run for this subscription. The shared900-request rolling-minute cap includes token requests. App memory is bounded to1280MiB with Go's memory target768MiB; Postgres is limited to256MiB. Existing unrelated containers are untouched.

Roll back by selecting the preceding Git SHA/image in deploy/.env and running `up -d --no-build --wait`. For a first deployment, stop only this Compose project and remove its Caddy site block. Never use `down -v`: the named database volume retains observations, sessions and keys. Back up with `docker compose ... exec -T database pg_dump -U lisboapublica lisboapublica` into a protected file before a later schema change.

This host had9.8GiB free at initial inspection; the measured snapshot rate was approximately1.7GB/day. Confirm sufficient capacity for the requested history retention before enabling long-term collection. TLS terminates at Caddy; the database is isolated on the internal private network and is not exposed on the host.

Initial deployment retains3 days of observations, plus one hour of pruning grace. At the measured rate, budget approximately5.2GB for observations and additional room for database WAL, cache, logs and image builds. Recheck disk usage as fleet observation volume changes.
