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
never renews the original clock. The shared five-second interval/configuration for other providers remains intact.

## Association and history

A published reference is shown as `Comboio <reference>`, without claiming physical fleet identity.
The popup association is an inferred episode keyed by supported route, destination context and reference,
with an independently generated journey ID. A unique ordered GTFS topology and uniquely matched nearby
station metadata are required. Popup call IDs use the unique published GTFS station-family root; matching child platforms collapse only through valid published parent relationships. Distinct roots and invalid parent chains remain unresolved. Conflicting platforms, simultaneous direction contexts, changed values under
the same source clock, backwards clocks and expired support suspend progress and dependent forecasts.
Source gaps clear transition memory. Plan changes and expired association support create separate episodes;
midnight and approximate Hub trip changes alone do not reset identity. Return runs remain separate and the
selected journey ID stays pinned until an explicit vehicle selection. Selecting the same map reference
again can open its currently supported new episode; data refreshes never release the pin.

The map link additionally requires exactly one supported reference, a fresh Hub observation and a unique
matching direct destination. Unassociated official station forecasts remain visible separately with no invented
map link. Station counts describe supported identified episodes, not all physical trains. Direction selection
uses the admitted topology's route/destination context. A unique longer path containing the same ordered
short-turn path provides its canonical direction; ambiguous candidates retain their published context.
Unassociated forecasts appear only for the selected direction or explicitly as direction unconfirmed.
Unknown or short-turn contexts without compatible
topology remain unavailable; they are not silently assigned a terminal direction.

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

The guarded [departure detector](../internal/patterns/metro_movement.go) emits on the first admissible
resumed model movement after a supported stop, without radius exit or a second positive sample. It rejects
uncalibrated parameters, transform/geometry mismatches, fallback/corrected points, repeated/backwards clocks,
backwards progress and gaps. **The current live source adapter has no frozen geometry/replay calibration or
admissible movement transform. Departure projection remains unavailable.** Hub coordinates alone cannot enable
it, and synthetic detector tests do not constitute calibration or physical accuracy validation.
[Offline calibration preparation](metro-departure-calibration.md) defines original-evidence collection,
independent reference windows, frozen journey splits and a reproducible candidate/holdout assessment.
The command never enables live departures.

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

Inferred arrival summaries and original contributing samples use the archive owner's separate `popup-events`
lane, checksummed compressed generations and manifest admission. They never populate certified actual-event SQL
records. The target is seven days inside the existing shared 10,000,000,000 allocated-byte archive budget;
TTL/FIFO can shorten coverage. The sampled raw-response archive remains at least 30 seconds, independently
of original-update processing. Repeated polls do not create repeated event proofs.

Proof revisions are at most 64 KiB. Pending writes are capped at 1024 records and 8 MiB and flush each second
while healthy. Without an archive owner, evidence is immediately marked unavailable and is not queued.
The committed label records successful archive admission, not a guarantee against subsequent TTL/FIFO eviction.
Queue/storage failures are explicit; official live data remains independent. Recovery reads only
verified committed proofs, returns partial event history and never restores live continuity. In-memory association
consultation is separately bounded to 1024 episodes and 256 ordered visits per episode; longer
paths fail inventory admission explicitly. The cache is not a promise that seven days fit in the cache.

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
The [completed mixed recovery assessment](validation/metro-live-recovery-2026-09-28.md) used the actual
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
