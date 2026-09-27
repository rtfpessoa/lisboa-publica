No blockers found. I recommend **local acceptance**.

The fixes in [VehiclePopup.tsx:25](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/src/VehiclePopup.tsx:25) detach and cancel geometry requests when overlays are disabled, guard result import, and ignore inactive geometry errors. The [new regressions](/Users/rodrigo.fernandes/dev/lisboapublica/frontend/tests/cm-paths.spec.ts:62) cover abort-before-release and first-page recovery.

The reviewed files match Maat candidate `c04bec2a4cf7404193a2fea698f419f1653129ee`. The [validation evidence](/Users/rodrigo.fernandes/dev/lisboapublica/docs/research/cm-selective-paths/VALIDATION.md) supports 80/80 controlled browser passes, PG/CR race checks, generation/build/vet, 983.94 MiB peak RSS, and Maat 87 with no critical regressions. The interrupted unfiltered attempt is excluded.

No separate nonblocking findings.

Read-only review; no edits or tests run. No production acceptance or deployment performed.