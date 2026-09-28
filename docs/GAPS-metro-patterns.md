# Metro patterns: decision-to-implementation audit

Audit date: 2026-09-27. Local base: `53fbe4c347032f770622896cd4273af515665760`.
This is a read-only implementation audit; no new behavior was implemented or deployed.
The first experimental delivery is a subset of the complete agreed transport product.
Closed decision tickets establish agreed requirements, not completed features.

## Remaining implementation work

| Requirement | Current evidence | Remaining work |
|---|---|---|
| Paired official/own evaluation report | [Evaluation](../internal/patterns/engine.go) stores bounded error sums; [views](../internal/patterns/views.go) and [UI](../frontend/src/MetroPatterns.tsx) expose evaluated/pending/lost counts. | Expose mean absolute error bounds, P90 bounds, availability, interval coverage and distinct days/journeys for waiting and onward calls. Retain suitable distributions and support summaries before detail expires; evaluation aggregates currently do not provide P90 distributions. Label proxy agreement explicitly. |
| Older compatible history fallback | `mean` filters the training window and `trim` removes older hot aggregates in [engine.go](../internal/patterns/engine.go). | When recent compatible components are absent, consult retained older compatible aggregates up to effective retention and expose their age and support. More recent samples alone do not implement this fallback. |
| Mixed resolution and profile compatibility | Sampling/bin width enter the [archive profile](../internal/patterns/archive.go); means/calibration require matching profiles. The [topology profile](../internal/app/metro_patterns.go) includes the complete static PlanID. | Combine aligned compatible histogram resolutions at a common effective precision; retain sampling/evidence distinctions. Demonstrate component compatibility across unaffected service versions rather than assuming every full-plan change invalidates every segment. |
| Route-specific service conditions | [recordPatterns](../internal/app/metro_patterns.go) marks the entire receipt disrupted when any line is not Normal. `step` resets all continuity when this global condition changes. | Associate a communicated condition with its actual line and source clock. An unrelated line alert must not classify all journeys as disrupted. Implement explicitly labeled general-context fallback where admissible. |
| Holiday grouping | The UI explicitly states holidays are unclassified; day grouping is weekday/Saturday/Sunday. | Add the agreed holiday calendar with reproducible provenance and appropriate handling of affected groups. |
| Historical revisions/reprocessing | Contradictions revoke affected statistics and preserve issued values; replay is implemented for archive recovery. | A general explicit reprocessing/correction workflow for supported source revisions and retained detail remains incomplete. Define targeted effects and preserve original issued forecasts; never claim reconstruction after detail expires. |
| Other operators | The collector and forecast engine currently integrate Metro only. | Carris Metropolitana next, then one operator at a time, with source-specific evidence, continuity and shared storage accounting. Existing app operator coverage does not establish patterns-collector coverage. |

## Evidence-dependent or deferred capabilities

The current hourly grid contains proxy signal counts.
Operational headway, physical occurrence probability/Wilson 95%, dwell and speed are unavailable.
They require admissible event/coverage or movement evidence; accumulating more ETA snapshots does not establish that evidence.
The first experimental method uses a future official anchor plus sequential arrival-to-arrival components.
Residual progress and recent operational adjustment remain deferred pending admissible inputs and evaluation.
No physical independent reference or accuracy improvement over official predictions has been demonstrated.

Representative weekday/weekend collection, allocated storage growth and effective retention remain operational work.
A nominal seven-day/12-month configuration under FIFO is not a measured capacity guarantee.
No live checks were performed in this audit, respecting the latest local-only instruction.

## Map status

The transport-patterns map has 56 closed decisions and four open tickets:
`medir-volume-historico-metro`, `retencao-agregados-e-capacidade`,
`obter-referencia-metro` and `validar-inferencias-metro`.
The related provider/history map has four resolved tickets within its accepted scope.
The station/direction map has eight closed specification decisions.
Those statuses do not certify every source can supply physical events.
The current main station popup/navigation fixes were preserved during the rebase.

## Recommended order

1. Fix line-specific condition handling and durable evaluation summaries/reporting using local synthetic fixtures.
2. Implement older-history fallback, compatible resolution/version handling and holiday grouping.
3. Review the feature on main before any authorized deployment; collect representative live evidence afterward.
4. Validate Metro availability/calibration and size the archive, then start Carris Metropolitana.

See [current behavior](metro-patterns.md), [performed checks](VALIDATION-metro-patterns.md)
and [release policy](../deploy/README.md#release-policy-and-earlier-experimental-evidence).

## Local implementation follow-up, 2026-09-27

The preceding table is the original audit, retained as dated evidence. The following work was subsequently implemented locally:

| Audit gap | Current local result |
|---|---|
| Comparison report | Durable bounded MAE/P90, paired/unpaired cohorts, point availability, band overlap/containment proportions, dates and distinct association support; API/UI exposure. Same-source proxy labels remain explicit. |
| Older history | Complete-day cold restoration and labeled recent/older, exact/general-context fallback within effective retained support. |
| Resolution/version compatibility | Sampling identities remain separate; compatible bin widths preserve conservative edges. Identical published segment geometry/endpoints allow reuse across unaffected plan changes. Ambiguous/missing evidence rejects reuse. |
| Route conditions | Only the actual line’s communicated condition affects its groups; unknown/conflicts remain unknown; general-context fallback is disclosed. |
| Holidays | Versioned mandatory national/Lisbon civil-day rules, local-time handling, reproducible primary references and tests. This is not an operator service calendar. |
| Reprocessing | Exclusive evidence-backed revisions, stale/expiry/topology guards, bounded replay, one-manifest publication and preserved issued forecasts. Larger-than-limit revisions are explicitly unavailable. |
| Other operators | Staged capture adapters for all eight existing operators reuse normalized live snapshots and share FIFO/accounting. Metro remains the only own-forecast adapter; own-arrival/event adapters for CM and later operators remain subsequent implementation work. |

No independent physical reference, representative live week, observed storage-growth guarantee or physical accuracy improvement has been produced by local synthetic tests. The four measurement/validation map tickets therefore remain open. Residual-progress/recent-adjustment methods, physical probability/Wilson, dwell and measured Metro speed remain evidence-dependent. The project must not claim every original product capability is complete merely because the first Metro release and staged capture are implemented.

## Local operator-adapter follow-up, 2026-09-28

The preceding audit and 2026-09-27 follow-up are preserved historical evidence.
The subsequent local implementation adds adapters for all seven later stages,
operator-scoped API/UI selection, both forecast functions, official cold-start
fallback, exact published paths/service contexts, bounded published-stop proxy
inference, component-only remaining travel, source expiry/loss handling, durable
reports/calibration, dictionary deduplication, atomic failure recovery and
operator-specific evidence-backed maintenance. No new position/shared-feed poll
was added. These adapters are conditional on compatible actual source fields;
their existence is not evidence that every live operator currently supplies them.

All code gaps listed in the original implementation table now have a local
implementation. Evidence-dependent/deferred capabilities above remain explicitly
unavailable. The four map tickets still require representative live collection,
capacity measurement or an independent reference; local synthetic tests cannot
close them. No deployment, server request or live provider call occurred in this
follow-up. The next measurement phase follows review on main and authorized
release, preserving the local-only restriction currently in force.
