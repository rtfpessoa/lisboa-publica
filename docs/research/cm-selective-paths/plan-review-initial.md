Request changes before plan acceptance. One blocker remains.

1. **Make the integrated resource gate prove that CM enrichment is exercised.** [The plan](/Users/rodrigo.fernandes/dev/lisboapublica/docs/PLAN-cm-selective-paths.md:32) correctly requires production integration, but the current harness can pass without testing it:
   - The prototype index lives outside the production cache ([test](/Users/rodrigo.fernandes/dev/lisboapublica/internal/app/continuity_resource_test.go:26)).
   - Churn vehicles carry no pattern identity ([fixture](/Users/rodrigo.fernandes/dev/lisboapublica/internal/app/continuity_resource_test.go:121)).
   - Concurrent calls only assert HTTP 200; cancellation tests vehicle listing rather than CM calls ([reads](/Users/rodrigo.fernandes/dev/lisboapublica/internal/app/navigation_resource_reads_test.go:15)).

   **Minimal change:** explicitly require the integrated gate to retain the production index through refresh and serialization, assert all **1,621 patterns / 57,286 visits**, and exercise identified CM vehicles with complete calls, opted-in first-page geometry, pinned subsequent pages, and calls cancellation. Preserve every existing fixture, workload and cap. The supplied **972.83 MiB prototype PASS** remains feasibility evidence; main must supply **production RSS ≤1,024 MiB** at final review.

Nonblocking advice:

- Make pattern/geometry publication and fallback atomic within each immutable static revision. `retainGeometry` currently retains only shapes and their timestamp; extend that handling so retained geometry cannot acquire associations from another import.
- Define bus-overlay re-enabling on a later calls page. Include geometry opt-in in query identity, obtain geometry from page zero of the pinned revision, and prevent late responses from restoring cleared highlights.
- Resolve CM plan/agency from the explicit pattern namespace and match the published line through `routes.txt.line_id`. Keep that separate from the aggregate CM catalog’s empty `StaticData.PlanID`.

The remaining stated requirements are adequate: interleaved and loop validation, no inferred operational claims, selective requests, unchanged network overlays and polling, Go/TS generation, PG/CR race checks, desktop/mobile browser coverage, the 72 prior regressions, storage limits, and normal Maat.

Resubmit after making the gate conditions explicit. Read-only review; no tests run.