# Application documentation

Lisboa Pública collects Lisbon transport data, normalizes it into shared entities, and serves a dashboard and a versioned API. This reference describes the implemented application, including unavailable data and the limits of its observations.

The baseline reference was reviewed at `89fb91b`; affected arrival references were updated for `ca4a5be` on2026-09-27. This is a code and fixture review, not a fresh upstream availability audit. External-source research retains its own verification dates.

## Reading paths

| Task | Start here | Continue with |
|---|---|---|
| Run or develop the app | [Project quick start](../README.md) | [Architecture](architecture.md), [domain glossary](../CONTEXT.md) |
| Understand a displayed value | [Data catalogue](data/README.md) | Its [integration](integrations/README.md), [associations](data/associations.md), and [API contract](../api/openapi.yaml) |
| Investigate missing or stale data | [Source references](integrations/README.md) | [Failure and recovery](architecture.md#failure-and-recovery), [time and availability](data/README.md#time-and-availability) |
| Interpret historical metrics | [History and derivation](data/history.md) | [Matching and continuity](data/associations.md), implementation and test links |
| Deploy or configure the server | [Deployment guide](../deploy/README.md) | [Runtime boundaries](architecture.md), [Metro credentials](integrations/metro.md#access-and-collection) |

## Canonical references

- [Architecture](architecture.md): runtime, startup, collection, reads, consistency, persistence, authentication and recovery.
- [Data catalogue](data/README.md): entities, operator coverage, provenance, time semantics and lifecycle.
- [Data associations](data/associations.md): identity matching, enrichment, ordering, rejection and fallback rules.
- [History and derivation](data/history.md): retained facts, aggregation, sampled metrics, retention and limitations.
- [Integrations](integrations/README.md): consumed transport, authentication and presentation sources, with one reference per integration.
- [Domain glossary](../CONTEXT.md): domain vocabulary; implementation details belong in the references above.
- [OpenAPI contract](../api/openapi.yaml): authoritative application operations and schemas. It generates the Go interface and TypeScript client.
- [Deployment](../deploy/README.md): operational instructions and production settings.

## Evidence and maintenance

Current behavior is established by the linked code, configuration, API schemas and existing behavior tests. Integration field inventories describe what this application processes; they are not exhaustive provider schemas. Synthetic examples are identified as such. Source claims not established by the implementation must retain dated primary-source evidence or an explicit uncertainty label.

The existing [source research](research/SOURCES.md), other `docs/research/` notes, [implementation plan](PLAN.md), [validation reports](VALIDATION.md) and [reference-product inventory](reference/INVENTORY.md) are historical evidence. They can predate current functionality or describe alternatives the application does not consume. They are not replacements for this reference.

New features and changes to behavior, fields, matching, persistence, metrics, configuration or integrations must update the affected canonical documentation in the same change. [Project instructions](../AGENTS.md) record this requirement. Check links, code/schema references, diagrams and English prose before declaring an update complete. All code, documentation and comments are in English; the UI supports Portuguese.
