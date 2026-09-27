One blocker remains; I do **not** recommend local acceptance yet.

**P2 — Disabling overlays does not cancel a pending later-page geometry request.** In [VehiclePopup.tsx:24](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/src/VehiclePopup.tsx:24), `showPath` changes only `enabled`, leaving the query key and observer unchanged. TanStack does not abort that in-flight request. Its eventual response can populate retained `path` at line 30 while overlays are disabled, violating the [approved cancellation requirement](/Users/rodrigo.fernandes/dev/lisboapublica/docs/PLAN-cm-selective-paths.md:7).

Reproduction: open with overlays off, advance to page 20, enable overlays while delaying the page-zero geometry response, then disable overlays before releasing it. The [browser cases](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/tests/cm-paths.spec.ts:41) cover successful later-page loading and delayed first-page closure, but omit this transition.

Minimal fix: explicitly cancel the pending geometry query on disable, or change its query identity to detach it; prevent the disabled request’s completion from populating retained path. Add the delayed later-page toggle regression.

The remaining evidence supports the reported gates, including 983.94 MiB RSS and 78/78 controlled browser passes. No separate nonblocking findings.

Read-only review; no edits or tests run. No production acceptance or deployment performed.