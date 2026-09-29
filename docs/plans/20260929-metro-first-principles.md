# Metro service from first principles — proposed implementation plan

Date: 2026-09-29. Status: revised after independent plan review; accepted after revision recheck. The user delegates implementation following accepted reviewer feedback. This document defines the desired model independently of existing runtime admission rules. Existing code is evaluated for reuse only after the contract below is established.

## Product contract

The map shows line geometry, station families and source-backed estimated train markers. Source acquisition targets 500 ms for direct waits and 1 s for Hub positions; rendering can evaluate an admissible estimate every 500 ms. These are separate clocks and do not promise new upstream coordinates every tick. No browser request fan-out.

A vehicle popup identifies the published reference, line and operational direction, highlights the next supported visit, and lists the entire selected published path in travel order. Passed/current/future are progress classifications, not assertions that every displayed earlier station was visited. Unknown origin or short-turn ambiguity stays explicit. Every visit contains separate arrival/departure cells for official predictions and our estimates, with historical occurrence estimates when supported. Missing values are shown as unavailable, never as zero or invented actual times.

A station popup aggregates its validated platforms, offers a tab per line/direction, and lists upcoming trains plus unassigned predictions in arrival order. A future return forecast can exist independently of the current active journey. An unassigned forecast never creates a new map train. Own and official estimates remain independently available. An anonymous row keeps a stable identity across ordinary ETA updates.

Estimates are useful without pretending to be physical measurements. Display estimated operational direction when supported, possible directions when ambiguous, and unknown when no usable evidence exists. Do not suppress every live link merely because independent physical direction calibration is unavailable.

## Vocabulary and identity

- Source reference: provider-published train label scoped by provider, line and service continuity; not a permanent physical unit.
- Operational journey: one active line/path/direction episode for a reference, with a stable application identity and explicit lifecycle.
- Forecast context: a published prediction under destination/path/time; it may describe a later journey and is not current allocation.
- Visit: an ordered station occurrence, qualified by path version and visit index. Repeated stations retain separate visits.
- Track progress: metres along validated oriented geometry, with uncertainty and source/model provenance; not raw latitude order.
- Operational direction: a supported estimated orientation; physical confirmation is a separate provenance class.
- Occurrence estimate: inferred arrival/first movement with evidence window. Countdown expiry is not an occurrence.
- Order constraint: supported ahead/behind relationship for journeys on a common downstream corridor. It is not complete fleet enumeration.

Canonical glossary updates must isolate task-owned additions from the user's pre-existing CONTEXT.md edits.

## Sources and joins

Use one server-side source adapter per upstream publication, one versioned topology and one service estimator. Both popup consumers and map links use its same classification.

| Source | Acquisition | Role and limitations |
|---|---|---|
| Direct OAuth token | Cache until provider expiry with margin | Server-only credentials; every acquisition counts against budgets |
| Direct tempoEspera/Estacao/todos | Serialized 500 ms minimum starts | All platform slots, original hora, published reference and destino, absolute arrival=hora+wait; keep anonymous slots and raw evidence |
| Direct estadoLinha/todos | Separate slower health lane, proposed 30 s | Service conditions; a state-fetch failure must not discard independently fresh valid waits |
| Direct infoEstacao/todos + infoDestinos/todos | Startup/cached catalogue; daily conditional refresh where supported | Published station/destination crosswalk; retain version/hash, original fields, exact GTFS legacy codes; unknown destination remains unknown |
| Hub vehicles/positions | Serialized 1 s minimum starts | Preserve published direction_id/pattern_id/shape_id, bearing provenance, trip/stop context and model processing/publication created_at with unknown underlying input age; all Metro positions remain estimated |
| Hub plans and discovered normalized GTFS | Existing bounded static lane | Exact legacy-code station join, parent families, ordered variants, shape geometry, trip/pattern direction and arrival/departure schedule priors |
| Direct interval catalogues | Optional cached prior only | Not a live fleet count; encoding and line/segment limitations preclude treating headway as complete inventory |
| Hub vehicle-specific ETA/GTFS-RT | Defer recurring extra acquisition | Previous inspection found missing original clocks/aggregation loss. Do not add it unless it contributes independently admissible information |

Earlier authenticated evidence dated 2026-09-29 established 24 destinations, 50 exact station-to-GTFS legacy joins and interval catalogue caveats. Inspect these actual retained responses rather than treating old documentation as newly verified facts. Preserve exact platform provenance; S26O/S27O are opaque platform identifiers, not direction labels. Exact equivalent predictions may merge; differing clocks/instants remain scoped alternatives.

