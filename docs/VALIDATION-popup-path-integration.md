# Popup/path integration release

September 27, 2026. The user authorized commit, push to main and deployment after local popup/navigation and selective CM path acceptance. Main implements, tests, fixes and makes acceptance decisions. Independent reviewers use quasar-alpha/xhigh read-only.

## Integration plan

Preserve both bec308a (reviewed popup/navigation/CM paths) and origin/main e99acd3 (deployed requested-stop arrival collection and nearby-stop navigation). Merge the OpenAPI schemas and regenerate both clients. Keep station arrival availability/expiry and nearby-stop keyboard/focus behavior; retain frozen vehicle navigation, exact CM paths, screen-pixel vehicle displacement, mode icons and grouped warnings. Move visit-capacity compaction into the relocated trip connector, retaining the new arrival-timing representation. Keep both workloads in the combined resource gate without reducing caps or fixtures. Update canonical references, then run combined browser, Go/race/database, generation/vet/build, normal Maat and independent final review before push/deploy.

## Verification

Final Postgres full race PASS (28.052s); final Cockroach full race PASS (239.885s). Generated contract parity, production TypeScript build and go vet PASS. The combined production resource envelope PASS (1042071552 bytes RSS / 993.80 MiB below 1024 MiB): all 1621 CM patterns / 57286 visits, all eight operators, 64 revisions, 20k history backlog, all 1000 CM rows/tick with original metadata plus independent pattern strings, overlap refresh, concurrent frozen calls/geometry reads, 64 CP snapshots and two 16 MiB decodes remain present. Added arrival-cache saturation, pinned result leases, evicted readers and response/mapping workspace also remain live. No caps, fixtures or retention were reduced.

Normal Maat gate PASS: Go score 89 (+2), critical regressions empty, no suppressions; TypeScript remains outside Maat coverage and is checked by build/browser.

An initial combined browser run passed 82/84 and found outside-click dismissal broken for station details after merging the existing map-pan exception. Main corrected the interaction: an outside map click closes details; dragging or zooming preserves the stop group. Six targeted desktop/mobile dismissal, keyboard/nearby, overlap-target and stale-ETA regressions passed (17.9s). The complete 84-check repetition passed (3.1m); after the independent gesture correction, the final 87/87 repetition passed (3.5m). Revised independent final review recommends local acceptance without blockers; main confirms the plan and requirements are met.

Evidence is under [combined logs](research/popup-path-integration/). Historical local evidence remains under docs/research; it is not relabelled as combined or production evidence.

## Legacy CM cache upgrade

Main found the deployed CM cache was fresh but lacked the new index, which would otherwise delay complete paths until the six-hour reuse TTL. The existing static-cache eligibility check now treats an index/attempt-free CM cache as legacy, so the normal scheduled collector refreshes it before reuse. No production data is cleared, no new network loop is introduced, and all existing budgets apply. A completed import with no verified path records a CMPathError; verified, empty and rejected attempts survive serialization/restore and resume normal TTL. Geometry failures retain their existing retry/reuse behavior. Postgres/race, vet, the normal Maat gate and the unchanged resource gate passed after this bounded upgrade fix. The final Cockroach full-race repetition also passed (239.885s).

## Independent plan correction

The first quasar-alpha/xhigh integration plan review found one blocker: final pointer displacement alone could classify a returning drag as a click and close the stop popup. Main records whether a pointer ever crossed the drag threshold, marks multipointer gestures non-clicks, and clears tracking on cancellation. New browser regressions cover returning drags at desktop/mobile widths and a real Chromium touch pinch. No backend or resource workload changed in this correction. Revised independent plan review recommends acceptance without blockers. The final 87/87 browser gate passed (3.5m). Separate revised final review recommends local acceptance; main confirms the requirements and releases the integrated candidate for commit/push/deployment.

## Independent final documentation correction

The first final reviewer found no runtime/merge/safety/resource blocker, but required the shared GTFS inventory and native CM field table to reflect stops, stop-times and explicit pattern mapping. Main corrected those canonical inventories, removed the obsolete route/trip/shape-only restriction and added a labelled synthetic exact-match/loop/fallback example. CM path validation is explicitly distinguished from scheduling. No code or test inputs changed; a fresh independent final review follows.

## Final local acceptance

The separate fresh quasar-alpha/xhigh final reviewer recommends local acceptance without blockers after the documentation correction. Main confirms preservation of both feature sets and all required gates. [Final review](research/popup-path-integration/final-review.md). Seven affected canonical documents have no missing local file links; both Git whitespace checks pass. Production acceptance will follow the actual rollout checks.
