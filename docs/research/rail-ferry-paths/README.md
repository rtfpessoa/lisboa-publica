# Quarantined archive measurement

The probe exercises the current parser's geometry branch for the three downloaded official fixtures by changing the fixture provider mode to `bus`. It does not change application code or implement train/ferry support. Stored measurements are candidate admission/allocation evidence, not production RSS.

Use a local JSON manifest containing `operator`, `agency`, `plan`, `from`, `until` and `file` for the three fixtures. The initial manifest is `/tmp/lisboa-overlay-plan/manifest.json`; current archive hashes and dates are recorded in `archives.json`. Do not use an external production database or unbounded archive fetches. If repeat research discovers different active archives, record their hashes and treat the results as a new measurement.

From the repository root:

```sh
python3 - <<'PY'
import json
from pathlib import Path
root = Path.cwd()
Path('/tmp/lisboa-overlay-measurement.json').write_text(json.dumps({
    'Replace': {str(root / 'internal/app/overlay_measure_test.go'):
                str(root / 'docs/research/rail-ferry-paths/measurement-probe.go.txt')}
}))
PY
OVERLAY_PLAN_FIXTURES=/tmp/lisboa-overlay-plan/manifest.json go test \
  -overlay /tmp/lisboa-overlay-measurement.json ./internal/app \
  -run '^TestPlanningRailFerryGeometry$' -count=1 -v
```

This runs without database writes or network requests. The probe reports total allocated bytes during parsing; it does not measure peak live heap or container headroom. The implementation must still test the real mode branch, geometry failure handling and full resource limits.
