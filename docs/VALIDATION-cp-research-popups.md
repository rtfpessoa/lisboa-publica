# CP investigation and popup validation

26 September 2026. Local changes only; no new CP source integration, application publication or production deployment.

## Scope and behavior

The [CP investigation and correction plan](research/CP-REALTIME-COVERAGE.md) explains intermittent upstream positions, missing service-detail fields, official sources that are not currently consumed, and remaining access/freshness/identity questions. [Sanitized evidence](research/cp-realtime-coverage/official-evidence.json) contains no credential values. No Mover Lisboa API is an application dependency. No further comparison-site API requests were made after the user's explicit clarification.

Search results and map layers now have visible close buttons. Search, detail panels, map layers and the mobile operator drawer dismiss when a pointer is pressed outside; search/details/layers also support Escape. Existing chart, account and source dialogs retain close buttons, backdrop dismissal, Escape and modal focus handling. Inside interactions preserve an open panel. Map layers retain their checkbox settings after closing.

Closing route details preserves the selected route filter. A small visible sidebar control reopens details or explicitly removes the filter. Reopening details hides the mobile sidebar so the details remain usable. Historical ranked routes still open their live detail view. Provider selection, original-time last-known behavior, overlays, auth, storage, upstream budgets and backend/API contracts are unchanged.

## Checks

- TypeScript and production Vite build: PASS. [Build log](research/cp-realtime-coverage/popup-build.txt). The existing map chunk size advisory remains; no bundling changes were made.
- Affected browser suite: **36/36 PASS**, 1.3 minutes. [Browser log](research/cp-realtime-coverage/popup-browser.txt). Runs use fixture APIs and a blank map base style, with desktop/mobile close buttons, outside clicks, Escape, inside interactions, preserved operator/layer selection, route-filter removal/reopening and historical ranking navigation, including keyboard compact navigation after a station selection.
- Existing provider continuity and correction regressions: included in that same passing run. Full official geometry fixture: 2,261 variants / 607,609 points, both desktop and mobile. No worker/page errors, unchanged geometry across operator refreshes, measured main JS heap approximately 230 MB; first geometry completion below 1.9 seconds. These are browser fixture measurements, not production server memory claims.
- Whitespace check: `git diff --check` PASS. Runtime source search found no Mover Lisboa API references in application/backend/deployment sources.

The complete authenticated backend/dashboard acceptance suite was not rerun: no backend code or contract changed, and its local database/auth fixture was not started for this task. An early unfiltered browser invocation reached those fixture-dependent tests and was stopped; it is not acceptance evidence. Earlier popup iterations exposed and fixed route-filter dismissal, mobile sidebar obstruction and ranked-route navigation regressions. A later rerun exposed a paused-clock test race after the fifth response: React Query's scheduled notification had not been flushed. Main corrected the harness to advance the paused clock while waiting for actual error text, retaining the five-attempt and ten-second no-further-retry assertions. [Five consecutive focused repetitions](research/cp-realtime-coverage/popup-retry-harness.txt) pass; product retry behavior was unchanged. Only the final logs above establish the passing result.

## Independent review

Existing real `quasar-alpha` / `xhigh` reviewers perform review only; main owns research, implementation, testing and all corrections. Plan review recommends acceptance of the revised CP research/correction plan and found no remaining reviewed popup code blockers after main's fixes. Final independent review recommends acceptance of the CP research/correction plan and local popup implementation, with no remaining blockers. Main reviewed the source findings, all selection entry points, corrected keyboard/mobile interactions, preserved filter/layer semantics, fresh passing browser/build artifacts and scope limits, and accepts this local work. Direct CP integration, publication and deployment remain separate work.
