# Security audit — Lisboa Pública

Audit date: **26 September 2026**. Repository revision: **a8792d9ab01d08e50f1663c69a93194cb4f8577e**. Public target: **https://lisboapublica.rtfpessoa.xyz/**. The repository records executable revision `72c8a63`; authorized read-only SSH inspection confirmed that running image revision, Go 1.26.8 and Alpine 3.23.6; the shared Caddy is 2.11.4, built with Go 1.26.3.

## 1. System, trust boundaries and main risks

The application serves a public React 19/TypeScript dashboard built with Vite, TanStack Query, MapLibre and Recharts. A Go `net/http` server implements an OpenAPI-generated API using oapi-codegen v2; oazapfts generates the browser client. Transit feeds are fetched and normalized on the server, cached in memory and persisted with pgx to Postgres/CockroachDB. Google Identity Services can establish browser sessions for API-key management. Metro uses server-side OAuth client credentials, independently of user login.

**No critical vulnerability was confirmed. One high-severity deployment exposure was confirmed:** the public TLS proxy is built with a Go version affected by the TLS KeyUpdate resource-exhaustion advisory. The affected runtime and public TLS boundary are verified; exhaustion was not attempted. Four defects were confirmed locally: public schedule processing ignores cancellation and performs work before pagination; the storage guard blocks logout/key revocation; concurrent key creation bypasses the 20-active-key quota; ZIP declared-size accounting overflows. Their severity and prerequisites are described below. This is not a clean bill of health: production resilience under abuse, security of the other services bound on the shared host and cloud/database privileges remain unverified.

Main risks are availability of a small public service, abuse of expensive historical/schedule queries, trust in upstream feed content and redirects, and future user identity/key ownership when Google login is enabled. At the time of the public checks, `dev_auth` was false and Google login was unconfigured. Read endpoints being anonymous is an intentional product requirement, **not an authorization vulnerability**.

### Reachable surface and boundaries

| Boundary | Surface / data | Controls and remaining concern |
|---|---|---|
| Internet → Caddy → Go | Public HTML/assets and `/api/v1/*` | HTTPS observed; repository reverse proxy overwrites the client-IP header. No API/global concurrent-work limit; browser header gaps. |
| Public client → read API | Operators, routes, shapes, stops, vehicles, planned trips/arrivals, metrics, history, fleet, traffic, rankings, coverage, Metro status | OpenAPI validation, page/range bounds and per-IP rate limiting. Pagination bounds returned rows, not all computation. |
| Browser → account API | `/api/v1/auth/google`, `/auth/development`, `/auth/me`, `/auth/logout`, `/keys`, `/keys/{key_id}` | Signed Google token verification, nonce binding, exact Origin for session mutations, session-only key management, owner checks. Google is currently unconfigured. |
| API-key caller → API | Bearer keys with `read:transit` / `read:history` | Hash lookup, scopes, revocation/expiration and principal limits. `/metrics` scope policy needs clarification. |
| Go → upstream providers | Fixed TML/CM/Metro endpoints; archive URLs from the TML plans feed | Verified TLS, timeouts, byte/row limits, rolling global and per-host request budgets. Archive hostname checked only before redirects. |
| Go → database | Cached feeds/history, user email/name, hashed sessions and keys | Parameterized SQL and application storage guard. Configured external TLS verify-full is confirmed; external role grants remain unverified. |
| Container → host / other services | Shared external `server_web` network | Repository specifies non-root, read-only rootfs, dropped capabilities, no-new-privileges and memory/CPU limits. Shared-network peers are inside the configured proxy trust boundary. |
| Browser → third parties | OpenFreeMap tiles/style; Google login script when configured | Fixed application URLs; third-party availability/content remains a trust dependency. Location is used locally to move the map, not posted to the application's API. |
| Developer/build → deployment | Manual Compose build/deploy; Go modules/npm lockfile and generator scripts | No tracked CI workflow found. Image tags float; exact image provenance/security checks are not enforced by tracked CI. |

There are no public file-upload, URL-fetch, arbitrary command, payment or administration endpoints in the inspected contract. Transit archives are upstream input, not user uploads. Port 8080 and the database port are not published by the tracked Compose files; this alone does not establish the host's actual listening ports/firewall rules.

## 2. Findings, ordered by severity

Severity describes this application and its prerequisites. “Confirmed” means the defect was demonstrated by code/local/public evidence; it does not imply a successful production exploit. Hardening advice is separately labeled.

### F5 — High: the public Caddy TLS proxy uses a known affected Go runtime

