# Metro departure calibration preparation — 2026-09-28

The user confirmed that no independent stop/first-movement reference is currently available. This increment
provides an offline collection contract and reproducible candidate/holdout assessment, not a calibrated
live movement adapter or physical accuracy validation. See the [current workflow](../metro-departure-calibration.md).

## Delivered preparation

- Explicit source and resolution provenance, versioned geometry/transform, original and receipt clocks,
  journey/visit identity, raw-evidence references and model stop/admissibility/correction/fallback flags.
- Independent reference windows for observed inputs, separately labeled synthetic windows for examples.
- Whole-journey train/holdout separation and a training-only stopped-pair noise envelope.
- Replay of the frozen candidate through the existing guarded first-movement detector; every admitted
  holdout visit retains its within-window, early, late or missing outcome.
- Deterministic JSON output with tool version and SHA-256 of the exact input bytes. External evidence
  authenticity/hashes are operator-managed. The command makes no network calls and never enables live departures.
- A populated synthetic example and an empty observed template that rejects use until evidence is supplied.

## Checks actually performed

`go test -race ./internal/patterns -run '^TestMetroCalibration' -count=1` passed in 1.747 s after the final
assessment guards and tests. Tests cover first admissible movement, frozen training versus holdout values,
missing/non-independent references, same-journey split leakage, repeated clocks, receipt order, absent raw
references/geometry, inadmissible training points, corrected motion, early/late outcomes and disabled live admission.

Two executions of `go run ./cmd/metro-calibrate -input internal/patterns/testdata/metro-calibration-synthetic.json`
produced byte-identical output verified with `cmp`. The [retained synthetic report](metro-departure-calibration-synthetic-2026-09-28.json)
records one synthetic training journey, one synthetic holdout journey and detection within the synthetic
reference window. Its noise and resolution values are invented example inputs/results, not measurements of Metro.
The empty observed template was executed and rejected with missing version/geometry/transform/provenance.

Default `go test -race ./... -count=1 -timeout 5m` passed (app 96.139 s, patterns 21.943 s); database-specific
cases skipped without `TEST_DATABASE_URL`. The final candidate-validity guard and added classification tests
were subsequently covered by the targeted calibration command above. `go vet ./...`, generated-contract
verification and the frontend build passed. No new provider probe, deployment or commit was performed.

## Remaining evidence boundary

Collect original source-qualified movement evidence and independent stop/first-movement windows, freeze
versions/splits and review a representative holdout against agreed physical criteria. The assessor cannot
verify a declaration of independence or turn unqualified Hub coordinates into admissible movement. Missing
source visits/exclusions must be retained and accounted for outside its admitted-visit report. Live departure
projection remains unavailable until that evidence and a reviewed production adapter exist.
