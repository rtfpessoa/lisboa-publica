# Metro popup corrections and position availability — 2026-10-03

Dated local and production evidence for the change that corrects Metro behind-visit
countdowns, forecast ordering, station forecast ordering and explicit Hub
position-availability reporting, and deploys the never-released `e651500` journey
rebuild to production.

## Releases

| Item | Value |
|---|---|
| Commits | `78b1173` (corrections), `dc46950` (review hardening), `1650828` (station order), `6aa8a02` (re-review test gaps) |
| Signed | All four report `G`; pushed to `origin/main` (`6aa8a02b89d7bdc1cee108d428e941920f1a2983`) |
| Maat gate | Passed on each commit (`status: passed`, score 97, no critical regressions); `ingest.go` maintainability and `hub_positions.go` feed extraction were adjusted to satisfy the gate |
| Image | `lisboa-publica:6aa8a02b89d7bdc1cee108d428e941920f1a2983`; container `lisboa-publica-dashboard-1`, OCI revision matches, healthy, 0 restarts |
| Previous image | `lisboa-publica:64b1ec4ba45c4dff78693ac7f596bb7870f5be75` |

## Independent reviews (Opus 5.5, read-only)

- Plan reviews: `~/docs/wayfinder/metro-popup-and-position-recovery/reviews/opus55-structure.md`,
  `opus55-evidence.md`, `opus55-risk.md`; blockers applied in the ticket resolutions.
- Implementation review: `opus55-impl-contract.md` and `opus55-impl-tests-deploy.md`
  (ACCEPT WITH FIXES / READY WITH FIXES); all behaviour findings fixed.
- Re-review of the fixes: `opus55-impl-rereview.md` (ACCEPT WITH FIXES; no P0/P1
  remained); the remaining P2 test gaps were closed in `6aa8a02` and verified with
  mutation probes (feed guards, inference retention, local ordering, suspension
  retention, 60 s window, window reset, departure-cell and sidebar regressions).

## Performed checks (local)

- `make test` (Go race suite and vet) passed on the final commit.
- `make check-generated` passed; generated Go/TypeScript equal regeneration.
- Frontend production build passed.
- 27/27 Metro Playwright cases passed, including the new behind-visit, ordering,
  unavailable-state and contextual-direction cases.
- `gofmt -l internal` clean.

## Production verification

Baseline before deploy (2026-10-03 20:22–20:23Z): Hub positions 448–457 rows, zero
`IA2N9`, all other agencies fresh; Metro operator had no availability field (old build).

After deploy (21:10Z start):

- `/api/v1/health` ok; database ok; `history_collection_status: collecting`.
- Metro operator: `status: ok`, `direct_status: ok`, `model_position_state:
  unavailable`, `last_model_position_at: null`, `estimated_positions: 0`, `error:
  null` — the new explicit state without changing operator status.
- Live frame: `status: ok`, `history_status: paused`; 8 supported trains admitted
  after the restart; official station forecasts preserved.
- Station-scoped calls carry `own_prediction` values with
  `model_version: metro-schedule-prior-v1:...` and `last_official_estimate` with the
  original source clock. Our own estimate is therefore visible again in production
  (schedule-prior form).
- The storage-measurement failure storm that had blocked operational writes since
  2026-10-02T23:05Z stopped after the restart (0 occurrences in the deploy window);
  history collection status reports `collecting`.

## Open limitations

- The patterns archive was restored on 2026-10-03: the checkpoint-size failure that
  paused it was fixed in `c314cd0`/`2aa51ce`, the manifest was compacted, and
  production reports `collecting` with advancing `as_of` and fresh checkpoints. Own
  schedule-prior arrivals and departures are visible again; historical
  component-model issuance follows as signals accumulate.
- Popup acceptance for behind visits and ordering is fixture/Playwright-based; the Hub
  still publishes no `IA2N9` rows, so map markers and vehicle-scoped popups cannot be
  exercised in production.
- The Hub omission itself is upstream (no-timeout Hub fetcher and 90-second publish
  window); see the 2026-10-03 debug session.

## 2026-10-04 platform-station mapping release

The 2026-10-04 Metro GTFS revision references platform-level stops whose legacy codes
(`SS3`, `AM1`, `MP2`, ...) are not published station ids, so full-line trips were
dropped and only short patterns survived (one direction per line, missing station tabs,
destination forecasts left without a direction). Commit `1c69411` removes the legacy
`id_antigo`/`LegacyStops` crosswalk, resolves each static stop through its
`parent_station` plus the existing unique name/coordinate matcher, and reports
unresolved stops. Evaluation on the real GTFS and the retained catalogue, plus the full
test suites, passed before the rollout.

Deployed as `lisboa-publica:1c694111023fdfc82c7781047ee0a5aaedf40269` (healthy,
0 restarts). Production after deploy: 9 trains across blue (33/42), red (60), yellow
(43/48) and green (50); São Sebastião offers all four direction tabs; Alameda offers
green and red tabs; no null-direction rows at the checked stations; no unmapped-stop
warning; archive still `collecting` with own estimates present.

## Additional deployments

- `c314cd0` (archive checkpoint/round-trip fix, reviewed) and `2aa51ce` (review fixes:
  current-day protection, loud failure, corrected rationale/tests). Production image
  `lisboa-publica:2aa51ce7ff921193fa3f69d2b48df0c41f6bc53d`, healthy, 0 restarts.

## Rollback artifacts

Directory `/root/lisboa-metro-popup-release/20261003` (mode 700) on
`root@roodle.rtfpessoa.xyz`:

- `dashboard-inspect.json`, `deploy.env.backup`, compose inputs copies;
- `postgres-before.dump` (custom format, sha256 `222f5521ddbb9caea90cd79dd0c9552e2c11c563ff4211026f0139ce46fde875`);
- `transport-history-before.tar.gz` (quiescent archive copy, sha256 `f4157443b0efe8a9abfc53213eb91041ac6943503ebd5dfcee194f0be891c557`);
- `image.override.json` pinning the new image.

Rollback to `64b1ec4` requires restoring the archive tar because the older binary
rejects the `metro-inputs` archive kind written by the new build.