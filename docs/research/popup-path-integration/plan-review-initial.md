**Recommend changes before plan acceptance: one blocker.**

[App.tsx:141](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/src/App.tsx:141) distinguishes clicks from drags using only the pointer’s final displacement. A drag that moves 30 pixels away, returns to its starting point, then releases closes the popup. This violates the requirement that dragging preserve the stop group.

Minimal fix: retain whether movement crossed the drag threshold, suppress dismissal during multipointer gestures, and clear tracking on cancellation. Extend the browser gate with a returning drag and mobile pinch, alongside the blank-click dismissal check. The [existing regression](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/tests/stop-navigation.spec.ts:30) covers wheel zoom and a one-way drag.

Otherwise, the integration plan is comprehensive and appropriately scoped. The reviewed merge points preserve native arrival matching/expiry, frozen planned vehicle references, exact CM paths, original GPS coordinates, screen-pixel separation, and both resource workloads. The legacy-cache refresh and serialized empty-attempt marker address rollout without clearing data or adding polling.

The proposed deployment preserves the pinned runtime, matching assets, rollback image and protected environment backups, existing Compose isolation and limits, with VERSION as the sole environment change. Keep successful final gates and separate final review mandatory before push; ensure `e99acd3` is an ancestor of the pushed head so the update fast-forwards.

Nonblocking findings: none.

Read-only review; no edits, tests, network access, deployment, or production acceptance performed.