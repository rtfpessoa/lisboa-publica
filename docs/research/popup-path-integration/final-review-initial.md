**One documentation requirement blocker remains; I cannot yet recommend final LOCAL acceptance.** No runtime correctness, merge preservation, safety, or resource blocker found.

**P2:** [tml-hub.md:70](/Users/rodrigo.fernandes/dev/lisboapublica/docs/integrations/tml-hub.md:70) still says CM consumes only route/trip/shape/optional-vehicle tables. The [implemented parser](/Users/rodrigo.fernandes/dev/lisboapublica/internal/app/cm_patterns.go:45) also requires stops and stop-times. The shared GTFS inventory omits CM `pattern_id`, and the [native vehicle field table](/Users/rodrigo.fernandes/dev/lisboapublica/docs/integrations/carris-metropolitana.md:33) omits its new mapping. This conflicts with [AGENTS.md:27](/Users/rodrigo.fernandes/dev/lisboapublica/AGENTS.md:27).

Minimal fix: update those inventories and the outdated restriction; add a labelled synthetic exact-pattern/fallback example. Preserve the distinction between CM path validation and a scheduling engine.

The recorded evidence supports 87/87 browser checks, PG/CR race passes, generation/build/vet, Maat 89 without critical regressions, and 993.80 MiB RSS with both workloads retained.

No separate nonblocking findings. Read-only review: no edits, tests, network access, or deployment performed. Main retains final acceptance authority; no production acceptance granted.