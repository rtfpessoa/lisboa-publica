# Runtime upstream limits and useful polling — 2026-09-26

Read-only independent research; main records decisions and owns tests. No load tests or Mover APIs.

| Runtime source | Verified limit/access | Cache/freshness evidence | Collection |
|---|---|---|---|
| TML Hub positions (Carris,TCB,Mobi,Metro,CP,TTSL,Fertagus) | Public; numeric quota undocumented | Cache-Control max-age3; publisher1s; provider adapters vary1–30s |5s shared request; local120/rolling60s conservative policy |
| CM v2 vehicles | Public;50req/s perIP burst50; sharedglobal500/s burst200;429 rejection | max-age5; synchronization1s |5s; local40/rolling1s |
| Metro status/waits | OAuth; user-confirmed subscribed1000/min, public plan not independently visible | Official TML adapter5s; direct publication SLA undocumented |5s; token reused; shared900/rolling60s actualattempts |
| Metro stations/token | Same subscription | Stations static; token expires_in |Stations initially cached; token on demand |
| Hub plans/metadata | Public; quota undocumented | plans max-age300; metadata HTTPcache5s does not imply specificationchanges5s |5min |
| CM lines/stops | Same CM limits | max-age3600 |Existing6h |
| Official normalized Oracle GTFS objects | Public published URLs; objectquota undocumented | Immutable planassets |Existing6h bounded sequential downloads |

Sources: [CM nginx](https://github.com/carrismetropolitana/api/blob/v2/apps/nginx/configs/nginx.conf), [CM vehicles endpoint](https://github.com/carrismetropolitana/api/blob/v2/apps/server/src/endpoints/network/vehicles.endpoint.ts), [CM synchronization](https://github.com/carrismetropolitana/api/blob/v2/apps/sync-vehicles/src/index.ts), [Hub positions](https://github.com/tmlmobilidade/go/blob/prd/modules/hub/apps/api/src/endpoints/v1/vehicles/handlers/get-vehicle-positions-json.ts), [Hub publisher](https://github.com/tmlmobilidade/go/blob/prd/modules/hub/apps/publish-vehicles/src/index.ts), [Hub metadata](https://github.com/tmlmobilidade/go/blob/prd/modules/hub/apps/api/src/endpoints/v1/vehicles/handlers/get-vehicle-metadata-json.ts), [Metro API store](https://api.metrolisboa.pt/store/apis/info?name=EstadoServicoML&provider=admin&version=1.0.1), [Metro adapter](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/apps/pt-tml-ml-api-fetch/src/index.ts).

Official adapter schedules: Carris5s,Metro5s,Fertagus5s,CP1s,TTSL1s,TCB10s,Mobi30s. These are collector schedules, not freshness guarantees. Bounded public probes12:02:27–12:02:45UTC returned200, no quota headers/ETag/Retry-After; CM Last-Modified. Median observation ages Carris37s,Metro17.7s,Fertagus8s,Mobi30s,TCB51s,TTSL61s,CM19–27s. CP had zero upstream rows twice. Faster local polling cannot make old source observations newer.

Steady5s cycle:12Hub+12CM+12Metrostatus+12Metrowaits =48 logicalrequests/min. Coldstatic adds15 (plans1,metadata1,sevenarchives,CMcatalog2,fourCMarchives), coldMetro adds token1/stations1. Conservative13cycles yields69 logicalrequests; Go redirect policy permits three actual HTTPattempts total each, thus207 under these assumptions. Hard shared900/rolling60s includes OAuth, conditionalrequests and redirects, protects unusual short tokens/failures, and requires onecollector. Perhost controls are additional bounds. No unknown quota is represented as unlimited.

For429/503 honor Retry-After seconds orHTTPdate without shortening it; absent/invalid header use30/60/120/240/300s increasing cooldown reset on successful response. No immediate retry loop. [RFC9110](https://www.rfc-editor.org/rfc/rfc9110.html#name-retry-after).

[OpenFreeMap](https://openfreemap.org/) explicitly has no request/mapview limit and noSLA; tiles remain demandloaded/cacheable, never polled. [Google verification](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token) is eventdriven and signingkeyCache-Control-aware. Alternative direct provider feeds are not runtime sources and their quotas remain unverified. UI live5s does not cause providerrequests pervisitor; history/trafficSQL remains30s, durablecache/history30s, static6h, metadata5min.

Main credential-backed bounded directMetro probe in the existing deployment used the verified container CA bundle: status/waits200, noCache-Control/ETag/Retry-After/quotaheaders. Two wait reads5s apart advanced newest sourcehora from20260926130621 to20260926130626 (Europe/Lisbon). Old inactive platform rows also remain in the source; existing eligibility checks exclude them. No credentials or tokens were printed/saved.
