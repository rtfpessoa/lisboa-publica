# Metro live main release preparation, 2026-09-28

This follow-up is authorized to repair Maat findings, commit, push to main and
then deploy a clean committed revision. Earlier replay reports retain their
original source hashes and measurement scope.

## Commit dependency closure

The source change is one Metro feature: generated OpenAPI types feed the scoped
HTTP/SSE projection and frontend subscription owner; the cache runtime consumes
original Metro publications and the archive journal; the departure assessment
uses the guarded movement detector. Configuration, synthetic fixtures, tests and
canonical documentation travel with those consumers. The existing glossary
edits and standalone HTML prototype are excluded and preserved locally.

| Component | Dependencies included in the release |
|---|---|
| Metro client and cache runtime | acquisition policy, source clocks, topology, generated API, archive journal and forecast association |
| Live HTTP/SSE | cache runtime, retained proofs, generated responses and existing read/authentication policy |
| Metro popups | generated TypeScript types, one subscription owner, map/vehicle navigation and reading-state support |
| Offline calibration command | dataset validation, train/holdout assessment, movement detector and explicitly synthetic fixtures |
| Verification | provider/runtime/HTTP/browser fixtures, replay harnesses, dated evidence and canonical documentation |

## Structural gate repair

The first normal commit candidate was blocked at Go91/delta−6. It combined
large collection, transition, journal, projection and calibration functions.
The second candidate reached95/delta−2 but still had one blocking validation
condition. The third and fourth candidates had no critical regressions but
remained blocked because their total score was below the97 baseline.

Repairs separate collection/reference rejection, association episodes, arrival
transitions, expiry/scoping, committed-proof restoration, station directions,
stream writes/authentication, archive encoding/publication and offline
calibration validation/fit/holdout assessment. Metro HTTP acquisition separates
request construction, status handling and bounded decoding. Runtime ownership
separates topology, active journeys and pending proofs; subscription request
context is separate from the projected frame. Source clocks, rejection rules,
request limits, inference labels and archive transaction order are unchanged.

Candidate five passed the normal pinned Git gate at **Go97/delta0**, with no
critical regressions and **zero suppressions**. Pinned bundle:
`cb1c6a244e1972174357b029bd304d4a3c6fb2aa`. Its prepared tree was
`b8e9f06b2b9a58c994fb26b69720546c1c0518ce`.
The configured 1Password signing operation subsequently failed, so this attempt
created no commit. No hook override or threshold change was used.
The gate covers Go; TypeScript remains outside its coverage. The reported
incomplete metric coverage and any advisory findings are not represented as a
zero-finding result.

## Checks after repair

- All Go packages passed race tests without a database URL: app54.500s and
  patterns14.337s. Database-dependent tests skip in this invocation; it does not
  certify the full database integration suite.
- Subsequent acquisition/ownership changes passed affected Metro/upstream race
  checks: app13.557s and patterns3.380s; final ownership checks also passed (app13.364s).
- The final full Go race invocation after acquisition/ownership repair also
  passed: app59.009s and patterns12.532s, with the same database-test limitation.
- The offline synthetic calibration report remained byte-identical to the
  retained pre-refactor report.
- `go vet ./...`, deterministic OpenAPI generation and the TypeScript production
  build passed. The existing large map chunk warning remains.
- All nine Metro Playwright fixtures passed in46.5s, covering both popup widths,
  countdowns/following, pinned continuity, timelines/navigation, five-second
  fallback, repeated SSE failures, cursor ordering and delayed fallback response
  rejection. These fixtures mock EventSource; native stream reset, cancellation,
  admission and slow-write behavior are covered separately by the Go tests.
- Affected documentation checks before release-note additions passed:17 Markdown files,376 local targets,
  25 Markdown fragments and248 OpenAPI local references. `git diff --check`
  passed.

The release-note additions also passed local checks:19 Markdown files,383 local
targets,26 Markdown fragments and248 OpenAPI local references.

The [32-client mixed replay](metro-live-recovery-2026-09-28.md) remains evidence
for its recorded pre-refactor candidate: it is not a newly repeated latency
measurement for these extractions. The [offline calibration preparation](metro-departure-calibration-preparation-2026-09-28.md)
remains synthetic and never enables live departures. Independent physical
movement evidence is still required.

## Production inventory

Read-only SSH inventory confirmed a clean host checkout on main
`e5b4658a0d5e9fd0423cfb640c3f261999f2322e`, running image revision
`044af1487488d50d16bf481c14f79be9461877c3`. Dashboard and PostgreSQL were healthy.
The active Compose input list includes the base, external, historical image and
archive overrides, local PostgreSQL override and final main-image override.
The rollout must preserve those inputs and append its own image override last.
The existing archive volume is `lisboa-publica_transport_history`.
Only dashboard may be recreated; the database and unrelated containers stay in
place. Protected environment values are not included in this report.

## Initial rollout and station regression

After 1Password was unlocked, normal signed commit `7a6f4228f9090c96ce44efaa33ff937e8aabf755` passed the pinned Go gate at97/delta0, pushed to main and deployed at2026-09-28T16:58:28Z. Public health and database health passed. Image/running artifact/public frontend hashes matched. Only dashboard was recreated; database, other containers, persistent mounts and resource limits were preserved. Metro cadence was configured at500 milliseconds.

Native public HTTP checks passed snapshots, ETag304, complete SSE reset/frame delivery and a Yellow-line station board. Actual browser inspection then exposed empty Alameda direction/train content at1280px and390px. That initial browser smoke checked stream delivery and page health but did not fail on empty station content; its successful exit is not evidence of working Alameda popups.

Read-only cached catalog inspection confirmed Alameda's parent plus four same-name child platforms. The inverse popup projection counted them as five independent candidates and fell back to `metro:AM`, while the selected catalog station was `metro:ML11060001`. A synthetic integration test failed with an empty direction catalogue before correction. The correction collapses only valid published Metro parent families, retains ambiguity across distinct roots, and rejects missing/cyclic/other-operator/mismatched parents. No provider calls, source clocks, physical-arrival claims or departure models change.
