# Validation evidence

Main agent owns implementation, testing and fixes. Research agents were read-only. Plan review used the explicitly requested quasar-alpha at xhigh and recommended acceptance after revisions; a separate final independent review is required before completion.

## Completed checks (2026-09-26)

- Deterministic regeneration: `./scripts/check-generated.sh` passes for both generated interfaces from api/openapi.yaml.
- `go vet ./...` passes.
- Postgres14.24 (latest3.319s): `TEST_DATABASE_URL='postgresql:///lisboapublica_test?host=/tmp' go test -race ./...` passes.
- CockroachDB26.2.6: latest full race/integration suite against isolated test schemas passes (18.332s), including migration/cache/session/key/snapshot SQL and all history/fleet/traffic/ranking queries.
- `npm run build` passes strict TypeScript and Vite production compilation. MapLibre's map chunk is approximately1MB; it loads lazily and its worker is bundled separately. Vite reports a chunk-size advisory.
- Playwright Chromium: desktop dashboard and optional development-session key creation/revocation pass; mobile test passes after correcting its initial assumption (the mobile drawer intentionally starts closed). Screenshot evidence in acceptance/.
- Live official TML/CM refresh populated all eight operators' static catalogs and available current positions; Carris normalized schedule fully parses within bounded archive limits. Static data restores after restart.
- Credential-backed Metro server starts through 1Password, with no secret printed or saved. Actual codigo is a string "200"; parser and fixtures corrected. Live /metro/status reports four normal lines. /arrivals for metro:ML11060055 (Roma) returns six labeled predictions with provider observation times and verified Telheiras destination.

## Behaviors covered by backend tests

All22 OpenAPI operations have scope or session policy. Request/pagination/date validation, public vs restricted reads, wrong/expired/revoked keys, session-only key management, one-time hashed-key storage, origin/login nonce checks, verified Google claim checks, logout and expiry are exercised. Google signature verification uses the official Google library in production; tests inject trusted verifier claims and do not claim a live Google login without configured client credentials.

Immutable cache revisions hold vehicle pages stable across refresh. Historical revision bounds fix both committed generation and date window; late observations and pruning do not shift subsequent pages. Snapshots deduplicate provider observation timestamps and survive restart. SQLSTATE40001 retries are bounded.

GTFS tests cover calendar exceptions, dates beyond24:00, Lisbon DST, parent-station arrivals and malformed times. Speed tests exclude inferred Metro positions and impossible jumps. Concurrent outbound-budget test allows exactly900 out of1,000 simultaneous requests, rejects additional calls at59seconds, and allows recovery at60seconds. Metro fixtures exercise OAuth Basic/client_credentials, short token expiry, shared30second cache, verified payload shape, stale prediction rejection, duplicate platform predictions and auth failures. Fleet metadata tests verify agency prefixes and reject unknown/conflicting crosswalks.

## Honest limitations

Historical observations begin with this installation, with configurable public retention (default30 days, this deployment3 days) and one hour of physical pruning grace for stable pagination. Operator positions may be sparse or stale. Exact commercial speed, completed trips, operational headway and depot/permanent allocation data remain clearly unavailable. TTSL direct TLS failure uses safe official TML data. Metro map positions are explicitly estimated and excluded from sampled speed/distance.

Public UI requires no authentication. Google account sign-in needs GOOGLE_CLIENT_ID and an authorized origin. Local development login is explicit and rejected in production. Run one ingestion instance: the900-request rolling-minute budget is per process and includes every upstream/token request.

## Independent review fixes

The first independent final review found five blockers. Main fixed all five:

- Empty native date values restore today's date; browser checks explicitly clear both date inputs.
- Schedule revisions bind immutable cache version, exact time window and prediction eligibility timestamp. The UI uses the existing revision-aware collection reader and paginates the frozen result locally. Arrival integration checks verify stable later pages across a cache refresh and reject changed explicit windows; prediction checks freeze eligibility through elapsed wall time.
- Stop queries invalidate when selected static source timestamps change, including after a long cold-start ingestion.
- Only exact verified agency/active-plan prefixes are normalized. Mismatched prefixes remain literal, observed plan_id is retained in the generated schema, and mismatched plans are excluded from current route joins. Tests cover mismatched agencies, old plans and extra prefixes.
- Metadata-only updates preserve GTFS status/errors; regression tests verify that a failed static source stays failed.

