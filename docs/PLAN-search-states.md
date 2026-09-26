# Visible search and explicit unsupported features

Main owns planning, changes, tests, fixes and acceptance. Existing real quasar-alpha/xhigh independent plan/final reviewers only; source/code research is read-only.

## Verified diagnosis

Actual production search Roma returns a stop and no page errors. Its flex child collapses from115px at900px height to18px at720px desktop/667px mobile, clipping all content behind padding. Sidebar overflow exists, so keep results from shrinking and use the existing scroll containers. Disabled search queries with no selected operators currently leave unexplained section headings.

Completion percentage and observed frequency are intentionally not computed by the current backend; imported schedules/position samples do not prove complete operation. Correct prior overbroad copy: official CM documents additional service/observed-arrival APIs that are not integrated/validated here. Say **not supported in this application**, not unavailable from every provider. Do not silently replace observed frequency with planned timetable frequency. Research: official CM API v2 README, existing fleet-field evidence and official TML Metro inference adapter.

Fleet stations mean vehicle depot allocations, not passenger stops. Current integrated feeds lack a verified vehicle-to-depot crosswalk. Label the tab “Estações de recolha” and explicitly unsupported; keep its explanation discoverable independent of observation loading/errors.

An effective Metro-only selection (including a Metro route filter even when other operators are selected) is fundamentally ineligible for sampled speed because its positions are inferred. Other operators support sampled speed when two valid reported observations exist; empty intervals do not make them unsupported. Mixed selections must explain Metro exclusion and still chart supported operators.

## Changes

1. Scope CSS flex-shrink0 to search results; retain bounded list overflow and scrolling sidebar. Add in-panel no-selection message without API calls/stale results; keep initial/loading/error/successful-empty distinct.
2. Keep completion/frequency disabled but show visible “Não suportado” badges and persistent prominent explanation tied by aria-describedby. Update live expanded metrics/source copy consistently and preserve trips-detected ranking.
3. Rename depot tab clearly, show badge and persistent explanation that no operator is supported in this application. Keep the explanatory panel clickable (aria-disabled is inappropriate if clicking shows details), distinguish passenger stops from allocations, show this independent of fleet loading/error.
4. Pass explicit speed capability message to Trend using the effective route filter; estimated vehicle details use the same unavailable wording. Metro-only unsupported takes precedence over loading/empty/erroneous speed values and appears in live metric, historical metric/chart, modal, speed ranking and traffic panel. Preserve volume, existing errors, partial data and other operators' empty states. Mixed selections show explicit Metro exclusion without suppressing supported data. No provider-general capability framework or generated API change needed for this known data provenance.

## Validation and delivery

Add failing browser regressions before changes: actual result bounding box/row hit-testing at720px desktop and667px mobile; click result opens details, scrolling accesses long lists. No-selection search fires no route/stop calls and gives in-panel guidance. Visible unsupported history/depot labels, depot explanation during pending/error. Metro-only speed live/history/modal/traffic/ranking unsupported even with estimated volume points; reported operator empty remains temporary, mixed selection charts reported speed and explains exclusion. Include a mixed selection filtered to a Metro route and an estimated Metro vehicle detail. Preserve existing search error/loading, tabs/defaults/fleet/mobile/arrival checks. TS/Vite build and serialized targeted/full browser suites; no unnecessary Go tests for unchanged backend. Normal Maat/signing hooks, commit/push, existing Compose/external database/Caddy deployment. Real HTTPS search at short desktop/mobile, labels/speed/traffic and resource health before independent final acceptance and main confirmation.

Out of scope: new upstream endpoints/integrations, depot inference, planned-frequency replacement, data storage/retention/API architecture changes. Record the separate CM service-metrics integration opportunity as an explicit follow-up decision, not a claim that upstream data cannot exist.


## Primary source clarification

[CM API v2](https://github.com/carrismetropolitana/api/blob/v2/README.md) documents /metrics/service/all and observed arrivals, while the current application imports positions/schedules and does not validate or collect those metrics. Prior statements that every provider inherently lacks operational metrics were too broad. This round makes current application support explicit. [TML Metro adapter](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/apps/pt-tml-ml-api-fetch/src/index.ts) infers positions and publishes null speed. Existing fleet evidence establishes no verified vehicle-to-depot join in the integrated data.

Plan review initially requested effective route/entity capability checks. Main added mixed-operator Metro-route and estimated-vehicle detail regressions; the existing real quasar-alpha/xhigh plan reviewer then recommended acceptance. Before changes, eight regression cases reproduced the defects; the single-point graph check was corrected to assert a visible dot rather than an empty line group. Targeted13browserchecks passed20.2s; final local suite/deployment review follow.
