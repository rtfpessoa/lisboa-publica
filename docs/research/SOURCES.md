# Provider evidence and source-to-feature map

Verified 2026-09-26 with public HTTP requests and read-only research agents.
This installation never calls Mover Lisboa APIs.

| Operator | Selected source | Coverage / freshness / access | Features and limits |
|---|---|---|---|
| Carris | [Official TML hub](https://go.tmlmobilidade.pt/hub/api/v1/vehicles/positions), active normalized [GTFS plans](https://go.tmlmobilidade.pt/hub/api/v1/plans), agency IA9T6 | Public 200; 250+ positions in probe, timestamps typically under 90 seconds; active plan valid through 2026-12-31 | Vehicles, routes, stops, shapes, scheduled trips; collect own snapshots. Direct [Carris GTFS](https://gateway.carris.pt/gateway/gtfs/api/v2.11/GTFS) and [GTFS-RT](https://gateway.carris.pt/gateway/gtfs/api/v2.11/GTFS/realtime/vehiclepositions) both public 200; static ZIP ~47MB, current filename gtfs_2026-09-25.zip. Hub selected for shared polling and normalized crosswalk. |
| Carris Metropolitana | [CM v2](https://api.carrismetropolitana.pt/v2/vehicles), [lines](https://api.carrismetropolitana.pt/v2/lines), [stops](https://api.carrismetropolitana.pt/v2/stops) | Public 200; 460 vehicle observations, 717 lines, 12,752 stops in main-agent probe; epoch millisecond timestamps | Map/search/live/detected fleet; sampled speed from consecutive coordinates. Upstream speed units not independently established, so do not reinterpret them. Models and plates often null. |
| TCB | TML hub, agency A3H3M | Public 200; 14 positions; active GTFS plan valid 2026-06-08..2026-12-31 | Reported positions, routes, stops, schedules, shapes; no complete registered fleet promised. |
| MobiCascais | TML hub, agency HF16N | Public 200; 43+ positions; active plan 2026-09-14..2027-12-31 | Reported positions/routes/stops/schedules/shapes and collected history. |
| Metro | TML hub, agency IA2N9; direct [GTFS](https://www.metrolisboa.pt/google_transit/googleTransit.zip) alternative | Public hub200 with 23+ positions; direct ZIP200, 4 lines/74 platform rows, calendar through 2026-12-31 | Hub positions are inferred from waiting-time predictions and GTFS geometry; label estimated and exclude from speed/distance metrics. [Official implementation](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/apps/pt-tml-ml-api-fetch/src/index.ts) calls inferTrainPositionOnShape and polls every5 seconds. Direct [realtime API documentation](https://api.metrolisboa.pt/store/apis/info?name=EstadoServicoML&provider=admin&version=1.0.1) requires account/subscription/OAuth bearer (probe401), so unused without credentials. |
| CP | Active TML normalized GTFS, agency N18KL; direct [CP GTFS](https://publico.cp.pt/gtfs/gtfs.zip) alternative | Public ZIP200; national direct feed174 routes/454 stops, calendar 2025-12-14..2026-12-12; CP initially absent, then9 positions appeared in a later probe; availability is dynamic | Show Lisbon-area stops and routes serving them. Scheduled arrivals/journeys; [Official CP parser](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/packages/parsers/src/pt/tml/cp/v1.ts) copies upstream latitude/longitude (reported positions) with null speed; hub positions require explicit provenance labels and no completed-trip inference. Direct feed has no shapes.txt. |
| TTSL | Active TML GTFS/positions, agency LTP61 | Hub public200; 3 positions; active plan2026-07-11..2027-07-01; original ZIP20KB,5 routes/10 terminals, shapes and calendars | Ferry routes/stops/scheduled journeys/reported positions; no fleet model data (vehicles.txt empty). [Direct feed](https://api.transtejo.pt/files/GTFS.zip) fails TLS certificate validation; never disable TLS verification. |
| Fertagus | TML hub, agency7NTB1; direct [GTFS](https://www.fertagus.pt/GTFSTMLzip/Fertagus_GTFS.zip) alternative | Public200; 7 hub positions; direct ZIP200,3 variants/14 stops, current service2026-09-14..2027-03-14 | Routes/stops/shapes/scheduled journeys/reported positions. Direct vehicles.txt has18 Alstom UQE 2P3500 records, but identifiers must crosswalk before attaching attributes to hub observations. |

The official [TML source repository](https://github.com/tmlmobilidade/go) and [open-data documentation](https://go.tmlmobilidade.pt/reference/open-data) establish hub provenance.
The documentation origin temporarily returned maintenance HTML; endpoint probes succeeded, so reliability is a current observation, not an SLA.
The official [TML docs repository](https://github.com/tmlmobilidade/docs) provides additional source evidence.

## Observable payload and request evidence

Hub envelope: data[], error, generated_at, status_code, timestamp.
Each position includes agency_id, vehicle_id, latitude, longitude, route_id, route_short_name, trip_id, pattern_id, shape_id, created_at and received_at (milliseconds), with nullable bearing/speed/stop_id/license_plate.
Use created_at for observation age, never received_at or local fetch time as a substitute.
The plans endpoint returns is_active, active_from/active_until (YYYYMMDD), agency_id/name, updated_at, and operation_gtfs_normalized_url/original_url.
Select active plans covering the Lisbon service date; URLs rotate, so discover them from the catalog.
Normalized GTFS retains raw route IDs (e.g. TTSL3_0); hub route IDs prefix [AGENCY] and trip/shape IDs prefix [PLAN][AGENCY]. Strip only verified leading prefixes and keep operator identity in every join. Normalized GTFS numeric agency IDs differ from hub identifiers.
CM vehicle fields include id, lat/lon, timestamp, line_id, route_id, trip_id, pattern_id and nullable model/license_plate.
Do not claim static feed download time establishes service validity or realtime freshness.

## Feature rules

Maps render reported provider positions and stops, plus verified GTFS geometry when present.
Search and provider filters use normalized routes/stops; CP is geographically limited to the Lisbon region.
Active count includes observations no older than180 seconds; stale vehicles remain visible only with stale labels.
Sampled speed estimates distance between consecutive plausible reported coordinates divided by elapsed time; exact commercial speed remains unavailable. Missing sampled speeds remain null.
Historical speed/volume and detected fleet use only this installation's retained snapshots.
Completed trips, precise headways, full fleet inventory, model/registration crosswalks and citywide road traffic are unavailable unless evidence supports them.
Arrivals and journeys from GTFS are explicitly scheduled; service calendars, exceptions, Lisbon timezone/DST and times beyond24:00 must be respected.
An empty successful realtime response means no current observations, not an upstream failure; a failed fetch preserves the previous cache and its timestamps.

## Reference and setup sources

[Reference site](https://moverlisboa.com/) observed with Playwright: white left sidebar, full-height attributed OpenFreeMap map, four tabs, eight operator controls, route/stop search, metric strip, expandable charts, fleet and trips tables.
Screenshots and [inventory](../reference/INVENTORY.md): desktop, mobile, history, traffic and fleet.
[roodle setup config](https://github.com/rtfpessoa/roodle/blob/main/server/api/config.yml), [generator directive](https://github.com/rtfpessoa/roodle/blob/main/server/api/doc.go), and [handler setup](https://github.com/rtfpessoa/roodle/blob/main/server/main.go) were inspected; it uses legacy v1.16.2, so only the setup pattern is referenced with v2. [roodle](https://github.com/rtfpessoa/roodle) is restricted to Go OpenAPI server setup reference; its domain/application code is not reused.
[Official oapi-codegen net/http setup](https://github.com/oapi-codegen/oapi-codegen/blob/main/docs/stdhttp-server.md), [strict generation](https://github.com/oapi-codegen/oapi-codegen#strict-server), and [oazapfts usage](https://github.com/oazapfts/oazapfts) support one-spec generation.
[Google ID-token verification](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token) defines signature, audience, issuer and expiry validation; only real verified tokens create production sessions.

Additional verified sources: [hub vehicle documentation](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/hub/v1/vehicles.mdx), [plans](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/hub/v1/plans.mdx), [metadata](https://go.tmlmobilidade.pt/hub/api/v1/vehicles/metadata), [TCB direct GTFS](https://backend.tcbarreiro.pt/download-gtfs) and [TCB license/open data](https://tcbarreiro.pt/tcb-transportes-colectivos-do-barreiro/dados-abertos/).
Hub metadata is a bare array; MobiCascais matching uses hub [HF16N]3001 to agency code21 metadata vehicle_id21-3001; CM agency codes41/42/43/44 similarly.
[Official commercial-speed definition](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/go/performance/metrics/operation/commercial_speed.mdx) requires completed ride distance and duration; sampled speed is labeled separately.
The original MobiCascais catalog was archived according to the [official catalog](https://dados.gov.pt/api/1/datasets/gtfs-rede-mobi-cascais/); prefer current hub plans.

## Subscribed Metro verification and archive limits

After the user's subscription update, a read-only credential-backed probe on2026-09-26 verified HTTP200 from `POST https://api.metrolisboa.pt:8243/token` using Basic consumer key/secret and `grant_type=client_credentials`. The token's actual expires_in is honored, including responses with only17/38seconds remaining. No token or consumer secret was recorded.

Authenticated API base `https://api.metrolisboa.pt:8243/estadoServicoML/1.0.1`: `/tempoEspera/Estacao/todos` returned75 platform observations; `/estadoLinha/todos` returned `codigo:"200"` and a resposta object with amarela/azul/verde/vermelha state strings and tipo_msg/curta fields; `/infoEstacao/todos` returned50 station records. `tempoChegada1` is numeric seconds or `"--"`; `hora` is compact Lisbon local time. Old observations and duplicate station/train/destination predictions are discarded. Line mapping is used only where terminal codes or single-line station membership establish it; unknown routes stay unknown. Station matching uses exact source code when available, otherwise normalized name plus verified nearby coordinates. Predictions never become GPS measurements.

Initial unauthenticated/403 probe results above predate subscription authorization. The implementation now supports the verified authenticated endpoints; credentials are supplied server-side via `config/metro.op.env`.

The active normalized Carris archive contains38.9MB compressed and401.6MB expanded, including4,654,086 stop-time rows. The parser streams CSV, bounds archives to64MiB compressed/512MiB expanded and5million rows per file, reuses record storage safely and interns stop identifiers. Static data is cached as compressed database chunks. Direct Carris GTFS is a viable alternative but lacks the normalized shapes and fleet metadata needed for these views.

Published hub fleet metadata is incorporated for MobiCascais and CM using verified agency code prefixes; entries from unknown agencies or conflicting prefixes are rejected. Static fleet metadata does not establish inventory completeness or permanent allocation.

Map bundling follows the [official MapLibre Vite worker instructions](https://maplibre.org/maplibre-gl-js/docs/): bundle the worker with `?worker&url` before constructing a map.
