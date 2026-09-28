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

The existing [Metro integration](metro.md) also supplies the experimental pattern collector. It introduces no new provider endpoint or upstream polling rate. See [Metro patterns](../metro-patterns.md) for the server archive, source-dependent forecast model and unavailable physical metrics. Other operators are not yet enabled in this collector.

The local completion follow-up adds route-specific conditions, versioned Lisbon holiday grouping, labeled older/general-context component fallback, unchanged-segment compatibility, conservative mixed-bin calibration and durable bounded MAE/P90/availability/band-support reports. Evidence-backed maintenance revises retained inputs atomically while keeping issued values. Staged normalized observation/prediction capture and experimental own-forecast adapters cover all eight existing operators under the same archive budget. Metro uses ETA transitions; later stages require verified published paths and coherent reported stop-state transitions. Forecast availability depends on actual compatible inputs, and physical validation remains unavailable. See [current behavior](../metro-patterns.md) and [remaining live evidence](../GAPS-metro-patterns.md).

Metro direct collection has its own 500 ms target (`METRO_REFRESH_MILLISECONDS`) under the shared attempt
budget; other sources retain their cadence. [Metro frames and inferred evidence](../metro-live-popups.md) describe
the source-to-popup projection, SSE/snapshot exposure and separate seven-day event journal. Neither fast capture
nor the guarded uncalibrated departure detector establishes physical timing accuracy.

Hub positions have a dedicated one-second acquisition target with shared-budget priority/headroom and two/five-second pressure backoff; faster publication is scoped to Metro. Other consumers reuse the latest shared response at their existing cadence. Direct Metro waits remain on the separate minimum-500 ms collector. Shared live context classification and complete journey checkpoints now govern selectable identity/recovery; experimental live modeled departure/position qualification remains unavailable. See [Hub](tml-hub.md), [Metro](metro.md) and [popup behavior](../metro-live-popups.md).

The [Metro integration](metro.md) also documents inferred final-visit completion and the optional reviewed wait/station-axis adapter. It consumes existing publications, introduces no endpoint or render-time request, and ships no qualified production configuration.
