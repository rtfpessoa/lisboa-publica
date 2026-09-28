# Metro live map and popups

The live Metro map and both popups consume a single complete dynamic frame. The browser keeps static
stops and line geometry in their existing catalogues. Other operators retain their existing collection and
popup paths. [OpenAPI](../api/openapi.yaml) defines the wire contract; generated Go and TypeScript are
produced with `make generate`.

## Collection and clocks

`METRO_REFRESH_MILLISECONDS` defaults to 500 and accepts 500–60000. The Metro collector serializes
status/waits requests, reuses tokens/stations, and measures the next start from the preceding start.
Slow requests discard missed ticks; there is no catch-up burst. Collection status and inference use the
completed-response receipt clock, separately from cycle-start timing. A source publication during the
request can therefore support a transition without being treated as a future receipt. The shared transport counts actual attempts,
including token, metadata, errors and redirects, under its existing 900/rolling-minute ceiling and tighter
host budgets/cooldowns. Normal Metro pairs cost 240 calls/minute. Subscription consumers must share one
collector/budget: this is a process budget, not a distributed quota coordinator for unrelated external clients.

Original platform `hora` clocks are parsed in Europe/Lisbon. Waits require a JSON integer in 0–7200 seconds;
null, missing, strings, fractions, negative values and invalid train references are unavailable. Their absence
cannot produce a zero-time arrival. Freshness is 90 seconds with the existing future-clock skew allowance;
an inferred past arrival additionally cannot use a clock later than its receipt. A receipt or browser render
never renews the original clock. Hub positions have a dedicated one-second minimum refresh-start target, with no overlapping or catch-up work. Faster publication is limited to Metro; the existing five-second consumer uses the shared latest Hub response for other operators. Shared ETA and other source schedules remain unchanged. Positions protect 20 Hub and 100 global attempt slots plus bounded in-flight protected request chains, slow to two/five seconds under pressure, and require a full healthy rolling minute per recovery step. Provider cooldowns/Retry-After take precedence. These are local policies, not provider freshness guarantees.

## Association and history

A published reference is shown as `Comboio <reference>`, without claiming physical fleet identity.
The popup association is an inferred episode keyed by supported route, destination context and reference,
with an independently generated journey ID. A unique ordered GTFS topology and uniquely matched nearby
station metadata are required. Popup call IDs use the unique published GTFS station-family root; matching child platforms collapse only through valid published parent relationships. Distinct roots and invalid parent chains remain unresolved. Conflicting platforms, incompatible forecast order, changed values under the same source clock, backwards clocks and expired support suspend the affected context. Coexisting direction forecasts alone do not reject a reference. The shared classifier scopes evidence by line/reference/destination/path, retains coherent prior continuity, and leaves an initially ambiguous reference unlinked. A missing optional wait cannot compete with a usable context. It never chooses a context by minimum ETA, row order or approximate Hub trip.
Source gaps clear transition memory. Plan changes and expired association support create separate episodes;
midnight and approximate Hub trip changes alone do not reset identity. Return runs remain separate and the
selected journey ID stays pinned until an explicit vehicle selection. Selecting the same map reference
again can open its currently supported new episode; data refreshes never release the pin.

The map link uses exactly one supported classified context for the same line/reference and a fresh scoped Hub observation. A second raw destination matcher is not used. Valid local forecasts remain visible even when a unique whole path is missing; no journey or vehicle link is invented. An ambiguous vehicle popup preserves its pinned timeline and shows valid reference forecasts grouped by direction under “Viagem por confirmar”. Station counts describe supported identified episodes, not all physical trains. Direction selection
uses the admitted topology's route/destination context. A unique longer path containing the same ordered
short-turn path provides its canonical direction; ambiguous candidates retain their published context.
Unassociated forecasts appear only for the selected direction or explicitly as direction unconfirmed.
Unknown or short-turn contexts without compatible topology cannot admit a journey; usable local forecasts remain separate and are not silently assigned a terminal direction.

