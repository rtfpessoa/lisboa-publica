> The offline workflow below validates the legacy reviewed adapter and independent physical references. The current operational model separately exposes experimental model departure estimates after supported stops; these do not claim physical accuracy. See [current behavior](metro-live-popups.md).

# Reproducible Metro departure calibration

The offline `metro-calibrate` command prepares a candidate for the guarded model-movement detector.
It does not collect observations, contact Metro, change server configuration or enable live departures.
No qualified live movement profile or retained stop-to-movement qualification was supplied as of
2026-09-28, so independently validated physical departure accuracy remains unavailable. The command has separate physical-reference
comparison and model-consistency paths. Independent physical timing references are required for a future
physical accuracy claim; they are not required to assess consistency of an experimental model estimate.
The included populated example is synthetic and establishes reproducibility, not production qualification.

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

`valid` means that the source-to-progress transform and journey/visit association were qualified for this
model. It must not mean merely that latitude/longitude are parseable. `stopped` is the model's supported
stop signal. Opaque Hub coordinates and a local countdown alone cannot qualify movement. A reproducible
wait-and-frozen-geometry transform needs its own versioned qualification against original publications;
it is model evidence and never independent GPS. Mark correction and fallback observations explicitly; the detector rejects
them and clears stop continuity. Record gaps/losses rather than joining points across them.

For `observed` physical-reference comparison, obtain a separate reference for each stop and first movement,
associated with the same journey/visit.
Record a reference provenance identifier and clock uncertainty as inclusive windows: `stopped_from` through
`stopped_through`, then `first_movement_from` through `first_movement_through`. The first movement window
must begin strictly after the last confirmed stopped instant. An ambiguous overlap requires a better
reference or a separate excluded case; do not replace it with an exact invented time. Provider ETA transitions,
the same Hub feed or an output from this detector are not independent physical truth.

For `model_consistency`, retain support intervals from the qualified model's original stop/movement
evidence, bound to the same journey/visit. Declare reference `kind: model_support`, and `model_provenance`
for the frozen qualification bundle, source/geometry/transform versions, original-record hashes and
support rules. Do not derive a reference by copying this detector's output to force agreement. The windows
describe model support, not physical confidence intervals. These declarations remain reviewable operator
assertions: the command does not authenticate their underlying records.

Assign entire journeys to `train` or `holdout` before fitting. Freeze this assignment and the proposed
geometry/transform version. Select a representative holdout across stations, directions, stops and source
quality conditions; one example cannot establish general accuracy. Keep acquisition within the existing
shared attempt budget; this command makes no additional upstream requests. The popup event journal and
30-second historical sampling do not supply a complete original movement/reference dataset.

## Input and replay

The executable contract is [MetroCalibrationDataset](../internal/patterns/metro_calibration.go), with a fully
populated [synthetic input](../internal/patterns/testdata/metro-calibration-synthetic.json). Start actual
collection from the [empty observed template](../internal/patterns/testdata/metro-calibration-observed-template.json);
it deliberately fails validation until real evidence and versioned bindings are supplied. A separate
[empty model-consistency template](../internal/patterns/testdata/metro-model-consistency-template.json)
also deliberately fails until those records exist. The top-level `kind` is `observed`, `synthetic` or
`model_consistency`; references respectively require `independent`, `synthetic` or `model_support`.
Model-consistency inputs additionally require nonempty frozen qualification provenance. Provenance declarations are operator assertions, not
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
training samples marked stopped and lying within the declared stopped window, with gaps at most
60 seconds. At least one such pair and separate training/holdout journeys are required. This conservative
pairwise envelope is a proposed model parameter, not a measured physical station radius or an accuracy guarantee.
Resolution remains the separately declared value; the detector uses the larger of noise and resolution.

The frozen candidate is then replayed on holdout visits through the existing detector. It emits at the
first admissible resumed movement beyond that threshold after a supported stop, without waiting for a
second moving sample or a radius exit. Reference labels do not change holdout model stop flags or the
training threshold. Each visit reports `within_reference_window`, `early`, `late` or `not_detected`, together
with the detection instant and original reference window. In the model-consistency path, a matching
outcome is instead `within_model_support_interval`; `purpose: model_consistency` and
`physical_accuracy: not_measured` prevent it being presented as physical calibration. Missing detections remain in the report.
The output is deterministic for identical input bytes and tool version. Retain missing-source visits and
excluded/ambiguous cases in the evidence bundle and report their coverage separately; do not remove them
to improve detection rates. This assessor does not quantify station/train coverage beyond its input visits.

## Review and live admission

Every output carries `live_enabled: false` and `review_required: true`, including observed inputs. Review
raw records, transform qualification, reference linkage and declared interval support, sample cadence,
coverage, early/late/missing cases and results across the representative holdout. Freeze the report,
input/evidence hashes, split, executable/source revision and model/geometry/transform versions before
considering an adapter change.
The command does not impose unagreed physical accuracy thresholds or automatically approve a candidate.
Changing geometry/transform or refitting against holdout labels requires a new version and a fresh holdout.

The legacy reviewed allowlist adapter requires an admissible production movement transform,
at least one retained qualified actual-source stop-to-movement chain per enabled configuration, separately
held-out whole journeys, prohibited-input checks, a reviewed versioned allowlist and startup admission through the live allowlist.
The offline command remains nonactivating; the server implements the separate reviewed allowlist gate. Independent physical timing
references remain required for any later physical accuracy claim. Synthetic replay, provenance text alone
or a candidate JSON file cannot enable it. See [live popups](metro-live-popups.md)
and [the detector](../internal/patterns/metro_movement.go) for current behavior and rejection rules.

## Reviewed live allowlist

Set `METRO_MODEL_ALLOWLIST` to a read-only local JSON file containing at most 32
[MetroModelAdmission](../internal/patterns/metro_model_admission.go) entries, at most 8 MiB total.
Unknown fields, duplicate profile/direction bindings and trailing JSON are rejected at startup.
An empty/unset allowlist leaves all profiles unavailable. No production admission file is shipped.

Each entry binds the topology profile and provider direction to a reviewer, the complete frozen
model-consistency dataset, its canonical Go JSON SHA-256, an ordered station axis and adjacent
segment durations in seconds. `MetroModelEvidenceSHA256` and `MetroModelGeometryVersion` compute
those exact bindings; geometry changes require a fresh reviewed dataset. The implemented transform is
`metro-wait-segment-v1`: original next-station wait divided by the frozen segment duration yields
modeled progress from a supported arrival anchor. Coordinates interpolate linearly between the frozen
station points. Axis metres must increase, stations must be unique and mapped path steps must be
adjacent and consistently signed. A profile/path mismatch disables projection.

Admission reruns training/noise fitting and whole-journey holdout. It requires a supported training
stop-to-movement emission, all referenced holdout detections within their model support intervals,
and zero emissions from repeated-clock, gap, correction, fallback, regression, invalid and restart
controls. Synthetic `kind` is rejected. Provenance/reviewer text remains an operator assertion;
review the retained original evidence and its transform before installing a file. A synthetic test
that declares this contract cannot establish that the asserted external evidence is real.

Live direction confirmation uses supported source station anchors, not sign-assuming interpolation.
Departure candidates remain private until direction is confirmed; confirmation does not change their
original first-movement time. Latest checkpoints retain frozen parameters, source inputs, revisions
and relationships as historical evidence; restored detectors start empty.
