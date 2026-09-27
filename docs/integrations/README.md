# Consumed integrations

The runtime consumes six external integration families. References group shared endpoints and discovered resources by integration; an operator is not automatically a distinct API. Field tables cover this application's decoder/normalizer, not complete provider schemas or current availability guarantees.

| Integration | Reference | Runtime role |
|---|---|---|
| TML Hub | [TML Hub](tml-hub.md) | Positions, plan discovery, normalized GTFS archive downloads, fleet metadata and public CP/selected-stop TML predictions |
| Carris Metropolitana v2 | [CM v2](carris-metropolitana.md) | Direct CM lines, stops, reported vehicles and requested-stop arrivals |
| Metro de Lisboa | [Metro](metro.md) | Credential-dependent line status, station metadata and waits, with OAuth |
| Google Identity | [Google Identity](google-identity.md) | Optional sign-in script and server-side ID-token validation |
| OpenFreeMap | [OpenFreeMap](openfreemap.md) | Browser map style and referenced map resources |
| Google Fonts | [Google Fonts](google-fonts.md) | Browser typography stylesheet and referenced fonts |

The application API is an internal boundary whose authoritative contract is [OpenAPI](../../api/openapi.yaml). Database/credential tooling belongs in [architecture](../architecture.md) and [deployment](../../deploy/README.md), not a transport-source page.

## Operator-to-source coverage

| Operator ID | Operator | Consumed transport inputs |
|---|---|---|
| `carris` | Carris | Hub positions, active normalized GTFS plan and requested-stop ETA, agency `IA9T6` |
| `cm` | Carris Metropolitana | Direct CM v2 catalogue/positions/requested-stop arrivals; four Hub GTFS plans for geometry/optional fleet records; Hub metadata |
| `tcb` | TCB | Hub positions, active normalized GTFS plan and requested-stop ETA, agency `A3H3M` |
| `mobi` | MobiCascais | Hub positions/GTFS/requested-stop ETA, agency `HF16N`, and verified Hub fleet metadata |
| `metro` | Metro de Lisboa | Estimated Hub positions/GTFS, agency `IA2N9`; direct waits/status/stations with credentials |
| `cp` | CP | Hub positions/GTFS, agency `N18KL`; Hub `/realtime/eta/gtfs` CP updates |
| `ttsl` | TTSL | Hub positions, active normalized GTFS plan and requested-stop ETA, agency `LTP61` |
| `fertagus` | Fertagus | Hub positions, active normalized GTFS plan and requested-stop ETA, agency `7NTB1` |

CM's geometry plans use agencies `LA77N`, `BNA17`, `YA15B`, `A2L1N`. All GTFS downloads are discovered from active Hub plans and admitted only from the authorized object-storage host. CP predictions are a dedicated Hub contract, not a direct CP API subscription. Rail/ferry paths come from published GTFS shapes; there is no external routing service.

## Shared collection policy

[The Go entry point](../../cmd/server/main.go) shares a provider client across general ingestion and Metro. [BudgetTransport](../../internal/app/upstream.go) caps attempts at 900/rolling-minute globally, 120/rolling-minute for TML and 40/rolling-second for CM, including followed redirects and Metro token requests. These are local policy values. Browser resources and Google verification are outside that client.

JSON responses are bounded; static archives have separate compressed/expanded/entry/row limits. The general fetcher supports bounded in-memory ETag/body reuse. Cooldowns on actual source failures and 429/503 defer retries to scheduled collection; redirect policy preserves the initial HTTPS origin and effective port. Five-second collection is not five-second source freshness.

## Evidence and exclusions

Every reference links code and existing fixtures/tests. [Provider research](../research/SOURCES.md), [polling research](../research/POLLING-LIMITS.md) and other notes record dated external observations. There are no runtime Mover Lisboa API calls, direct CP GTFS downloads, direct Carris/Fertagus/TTSL fallback downloads, or a separate rail/ferry geometry feed. Alternative sources mentioned in research remain alternatives, not consumed integrations.

Read [the catalogue](../data/README.md) for data availability, [associations](../data/associations.md) for joins and [history](../data/history.md) for persistence/metrics.

Position integrations also supply normalized membership/original clocks for backend-owned [reporting state](../data/README.md#backend-owned-reporting-state). No new upstream endpoint or credential is required; latest reporting persistence is separate from position/history retention.
