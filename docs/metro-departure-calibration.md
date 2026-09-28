# Reproducible Metro departure calibration

The offline `metro-calibrate` command prepares a candidate for the guarded model-movement detector.
It does not collect observations, contact Metro, change server configuration or enable live departures.
No independent physical reference was supplied as of 2026-09-28. Live departure times therefore remain
unavailable. The included example is synthetic and establishes reproducibility, not physical accuracy.

## Collect and bind evidence

Keep immutable original observations alongside an input JSON file. Record original provider clocks and
local completed-response receipt clocks separately. Repeated receipt/render times are not new movement
samples. Preserve raw duplicate/conflicting publications in the evidence bundle; normalize only identical
original observations into one sample. Changed equal-clock publications must be investigated, not ordered
by receipt time to create movement. The assessor requires strictly increasing original clocks per visit.

For every usable visit, record the supported journey/visit identity, the station association, the exact
versioned geometry and the exact transform from original coordinates to longitudinal progress in metres.
Use one geometry/transform per dataset. Maintain a mapping from each sample's `evidence_ref` to its
original source record, including source URL, original coordinates/stop signal, any matching evidence and
admissibility decision. Freeze those files and their checksums with the dataset. The command records a
SHA-256 of the input JSON; it does not read, hash or authenticate the referenced external records.

`valid` means that the source-to-progress transform and journey/visit association were independently
qualified for this model. It must not mean merely that latitude/longitude are parseable. `stopped` is the
model's supported stop signal. A Hub coordinate, rounded/interpolated ETA position or a local countdown
alone cannot qualify movement. Mark correction and fallback observations explicitly; the detector rejects
them and clears stop continuity. Record gaps/losses rather than joining points across them.

Obtain a separate reference for each stop and first movement, associated with the same journey/visit.
Record a reference provenance identifier and clock uncertainty as inclusive windows: `stopped_from` through
`stopped_through`, then `first_movement_from` through `first_movement_through`. The first movement window
must begin strictly after the last confirmed stopped instant. An ambiguous overlap requires a better
reference or a separate excluded case; do not replace it with an exact invented time. Provider ETA transitions,
the same Hub feed or an output from this detector are not independent physical truth.

Assign entire journeys to `train` or `holdout` before fitting. Freeze this assignment and the proposed
geometry/transform version. Select a representative holdout across stations, directions, stops and source
quality conditions; one example cannot establish general accuracy. Keep acquisition within the existing
shared attempt budget; this command makes no additional upstream requests. The popup event journal and
30-second historical sampling do not supply a complete original movement/reference dataset.

## Input and replay

The executable contract is [MetroCalibrationDataset](../internal/patterns/metro_calibration.go), with a fully
populated [synthetic input](../internal/patterns/testdata/metro-calibration-synthetic.json). Start actual
collection from the [empty observed template](../internal/patterns/testdata/metro-calibration-observed-template.json);
it deliberately fails validation until real evidence and versioned bindings are supplied. The top-level
`kind` is `observed` or `synthetic`; observed inputs require every reference to declare `kind: independent`,
while synthetic inputs require `kind: synthetic`. Provenance declarations are operator assertions, not
verified external authenticity. Resolution in metres needs documented source/transform evidence and must
not be guessed from a display's decimal precision.

From the repository root:

```sh
go run ./cmd/metro-calibrate \
  -input internal/patterns/testdata/metro-calibration-synthetic.json > /tmp/metro-calibration.json
# With actual collected evidence:
go run ./cmd/metro-calibrate -input /path/to/frozen-observations.json > /path/to/assessment.json
```

The input is one JSON document, at most 8 MiB, with at most 50,000 samples and 5,000 referenced visits.
Unknown fields, missing provenance/references, repeated/backwards clocks, non-finite progress, receipt clocks
before publication and journeys in both splits are rejected. Every reference must have samples. References
without matching observations are not silently counted as validated coverage.

The candidate noise envelope is the maximum absolute consecutive progress difference between admissible
training samples marked stopped and lying within the independent stopped window, with gaps at most
60 seconds. At least one such pair and separate training/holdout journeys are required. This conservative
pairwise envelope is a proposed model parameter, not a measured physical station radius or an accuracy guarantee.
Resolution remains the separately declared value; the detector uses the larger of noise and resolution.

The frozen candidate is then replayed on holdout visits through the existing detector. It emits at the
first admissible resumed movement beyond that threshold after a supported stop, without waiting for a
second moving sample or a radius exit. Reference labels do not change holdout model stop flags or the
training threshold. Each visit reports `within_reference_window`, `early`, `late` or `not_detected`, together
with the detection instant and original reference window. Missing detections remain in the report.
The output is deterministic for identical input bytes and tool version. Retain missing-source visits and
excluded/ambiguous cases in the evidence bundle and report their coverage separately; do not remove them
to improve detection rates. This assessor does not quantify station/train coverage beyond its input visits.

## Review and live admission

Every output carries `live_enabled: false` and `review_required: true`, including observed inputs. Review
raw records, transform qualification, independent reference linkage/clock uncertainty, sample cadence,
coverage, early/late/missing cases and results across the representative holdout. Freeze the report,
input/evidence hashes, split, executable/source revision and model/geometry/transform versions before
considering an adapter change.
The command does not impose unagreed physical accuracy thresholds or automatically approve a candidate.
Changing geometry/transform or refitting against holdout labels requires a new version and a fresh holdout.

Enabling live departure projection still requires an admissible production movement transform, reviewed
independent observations, accepted accuracy criteria and explicit integration work. Synthetic replay,
provenance text alone or a candidate JSON file cannot enable it. See [live popups](metro-live-popups.md)
and [the detector](../internal/patterns/metro_movement.go) for current behavior and rejection rules.