The Hub direction/trip is a model context, not independent proof. Validate plan/route/path together; do not count its coordinate, bearing and trip as three independent witnesses. A destination catalogue maps a forecast endpoint, not by itself a current vehicle direction.

## Acquisition, delivery and budgets

Preserve the shared 900 global/120 Hub attempts per rolling minute, under the user's 1000 ceiling; these are local caps, not provider guarantees. Account for token, retry, redirect and catalogue attempts before dispatch. Direct waits at 2 Hz use about 120 attempts/minute; Hub positions at 1 Hz use about 60; status at 30 s uses about two. Other operators already sharing these lanes must be included in the total. No per-line, per-train or per-popup upstream requests.

Use finite deadlines, no overlapping cycles/catch-up bursts, Retry-After cooldowns and bounded adaptive 1/2/5 s degradation. Report original source age, receipt age, duplicate fraction, changed-source cadence and delivery lag separately. Polling an unchanged publication does not make it new training evidence.

Keep a combined scoped backend snapshot and native SSE: full reset on connect/reconnect, revision-based complete coalesced frames, no unbounded queues, independently bounded stream admission. Client fallback no faster than five seconds while SSE unavailable; cancel fallback on a successful reset and discard late reads. Static geometry is fetched/versioned separately. Client countdown and interpolation ticks are local and do not renew freshness or create inferred events.

## Estimated service reconstruction

1. Build exact station families and oriented path variants from catalogue/GTFS. Validate axis projection, ambiguity at crossings, monotone station placements and shape version. Do not assume shape_dist_traveled uses metres; compute geodesic metres locally.
2. Preserve each original source update and revisions. Freeze direction continuity on duplicate clocks, invalid coordinates, incompatible plan/path, gaps or ambiguous station projection. Three advancing position-model publications with two consistent displacements exceeding their uncertainty envelope can support an estimated model-motion direction. Hub processing clocks are not original direct input clocks; record that dependency and unknown input age. They cannot renew direct evidence, create independent training samples or prove physical motion. A direct-clock-admitted chain is a separate evidence class.
3. Treat fresh exact provider model context as a candidate orientation, corroborated where available by source-axis progression and coherent multi-station direct arrival sequences. Resolve competing contexts with explicit support and persistence; never minimum ETA, row order or rejection of the other candidate alone. If only model context supports the direction, label it source-estimated with lower confidence. Different destinations can share a direction and differing origins can share a downstream path.
4. Maintain one current journey per line/reference. Suspend uncertain orientation rather than selecting both. A supported opposite movement closes the old episode and starts a successor atomically. A supported final-station occurrence can close the old journey, but terminal proximity or anticipated reversal does not activate the successor.
5. Determine the next visit from along-path position/progress with uncertainty and usable station evidence. Reject unsupported backwards jumps. Earlier path visits remain unknown when no continuity establishes that the train passed them.
6. Detect estimated stop arrival using station-axis proximity plus supported stop/low-motion evidence and hysteresis. Detect departure at first displacement above the noise envelope after a supported stop, retaining first-movement and confirmation clocks separately. Numeric thresholds are profile data validated against real-source replay, not unexplained universal constants. ETA-derived position/zero waits cannot be counted as independent stop measurements.
7. Persist complete latest journey state asynchronously with bounded atomic lifecycle generations. Temporary archive unavailability must not erase fresh ephemeral source-backed estimated direction/map/forecasts; expose history durability separately. Restart recovers saved history with continuity reset and fresh source re-admission, never fabricated movement during downtime.

## Order-preserving forecast association

Maintain partial ahead/behind relations using fresh comparable position intervals on the same oriented common downstream corridor, compatible stop patterns and coherent original clocks. Do not establish strict order if uncertainty intervals overlap. Invalidate constraints on gaps, reversal, short-turn exit, joining branches, plan changes or source corrections. No-overtaking is a corridor assumption, not a global assertion at terminals/junctions.

For each station/direction/time slice, partition source rows by source revision and compatible platform context. Keep direct published reference anchors. Match anonymous slots using a bounded monotone dynamic-programming assignment with explicit unmatched trains and unknown/unobserved train placeholders. Preserve top-three censoring: knowing A precedes B does not prove adjacency or completeness. Candidate assignments respect path reachability, uncertainty/arrival intervals and elapsed station visits.

