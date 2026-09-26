# Maat cleanup

The pinned Maat bundle and normal global Git hooks remain enabled. No thresholds were lowered and no rules were ignored. Maat analyzes the Go source in this repository; TypeScript is validated separately by its compiler and browser suite.

The initial Go score was **47/100**. After extracting GTFS archive/table handling, authorization, verified Google claims, Metro platform predictions, ingestion normalization/enrichment, schedule rendering, filter parsing and null-safe fleet sorting, the score is **68/100**. Exported API documentation and policy constant names were improved as well. The configured absolute threshold is **95**; that absolute scan **does not pass**.

Remaining findings include complexity/maintainability, return-count limits on Go error guards, parameter counts at existing transport interfaces, module-cohesion heuristics, and exported-method test-name heuristics despite HTTP integration coverage. These are recorded as unresolved advisories, not represented as a clean Maat result. Further structural changes should be weighed against the requested small scope and avoidance of unnecessary abstractions.

Validation after cleanup: Postgres race tests, Cockroach race tests, Go vet, deterministic OpenAPI generators, TypeScript production build, and four Playwright dashboard checks. Deployment-specific proxy and configurable-retention behavior have focused HTTP/database regression checks. See VALIDATION.md for the latest results.

The first normal commit attempt encountered a gate implementation error: an existing empty initial Git tree was sent to Maat as a diff baseline, which produced “no modules found.” The gate's `_base_commit` now recognizes an empty tree and uses its existing first-snapshot policy, still analyzing the complete staged candidate. The normal hook remains installed. This narrow repair and a regression test are local changes in code-factory's `git/scripts/maat_gate.py` and `scripts/test_maat_gate.py`; all22 gate tests pass. Neither Maat's pinned binary nor thresholds were changed.
