# Experimental Metro patterns: implementation and collection after deployment

On 2026-09-27 the user authorized finishing the first experimental implementation and improving the setup with live collection after deployment. Weekday/weekend sizing and independent physical validation remain operational work, rather than release blockers. The decision map is in `/Users/rodrigo.fernandes/docs/wayfinder/metro-padroes-e-previsoes`.

Deliver server-only JSON+Zstd detail by operator/hour (seven days), sparse hourly Parquet+Zstd aggregates by operator/day (12 months), a configurable global 10 GB archive budget and FIFO by data age. Detail sampling and histogram bins start at 30 seconds; checkpoints and evaluation start at 60 seconds. The existing operational database budget stays separate. A file lock admits one archive owner. Serialized readers close their files before FIFO can unlink them.

Reuse the existing Metro response, preserving unknown fields. Admit experimental positive-to-zero signals only through three consecutive, uniquely compatible planned stations, continuous eligible presence and admissible source clocks. Known losses between detail samples revoke support. These are proxies, never physical arrival observations. Sum future arrival-to-arrival components at their predicted origin hours, beginning with a fresh future official anchor for the supported association. Show official and own forecasts for waiting and subsequent stops. Dwell, speed and residual progress methods remain unavailable without admissible inputs.

Show calculated values from the first samples, with explicit support and uncertainty. Do not convert missing signals into physical occurrence probabilities or Wilson denominators. Select the first eligible forecast per episode/target/own horizon/function/mode/profile for 80% nominal calibration. Group paired evaluation by the official horizon at issuance. Proxy evaluation is separate from physical validation. Do not automatically enable a recent adjustment based on this initial sample.

Validate deduplication, chronology, gaps/conflicts, DST, sequential remaining components, missing support, calibration, restart, codec roundtrips, generation publication, FIFO/temporary accounting, authorization and the comparison UI. Build and run checks before deployment. Prolonged observation and annual capacity stay operational follow-ups. Integrate onto the deployed source baseline before packaging, preserving newer navigation and vehicle reporting behavior.

On the subsequent 2026-09-27 instruction, the user required rebasing onto current `main` and testing locally only. This supersedes the earlier deployment authorization: no deployment or publication from this worktree until the implementation is on `main`. Preserve the earlier release observations as dated evidence, not approval to reuse uncommitted artifacts.

The local gap-completion follow-up adds durable proxy comparison reports, labeled older/general-context fallback, segment and mixed-resolution compatibility, line-specific conditions, versioned holiday grouping, evidence-backed maintenance and staged normalized observation capture for all existing operators. Validation remains local. Only Metro has an own-arrival forecast adapter; subsequent operator adapters and physical/capacity evidence are not certified by this delivery. See [implementation audit](GAPS-metro-patterns.md).

## Local adapter completion, 2026-09-28

The earlier paragraph describes the 2026-09-27 delivery. The later follow-up
implements all seven additional experimental adapters, verified source paths,
reported-stop proxies, remaining components, operator/route/direction selection,
independent official cold-start values, both forecast functions, bounded
calibration/reporting, shared FIFO/recovery and operator-specific corrections.
Activation still adds one operator at a time and defaults to Metro. All testing
and review remain local; no current live-source availability or physical
accuracy improvement is certified. The four independent-reference/capacity map
tickets remain operational follow-ups, as documented in the dated audit.