Publish an inferred association only when every feasible supported assignment agrees on that slot; otherwise leave it anonymous. Never use own forecasts as new independent anchors or extend association TTL via repeated receipts. A contradictory named anchor invalidates affected inferred assignments and retains the forecast with a scoped conflict; it cannot silently reorder the physical cohort. Keep method/evidence/window/constraint IDs and competing counts for audit. The model works with partial order; it does not wait for an impossible provider-wide completeness guarantee.

## Our arrival and departure estimates

Start with an explainable component model rather than speculative deep learning: directed segment running time, station/direction dwell and current episode delay. Condition on compatible topology, time-of-day/day type and available disruption context; use bounded robust empirical quantiles and shrink sparse groups to explicitly labeled compatible parent cohorts. Derive priors from validated GTFS timing differences where meaningful. A missing/zero scheduled dwell is not a measured positive dwell prior.

From a supported current anchor, recursively compute next arrival from departure plus running time, and departure from arrival plus dwell. Maintain separate independently issued official values and dependent own values; record official-anchor dependence. Condition a stopped-train departure on elapsed dwell, using remaining-dwell distribution rather than repeatedly adding the full dwell duration. Arrival <= departure <= next arrival; untrusted forecasts stay separate rather than modifying official source instants to force consistency. Valid new revisions can increase countdowns.

Historical samples retain event windows, original publication clocks, method/profile, source dependence and quality. ETA transition proxies may train a clearly labeled forecast-consistency model; modeled positions cannot establish physical running/dwell accuracy. Positive dwell estimation needs supported stop/first-movement history or meaningful schedule priors; otherwise departure remains unavailable until collection supplies it. Do not bootstrap fake training from current predictions or pseudo-events produced only by clock advancement.

Prequential issuance and train/journey/day-separated chronological holdouts compare official, schedule-prior, historical-baseline and proposed models at matched horizons. Report MAE, P90 absolute error, interval coverage and availability; distinguish same-source proxy consistency from physical accuracy. Freeze parameters/version and prevent future revisions from leaking into past issued forecasts. Enable experimental estimates when replay proves model consistency and exclusion rules; physical validation is an additional metric, not an impossible blanket gate on all modeled UI.

Research basis: event/segment propagation and stochastic dwell modeling are candidate foundations. Literature must be read and cited with applicability limits before final algorithm selection; no Kalman/ML algorithm or local accuracy claim is justified merely by citing unrelated train tracking papers.

## API and frontend

Authoritative OpenAPI defines one current estimated operational journey, evidence/confidence, source ages, ordered visit progress, independent official/own arrival/departure, history durability and nullable forecast ownership. Adapt current transport endpoints if they can expose this contract cleanly; remove conflicting compatibility logic from these Metro consumers rather than adding another matcher.

Vehicle popup: reference, line, clearly labeled estimated direction/destination, age/confidence, highlighted next visit and ordered complete path. Show previous/current/future states, independently labeled times, stable countdowns, unknowns and successor action. Follow the selected current estimate while fresh; manual pan pauses, explicit resume restarts. Station popup: line/direction tabs, ordered upcoming rows, anonymous forecasts, source labels and arrival/departure. Inventory counts and forecast counts are distinct; no fake zero physical inventory claim from missing physical qualification.

## Implementation sequence and review gates

1. Independent quasar-alpha/xhigh plan review, read-only. Revise this plan yourself for valid findings before application edits.
2. Implement source/catalogue preservation and one operational topology/evidence model. Public acquisition remains bounded and existing data retained.
3. Implement estimated journey state, position projection/next visit and lifecycle, with state durability separate from live availability.
4. Implement partial-order/anonymous forecast association and historical component forecasts including departures where support exists.
5. Update OpenAPI through existing generation, both popup consumers/map delivery and canonical documentation together.
6. Same reviewer type, xhigh, read-only code review against the accepted plan. Primary agent applies valid fixes and repeats until no actionable issues; do not count a failed/unavailable reviewer as approval.
7. Relevant Go/race, API generation, frontend compilation and actual browser checks. Normal Maat repairs without bypass. Signed commit(s) and push final changes; preserve unrelated worktree edits. No deployment requested here.