Previous/current/remaining visits come from the ordered path. Live progress is estimated from usable direct
arrival anchors; missing reachability stays explicit. Each original update is processed before browser coalescing.
Positive-to-zero publication transitions under a continuous supported context create `Chegada inferida` evidence,
with the preceding positive and first zero source clocks. The displayed point/window describes publication evidence,
not a physical confidence interval. Isolated zero, countdown expiry and first sighting after a gap do not create events.
A later incompatible positive wait withdraws the inferred arrival and retains a correction proof before
backwards-progress suspension. When the correction proof has committed, durable restoration selects it
rather than the earlier arrival. Failed or pending writes cannot guarantee that a later restoration includes
an uncommitted withdrawal; history status and persistence labels expose that boundary. Previously issued
immutable frames remain unchanged.

The guarded [departure detector](../internal/patterns/metro_movement.go) emits at the first
qualified source displacement after a supported stop, beyond the larger frozen noise/resolution envelope.
The versioned wait adapter binds a reviewed station-axis geometry, segment durations, original waits,
profile/direction and visit. A supported positive-to-zero arrival anchors a stop; opaque Hub positions and
render ticks cannot qualify one. Model interpolation cannot independently confirm its own direction.
Three advancing source station anchors and two consistent above-envelope steps on the same fixed axis
confirm movement direction. Candidate departures retain their first movement clock separately from later
confirmation; absent confirmation or qualification keeps their passenger times unavailable.

`METRO_MODEL_ALLOWLIST` optionally points to a bounded reviewed configuration file. Its entries must pass
[original-input model-consistency replay](metro-departure-calibration.md), checksums, whole-journey holdout
and prohibited-input controls. No qualifying actual-source configuration was supplied for this release;
therefore production model departures and local modeled coordinates remain unavailable. A temporary gap
preserves established historical estimates. Same-visit model regression/correction withdraws the main
value as “Estimativa retirada”, retaining bounded revisions and original evidence. This is experimental
model support, not physical timing calibration.

The existing forecast engine can contribute own predictions only through an active episode with at least three
supported station signals matching the popup's retained original transition windows, the same route/destination
and exact compatible profile. Reference equality is insufficient. Official and own predictions remain independent,
with original expected times/expiry; the two popups read the same projection. Missing own support is explicit.
Arrival-to-arrival components are not used as physical segment travel time for the departure detector.

## Delivery and UI

`GET /api/v1/metro/live/stream` accepts one Metro map route filter and either a selected vehicle/journey or station.
It emits `reset` on every initial/reconnection, then complete `frame` events only when scoped content changes.
Event IDs are connection-local monotonic cursors; frame revisions are opaque content hashes and journey IDs
are association identities. Last-Event-ID does not request replay. Source validity expires without another receipt.
Frames include the selected journey outside the route filter, and the station view's supported inventory.
An oversized frame or exhausted episode inventory is rejected explicitly, never silently truncated.

`GET /api/v1/metro/live` builds the same frame. It returns an interest-specific ETag and 304 for a matching
If-None-Match without renewing source clocks. The browser uses this combined fallback at least five seconds
apart only while SSE is unavailable, extending waits for Retry-After. Repeated reconnect failures share
that minimum interval, including successful and 304 snapshot attempts. A successful reset cancels fallback;
late/aborted responses cannot overwrite newer streamed state. A missing initial reset and fallback reads
are bounded by ten-second timeouts. Reconnect backoff is 1–30 seconds.
Hidden tabs close streams/reads and reconnect with a full reset when visible. There are no separate Metro
vehicle, station board, journey or station-vehicle polling loops in the live view.

