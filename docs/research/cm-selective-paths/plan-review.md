No plan blockers found. I recommend accepting the plan for implementation.

It addresses qualified pattern identity, agency/plan/line matching, order-independent validation, loops, atomic pattern/geometry revisions, pinned pagination, cancellation, and explicit fallback without selecting another path. Polling, overlays, fixtures, and resource caps remain unchanged.

Production acceptance still requires the [integrated gate](/Users/rodrigo.fernandes/dev/lisboapublica/docs/PLAN-cm-selective-paths.md:34): production parser/cache/serialization/refresh, exactly 1,621 patterns and 57,286 visits, the full unchanged workload, and RSS ≤1,024 MiB. The prototype’s 972.83 MiB PASS does not satisfy that gate. Generation, Go/TS, PostgreSQL/Cockroach race checks, desktop/mobile browser checks, previous 72 regressions, and normal Maat remain mandatory.

Nonblocking integration advice:

- Resolve CM patterns without applying the aggregate `StaticData.PlanID` comparison in [vehicle_calls.go](/Users/rodrigo.fernandes/dev/lisboapublica/internal/app/vehicle_calls.go:181); the plan already recognizes CM’s empty aggregate plan.
- Give the highlight its own MapLibre update effect. The existing [map updater](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/src/Map.tsx:25) depends on vehicle heartbeats, and the route effect performs `fitBounds`.

Review was read-only; no files changed or tests run.