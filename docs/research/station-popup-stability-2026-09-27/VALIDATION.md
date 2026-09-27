# Station popup release verification, 2026-09-27

The deployed code is Git commit `383bae74ff7856b04790482a2f17fcce83f64bcb` on `main`.
See [deployment metadata](deployment.json) and the [normal Maat gate record](maat.json).
Current behavior belongs in the [popup reference](../../VEHICLE-POPUPS.md),
[architecture](../../architecture.md), [data catalogue](../../data/README.md)
and [deployment guide](../../../deploy/README.md).

## Commit gate

The first candidate was blocked for complexity, logical-operator count and maintainability in `stationCoverage`.
The repair separated evidence-based coverage presentation from requested-stop collector availability.
The normal signed Git commit then passed at scoped Go score 90, delta 0,
with no structural regressions and zero suppressions.
No hook, bundle, threshold or rule was bypassed or weakened.
TypeScript was unchecked by Maat and validated separately.
The configured absolute target of 95 remains unmet.

## Application checks

- `go test ./...` passed after the repair; app package 6.199s.
- `go test -race ./...` passed; app package 35.233s.
- `go vet ./...`, generated-contract consistency and whitespace checks passed.
- Production TypeScript/Vite build passed, including a fresh build from the committed source archive.
- Across targeted browser runs, all 49 distinct relevant cases passed.
  The final refresh/transit run passed 12 cases in 2.2m.
  Coverage includes delayed frames, offline expiry, missing directions/pages,
  obsolete responses, desktop/mobile reading/focus and station/operator navigation.

A repeated full Go run exposed a pre-existing CM test expectation that failed near midnight.
The requested hour can extend past the provider's current-day coverage,
so an otherwise valid publication is correctly projected as partial for that interval.
The test now verifies publication status separately and uses an explicit interval bound.
No CM collection or public availability behavior changed.

The earlier full browser run passed 116 cases, skipped two optional geometry fixtures and failed four cases.
One obsolete Metro-warning assertion was corrected and passed in the targeted run.
Three fixture-free dashboard checks required a live backend absent from the fixture Vite server;
they were not represented as passed.
No PostgreSQL/Cockroach integration connection was configured for the final local Go runs.

## Production rollout

An initial deployment comparison detected omitted transport settings in a previously active Compose override.
The previous image was restored automatically;
the complete original runtime environment was then restored through the original Compose inputs.
The old override also pinned an experimental image outside `main`.
The user explicitly requested an image override and deployment of `main`.
The final release therefore uses only the committed main source,
without incorporating the uncommitted experimental Metro feature from the other worktree.
Its previous image and archive volume remain available for rollback.

The final Compose image override selects `lisboa-publica:${VERSION}` after the existing configuration inputs.
Runtime environment, isolation, limits, networks and other running containers matched the pre-rollout baseline.
The protected host backup is recorded in the deployment metadata;
credentials and private container inspections were not copied into Git.

All seven server/frontend artifact hashes matched the running container.
The public root HTML matched the built frontend index hash.
Public health reported application and database `ok`, and the container had zero restarts at verification.
A public browser smoke test opened Marquês de Pombal as one search entry with two line sections,
no platform-based nearby navigation, no provisional direction coverage labels,
and one historical-actual disclaimer.
Only scalar results were retained; no production screenshots, traces or train records were exported.
This verifies the checked station state, not every provider transition or a live screen-reader session.