Countdowns recalculate expected instant minus current time every second and on visibility return. A new forecast
can increase the countdown; expiry never becomes history. Both forecast origins are labeled separately.
Station sections distinguish upcoming, other and suspended/old contexts, including missing ETA rows.
Rows sort by usable official ETA, otherwise own ETA, then missing, with stable journey IDs for ties.
Station direction and reading/focus anchors survive frame replacement/reconnection.

Selecting a supported journey starts camera following. Manual pan/zoom pauses it; `Retomar seguimento` resumes
the same pinned journey. Lost support suspends following without extrapolation or switching to a return run.
`Ver próxima estação` scrolls the list only. Reduced motion disables follow animation. Countdown ticks do not
rebuild vehicle map sources; validity/reporting boundary changes still update markers.

## Durability and resource limits

Complete latest journey checkpoints use the archive owner's additive `popup-checkpoint` lane, independently of the `popup-events` proofs. Checkpoints retain the ordered calls, original source clock, topology profile, original contributing points and event proof revisions. They use verified compressed blocks and the same atomic manifest/allocated-byte FIFO owner. A batch can admit several journey records through one generation. The seven-day target is subordinate to the existing shared 10,000,000,000 allocated-byte cap and original source age; reads and commit clocks do not extend it.

A new identity remains internal until its complete baseline commits. Before that, or when the archive is unavailable, source forecasts/map positions remain usable without a selectable journey. Subsequent progress coalesces by identity; revision/generation/commit metadata distinguishes pending progress from the last committed revision. Healthy checkpoint batches flush every second, and frames read confirmed runtime state directly, without waiting for another source poll. A late commit acknowledgement cannot discard a newer queued revision.

Checkpoints are capped at 256 KiB including metadata (runtime payload admission retains 1 KiB for metadata). Dirty identities and proof records have individual count caps of 1024 and share an 8 MiB pending budget that includes a 1 KiB reservation per checkpoint for encoded metadata. Proofs are at most 64 KiB. Hot association consultation is separately bounded to 1024 episodes and 256 ordered visits per episode. Paths or writes exceeding bounds fail explicitly; data is never silently truncated. Without an archive owner, no pending writes or selectable identities are promised.

Pinned recovery looks up one verified latest checkpoint by its manifest key, including journeys with no stop events. It preserves identities, calls and original clocks, honors the original-age TTL for hot and cold reads, but restores history with suspended association, no current/next visit, no predictions and no live movement continuity. Legacy event-only records retain explicitly partial recovery. Missing records are explicitly unavailable; absence cannot distinguish eviction from a never-committed identity. Known expiry/corruption have distinct outcomes. A selected ID is returned only when its record is in the frame. The browser may preserve its last received timeline while displaying the explicit recovery limitation. Pending progress can be lost in a crash; restored history is the last committed state, not a guarantee of the latest pre-crash observation.

Streams default to 256 KiB encoded frames, 64/process, 16/IP and 4/authenticated principal. These admission
limits are configurable through `Options.MetroStreamLimits`; they are starting limits, not measured capacity.
Each stream processes at most twice per second with no unbounded unsent frame queue. Writes have a five-second
deadline cleared after each successful flush, and comments heartbeat every 15 seconds. Projection holds finite
read admission only while building a frame; transient occupied read slots coalesce the next tick
without closing a healthy connection. Normal JSON deadlines are preserved. Authentication uses the same
public/session/header-key policy as other reads, closes at expiry and checks revocation/scopes every 30 seconds.
No credentials belong in URLs. Header-key clients must use a streaming fetch client rather than native EventSource.

## Validation boundary

Synthetic Go tests cover parser absence semantics, original transitions, conflicts/gaps, evidence separation,
coalesced history, conditional/scoped frames, capacity failures, connection admission, principal expiry
and SSE cancellation. Archive tests cover dedupe,
restart, corruption and seven-day TTL. Movement tests cover the first admissible movement and rejected inputs.
Forecast binding tests reject reference-only, insufficient, duplicate and incompatible episode support.
Browser tests cover inventory/countdowns, a shared selected journey, camera pause, reconnect preservation
and five-second fallback cancellation after a successful reset. Explicit same-reference selection can
release a previous journey pin. A slow synthetic socket verifies bounded write admission cleanup; a
slow collector fixture verifies serialized 500 ms starts without catch-up requests.
These establish code behavior; they do not establish physical event accuracy or provider completeness.

