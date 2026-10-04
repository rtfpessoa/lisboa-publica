# Metro de Lisboa direct API

The direct integration supplies line status, station metadata and waiting-time predictions. It complements [Hub estimated positions and GTFS](tml-hub.md), which remain a separate source. Direct predictions are not GPS or confirmation that a train physically arrived.

## Access and collection

| Method/resource | Role |
|---|---|
| `POST https://api.metrolisboa.pt:8243/token` | OAuth client-credentials token |
| `GET /estadoLinha/todos` | Four line states |
| `GET /tempoEspera/Estacao/todos` | Platform waits/train references |
| `GET /infoEstacao/todos` | Station names/codes/coordinates/line membership |
| `GET /infoDestinos/todos` | Published destination-code/name catalogue |

Data paths use `https://api.metrolisboa.pt:8243/estadoServicoML/1.0.1`. Configure server-only `METRO_CLIENT_ID` and `METRO_CLIENT_SECRET`; [the 1Password reference file](../../config/metro.op.env) supplies secret references, not values. [Deployment](../../deploy/README.md) documents operational TLS handling; verification remains enabled. No browser OAuth callback is used by the client-credentials grant.

The Metro loop runs with both credentials even if general ingestion is disabled. Its waits lane has a dedicated 500 ms minimum start target (`METRO_REFRESH_MILLISECONDS`, 500–60000), a 20-second context and no overlapping/catch-up work. A separate metadata lane verifies line states every 30 seconds and refreshes station/destination catalogues daily, retrying missing/failed catalogues. Cached catalogues preserve their own receipt ages. Fresh waits remain available when status collection fails; status age/error is exposed separately. These are acquisition policies, not source-update promises.

The shared provider budget includes token calls and permitted redirects. An HTTP 401 clears the cached token for a later attempt; the code does not immediately busy-retry. Missing credentials produce unconfigured direct status while Hub schedules/estimates remain independent.

## OAuth and envelopes

| Input/output | Application handling |
|---|---|
| Request Basic credentials | Server consumer ID/secret; never supplied to frontend |
| Form `grant_type=client_credentials` | Fixed grant |
| `access_token` | Nonempty string; transient in-memory bearer |
| `expires_in` | Positive integer seconds; token expiry uses `ttl - min(10 seconds, ttl/4)` |
| `token_type` | Must equal `Bearer`, case-insensitively |
| Data envelope `codigo` | Numeric `200` or string `"200"` required |
| Data envelope `resposta` | Present JSON decoded into the endpoint-specific shape |

OAuth response decoding is bounded to 64 KiB; data envelopes to 2 MiB. Tokens are not persisted with station/wait state. Expiry respects the returned lifetime with a safety margin; it is not assumed to be a fixed hour.

## Line-state fields

`resposta` for line state is decoded as a string map. [The status mapper](../../internal/app/metro.go) reads:

| Keys | Treatment/output |
|---|---|
| `amarela`, `azul`, `verde`, `vermelha` | Nonempty state text required for each line |
| `amarela_curta`, `azul_curta`, `verde_curta`, `vermelha_curta` | Trimmed short description |

A line is displayed as normal only when its state equals `ok` case-insensitively and short description equals `normal`; otherwise the published text is shown as a provider warning. Missing line state makes direct status error and clears new waits. Other decoded map keys do not populate the public line-status projection.

## Station fields

| Raw field | Expected type/use | Output/association |
|---|---|---|
| `stop_id` | String station code | Exact code lookup and wait membership |
| `stop_name` | String | Destination display and fallback name matching |
| `stop_lat`, `stop_lon` | Numeric strings parsed for fallback | Coordinate-tolerance association to GTFS |
| `linha` | Published string listing line membership | Single-line inference when destination does not establish line |
| `stop_url`, `zone_id` | Bracketed URL string / zone string | Retained in the direct cache and input interpretation; no invented array or physical platform mapping |