A cache-format compatibility check also caught old full-name StopTime JSON decoding as zero after compact field tags were introduced. Legacy and compact forms now restore identically. Live Metro scheduled departures now show actual GTFS times, and direct prediction results remain separately labeled.

Latest checks after fixes: Postgres race suite3.779s; Cockroach race suite20.175s; go vet and deterministic generated checks pass; TypeScript/Vite build passes. Browser results and subsequent independent verdict recorded below.

Final browser suite: **4 passed in29.3s**, covering public desktop map/search/operator filters/routes/trip pagination/history/charts/traffic/fleet, cleared date inputs, local scoped-key creation/revocation/logout, mobile viewport/navigation/modal keyboard behavior, and an advancing Metro arrivals window on the30-second tick. Latest full Cockroach suite including omitted-bound revisions: **32.943s**; Postgres: **3.140s**. Regeneration/vet/build checks remain passing.

The separate independent **quasar-alpha/xhigh final reviewer recommends acceptance**, after three read-only review rounds. Its final verdict: “All five original blockers and both follow-up issues are resolved.” The main agent confirms the authorized requirements are met, including the public UI amendment, verified subscribed Metro token/status/prediction flow, and the single-process900-request rolling60-second cap. Production Google sign-in remains configured through an external client ID; this installation's dashboard is intentionally public.

Final running-server smoke after restart: health200/ok; operators200 with eight rows; Metro status200/ok with four lines; Roma arrivals200 with six predictions and94 scheduled arrivals in the first100-item page. Latest binary and frontend production build run at http://127.0.0.1:8080.

## Commit and deployment preparation

Maat cleanup preserves behavior under the existing integration fixtures. Latest Postgres race suite including configured-retention and proxy-budget regression checks:3.886s. Browser suite:4 passed in27.2s; the initial rerun began before cache restoration finished and failed connection checks, then passed after health became ready. Trusted proxy tests prove separate public clients have separate budgets, direct untrusted spoofing cannot create new buckets, and malformed forwarded values fall back to the proxy peer. Retention tests prove a configured three-day public window, config reporting, and three-day retention with one hour of physical pruning grace. Default retention remains30 days; deployment retention is three days, as selected by the user.

Maat absolute score improved47→68, but remains below its configured95 threshold. No hook or rule suppression is used. These remaining advisories are documented in MAAT.md.

Production cold ingestion loaded all eight providers with a kernel-measured823.8MiB peak under the1280MiB container limit, no OOM or restart. The direct Metro endpoint initially failed TLS on Linux because the provider sent only its leaf certificate. Its AIA names Sectigo Public Server Authentication CA OV R36. The issuer-distributed intermediate and Metro hostname certificate both verify against the host's public CA bundle; the image now installs that verified intermediate with TLS verification still enabled. Final refreshed-image/cutover checks follow.

Metro's endpoint currently omits its intermediate certificate. The image includes the publicly issued Sectigo OV R36 intermediate from the certificate's AIA URL, http://crt.sectigo.com/SectigoPublicServerAuthenticationCAOVR36.crt. DER SHA-256: `6542d176bed50f193c0ce297ae44ecd8a0a86bec2ede682769344059b4e78530`. It verifies against the standard public CA bundle before installation during the image build. TLS chain and hostname verification remain enabled; this is not an insecure transport workaround.

## External CockroachDB deployment

The selected deployment now uses the `cockroach-lisboapublica` cluster and30-day retention. Keep its `DATABASE_URL` with `sslmode=verify-full` in protected `deploy/.cloud.env`; never commit it. Use both environment files and the external override:

```sh
docker compose --env-file deploy/.env --env-file deploy/.cloud.env -f deploy/compose.yaml -f deploy/compose.external.yaml config --quiet
docker compose --env-file deploy/.env --env-file deploy/.cloud.env -f deploy/compose.yaml -f deploy/compose.external.yaml up -d --wait
```

Set `SNAPSHOT_RETENTION_DAYS=30` in deploy/.env. The override removes the local-database startup dependency; the local service is profile-gated and its existing volume is preserved. Stop the previous local database after verifying the cloud cutover. The server retains30 days of observations plus one hour of pruning grace; observed history starts when collection begins in this database.

The initial observed storage rate projects approximately50GB for30 days, plus database overhead. Cloud storage and request-unit spending limits are managed in the Cockroach Cloud console; this deployment does not change account billing limits. Verify the configured allowance fits retention. Existing Basic plans and newer Continuum plans differ: https://www.cockroachlabs.com/cockroachdb/pricing/ and https://www.cockroachlabs.com/pricing/.