Tests must cover actual retained Red-line/coexisting destination inputs, all line paths/short turns/interchanges, platform duplicates and unequal clocks, missing identities, partial-order censoring/overlap/unseen trains, stale/reordered updates, reversal, terminal closure, gap/restart/write failure, dwell conditioning, leakage-free historical issuance, independent either-source UI, source-expiry behavior and 500 ms local rendering. Measure stream publication-to-DOM separately from source cadence and actual correctness. Evidence-dependent source limitations remain explicit acceptance boundaries.

## Facts and reading paths

- [Official Metro API catalogue](https://api.metrolisboa.pt/store/apis/info?name=EstadoServicoML&provider=admin&version=1.0.1).
- [GTFS vehicle-position semantics](https://gtfs.org/documentation/realtime/feed-entities/vehicle-positions/) distinguishes position measurement timestamps from feed clocks; it does not certify this Hub's modeled inputs as GPS.
- [GTFS schedule reference](https://gtfs.org/documentation/schedule/reference/) for static station/visit relationships and scheduled clocks.
- Local dated actual source audit: /Users/rodrigo.fernandes/docs/plans/debug/20260929T083711Z-metro-source-join-audit/REPORT.md and authenticated-followup-20260929T090500Z/FOLLOWUP.md.
- Current integration inventory: docs/integrations/metro.md and docs/integrations/tml-hub.md. Their descriptions are implementation evidence, not accepted constraints on this redesign.


## Binding amendments from independent plan review

The following rules override less-specific wording above. Requested review was dispatched with `model=quasar-alpha`, `reasoning_effort=xhigh` and read-only instructions. Seven actionable findings were accepted. The reviewer subsequently withdrew an unsupported caution about the effective model identity; the accepted dispatch is the available model-selection evidence.

### Ownership universe and activation

Published ownership never changes its source reference. Deterministic inferred ownership requires every feasible assignment to agree inside a locally evidenced candidate-exclusion window; non-overtaking alone cannot close that window. Estimated operational ownership exposes nullable original reference, estimated reference/journey, method, candidate alternatives, unknown/unmatched hypothesis, assumptions, evidence expiry and optional calibrated confidence. An estimate can attach to an existing operational journey, never create a map vehicle or become a named anchor. Unknown candidates cannot be dropped to manufacture uniqueness.

Matching is bounded to the existing visit/identity limits; limit exhaustion returns unknown with a capacity reason. Generate candidates only for fresh reachable same-direction shared-path journeys whose supported prediction intervals overlap the row's interval. Preserve a separate hidden/unmatched candidate. Retain immutable constraints and source revision IDs. A deterministic exclusion certificate enumerates its anchors, spatial/time boundaries and invalidating joins/turns; no provider-wide completeness assertion is used.

An estimated-association profile must pass chronological episode/day-separated identity-masking replay, candidate removal, cohort thinning, top-three censoring, missing slots, insertion/exit and short turns. Candidate reference labels are legitimate production inputs; target slot identity, target-derived join keys and future named corrections are forbidden. Hub models generated from original named rows remain explicitly dependent features and receive ablation checks. Natural anonymous rows have unknown truth. Freeze selection thresholds on a separate calibration split; report heldout precision/error, coverage and correct unknown rejection by line/direction/profile. Only expose numerical confidence where its lower precision bound is justified; absent support, expose possible candidates without asserting ownership. Independent physical allocation remains unmeasured.

### Clock and availability classes

For Metro Hub points keep model-publication time, our receipt, and known underlying input clock separately. Underlying age is nullable and remains unknown when the provider does not supply it. Repeated direct `hora` under advancing Hub clocks cannot renew direct predictions, inferred ownership TTL or original-input training support. It may update a labeled model marker within model-publication availability; operational direction retains its exact evidence class. A test republishes stale/unchanged direct inputs with advancing Hub clocks and checks all these boundaries.

Source wait availability, line-state age, model-marker publication availability and history durability are separate. Wait/status/catalogue requests use separate pacing and locks; a slow state/catalogue request cannot hold the waits lane or runtime publication owner. Atomic frame reads use bounded copies, never network or filesystem work under the estimator lock.

### Identifiable timing and evaluation

Arrival-to-arrival proxies identify combined stop-plus-run components. They cannot independently fit dwell and running durations. Use positive route/visit-compatible scheduled priors for cold-start departure; label zero/unsupported priors unknown. Learn separate dwell/run only from admissible stopped/first-movement windows; schedule-based decomposition retains its assumption and source version. Use the actual retained GTFS timing evidence in the linked research note.

For reference window `[L,U]`, a point prediction P has absolute-error lower bound `max(L-P, P-U, 0)` and upper bound `max(abs(P-L),abs(P-U))`. Preserve whole-window and overlap interval coverage separately. Compare own/official values on matched frozen issuance cases and separately report either-source availability, unsupported outcomes and withdrawn support. Calibrate total-horizon prediction intervals on heldout cases; do not sum marginal quantiles and call the result calibrated. Experimental consistency admission does not require physical calibration, but sparse/unsupported learned strata remain at disclosed priors or unknown.

### Atomic live state and durability

One in-memory owner commits old closure, new admission and current selection together. The published frame sees either complete before or complete after state, never two current episodes. Disk acknowledgement is not current admission. Archive the complete lifecycle generation asynchronously; fence acknowledgements by revision/generation, retain bounded pending generations, and make overflow/write failure explicit history gaps without suppressing fresh current markers/forecasts. Retry follows bounded backoff; no infinite pending queue. Ordinary progress can coalesce; lifecycle/correction generations cannot lose one half.

Prolonged source gaps, reference reuse after a closed episode, same-direction re-entry and plan changes split episodes as well as reversal. Restart restores last committed history/relationships with no active detector continuity; fresh input creates or re-admits an explicitly identified live episode without pretending an interrupted occurrence was observed. Contradictory occurrence evidence withdraws its dependent training and active estimates, retaining frozen issued values and correction lineage. Old successful writes cannot resurrect an earlier journey.

### Changed-update evidence capture

Retain normalized changed direct revisions and paired Metro model positions with clock classes, selected source/path/artifact hashes, receipt order, model version, corrections and explicit gap records. Processing-only duplicate Hub receipts do not produce unlimited capture. A versioned bounded lane uses the existing archive owner and global allocated-byte/FIFO cap: proposed maximum record 256 KiB, queue 64 records and 8 MiB, seven-day target subordinate to disk cap. Queue overflow marks a gap and cannot stop live service; late completion cannot erase that gap. Structural limits are validated with actual retained data before any claim about supported timing resolution. Capture/prior/proxy/model profiles remain separated from older sampled histories.

Use current native archive ownership/retention primitives if compatible; do not add a second writer owner. Measure record size, allocation overhead, queued peak and write/ingestion cost from actual paired fixtures. The historical 30-second sampled archive cannot be presented as supporting sub-second departure evidence. Current occurrences cite their actual original support window width.

### Latency and finite interpolation

Budget replay includes shared ETA work, static/catalogue/token work, redirect/retry chains, startup bursts and protected 20-Hub/100-global headroom. One positions acquisition owner serves all operators. Preserve 900-global/120-Hub rolling caps; overload defers work with explicit availability and cannot starve existing due work. Model interpolation is permitted only on a supported within-segment interval, with source/model clock classes and finite validity. No extrapolation beyond the next visit or freshness boundary; local ticks do not emit events/training records. Uncertainty grows until the next admissible anchor; unsupported bounds stop interpolation.

Healthy target: estimator publication processes a changed admissible source input within 500 ms, and scoped SSE publication-to-DOM P95 is at most 1 s under a declared representative profile. Local marker/countdown evaluation targets 500 ms. Source cadence is measured independently and never included as a guaranteed UI latency. Browser tests cover stale expiry without a new frame, local rendering, reset/reconnect, late fallback and static-version replacement.

### Forecast-track identity and unresolved station rows

A forecast-track ID is continuity of source evidence, not a train identity. Partition by station/line/direction/platform lineage. Preserve identity across supported ETA revisions and uniquely reconciled slot shifts. Named reconciliation adds source ownership without changing that track's identity only if continuity is unique. Ambiguous replacement/split retires the old track and allocates distinct new IDs; expired rows retire by original evidence TTL. Never keep an anonymous slot's ID when a different forecast demonstrably moves into the slot.

Validate station/line/direction aggregation without a physical `cais` crosswalk. Exact equivalent source forecasts retain all platform clocks; conflicts remain separate. Unknown-direction valid forecasts appear in a visible unresolved section alongside directional tabs. A selected tab never silently hides this otherwise usable data.

## Additional reading and evidence

[Model research and source timing evidence](20260929-metro-model-research.md) states the selected event/component foundations and their applicability limits. [Actual static timing counts](20260929-metro-static-timing-evidence.json) demonstrate available positive schedule priors without claiming observed dwell. [Early review working notes](20260929-metro-plan-review-working-notes.md) preserve the initial findings; the final review/recheck will be recorded as separate dated assets.