**Classification:** confirmed affected deployed dependency and reachable TLS boundary; production resource exhaustion was not attempted. This is a shared proxy finding, not a claim that every scanner advisory is exploitable.

**Affected:** public HTTPS listener for `https://lisboapublica.rtfpessoa.xyz/`; running `server-caddy-1`, Caddy **2.11.4**, build-info **Go 1.26.3**. Source deployment reference: [Caddyfile.fragment:1](deploy/Caddyfile.fragment#L1). The actual shared Caddy image is `caddy:latest`, outside this repository's Dockerfile.

**Evidence:** authorized `caddy version`/`caddy build-info` and a read-only copy of the running executable establish the build version. `govulncheck -mode=binary` reports [GO-2026-6090 / CVE-2026-56862](https://pkg.go.dev/vuln/GO-2026-6090): excessive post-handshake TLS KeyUpdate messages can make a server repeatedly derive keys. The advisory fixes the Go 1.26 branch in **1.26.6**; this proxy has **1.26.3**. One ordinary handshake confirmed public TLS 1.3. This processing precedes the application's API/IP rate limiter. The Caddy container has no configured memory cap. The application executable itself uses Go 1.26.8 and is not affected by this version range.

**Impact:** a client that completes the public TLS handshake can target the shared proxy's TLS processing, potentially consuming CPU and affecting this dashboard and other hosts served by Caddy. No data exfiltration/RCE or successful outage is claimed. API request limits do not patch the TLS implementation.

**Safe verification:** read `caddy build-info`, inspect the corresponding standard-library advisory and run a binary vulnerability check on a local read-only copy. An ordinary HTTPS handshake establishes the boundary. **Do not send repeated KeyUpdate messages or run a live DoS test.**

**Specific fix:** replace/rebuild the shared proxy with a tested official Caddy artifact compiled using a supported patched Go version (at least 1.26.6 on this branch), verify `build-info` and scan it before deployment. Pin its digest and schedule security updates. Test configuration/TLS and all hosted sites before an authorized rollout; updating `caddy:latest` without inspecting the actual build is insufficient evidence. No proxy change was made in this audit.

### F1 — Medium: public schedule work continues after cancellation; pagination does not bound its cost

**Classification:** confirmed resource-control defect; production denial of service was not attempted or established.

**Affected:** `/api/v1/arrivals`, `/api/v1/trips`; [server.go:469](internal/app/server.go#L469), [server.go:497](internal/app/server.go#L497), [gtfs.go:108](internal/app/gtfs.go#L108), [gtfs.go:145](internal/app/gtfs.go#L145), [gtfs.go:170](internal/app/gtfs.go#L170). Related historical cost: [history.go:110](internal/app/history.go#L110), [history.go:142](internal/app/history.go#L142), [history.go:179](internal/app/history.go#L179), [history.go:210](internal/app/history.go#L210). Database pool: [store.go:77](internal/app/store.go#L77); HTTP timeouts: [main.go:72](cmd/server/main.go#L72).

**Evidence:** arrivals checks only that `stop_id` is nonempty, then scans every selected operator's schedule. `stopVisits` traverses active trip stop times even for an unknown stop. Both schedule endpoints build/sort their full result before `paginate`. These loops receive no context and never check request cancellation. A local copied-repository fixture with 3,000 trips × 40 stops and an already canceled request to `/api/v1/arrivals?stop_id=carris:missing&operators=carris&limit=1` still completed processing and returned HTTP 200 in **47.9 ms** under the race detector. This is a behavioral proof, not a production capacity measurement.

Historical list handlers likewise run aggregation over the requested window and collect all grouped results before paging. They use the request context but add no operation-specific deadline or DB statement timeout. A `net/http` write timeout does not itself provide a database-query or CPU-work deadline. One shared pool has eight connections. The 48-hour schedule/30-day history limits and 300 requests/minute/IP mitigate abuse but do not cap concurrent expensive work across clients.

**Impact:** anonymous callers can consume unnecessary CPU/allocations, and canceled schedule requests continue doing it. Expensive historical requests can contend with health/auth/collector database operations. An outage or a precise sustainable abuse rate is **not confirmed**; distributed clients and larger retained datasets increase the concern.

**Safe verification:** only locally, populate the synthetic schedule above, call the handler with `httptest` and a pre-canceled context, and observe it still returns 200 after scanning. Compare `limit=1` with larger limits on the same fixture. For historical cost, inspect a local `EXPLAIN` plan and measure a small disposable dataset; do not benchmark 30-day queries on production.

**Specific fix:** validate the stop/provider against the cached network before schedule traversal, scope arrivals to its operator, avoid constructing discarded trip results, and check context cancellation in long loops. Add endpoint-specific context deadlines, a DB statement timeout, and a small bounded concurrent-work budget for expensive reads. Apply SQL pagination/search where practical or reuse bounded aggregate results; first fix these controls without introducing a general query framework. Verify canceled/unknown-stop requests exit promptly and ordinary valid requests still work.

### F4 — Medium: storage-budget failure prevents logout and key revocation

**Classification:** confirmed security-control availability defect, conditional on storage pressure/measurement failure and an existing credential. Current production budget is enabled; the threshold condition was simulated locally, not imposed on production.

**Affected:** `POST /api/v1/auth/logout`, `DELETE /api/v1/keys/{key_id}`; [auth.go:175](internal/app/auth.go#L175), [auth.go:253](internal/app/auth.go#L253), [storage_budget.go:138](internal/app/storage_budget.go#L138), [storage_budget.go:14](internal/app/storage_budget.go#L14), [storage_budget.go:55](internal/app/storage_budget.go#L55).

**Evidence:** all auth mutations use `Store.exec`, which reserves against the 4.5 GB operational ceiling, including revocation and deletion. Unlike pruning, they do not use the cleanup headroom up to the 5 GB application ceiling. A local fixture with an existing session/key and a fake measurement of 4,500,000,000 bytes rejected both operations: the key remained unrevoked, one session row remained and no clearing cookie was emitted. Logout returns on the DB error before its `clearCookie` call. Measurement failure also blocks these writes by code. Authentication/read paths do not consult this write-budget state, so existing unexpired credentials remain usable.

**Impact:** during storage pressure or measurement failure a user cannot invalidate a suspected stolen credential through the normal API. Clearing only a browser cookie would not invalidate a stolen session. New-login exposure is currently limited by unconfigured Google login, but this will matter when key management is enabled or existing sessions are present. No production credential or storage threshold was altered to prove it.

**Safe verification:** in a disposable schema, create a synthetic session/key, set the store's measurement function to return the operational ceiling, invoke logout/revoke and verify errors plus unchanged credential rows. This creates no large data allocation.

**Specific fix:** distinguish credential issuance from security-reducing cleanup. Reserve a small bounded allowance for deletion/revocation from the existing cleanup headroom without relaxing the user's 5 GB guard. Clear the browser cookie even when server-side logout fails, while explicitly reporting that server-side invalidation was not completed. Define an emergency revocation path/runbook for unavailable measurements and test both threshold and measurement-failure conditions; do not silently report successful revocation.

### F2 — Low: concurrent creation bypasses the active API-key quota

**Classification:** confirmed authenticated quota bypass; dormant for new users while Google login is unconfigured.

**Affected:** `POST /api/v1/keys`; [auth.go:224](internal/app/auth.go#L224), [auth.go:244](internal/app/auth.go#L244), [storage_budget.go:139](internal/app/storage_budget.go#L139), [limits.go:36](internal/app/limits.go#L36).

**Evidence:** `CreateKey` counts active keys before calling `Store.exec`. The write lock protects only the insertion, not the preceding count. Local isolated-schema reproduction: 19 active fixture keys, eight concurrent creates held at the existing write lock until their counts completed, then release. **All eight succeeded; 27 active keys remained despite the cap of 20.** The corrected harness used separate response writers and passed the race detector. An initial shared-recorder harness race was a test defect, not an application race finding.

**Impact:** a session user can exceed their own allocation quota and create additional database records/principal buckets. It does not reveal another user's keys or grant additional scopes. The global application storage guard still applies, limiting this issue's severity.

**Safe verification:** use a disposable local Postgres schema, seed 19 active keys for one synthetic owner, start concurrent `CreateKey` calls and count resulting active rows. Never create production keys for this test.

**Specific fix:** make quota check and insert one atomic operation. For the current single process, include both under the existing lock; a durable database transaction with per-owner locking/serialization is preferable if multiple instances may ever be supported. Retain the scope/name/expiry checks, and add a concurrent integration regression asserting the count never exceeds 20.

### F3 — Low: ZIP expanded-size accounting wraps around

**Classification:** confirmed input-validation/budget bypass at the upstream-file boundary; no public upload path and no demonstrated memory-exhaustion exploit.

**Affected:** [gtfs_archive.go:23](internal/app/gtfs_archive.go#L23), [gtfs_archive.go:54](internal/app/gtfs_archive.go#L54), [limits.go:15](internal/app/limits.go#L15).

**Evidence:** the aggregate is an unsigned integer: `size += f.UncompressedSize64` runs before comparing against 512 MiB. A tiny **618-byte ZIP** with the five required entries (`routes.txt=128`, `stops.txt=MaxUint64-100`, and `trips.txt`, `stop_times.txt`, `calendar.txt` each zero), was accepted by `openGTFS`: addition wrapped to 27 bytes and the aggregate check passed. The test did not expand large data. Per-entry `io.LimitReader`, ZIP integrity checks and later parser rejection remain, so this does **not** demonstrate unlimited decompression.

**Impact:** malformed or compromised upstream archives can bypass the advertised aggregate declared-size gate. Parsing relies on later, coarser limits instead of rejecting the archive before processing. Current CSV/row limits are large relative to the 1,280 MiB configured container; unusually large records or archives deserve separate resource testing. No filesystem extraction occurs, so this is not Zip Slip.

**Safe verification:** locally build a ZIP with `archive/zip.Writer.CreateRaw`, the required entry names and the declared sizes above; call `openGTFS` without opening/decompressing entries. Expected after a fix: immediate rejection. Do not serve malformed archives to the live collector.

**Specific fix:** compare each size against the remaining allowed budget **before** adding, e.g. reject when `f.UncompressedSize64 > maxGTFSExpandedBytes-size`. Add an entry-count limit and measured CSV record/field bounds, while preserving valid official archives. Add an overflow fixture regression and retain the real-feed compatibility checks.

### H1 — Low: missing browser security headers

**Classification:** confirmed deployment observation; defense-in-depth advice, not confirmed XSS or account clickjacking.

**Affected:** HTTPS `/`; [server.go:120](internal/app/server.go#L120), [Caddyfile.fragment:1](deploy/Caddyfile.fragment#L1).

**Evidence:** public HTML/API responses include `X-Content-Type-Options: nosniff` and `Referrer-Policy: strict-origin-when-cross-origin`. The tested HTML has no CSP header/meta, `frame-ancestors`, `X-Frame-Options`, HSTS or Permissions-Policy. HTTP `/` returns a 308 to HTTPS. No attacker-controlled executable HTML sink was found. Cross-site framing of sensitive session actions was not tested; SameSite/Origin protections and unconfigured Google login limit that claim.

**Impact:** less defense against future script injection/unsafe third-party content, framing and first-visit HTTP downgrade. Missing HSTS is not evidence that credentials were sent over HTTP; HTTPS session cookies are Secure.

**Safe verification:** `curl -I https://lisboapublica.rtfpessoa.xyz/` and inspect response headers; inspect HTTP redirect separately. No framing exploit is needed.

**Specific fix:** set CSP `frame-ancestors 'none'` (or the actual intentional embed allowlist) and optionally `X-Frame-Options: DENY`; deploy an application-specific CSP in report-only mode first, accounting for Google Identity Services, OpenFreeMap, MapLibre workers and current inline React styles. Add HSTS on this host after confirming HTTPS operation; do not apply `includeSubDomains`/preload to unrelated hosts without validation. Restrict unused browser features with Permissions-Policy, retaining self geolocation if desired. [OWASP guidance](https://cheatsheetseries.owasp.org/cheatsheets/HTTP_Headers_Cheat_Sheet.html), [CSP](https://cheatsheetseries.owasp.org/cheatsheets/Content_Security_Policy_Cheat_Sheet.html).

### H2 — Informational: supply-chain checks and deployment reproducibility are not encoded in CI

**Classification:** confirmed repository gap; hardening advice, not a demonstrated supply-chain compromise.

**Affected:** [Dockerfile:1](Dockerfile#L1), [Dockerfile:9](Dockerfile#L9), [Dockerfile:20](Dockerfile#L20), [frontend/package.json:1](frontend/package.json#L1), [Makefile:1](Makefile#L1), [generate.sh:1](scripts/generate.sh#L1).

**Evidence:** no tracked GitHub Actions/GitLab/Jenkins/CircleCI workflow was found. Go checksums and npm's lockfile exist; Docker uses `npm ci`, the generator's Go version is pinned, and deployment images use a Git-SHA tag. Node/Go/Alpine base tags float, and `@vitejs/plugin-react` is declared as `latest` (the current lockfile still pins the installed version). The repository has no mandatory dependency/image/secret scan or provenance verification. GitHub branch protection, organization workflows and remote deploy settings were not inspected.

**Impact:** a future vulnerable dependency/base image or uncontrolled rebuild can reach deployment without a tracked automatic security gate. No package tampering was found.

**Safe verification/fix:** inspect the tracked files; add a small CI workflow with read-only default token permissions, tests, deterministic generated-contract check, npm audit, govulncheck, secret and final-image scanning. Pin actions/base images by digest with deliberate updates. Do not expose deployment credentials to untrusted PR jobs or use privileged PR execution. Verify the built binary/image as well as source. These changes are recommendations only; none were made during this audit.

## 3. Suspected or conditional issues not confirmed as application vulnerabilities

### S1 — Application dependency advisories without a confirmed reachable vulnerable call

Source `govulncheck v1.8.0` with Go **1.27.1** and the Go vulnerability database (last modified 24 September) produced **two distinct advisories, no symbol-level call trace**:

| Advisory | Dependency | Scanner/evidence | Assessment / action |
|---|---|---|---|
| [GO-2026-6443](https://vuln.go.dev/ID/GO-2026-6443.json), CVE-2026-84445 / [GHSA-2v4p-qf9q-27wj](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj) | `google.golang.org/grpc v1.84.0`, [go.mod:47](go.mod#L47) | Module and imported-package findings; affected HTTP/2 server/xDS routing symbols have no reported call chain. | App serves `net/http` and does not create a gRPC/xDS server. A remotely reachable panic is **not established** here. **Primary sources disagree:** the maintainer GHSA lists stable **1.84.0 as patched**, whereas the Go advisory range flags it until a 1.85 development patch. The locked version therefore must not be declared unpatched solely from this scanner result. Reconcile the advisory range with the maintainer/installed artifact before changing versions; do not blindly downgrade or adopt a prerelease. |
| [GO-2026-5932](https://vuln.go.dev/ID/GO-2026-5932.json) | `golang.org/x/crypto v0.57.0`, [go.mod:40](go.mod#L40) | Module-only warning about the unmaintained `openpgp` packages; `go list -deps ./cmd/server` contains no OpenPGP package. | Not evidence that the application's actual crypto use is vulnerable. Do not introduce that OpenPGP package; no OpenPGP replacement is needed for existing code. |

`npm audit --json` reported **zero advisories** across 140 dependencies, including development dependencies. This is a point-in-time advisory check, not proof that dependencies contain no vulnerabilities. The exact deployed app binary was also checked in binary mode: Go 1.26.8, with 20 module/package/symbol/wildcard records for these same two IDs. Binary records are not application call chains; source dependency analysis found no OpenPGP import and no gRPC/xDS server construction. The conflicting gRPC ranges and differing scan modes are recorded rather than claiming a clean binary scan. Dashboard Alpine packages were compared with the official 3.23 main security database: 18 installed packages inventoried; 153 database-record comparisons covered 10 split/binary packages across five origins (`apk-tools`, `busybox`, `musl`, `openssl`, `zlib`). Of these, 147 had nonzero fixed versions and six were zero markers; no installed version was older than a recorded nonzero fix. The other eight installed packages had no matching published security-fix records. This narrower check is not a full image/host scan. [Official govulncheck limitations explain binary call-graph limits](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck#hdr-Limitations).

### S2 — Redirects can leave the archive hostname allowlist

[cm_shapes.go:13](internal/app/cm_shapes.go#L13) permits initial HTTPS archive URLs only on the official Oracle object-storage hostname. [main.go:50](cmd/server/main.go#L50) permits up to two followed redirects to **any HTTPS hostname**, without repeating hostname/port/address checks. Default transport can also inherit environment proxy settings.

There is no public endpoint that accepts a fetch URL. Exploitation requires control of an approved upstream response, object/redirect or another upstream trust failure. No such control or private-network request was demonstrated. Fix by enforcing the intended host/port policy on every redirect (or disabling redirects), rejecting URL credentials and validating destination addresses if arbitrary upstream hosts are ever introduced. Use a local fake RoundTripper/redirect server to test the policy without accessing private or third-party targets. [OWASP SSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html).

### S3 — Email is used as permanent account identity

[auth.go:304](internal/app/auth.go#L304) verifies Google claims but discards `Payload.Subject`; sessions/key ownership use email in [auth.go:116](internal/app/auth.go#L116), [auth.go:192](internal/app/auth.go#L192) and [auth.go:254](internal/app/auth.go#L254). Different Google subjects with the same verified email would be treated as the same owner. Actual email reassignment/account takeover was not demonstrated and no real Google credential was used.

Before enabling account management, identify users by `(issuer, sub)` and retain email as a mutable contact/display attribute; plan existing-key migration explicitly. This matters for managed accounts and address reassignment. [Google's ID-token documentation recommends the stable subject](https://developers.google.com/identity/openid-connect/openid-connect#an-id-tokens-payload).

### S4 — `/metrics` scope policy overlaps historical data

[history.go:53](internal/app/history.go#L53) reads retained speed/distance/trip counts; [openapi.yaml:757](api/openapi.yaml#L757) grants this endpoint `read:transit`. A local policy check confirmed a transit-only identity passes. Implementation follows its declared spec, so this is **not a confirmed enforcement bypass**. While public reads are enabled the same transit data is intentionally anonymous.

Decide whether `read:history` is meant to cover all retained aggregates when reads become private. If so, change this endpoint's declared scope or separate live and historical metrics; regenerate both clients and test the boundary. Otherwise document the exception.

### S5 — Runtime isolation, database privileges and secret/error handling require further evidence

The complete Docker subnet is configured as a trusted proxy network; a compromised peer on that shared network could forge the custom client-IP header by calling Go directly. Public clients cannot bypass Caddy's header overwrite based on the inspected files. Prefer a dedicated proxy/app network or narrowly trusted proxy addresses; verify the active topology before changing it.

Authorized container configuration inspection confirmed the external Cockroach DSN selects `sslmode=verify-full`; values were not printed. No cloud/database credential was used to connect or inspect the role's grants. The optional local Postgres profile uses the bootstrap account for the app; review least privilege before reactivating it. Application startup logs underlying database errors ([main.go:35](cmd/server/main.go#L35)); malformed DSN error redaction and operational log access deserve a synthetic local check, not printing real credentials. No real credential leak was observed.

Authorized read-only server inspection is recorded below. Other services were not probed or application-audited; cloud edge firewall, certificate-renewal execution, backup access, complete host package patch status and remote CI access remain unverified.

## 4. Prioritized remediation plan

1. **Patch the public TLS proxy (F5):** verify a patched Caddy/Go artifact, then perform a separately authorized rollout on the shared proxy, preserving all hosted sites. This takes precedence over application/UI hardening.
2. **Protect public-service availability (F1):** reject unknown stop IDs before traversal, scope to their operator, honor cancellation, set query/operation deadlines and cap concurrent expensive reads. Use local representative data to establish safe historical-window/work budgets. Preserve intentional public reads and existing provider quotas. In parallel, review/restrict the additional WUD/CORS host bindings and verify cloud edge controls, especially because WUD mounts the Docker socket; their authentication was not tested.
3. **Restore reliable credential invalidation (F4):** retain bounded cleanup headroom for logout/revocation and explicitly handle measurement failure. Complete the external database privilege review separately; do not relax the application storage guard.
4. **Harden feed boundaries (F3/S2):** fix overflow before addition, verify CSV/entry bounds against real feeds, and enforce redirect destination policy. These small changes protect the collector without new integrations.
5. **Before enabling Google key management (F2/S3/S4):** make the quota atomic, use stable subject ownership, and settle historical scope semantics; retain current nonce/Origin/owner checks. Add the negative and concurrent tests described above.
6. **Add host-specific browser headers (H1):** frame restriction first, then tested CSP/HSTS/Permissions-Policy. Test map workers, third-party tiles and Google UI; avoid policies affecting other hosted applications.
7. **Maintain the supply chain (S1/H2):** reconcile the gRPC scanner/maintainer range disagreement before deciding on an update, retain deployed-artifact scans, pin/update base images and add minimal CI checks. Keep OpenPGP warnings accurately scoped. Retain no-secret logs and restricted deployment credential access.

No remediation was performed during this audit.

## 5. Checks, results and limits

### Performed

- Inspected the versioned OpenAPI contract, generated server/client use, all handwritten backend areas, frontend rendering/key handling, database schema/queries, ingestion/archive/OAuth/transport limits, Docker/Compose/Caddy files, generator/build scripts and tracked CI inventory.
- Read-only independent code exploration covered auth/proxy/rate controls and frontend/feed/input/resource boundaries. Main owned test execution and findings. Independent plan review accepted the audit scope and requested explicit authorization/resource/deployment distinctions.
- `go test -race ./...`: **passed, 2.271 s** without a test DSN; database tests are skipped in that mode. With an explicitly local Postgres DSN: **passed, 4.603 s**, using disposable schemas. Optional official-feed lab fixtures were not enabled; no test requested production/provider data.
- `go vet ./...`: **passed**.
- Eight additional audit tests in a **temporary copied repository**, with disposable local Postgres schemas: **passed, 3.017 s**. They demonstrated quota overflow, canceled schedule processing, ZIP arithmetic overflow and blocked credential invalidation; verified invalid page/offset/duplicate-limit/operator/hour/date inputs return 400; checked `/metrics` scope policy; rejected wrong Google issuer/audience/expiry/email-verification/nonce; verified cross-user key list/revoke isolation and foreign-Origin create/revoke/logout rejection. The Google verifier boundary used synthetic claims, not real login credentials.
- Existing integration tests additionally verified bearer-key scopes, invalid keys, expiry/revocation, hashed storage, session-only management, nonce/Origin, public/private reads, trusted/untrusted proxy IPs and bounded outbound request budgets. SQL inputs are parameterized; no application command-execution or raw HTML rendering sink was identified. React renders API/provider/error/key strings as text; key secrets stay in component state and are not written to localStorage/sessionStorage.
- `npm audit --json`: **zero known advisories**, 140 dependencies.
- `govulncheck v1.8.0`, source mode/symbol level, local Go 1.27.1: the two advisory IDs and reachability limitations are recorded in S1. No vulnerable application symbol call chain was reported.
- Tracked sensitive-file-name inventory found `config/metro.op.env`; inspected values are **1Password references, not credential values**. Git excludes local environment/cloud files. Gitleaks **v8.30.1** scanned 15 commits / approximately 951 KB and reported **no leaks** with full redaction enabled. The first install path failed because the module retains its earlier name; the repository was unchanged. Downloading scanner dependencies through the configured mirror stalled, so the scanner was run with a process-only official Go proxy setting. Filename exclusion/scanner success cannot prove that every possible secret was detected.
- Approximately **14 sequential, low-volume HTTP requests** to the specified host, including repeated checks while correcting the local evidence script. Public `/`/config/health/operators returned 200; anonymous `/auth/me` and `/keys` returned 401; foreign-Origin operators had no permissive CORS headers and key preflight returned 404; `/.env` returned 404. `/.git/HEAD` returns **SPA HTML**, not a Git reference — a 200 alone is not evidence of repository disclosure. Login nonce cookie attributes were verified with its value redacted. No production POST/DELETE, login, key creation or historical-query benchmark was made.
- HTTPS verification succeeded with system-trusted curl; one OpenSSL handshake negotiated TLS 1.3 with chain verification result 0. HTTP redirects to HTTPS with 308. The first Python client failed because its local CA configuration lacked the issuer; rerunning with the system CA bundle succeeded, so this was not classified as a site certificate vulnerability.
- Header findings are based on observed public responses. No live XSS, CSRF, SSRF, SQLi, framing, decompression, exhaustion or authorization exploit payload was sent.

### Deployment controls observed in files

[compose.yaml:33](deploy/compose.yaml#L33) specifies read-only filesystem, 64 MiB noexec/nosuid temporary storage, all capabilities dropped, no-new-privileges, 1,280 MiB/1.5 CPU limits and rotated logs. [Dockerfile:31](Dockerfile#L31) uses UID 10001. The database is on an internal network and the external override disables its default dependency. No Docker socket/host mounts or published dashboard/database port is present in these files. TLS verification remains enabled for upstream data; Metro's added public intermediate certificate is verified against the CA bundle at build time. Global outgoing attempts are capped at 900/rolling minute, including OAuth/redirects; CM/TML budgets and Retry-After cooldowns are separately enforced. These are positive controls; authorized inspection below confirms the current dashboard controls. A writable, uncapped shared Caddy is separately assessed.

The normal Git guard rejected a read-only query of hook/signing configuration; that optional inspection was omitted. No hook/signing setting or security threshold was changed or bypassed.

### Authorized runtime and host inspection

These were passive configuration/process reads, not port probes or changes:

- Running dashboard image is `lisboa-publica:72c8a63336aa09f0d80fe1ac9789535efba0f531`, healthy, zero OOM/restarts. Non-root `dashboard`, non-privileged, read-only rootfs, dropped ALL capabilities, no-new-privileges, no bind mounts or published ports, memory limit 1,342,177,280 bytes. Its configured public/read/auth/rate/retention/storage/proxy settings match the repository; trusted proxy subnet is `172.19.0.0/16`. Both protected deployment environment files are mode **0600**; credential presence was recorded as booleans only. External DSN TLS mode is `verify-full`, without printing its value. The previous local database container is stopped and has no published port.
- Active Caddy admin configuration, read from container loopback, shows compression and the dashboard upstream `lisboa-publica:8080`, with the custom client-IP header overwritten. Admin listener uses default localhost:2019 and is not published. The shared proxy runs as the image's default root user with writable rootfs/config/data, no memory cap, and publishes only HTTP/HTTPS/HTTP3. Caddy is 2.11.4 / Go 1.26.3; F5 covers the confirmed TLS advisory. Restrict shared-proxy privileges/resources where practical without breaking binding/certificate renewal.
- Exact application binary metadata confirms Go **1.26.8**, Linux/ARM64 and dependency versions. Binary govulncheck records the same two advisory IDs discussed in S1. The separate Caddy binary scan has **28 advisory IDs / 340 module-package-symbol records**; this is not 340 vulnerabilities or proof of 28 reachable exploits. F5 is prioritized because the relevant TLS server boundary is confirmed. Other Caddy library/feature findings need reachability and primary-source triage; this audit does not presume its gRPC/xDS/SSH/OpenPGP/Chi/JSON features are in use.
- Dashboard Alpine **3.23.6** installed-package inventory was compared with the official [Alpine 3.23 main security database](https://secdb.alpinelinux.org/v3.23/main.json), using read-only `apk version -t` comparisons: **18 installed packages inventoried; 153 database-record comparisons covering 10 packages across five origins, including 147 nonzero fixed versions and six zero markers; zero installed versions below nonzero listed fixes**. Eight packages had no matching published security-fix records. This does not cover all host/Caddy OS packages, unknown vulnerabilities or a comprehensive image scanner.
- Host is Ubuntu **24.04.5 LTS**, kernel **6.8.0-142-generic**. `ss`, Docker bindings and iptables show SSH 22, Caddy 80/443, and two additional services on **61003 (WUD)** and **8111 (CORS proxy)** bound to all IPv4/IPv6 interfaces. UFW is inactive; host INPUT policies are ACCEPT, DOCKER-USER has no restricting rules, and Docker explicitly accepts the published ports. Cloud-provider firewall rules were not inspected; external reachability/authentication of these extra services was not probed. WUD has a Docker-socket mount, increasing its security importance if its service is accessible/compromised. Review whether these bindings are intended, restrict management/proxy access, and apply restrictions in the Docker forwarding/edge path rather than assuming UFW alone protects published ports. No unrelated service endpoint was contacted.
- `sshd -T`: root login is key-only (`without-password`), public-key auth enabled, password auth enabled for eligible non-root users, interactive auth disabled, forwarding/X11 permitted. No credential brute force or password-strength test; restrict password login/forwarding if not required, preserving an authorized recovery path. These are hardening observations, not proof that SSH authentication is vulnerable.

### Audit limits

- Read-only SSH checks began **only after explicit authorization** for this audit. Existing SSH authentication was used; no cloud/1Password/GitHub credential or external database login was used. Runtime configuration was filtered in memory and secret values were not printed.
- Exact app/Caddy build versions and active container/listener/firewall settings were read; source and app/Caddy binary checks plus a dashboard Alpine main security-fix comparison were performed. No comprehensive image/rootfs/host package scan, cloud firewall/API review, DB-grant/backup review or cloud spending-control review was performed.
- No real Google login, authenticated production session, cross-account credential or provider credential test. Login-related findings are supported by code and local fixtures, with current production enablement clearly stated.
- No port/host scan, other-host security test, brute force, stress/load test, live exploit or production-data/configuration change. No application source, lockfile, generated contract or deployment configuration was modified.
- No assertion that the 5 GB application storage guard is a hard Cockroach cluster-wide quota. No assertion that missing headers prove XSS, imported advisories prove RCE, or a working ordinary request establishes denial-of-service resilience.
- This is a point-in-time application audit with focused source analysis and local tests, not formal verification, a host penetration test or assurance that every vulnerability has been found.

### Final review and acceptance

Independent final review by `quasar-alpha` at `xhigh` recommends acceptance of this audit report, with no remaining report blockers. Main independently confirmed the required sections, evidence qualifications and authorized inspection boundaries. Acceptance concerns the audit deliverable; the listed vulnerabilities remain unresolved. Only this report was added to the repository; no application code or production configuration was changed.
