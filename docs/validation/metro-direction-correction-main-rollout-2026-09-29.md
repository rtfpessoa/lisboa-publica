# Metro direction correction main rollout — 2026-09-29

This dated evidence follows the [local implementation validation](../plans/20260929-metro-direction-correction-validation.md). Current runtime, matching and popup behavior remains documented in the canonical architecture, data, integration and live-popup references.

## Commit gate and final local checks

The normal signed commit `64b1ec4ba45c4dff78693ac7f596bb7870f5be75` contains the complete Metro correction, generated contract, fixtures, tests and canonical documentation. The signature was verified locally. The pre-existing glossary and standalone popup prototype remain excluded.

The first Maat candidate was blocked by 18 structural regressions despite score 97/delta 0. Repairs separated platform revision/station equivalence, forecast merging/conflict identity, unique monotone order matching, local call construction and source-gap handling. No rule, threshold, hook or suppression was changed.

The [accepted normal gate verdict](metro-direction-correction-2026-09-29-maat.json) reports Go score **97**, delta **0**, no structural regressions and **zero suppressions**, for candidate tree `044c44bba5c08963be214e821433f7ccd84a4dc5` against `f9744baa6049095a4959a63a56d51cd13c7ec2a7`. TypeScript is unchecked by Maat. The report marks implementation-simplicity and maintainability coverage incomplete; this is not a claim of complete metric coverage or zero advisory findings.

After all structural repairs, the exact staged-tree export compiled `internal/app` and `internal/api`; the complete `make test` command passed `go test -race ./...` and `go vet ./...`. `make check-generated`, the frontend production build and whitespace checks passed. The known frontend bundle-size warning remains. The earlier 16 Metro live and eight pattern browser cases, including actual local EventSource sockets at desktop/mobile widths, validate the unchanged frontend correction; they were not rerun after the backend-only helper extraction.

## Performed clean-main deployment

The implementation commit was pushed to `main` before the verified server checkout was fast-forwarded and built. Frontend and backend share image `lisboa-publica:64b1ec4ba45c4dff78693ac7f596bb7870f5be75`; its OCI revision matches that commit. Deployment health verification completed at `2026-09-29T10:45:11.936148+00:00`; the scoped station check completed at `2026-09-29T10:46:32.967Z`. The [safe verification record](metro-direction-correction-main-rollout-2026-09-29.json) contains the image ID, preserved-state assertions, backup checks and actual public/browser/scoped observations.

All ten previously active Compose inputs were preserved, followed by one image-only override. The resolved candidate differs only in the dashboard image. Running environment, 1280 MiB memory limit, CPU quota, security, read-only filesystem, mounts and network names match the preceding container. One collector runs. PostgreSQL remained healthy in the same container and `lisboa-publica_postgres_20260928` volume; `lisboa-publica_transport_history` was preserved. Other services were not recreated.

Protected release material is in `/root/lisboa-metro-direction-release/64b1ec4ba45c`, with mode-700 directory and mode-600 configuration, inspections and backups. The consistent PostgreSQL custom dump is 53,526,393 bytes; its catalog was verified. The collector was stopped before the quiescent archive copy: 541 tar members, 21,401,600 bytes, SHA-256 `8c80c0690589ae7f2c399beb7c7cfb618dc27f27ad2519a9746847cf8e84d891`. Tar listing and checksum passed. No restore/rollback was executed. The previous compatible image remains retained.

## Performed production checks and limits

- Container dashboard/database health and public HTTPS health passed; public status and database report `ok`.
- The initial actual unscoped Metro snapshot reported archive `collecting`, 32 vehicles and 15 context-only train records. No confirmed direction or current vehicle link was exposed. Unscoped/route-only frames deliberately omit popup forecast groups; these require station or vehicle interest.
- Eight real Red-line vehicle scopes retained usable predictions and platform provenance. References 23D and 24D had Aeroporto forecasts; 22D, 25D, 26D, 27D and 28D retained both possible destination contexts. These are published forecast contexts, not physically confirmed movement directions. No current journey was fabricated for those vehicle selections.
- The real scoped station `metro:ML11060073` retained São Sebastião/Aeroporto direction choices and six usable unassociated forecasts, all with platform evidence. Both qualified current-direction counts were zero; forecast presence is not a qualified physical fleet count.
- Production pages rendered at 1280 px and 390 px without page errors. Actual browser EventSource initial/fresh-connection resets succeeded through Caddy: 1074.9/667.9 ms at desktop width and 726.9/512.7 ms at mobile width. These four reset measurements do not prove delivery P95, every-update cadence, cursor-resumption behavior or failure-load recovery.
- The startup log sample contained one API write failure reporting a broken pipe to the proxy, with no fatal/panic entry. Health, scoped reads and native resets succeeded. No zero-error or sustained-load claim is made.

Direct Metro still targets 500 ms; Hub positions retain the existing one-second target and shared 900-global/120-Hub attempt budgets. No new upstream calls, budget changes or model activation were introduced. The production model allowlist remains unset. Supported own estimates remain preserved by the implementation and browser fixtures; no available production own estimate, physical timing precision or independently confirmed movement was observed in this rollout.

Post-rollout documentation adds this dated evidence and links from the documentation index, Maat record and deployment reference. This documentation follow-up does not replace the running implementation image. Local Markdown targets and whitespace are checked before its commit; no diagrams changed.
