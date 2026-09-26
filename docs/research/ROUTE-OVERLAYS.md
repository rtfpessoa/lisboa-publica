# Official route overlay evidence

Research performed on 26 September 2026 using bounded public HTTP requests and read-only repository inspection.
No Mover Lisboa API was queried, and no production, implementation, or test files were changed.

## Existing data path

The GTFS reader already parses trips and shapes, but only selects the first encountered shape for each route.
Because trips are traversed from a Go map, that selection is nondeterministic and loses alternate directions and variants.
The reader does not retain `direction_id`.
See [trip parsing](../../internal/app/gtfs_parser.go#L120),
[shape parsing](../../internal/app/gtfs_parser.go#L153),
[first-shape selection](../../internal/app/gtfs_parser.go#L180),
and [geometry construction](../../internal/app/gtfs_parser.go#L199).

`StaticData` already stores route details, and its cache is gzip JSON; extending this static cache avoids another collector.
See [static data](../../internal/app/data.go#L74) and [cache encoding](../../internal/app/store.go#L142).
The API currently exposes one nullable coordinate array only on route detail; route listing returns summaries.
See [OpenAPI](../../api/openapi.yaml#L1769) and [route endpoints](../../internal/app/server.go#L370).
The frontend already renders a selected route as a GeoJSON LineString and has a map layer control.
See [Map.tsx](../../frontend/src/Map.tsx#L10).

## Verified normalized GTFS geometry

Each archive below was discovered from the public [TML Hub plans endpoint](https://go.tmlmobilidade.pt/hub/api/v1/plans),
filtered to its active agency plan, downloaded using its `operation_gtfs_normalized_url`, and inspected directly.
Plan URLs are temporary storage URLs and should continue to be discovered, not hardcoded.
The [official plan documentation](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/hub/v1/plans.mdx)
defines active dates, original/normalized URLs, and recommends normalized plans.

| Operator | Active plan | ZIP bytes | Shapes text bytes | Routes with shape / routes | Unique shapes | Shape points | Referenced variants |
|---|---|---:|---:|---:|---:|---:|---:|
| Metro | RSRHS | 604,026 | 801,222 | 5 / 5 | 19 | 19,033 | 19 |
| Carris | 82YP2 | 38,851,828 | 7,362,321 | 174 / 174 | 323 | 135,796 | 323 |
| TCB | YFKP6 | 1,357,193 | 2,574,251 | 76 / 76 | 76 | 50,052 | 76 |
| MobiCascais | QQQN3 | 1,618,009 | 2,246,623 | 99 / 99 | 144 | 56,280 | 144 |
| CM / Viação Alvorada | VNWG3 | 18,278,273 | 37,107,812 | 271 / 271 | 479 | 875,281 | 474 |
| CM / Rodoviária de Lisboa | PCN1R | 24,538,495 | 21,283,264 | 349 / 349 | 558 | 478,785 | 558 |
| CM / Transportes Sul do Tejo | ZN3JG | 10,798,493 | 13,322,380 | 155 / 155 | 298 | 283,961 | 298 |
| CM / Alsa Todi | K56AM | 21,733,163 | 3,773,522 | 166 / 166 | 306 | 101,633 | 291 |

Counts are observation results from the linked first-party feed, not promised future coverage.
Referenced variants count distinct `(route_id, shape_id, direction_id, headsign)` tuples with at least two shape points.
All inspected trip tables had nonempty `shape_id`; both directions 0 and 1 occur in each non-CM operator.
All individual CM archives stay below the existing 64 MiB compressed and 512 MiB expanded limits;
their expanded totals are respectively 161,587,483, 171,171,411, 117,858,436, and 254,436,817 bytes.
The bounds are defined in [limits.go](../../internal/app/limits.go#L9).

Metro illustrates why one shape is insufficient: route `1_0` references several official shapes such as `A_0_0`,
`A_0_1`, `AA_0_0`, and `1A_0_1`, with direction 0 toward Santa Apolónia and direction 1 toward Reboleira.
MobiCascais route `M01_A` references `M01_A_0` toward CascaiShopping and `M01_A_1` toward Parede Terminal.
Carris route `100_0` references `100_0_ASC` toward Sacavém and `100_0_DESC` toward Martim Moniz.
These examples were read from the respective active trip tables published through [Hub plans](https://go.tmlmobilidade.pt/hub/api/v1/plans).

GTFS defines `trips.shape_id` as the travel-path reference, `direction_id` as a trip direction,
and `shape_pt_sequence` as shape ordering.
Sort coordinates by sequence and emit longitude before latitude.
See the [official GTFS reference](https://gtfs.org/documentation/schedule/reference/)
and [official route/trip examples](https://staging.gtfs.org/documentation/schedule/examples/routes-stops-trips/).
Published shapes should be used directly; connected stop coordinates are not an equivalent path.

## CM JSON alternative and request costs

The current CM collector fetches only lines/stops and consequently has no CM route geometry.
See [cmStatic](../../internal/app/ingest.go#L382).
Its public [v2 lines endpoint](https://api.carrismetropolitana.pt/v2/lines) returned 717 lines,
advertising 1,647 unique pattern IDs in a 341,770-byte response.
The [official API README](https://github.com/carrismetropolitana/api/blob/v2/README.md#patterns)
documents patterns and shapes separately.

An actual request to [patterns/1001_0_2](https://api.carrismetropolitana.pt/v2/patterns/1001_0_2)
returned HTTP 200 and a 178,357-byte array of pattern versions.
The observed version contains `line_id:1001`, `route_id:1001_0`, `direction_id:1`,
`headsign:Alfragide (Hosp Veterinário)`, `shape_id:[XS3H8]2`, and a `valid_on` date list.
A version must be selected using its validity dates rather than assuming the response is a single object.
The corresponding [encoded shape endpoint](https://api.carrismetropolitana.pt/v2/shapes/%5BXS3H8%5D2)
returned HTTP 200, 147,627 bytes, and `geojson.geometry.type:LineString` with 1,204 longitude/latitude coordinates.
It also repeats coordinates under `points`; retaining only the geometry avoids that duplication.
Both endpoints advertised `cache-control: public, max-age=3600` and Last-Modified headers.

Downloading all patterns and their shapes would require approximately 3,294 requests before deduplication,
which exceeds the required 900 requests per rolling 60 seconds if issued in one burst.
This is an inference from the observed 1,647 IDs and the documented two-step lookup.
By comparison, four active CM plan downloads reuse the existing plan discovery response and cost four requests per refresh.
Read only routes/trips/shapes for CM overlays; parsing and retaining all CM stop times is unnecessary for this feature.
The observed CM JSON pattern includes raw numeric `line_id:1001`, while individual GTFS trip records use raw route IDs
such as `1001_0`; no CM route-table row was retained to independently verify its `line_id` field.
Verify the normalized route-table line mapping before attaching GTFS variants to the existing UI line catalog;
do not assume that stripping a route suffix always identifies a line.
See [official CM line/route hierarchy](https://github.com/carrismetropolitana/api/blob/v2/README.md#lines)
and the individual active plans in [Hub plans](https://go.tmlmobilidade.pt/hub/api/v1/plans).

The combined [CM v2 GTFS endpoint](https://api.carrismetropolitana.pt/v2/gtfs) advertised 71,976,485 compressed bytes,
and the bounded download rejected it at 64 MiB.
Do not increase the existing archive bound to accommodate a combined archive when four bounded plans work.
The public Hub plans endpoint advertised a five-minute cache, and individual plan validity provides the schedule dates.
HTTP availability and cache headers do not establish an SLA; geometry refresh should retain prior data on failure and
report its source/plan timestamp separately from real-time vehicle freshness.
See [current static refresh behavior](../../internal/app/ingest.go#L190)
and [official plan fields](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/hub/v1/plans.mdx).

## Minimal source-to-feature recommendation

This is a recommendation inferred from the verified data and existing code, not an implementation decision.

Keep independent `Metro lines` and `Bus routes` switches in the existing map layer panel.
Filter each overlay to selected operators; the bus switch covers Carris, CM, TCB, and MobiCascais.
Use the active normalized GTFS geometries above, preserving each `(route, shape, direction)` variant and optional headsign.
For CM, gather the four active normalized plans in the existing static collector and associate each variant to the
existing CM API line using a verified route-table or CM JSON route mapping, deduplicating identical variants within the operator.
There is no need for another provider service, collector, routing engine, or database geometry extension.
This follows the existing [static cache](../../internal/app/data.go#L74)
and [collector](../../internal/app/ingest.go#L190), plus the verified GTFS fields above.

Expose paginated geometry variants from the same versioned OpenAPI API with operator filtering and immutable revision.
Listing route summaries and making a route-detail request for every visible route would add unnecessary browser requests,
because [listRoutes currently omits geometry](../../internal/app/server.go#L370).
Use a FeatureCollection of LineStrings with operator, route, variant, color and direction properties;
two line layers filtered to Metro and bus categories suffice for independent visibility.
Place these lines below stop/vehicle layers and retain the selected route as a distinct highlight.
MapLibre directly supports [GeoJSON line sources](https://maplibre.org/maplibre-gl-js/docs/examples/geojson-line/),
[source updates](https://maplibre.org/maplibre-gl-js/docs/API/classes/GeoJSONSource/),
and [layer visibility](https://maplibre.org/maplibre-gl-js/docs/API/classes/Map/#setlayoutproperty).

Deduplicate shape coordinates, retain only referenced shapes, and store static geometry once per current provider plan.
The inspected shape inputs total 88,471,395 text bytes across the five requested providers, including four CM plans;
this is input text size, not a measured JSON/database footprint.
Do not store raw ZIPs, full duplicate shape `points`, geometry in vehicle snapshots, or unnecessary CM timetables.
Keep the existing gzip cache and measure encoded cache/storage growth before enabling full overlays;
the current 128 MiB write allowance uses a storage-overhead multiplier, so input text totals cannot prove budget compliance.
See [gzip encoding](../../internal/app/store.go#L142),
[storage limits](../../internal/app/storage_budget.go#L13), and [guarded writes](../../internal/app/store.go#L220).
The existing rolling [upstream transport](../../internal/app/upstream.go#L28),
900-request cap and static cache lifetime in [limits.go](../../internal/app/limits.go#L9)
must also apply to the added four CM requests; UI toggles must consume only the local generated client/API.
Missing or invalid official geometry should produce a visible unavailable state, never an invented straight-line path.

## Main verification after research

The main agent downloaded all eight active normalized archives on2026-09-26 and verified raw GTFS route rows: CM route1001_0 has line_id1001;2002_0→2002;3018_0→3018;4001_0→4001. All four contain the line_id column. The earlier unverified claim is now directly supported by these archived observations. No prefix inference is required. Main parser measurement with2m local projected vertex simplification retained1621CMvariants,478785summedgeometrypoints and1,999,919gzipbytes (7,999,676-byte guarded reservation). Carris323variants/40,861points/21,278,590gzipbytes including its existing timetable, below128MiB allocation guard. Metro19/1401;TCB76/14756;Mobi144/34097.