The healthy-SSE P95 backend frame-publication to changed popup DOM revision objective is one second.
The earlier [completed mixed recovery assessment](validation/metro-live-recovery-2026-09-28.md) used the actual
Go/Caddy/compiled frontend, 32 clients for 15 minutes and mixed station/vehicle views. Sixteen clients used
390 px viewports; a controlled 50 ms request plus 50 ms response-write application relay also delayed ongoing
SSE writes. The measured healthy-stream DOM P95 was 969 ms and all ten recovery assertions passed. This is
an explicitly simulated application-delay profile, not a measured 100 ms packet RTT or mobile hardware result.
Cap/slow-client/auth checks remain component evidence; compressed wire bytes were not measured.

Independent physical timing validation requires synchronized observations and a frozen holdout calibration.
Source cadence experiments, synthetic replay and the offline candidate report cannot substitute for it.
The [earlier transport assessment](validation/metro-live-release-2026-09-28.md) preserves its original
positive-only scope, while [departure preparation](metro-departure-calibration.md) describes the current
collection/assessment workflow and live-admission boundary.

The first shared-classification/checkpoint slice is covered by its [dated validation](validation/metro-association-checkpoints-2026-09-28.md). The [completion follow-up](validation/metro-live-completion-2026-09-28.md) implements the guarded adapter, lifecycle and local rendering, with separate baseline admission measurement. Production activation still requires retained actual-source qualification; no profile is enabled by synthetic checks.

Verified cold checkpoint recovery can populate the bounded hot historical cache without restoring continuity or creating writes. Failed recovery consultation has at most 1024 negative entries: unavailable/expired results are memoized for 60 seconds, other failed results for five seconds. This avoids repeated legacy event scans on every stream tick; these timers do not alter source clocks or retained evidence lifetime.

When an explicit missing-pin recovery retains the browser's previous timeline, active official/own predictions and current/next markers are removed; historical evidence remains available.

Initial SSE reset projection waits at most one second for the existing two-slot expensive-read admission. The existing process/IP/principal stream limits bound waiters, and cancellation releases admission. Sustained pressure still returns HTTP 503; ordinary snapshots remain fail-fast and established SSE ticks coalesce temporary busy projections. This avoids converting brief scope/pin initialization contention into a five-second fallback cycle without increasing concurrency.

## Completion and local model rendering

A newly supported positive-to-zero arrival at the admitted final visit stages inferred completion.
Countdown expiry, an isolated zero, proximity and intermediate arrivals cannot close the journey.
Closure revisions freeze until their complete checkpoint commits; later source updates cannot coalesce
away the mandatory lifecycle record. The timeline remains consultable, with no active terminal journey.
Later forecasts under the same reference cannot reopen that completed episode.

A compatible opposite-direction candidate remains private until three qualified fixed-axis positions
confirm its direction. Handoff closes the previous active association and activates the successor in one
archive generation. First movement and confirmation clocks are separate; old popup pins retain their
identity and require explicit “Abrir viagem atual” selection. Restart restores relationships/history,
never direction/movement continuity. Forecast coexistence alone cannot activate a return journey.

For qualified configurations only, the backend supplies an absolute source-bound segment interval and
frozen station-axis endpoints. The browser evaluates that explicitly labeled estimated position every
500 ms, moving the map/follow target without additional requests or inference events. This is linear
station-axis modeling, not an assertion of exact track geometry or GPS. Invalid/expired support stops
interpolation; it cannot manufacture arrival at the interval endpoint or renew source clocks.
