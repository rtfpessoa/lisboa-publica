# Application documentation

Lisboa Pública collects Lisbon transport data, normalizes it into shared entities, and serves a dashboard and a versioned API. This reference describes the implemented application, including unavailable data and the limits of its observations.

The baseline reference was reviewed at `89fb91b`; affected arrival references were updated for `ca4a5be` on2026-09-27. The 24-hour position retention, informational age detail and independent permanent vehicle facts were updated on 2026-09-27. This is a code and fixture review, not a fresh upstream availability audit. External-source research retains its own verification dates.

## Reading paths

| Task | Start here | Continue with |
|---|---|---|
| Run or develop the app | [Project quick start](../README.md) | [Architecture](architecture.md), [domain glossary](../CONTEXT.md) |
| Understand a displayed value | [Data catalogue](data/README.md) | Its [integration](integrations/README.md), [associations](data/associations.md), and [API contract](../api/openapi.yaml) |
| Investigate missing or stale data | [Source references](integrations/README.md) | [Failure and recovery](architecture.md#failure-and-recovery), [time and availability](data/README.md#time-and-availability), [backend reporting state](data/README.md#backend-owned-reporting-state) |
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

The [popup and selective-path integration validation](VALIDATION-popup-path-integration.md) records the combined release with the existing stop-arrival collectors.

[Direction boards and independent journey times](VEHICLE-POPUPS.md) describe station selection, full visit pagination, source evidence and current actual-event limitations. Canonical matching, history and integration references above remain authoritative for their respective topics.

Dated delivery evidence for [Metro published routes](research/metro-published-route-2026-09-27.md) and [durable reporting state](research/vehicle-reporting-state-2026-09-27.md) records investigated source behavior and performed checks; use the canonical references for current application behavior.

[Station popup rollout verification](research/station-popup-stability-2026-09-27/VALIDATION.md)
records the normal Maat repair, browser/Go checks and exact-main deployment on 2026-09-27.

## Experimental transport patterns

Read [Metro patterns](metro-patterns.md) for the current experimental model, both forecast functions and unsupported metrics. [Implementation validation](VALIDATION-metro-patterns.md) records checks and limitations; extended live sizing and physical validation remain operational follow-ups.

[Metro patterns gap audit](GAPS-metro-patterns.md) compares agreed decisions with the first experimental implementation and separates missing code from evidence-dependent capabilities.

The local completion follow-up adds route-specific conditions, versioned Lisbon holiday grouping, labeled older/general-context component fallback, unchanged-segment compatibility, conservative mixed-bin calibration and durable bounded MAE/P90/availability/band-support reports. Evidence-backed maintenance revises retained inputs atomically while keeping issued values. Staged normalized observation/prediction capture and experimental own-forecast adapters cover all eight existing operators under the same archive budget. Metro uses ETA transitions; later stages require verified published paths and coherent reported stop-state transitions. Forecast availability depends on actual compatible inputs, and physical validation remains unavailable. See [current behavior](metro-patterns.md) and [remaining live evidence](GAPS-metro-patterns.md).

[Metro live map and popups](metro-live-popups.md) documents current scoped SSE delivery, five-second fallback,
shared reference classification, direction-grouped forecasts, countdown/follow behavior, complete event-free checkpoint recovery and resource limits. [Follow-up validation](validation/metro-association-checkpoints-2026-09-28.md) records the performed local checks and remaining movement/lifecycle work.

[Dated Metro transport assessment](validation/metro-live-release-2026-09-28.md) records the synthetic
Go/Caddy/browser measurements, performed behavior checks and remaining validation boundaries.

[Metro acceptance audit](validation/metro-live-acceptance-audit-2026-09-28.md) distinguishes the completed
positive-only transport checkpoint from mixed recovery replay and physical-calibration work.

[Metro departure calibration preparation](metro-departure-calibration.md) documents the offline collection
contract, separate physical-reference/model-consistency paths, reproducible candidate replay and current live-admission boundary.

[Departure preparation validation](validation/metro-departure-calibration-preparation-2026-09-28.md) records
the synthetic CLI checks and the absence of independent physical observations.

[Metro failure/recovery validation](validation/metro-live-recovery-2026-09-28.md) records the completed
32-client mixed replay, fallback/correction fixes, measured profile and remaining evidence boundaries.

[Metro main release and production verification](validation/metro-live-main-rollout-2026-09-28.md) records
Maat repairs, post-refactor checks, commit dependency closure and production rollout verification.

The [Metro completion follow-up](validation/metro-live-completion-2026-09-28.md) records lifecycle/direction/model-admission checks. The [live popup reference](metro-live-popups.md) describes implemented guards; [model admission](metro-departure-calibration.md) distinguishes supported experimental estimates from unmeasured physical timing.

[Metro completion main rollout](validation/metro-live-main-rollout-2026-09-29.md) records the clean-main image, preserved configuration/volumes, protected backups and actual production health/EventSource checks.


[Metro direction correction rollout](validation/metro-direction-correction-main-rollout-2026-09-29.md) records the normal Maat repair, complete final Go checks, clean-main deployment and actual Red-line forecast/provenance and browser reset observations. Current direction remains distinct from possible forecast direction; consult the canonical live and association references for implemented semantics.

[Metro first-principles plan](plans/20260929-metro-first-principles.md), [accepted review](plans/20260929-metro-plan-review-final.md) and [model research](plans/20260929-metro-model-research.md) preserve the dated design and source evidence. Current implemented semantics belong in [the live reference](metro-live-popups.md), [source reference](integrations/metro.md), [associations](data/associations.md) and [history](data/history.md).

[First-principles validation](validation/metro-first-principles-20260929.md) records actual-source replay, independent review corrections, native/browser measurements and physical uncertainty for the current increment.
