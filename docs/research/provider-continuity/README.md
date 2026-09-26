# Quarantined continuity diagnostics

These artifacts characterize current behavior for planning research. They are not application fixes or permanent tests. All browser API responses are mocked; non-local traffic is blocked, except the map style request which is fulfilled locally.

From the repository root, create a Go overlay without adding a test to the application:

```sh
python3 - <<'PY'
import json
from pathlib import Path
root = Path.cwd()
replacement = root / 'docs/research/provider-continuity/continuity_probe_test.go.txt'
Path('/tmp/lisboa-continuity-overlay.json').write_text(json.dumps({
    'Replace': {str(root / 'internal/app/continuity_probe_test.go'): str(replacement)}
}))
PY
go test -race -overlay /tmp/lisboa-continuity-overlay.json ./internal/app -run '^TestDiagnostic' -count=1 -v
```

Adding `DIAGNOSTIC_REQUIRE_CONTINUITY=1` before the Go command intentionally fails on current behavior: CP stays selected but a successful empty snapshot removes its position/count. The characterization assertions will need revision if the later policy changes.

For the browser probe, start the existing frontend on loopback port 5191 in one terminal, then run the probe in another:

```sh
cd frontend
npm run dev -- --port 5191
```

```sh
node docs/research/provider-continuity/browser-probe.mjs
```

The browser fixture accelerates only its mocked vehicle refresh to one second; production's refresh cadence is unchanged. Its assertions check CP selection and MapLibre source features through nonempty/error/empty/recovery. Stored JSON/logs contain public observation summaries or local fixtures, not credentials. Vehicle withdrawal IDs are hashed in the compact evidence.
