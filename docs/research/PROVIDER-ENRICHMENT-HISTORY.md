# Runtime provider fields, UI connections and historical storage

Research by main,2026-09-27. Limited read-only public source inspection and single endpoint samples; source populations vary. No Mover API requests, credentials or private subscriptions.

## Source-to-feature map

| Provider | Consumed sources | Useful additional verified fields | Feature and limits |
|---|---|---|---|
| Carris | Shared Hub positions, active normalized GTFS | vehicles.txt published capacities/accessibility where present | Exact existing vehicleID only; no depot allocation. Existing models/plates already reach fleet and popups. |
| Carris Metropolitana | Directv2 vehicles; catalogs; four existing GTFS archives; Hub metadata | capacity_seated/standing/total, wheelchair_accessible/contactless; Hub available_seats, wheelchair, contactless, propulsion | Published specification in popup. Values are not live free-seat/occupancy measurements. Existing agency-qualified identity crosswalk only. |
| Mobi Cascais | Hub positions/metadata; active GTFS | available_seats, wheelchair, contactless, propulsion | Strip only verified21-prefix. Same optional specification labels. |
| Metro Lisboa | Direct OAuth status/waits/stations plus Hub estimated positions/static | Already consumed prediction/platform/service fields | Physical fleet113units does not match estimated train entities; no invented capacity/model joins. |
| CP | Hub positions, national normalized GTFS, new public Hub trip updates | Verified arrival timestamps/delay, planned service/endpoints, human stop names | New accepted CP map/popups implementation. No ETA-to-GPS conversion. Public feed coverage/expiry clearly labeled. |
| Fertagus | Hub positions and GTFS | Published physical fleet metadata has no verified liveIDcrosswalk | Preserve schedule/geometry; missing physical specifications explicitly unavailable. |
| TTSL | Hub positions and GTFS | Checked vehicles.txt/metadata empty | Keep verified station/route/schedule fields; no vessel allocation from descriptive fleet pages. |
| TCB | Hub positions and GTFS | Checked vehicles.txt/metadata empty | Same explicit unavailability; no inferred fleet specifications. |

[Official Hub documentation](https://go.tmlmobilidade.pt/reference/hub) and [vehicles extension](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/gtfs/schedule/vehicles.mdx) describe published network and vehicle fields. [CM vehicle types](https://github.com/carrismetropolitana/api/blob/v2/packages/types/src/api/vehicles.ts) distinguish capacities from occupancy. Hub metadata sample1461rows: CMagency-prefixed1315, Mobi122; others do not establish exact live joins. available_seats is an integer published in vehicle metadata, treated as seated specification, not dynamic availability. Null/absent remains unknown; explicitfalse/zero retained. [Official operation vehicle schema](https://github.com/tmlmobilidade/go/blob/prd/packages-new/types/operation/src/vehicles/vehicle.ts) places available_seats under specifications. [Hub schema](https://github.com/tmlmobilidade/go/blob/prd/packages-new/types/hub/src/v1/api/vehicles/vehicle-metadata.ts) defaults wheelchair/contactless tofalse: a negative published value may reflect source omission, so UI says “Não indicado”, not a verified absence of equipment. True is labeled published, null unavailable. Hub vehicle_type values bus/tram are modes, not typology codes. Propulsion code schemes differ across feed versions; keep raw published values.

## Requests, access and freshness

Reuse existing fetched bodies; no new request frequency. See [verified budgets](POLLING-LIMITS.md): public TML numeric quota unpublished, conservative120/min; public CM50/s documented, app40/s; authenticated Metro user subscription1000/min, shared application900actualattempts/min. CP now adds12logicalrequests/min at5s. Normal baseline is60/min, cold static/token adds17; three actual attempts each stays conservative, hardtransportbudget still protects all requests/cooldowns. Metadata5min, static6h, live5s. Provider observation clocks remain authoritative, not fetch time. Empty positions can reflect source omission; retained positions carry original freshness and do not enter current metrics.

## UI wiring audit

Live model/plate/typology/propulsion already reach vehicle popups; historical five-field metadata reaches fleet table/distributions. Station arrivals and route schedules are wired independently of GPS. The CP work fixes train-vs-station overlapping clicks, shared paginated revision windows and arrival visibility. Newly identified gap: Hub propulsion/accessibility/capacity fields are discarded at decoding; direct CM capacity fields likewise. Implement end-to-end optional specifications, not a new fleet/station analytics product. Unsupported metadata cannot be repaired with descriptive pages or mismatched identifiers.

## Storage evidence and decision

cache_parts already stores gzip. New snapshots contain only source_id, model, license_plate, typology and propulsion metadata plus typed SQL analytical facts; existing history collector reduces positions to five-minute records. A local53014-row representative restored database measured average JSONB119.34bytes and row287.10bytes excluding indexes/MVCC. Such small payloads are poor individual gzip targets. PostgreSQL [TOAST](https://www.postgresql.org/docs/current/storage-toast.html) normally compresses larger values transparently, not these tiny rows.

Preserve queryable facts and compress only old large legacy full-vehicle payloads when beneficial. A nullable per-row gzip archive plus unchanged metadata projection is lossless and compatible with existing fleet SQL and rollback binaries. No dictionary service, hot/cold query engine, retention increase or forced tiny-row gzip. Tests quantify fixture savings; actual savings depend on legacy population and database reclamation. App guard still stops history around4GB, operations4.5GB, absolute5GB; code cannot enforce an instantaneous cluster billing cap against unrelated writers/replication/MVCC outside its measurement.

Native compression clarification: [Cockroach engine setting](https://github.com/cockroachdb/cockroach/blob/master/pkg/storage/pebble.go) controls SST compression; encoded pg_column_size is not physical compressed storage. Extra app archival remains a no-op on Cockroach where marginal savings cannot be established. On PostgreSQL, use stored pg_column_size including existing TOAST, not JSON text length. This is a deliberate safeguard against growing the bounded production database.
