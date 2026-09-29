# Metro prediction research and source timing evidence

Read on 2026-09-29; this is design evidence, not implemented behavior or a measured accuracy claim.

## Algorithm foundations

[Kecman and Goverde, An online railway traffic prediction model](https://repository.tudelft.nl/file/File_515bbd41-2b54-4384-a92b-e11b1957ac35?preview=1) describes a timed event graph linking run, dwell and headway activities. Historical process-time distributions and current delays drive repeated event prediction. This supports an interpretable event/segment architecture and precedence constraints. Their inputs include measured infrastructure/train events; Lisbon's modeled inputs do not inherit that measurement quality, calibrated percentiles or reported accuracy. Do not copy their numerical headway or dwell constants. Our adaptation must identify source-derived proxies, frozen issuance and topology separately.

[Dynamic train dwell time forecasting: a hybrid approach to address the influence of passenger flow fluctuations](https://link.springer.com/article/10.1007/s40534-023-00311-7) analyzes station/time-dependent dwell and updates estimates with passenger-flow data. It supports separating dwell from running time and tracking contextual uncertainty. Lisbon's inspected APIs provide no equivalent live passenger-flow observations; do not implement its passenger adjustment, copy coefficients or claim its accuracy. Start with supported local dwell distributions/schedule priors and disclose the missing explanatory inputs.

## Concrete local priors

[Retained static timing evidence](20260929-metro-static-timing-evidence.json) was computed directly from the normalized Metro GTFS ZIP acquired at 08:40 UTC on 2026-09-29. It contains 34,968 visits in 2,527 trips. There are 29,292 positive scheduled dwells between 15 and 55 seconds, plus 5,676 zero dwells. Scheduled adjacent running durations range from 43 to 156 seconds. These are schedules, not observed durations; aggregated counts do not establish station/direction compatibility. They demonstrate that a data-backed cold-start dwell/run prior exists for many visits, so departure predictions need not wait universally for physical timing collection. Zero/unsupported visit priors still require explicit fallback or unknown.

Implementation must select priors by exact plan/route/oriented station pair or station visit, using matching day/time patterns where available; never average across opposite directions/short-turn terminal dwell. Historical modeled departure/arrival windows must be versioned separately from these schedule priors and must not relabel prediction revisions as real events.

## Practical distinction for anonymous ownership

Non-overtaking supplies partial-order constraints even with an incomplete cohort. It cannot establish train-slot adjacency by itself. A unique feasible association needs additional bounds/anchors excluding hidden candidates. If a probabilistic association is chosen later, it must be exposed as such with a justified threshold, not described as logically proven uniqueness. Censored top-three boards and uncertain Hub model inventory must remain explicit in the replay cases.