The direct station records are held in the Metro cache, not substituted wholesale for the public GTFS stop catalogue. [Station/line matching](../data/associations.md#metro-station-and-line-matching) first uses exact codes, then the unique normalized-name-prefix or exact compact-name/coordinate fallback. Multiple official station candidates are rejected; the tolerance is not a metric-distance threshold. Live popup calls map back to the unique GTFS station family, collapsing published platforms through a complete, acyclic Metro parent chain with a matching root. Distinct roots or invalid parent relationships retain the official-code fallback.

## Waiting-time fields

| Raw field | Type/validation | Application use |
|---|---|---|
| `stop_id` | String | Match the selected direct station |
| `cais` | String | Decoded as `Platform`, retained in direct cache; not used to construct public arrivals |
| `hora` | String `YYYYMMDDHHMMSS`, Europe/Lisbon | Original `observed_at`; must be fresh within 90 seconds and not over 30 seconds ahead |
| `comboio`, `comboio2`, `comboio3` | Train-reference strings | Nonempty reference required for generic arrival `trip_id`; missing references remain anonymous forecasts in Metro live frames |
| `tempoChegada1`, `tempoChegada2`, `tempoChegada3` | Raw JSON decoded into an integer and bounded to 0–7,200 seconds | `expected_at=hora+wait`; JSON `null`, omitted fields, `"--"`, malformed values, fractions and numeric strings are rejected |
| `destino` | String published destination code | Terminal/line lookup, headsign and prediction identity |

Prediction IDs combine station, train and destination, qualified under `metro`. Source URL and `kind=prediction` stay explicit. Recognized terminal codes or single-line station membership establish line; unknown route remains unknown. Empty or unknown destination codes do not prevent prediction admission; an unmatched destination uses a published-code fallback label. Duplicate prediction identities retain the newer source observation. Results honor query time/route/stop filters and sort by expected time then ID.

## Usage and storage

| Data | API/UI consumer | Durable cache | Historical role |
|---|---|---|---|
| Line states/availability | Metro status and source panel | Direct status in `cache_parts` kind `direct`; direct health metadata | No vehicle-history observations |
| Wait/station raw fields | Predicted-arrival construction | Same compressed/chunked direct cache | No retained prediction history |
| Public predicted arrivals | Stop arrivals/Metro UI | Derived from published direct state | Never converted to measured positions/speed/distance |
| OAuth token | Server requests only | None | None |
| Hub Metro estimated positions | Map/network context through Hub | Separate static/live Hub state | Excluded from valid sampled speed/distance |

Metro publishes direct health/state in memory regardless of successful durable writes; persistence is attempted at most every 30 seconds. Restart restores direct state with its original source clocks, but a restored wait is not automatically a usable fresh prediction. [Arrival reads](../../internal/app/metro.go) require successful/recent direct status and original fresh wait time.

On fetch failure, prior waits/stations may remain cached with error status, and predicted-arrival reads withhold unusable direct predictions. Planned GTFS schedules remain distinct. Explicit JSON null and omitted waits remain unavailable; neither can manufacture a zero-second forecast or inferred arrival. Unknown route or station associations are not fabricated. For a stop/reference/destination forecast, the
latest original publication wins. The generic arrivals selector withholds different ETAs under the same latest clock until a newer unambiguous publication arrives. Live forecast contexts retain usable conflicting named rows with a disclosed limitation; they cannot supply coherent event/path support. Provider row order cannot resolve the conflict.

## Examples and evidence

Synthetic example: numeric `tempoChegada1=120` with valid `hora=T` and nonempty `comboio` yields expected time `T+120 seconds`. Recollecting the same `hora` does not make it newer. `tempoChegada1="--"` omits that prediction, and explicit `tempoChegada1=null` is also unavailable. A `cais` value can exist in the durable cache without a public arrival platform field.

Implementation: [Metro client, matching, arrivals and storage](../../internal/app/metro.go), [shared limits](../../internal/app/limits.go), [startup conditions](../../cmd/server/main.go). Existing evidence: Metro cases in [app tests](../../internal/app/app_test.go), [scope/security checks](../../internal/app/security_fixes_test.go), [upstream policy tests](../../internal/app/upstream_policy_test.go). Dated subscribed-endpoint observations are in [source research](../research/SOURCES.md#subscribed-metro-verification-and-archive-limits). Access can change; this reference states the implemented client contract, not subscription availability.

## Popup directions and safe vehicle journeys

Station boards group each line by validated route orientation; a compatible short variant can share the direction while showing its distinct destination on that row. Direct waits remain station forecasts independently of any vehicle journey. An exact fresh direct train identifier can establish a destination only when the matching records agree. The approximate Hub vehicle-trip assignment cannot establish a complete timed vehicle journey or historical actual stop occurrences. See [popup behavior](../VEHICLE-POPUPS.md).

The UI presents a verified GTFS parent station and its child boarding places as one station popup with all station directions. Direct `cais` is still not associated with a particular GTFS child platform. A visit at or behind a train's supported marker never renders a forward countdown: it shows the inferred occurrence, else the retained `last_official_estimate` (a new nullable per-visit field kept outside `arrival.prediction`, exempt from prediction expiry and withdrawn with a retracted arrival), else explicit unknown wording. Departure has no last-official-estimate class: it shows only a model departure estimate or explicit unknown. Station coverage considers the forecasts actually returned in the board or selected result page; an unavailable shared TML publication cannot hide usable direct Metro waits. Conversely, expired direct waits do not become current merely because TML or the browser refreshes. The page's coverage clock is the latest contributing original prediction update when supplied; individual evidence retains its own clocks and expiry. The same original clocks govern retained frames during request failures.

The compatibility vehicle-journey endpoint can use that approximate Hub trip as a line/direction path hint under the same exact current plan/route, while retaining an explicitly estimated association. A fresh direct destination must not contradict it. Without a trip reference, that direct destination can select a published path only when every matching route candidate has the same ordered stop IDs/sequences. The route has no journey ID, train timetable or actual/predicted stop times; fresh unique published stop/status evidence can give estimated progress. No new Metro requests, credentials or persistence are involved. The [dated source check](../research/metro-published-route-2026-09-27.md) explains the provider's destination-based approximate trip selection and its limits.

Hub inferred Metro entity IDs also receive latest reporting state. This tracks accepted publication membership and source clocks, not physical train activity. Direct station waits/destinations do not renew the Hub observation or establish that an inferred entity is still reporting. See [reporting states](../data/README.md#backend-owned-reporting-state).

## Pattern collection and original wait preservation

The experimental collector reuses `tempoEspera/Estacao/todos` from the existing authenticated refresh. `MetroData.raw_waits` retains all fields of the decoded response array, including unknown nested values; typed `waits` remains available to existing live publication. The archive stores that source array and normalized inputs without issuing another request. Errors retain a gap/status rather than presenting old predictions as new training.

| Source field | Pattern use | Limitation |
|---|---|---|
| `stop_id`, `cais`, `destino` | Station/platform/destination context | Duplicate contexts are rejected; destination does not identify a physical journey |
| `hora` | Provider source clock in Europe/Lisbon | Offset absent; ambiguous/nonexistent civil clocks rejected; experimental eligibility requires age 0–90 s |
| `comboio`, `comboio2`, `comboio3` | Published ID and eligible presence across slots | Missing/placeholder/duplicated IDs reject support; no physical fleet identity inferred |
| `tempoChegada1` | Positive-to-zero first-slot signal | Initial/repeated zero is insufficient; three planned consecutive stations and continuity required |
| `tempoChegada1/2/3` | Future official anchor/target for the supported ID | Nonfinite/negative/invalid values rejected; missing or ambiguous official values stay unavailable |
| Unknown fields, including fields with unverified semantics | Preserved in sampled source JSON | Not used as departure/dwell/speed evidence |

Planned topology comes from the existing GTFS schedule (including compact retained visits), separately from ETA order. Nearby station matching additionally handles exact alphanumeric punctuation variants with unique coordinates within the existing tolerance; ambiguous candidates are not selected. Source profile/topology, cadence and resolution separate histories. Official future arrival anchors plus compatible future arrival-to-arrival components produce explicitly dependent own estimates. The new read endpoint is `/api/v1/metro/patterns`; existing service status, route navigation, arrivals and estimated-position behavior remain available. [Current model](../metro-patterns.md) documents calibration, both forecast functions and unsupported physical metrics.

The local completion follow-up adds route-specific conditions, versioned Lisbon holiday grouping, labeled older/general-context component fallback, unchanged-segment compatibility, conservative mixed-bin calibration and durable bounded MAE/P90/availability/band-support reports. Evidence-backed maintenance revises retained inputs atomically while keeping issued values. Staged normalized observation/prediction capture and experimental own-forecast adapters cover all eight existing operators under the same archive budget. Metro uses ETA transitions; later stages require verified published paths and coherent reported stop-state transitions. Forecast availability depends on actual compatible inputs, and physical validation remains unavailable. See [current behavior](../metro-patterns.md) and [remaining live evidence](../GAPS-metro-patterns.md).

The patterns read can expose fresh direct-cache official points independently of experimental history. It applies the strict future-slot/source-clock eligibility, rejects conflicting simultaneous platform/destination rows and never inserts this response-only fallback into training or evaluation. Existing arrivals parsing described above remains a separate surface.

## Coherent operational service projection

[The current live map and popups](../metro-live-popups.md) use one classifier and current estimated episode. Direction can be operationally estimated from a unique direct context or a validated three-position modeled chain; physical allocation/accuracy remains unknown. Coexisting opposing forecasts remain independently visible. Current estimates no longer require the legacy reviewed model allowlist or a committed baseline.

`infoDestinos` supplies string `id_destino` / `nome_destino` pairs. Unique normalized destination names join GTFS headsigns; older caches retain the verified legacy mapping fallback. Unsupported codes remain unresolved. The 2026-09-29 authenticated audit found no physical `cais` crosswalk and no observed fleet-unit/live-reference join. Static interval catalogues are not polled as train inventory or independent timing evidence.

GTFS platform stops resolve through their parent station and the unique name/coordinate match against the published Metro catalogue (unresolved stops are reported); `pattern_id` is retained for Metro trip/context checks. Source model fields, direct catalogues, original `hora`, full ordered visits and positive timetable dwell/run priors retain separate meanings. Arrival-to-arrival historical proxies do not identify dwell and run separately. [The timing evidence](../plans/20260929-metro-static-timing-evidence.json) records 29,292 positive scheduled dwell values, not measured stops.

Official arrivals and experimental own arrival/departure forecasts remain separate. Schedule-based cold starts are labeled; elapsed dwell conditions compatible distributions, and supported departure evidence anchors later running time. A qualified modeled stop/movement transition can disclose an experimental departure occurrence, with distinct first-movement/confirmation clocks and no physical accuracy claim. Unsupported times stay unknown.

Anonymous forecast tracks preserve source-row/slot provenance and unique revision/slot-shift continuity. Candidate owners use reachable same-direction paths, ETA overlap and comparable non-overtaking anchors; an unseen owner remains feasible. No ownership is asserted without heldout qualification. Candidates never create a map train or named anchor. Station aggregation preserves platform clocks, conflicts and unresolved directions.

Memory commits complete lifecycle transitions immediately. [Checkpoint/capture durability](../data/history.md) is separate, bounded and asynchronous; incomplete persistence does not hide current forecasts. Restart restores suspended committed history without live detector continuity. The [actual regression fixtures](../../internal/app/testdata/metro-20260928/README.md) and [dated plan](../plans/20260929-metro-first-principles.md) identify evidence and admission limits.
