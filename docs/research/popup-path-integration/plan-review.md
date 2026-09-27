**Recommend acceptance of the revised plan. No plan blockers remain.**

The [gesture fix](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/src/App.tsx:134) addresses the original blocker: threshold crossing remains recorded, multipointer gestures suppress dismissal, and cancellation clears tracking. The [saved evidence](/Users/rodrigo.fernandes/dev/lisboapublica/docs/research/popup-path-integration/gesture-regressions.txt) reports all nine targeted checks passing, including returning drags and Chromium touch pinch.

The reviewed merge points preserve both feature sets: native arrival matching and expiry, anchored nearby-stop navigation, frozen planned vehicle references, exact CM paths, original GPS coordinates, and screen-pixel separation. The legacy-cache upgrade uses the existing collector and persists completed empty attempts. Combined evidence retains both resource workloads and reports 993.80 MiB RSS, passing PG/CR race checks, generation/build/vet, and Maat 89 without critical regressions or suppressions.

The deployment plan is appropriately narrow: off-host Linux/ARM64 compilation, matching assets, the pinned runtime, retained rollback image and protected environment backups, VERSION-only environment change, and existing Compose isolation and limits. Keep final **87/87**, separate independent final acceptance, and ancestry verification against current `origin/main` mandatory before the fast-forward push and deployment.

Nonblocking findings: none.

Read-only review; no edits, tests, network access, or deployment performed. **No production acceptance granted.**