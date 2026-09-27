# Stop arrivals production acceptance

Validated on2026-09-27 at [Lisboa Pública](https://lisboapublica.rtfpessoa.xyz).

Implementation commit `ca4a5be3752f0211de7c70d1bd350c4768178db1` was integrated with the incoming canonical documentation and pushed to `main`. The deployed source revision is **`756d5fe3a36b3207e909cde235292722b51fe080`**. Later evidence-only commits do not change the executable.

The normal pinned Maat Go gate passes at87/delta0, with no critical regressions and zero suppressions. TypeScript remains outside Maat's language coverage; compiler/build and browser tests validate it separately. The absolute95 target remains unresolved. [Local implementation and validation](VALIDATION-stop-arrivals.md) records full PostgreSQL race, deterministic generation, vet/build, browser regressions and the unchanged full-network resource workload after refactoring (1002.09MiB below1024MiB).

## Rollout

The Linux/ARM64 binary was compiled with Go1.27.1 from the clean deployed revision. Built frontend assets and binary hashes match inside the running image. The rollout uses the existing verified runtime base and Compose/external-database setup, preserving environment values, isolation, limits, networks and health checks. Only the dashboard was replaced; all eight other containers remain unchanged. Credential backups remain protected on the server, and the prior image is retained for rollback.

[Deployment summary](research/stop-arrivals/production-validation/deployment.json) and [independent post-rollout verification](research/stop-arrivals/production-validation/deployment-verification.json) record the immutable image and revision. [Build provenance](research/stop-arrivals/production-validation/server-build-info.txt) and [artifact hashes](research/stop-arrivals/production-validation/artifact-hashes.json) establish what was deployed. No credential/environment dumps are included.

## Production checks

- Public health and database status return`ok`; public configuration is preserved.
- Real CM arrivals were demonstrated at Oriente: six rows at`cm:060001`, including three predictions. Partial coverage is shown explicitly. Another sampled stop also returned scheduled/predicted rows during the probe, then briefly returned loading when its local collection expired.
- Carris`carris:1902` returned eight rows including one prediction with an original source clock and validity. Earlier probes correctly labelled expired TML updates stale and retained planned fallback. Fresh predictions depend on usable source updates; no provider-wide continuous coverage claim is made.
- Actual production browser checks passed at1280px and390px: a nearby stop group, buttons, focused arrows, real arrival rows, Escape focus return, no horizontal overflow and no page exceptions. [Browser report](research/stop-arrivals/production-validation/browser.json), [desktop](research/stop-arrivals/production-validation/ui-1280.png), [mobile](research/stop-arrivals/production-validation/ui-390.png).
- Post-rollout health is healthy, OOM false and restart count0. Recorded cgroup peak is984,580,096bytes (938.97MiB) under the1280MiB container limit, with zero memory/OOM events. This runtime measurement is distinct from the local1024MiB test gate.

[Public API evidence](research/stop-arrivals/production-validation/public-checks.json) and [memory counters](research/stop-arrivals/production-validation/runtime-memory.txt) retain the observations. These are dated samples, not forecasts of future source availability.
