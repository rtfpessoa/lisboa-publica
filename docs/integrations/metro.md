# Metro de Lisboa direct API

The direct integration supplies line status, station metadata and waiting-time predictions. It complements [Hub estimated positions and GTFS](tml-hub.md), which remain a separate source. Direct predictions are not GPS or confirmation that a train physically arrived.

## Access and collection

| Method/resource | Role |
|---|---|
| `POST https://api.metrolisboa.pt:8243/token` | OAuth client-credentials token |
| `GET /estadoLinha/todos` | Four line states |
| `GET /tempoEspera/Estacao/todos` | Platform waits/train references |
| `GET /infoEstacao/todos` | Station names/codes/coordinates/line membership |

Data paths use `https://api.metrolisboa.pt:8243/estadoServicoML/1.0.1`. Configure server-only `METRO_CLIENT_ID` and `METRO_CLIENT_SECRET`; [the 1Password reference file](../../config/metro.op.env) supplies secret references, not values. [Deployment](../../deploy/README.md) documents operational TLS handling; verification remains enabled. No browser OAuth callback is used by the client-credentials grant.

The Metro loop runs with both credentials even if general ingestion is disabled. Refresh is serialized, at most once per five seconds, and has a 20-second context. Each refresh fetches states, then waits; station records are fetched only if no prior station list is available. Restored stations can therefore be reused. These are collection rules, not promises of source updates every five seconds.

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

The direct station records are held in the Metro cache, not substituted wholesale for the public GTFS stop catalogue. [Station/line matching](../data/associations.md#metro-station-and-line-matching) first uses exact codes, then the implemented first-compatible name-prefix/coordinate fallback. There is no general uniqueness rejection or metric-distance check in this fallback.

## Waiting-time fields

| Raw field | Type/validation | Application use |
|---|---|---|
| `stop_id` | String | Match the selected direct station |
| `cais` | String | Decoded as `Platform`, retained in direct cache; not used to construct public arrivals |
| `hora` | String `YYYYMMDDHHMMSS`, Europe/Lisbon | Original `observed_at`; must be fresh within 90 seconds and not over 30 seconds ahead |
| `comboio`, `comboio2`, `comboio3` | Train-reference strings | Nonempty reference required for each corresponding prediction; qualified `trip_id` |
| `tempoChegada1`, `tempoChegada2`, `tempoChegada3` | Raw JSON decoded into an integer and bounded to 0–7,200 seconds | `expected_at=hora+wait`; JSON `null` currently decodes as zero; omitted fields, `"--"`, malformed values and numeric strings are rejected |
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

On fetch failure, prior waits/stations may remain cached with error status, and predicted-arrival reads withhold unusable direct predictions. Planned GTFS schedules remain distinct. The current integer decoder treats an explicit JSON null wait as zero: with a nonempty train reference, fresh `hora` and an admitting query window, this can yield `expected_at=hora`. This is a parser limitation, not evidence that the provider published a zero-second wait. Omitted wait fields are rejected; unknown route or station associations are not fabricated.

## Examples and evidence

Synthetic example: numeric `tempoChegada1=120` with valid `hora=T` and nonempty `comboio` yields expected time `T+120 seconds`. Recollecting the same `hora` does not make it newer. `tempoChegada1="--"` omits that prediction, whereas explicit `tempoChegada1=null` currently behaves as zero if other admission conditions hold. A `cais` value can exist in the durable cache without a public arrival platform field.

Implementation: [Metro client, matching, arrivals and storage](../../internal/app/metro.go), [shared limits](../../internal/app/limits.go), [startup conditions](../../cmd/server/main.go). Existing evidence: Metro cases in [app tests](../../internal/app/app_test.go), [scope/security checks](../../internal/app/security_fixes_test.go), [upstream policy tests](../../internal/app/upstream_policy_test.go). Dated subscribed-endpoint observations are in [source research](../research/SOURCES.md#subscribed-metro-verification-and-archive-limits). Access can change; this reference states the implemented client contract, not subscription availability.

## Popup directions and safe vehicle journeys

Station boards group each line by validated route orientation; a compatible short variant can share the direction while showing its distinct destination on that row. Direct waits remain station forecasts independently of any vehicle journey. An exact fresh direct train identifier can establish a destination only when the matching records agree. The approximate Hub vehicle-trip assignment cannot establish a complete timed vehicle journey or historical actual stop occurrences. See [popup behavior](../VEHICLE-POPUPS.md).
