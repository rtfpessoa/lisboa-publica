# Verified fleet fields — 2026-09-26

Read-only source research; main records decisions and owns tests. Counts are probe observations, not complete inventory guarantees. Provenance: existing eight official downloaded normalized GTFS archives and [active plans](https://go.tmlmobilidade.pt/hub/api/v1/plans), [positions](https://go.tmlmobilidade.pt/hub/api/v1/vehicles/positions), [metadata](https://go.tmlmobilidade.pt/hub/api/v1/vehicles/metadata).

| Operator | Metadata and verified identity | Source-to-feature decision |
|---|---|---|
|Carris|841GTFSrecords,841models,781plates;245/270liveIDs match;840make=model|Deduplicate names; expose raw published typology/propulsion|
|CM|FouralreadydownloadedGTFSs1617records;411/434liveIDs match,Hubmetadataonly14agency44records|Read optionalvehicles.txt in existing geometryarchives; qualify [agency]vehicle_id; merge nonempty fields|
|Mobi|164GTFSrecords;43/43liveIDs match|Keep current exact21-prefix crosswalk; expose published codes|
|Metro|113physicalUTunits vs25estimatedentityIDs;0exactmatches|Explain identity unavailable; never attach physicalunitmodel/plate|
|Fertagus|18physical3501–3518 units vs live14239–14246 IDs;0matches|No verified crosswalk; metadata unavailable|
|TCB/TTSL|Publishedvehicles.txt empty in checked activeplans|Metadata unavailable from verifiedsource|
|CP|No current positions/Hubmetadata during probe; largeGTFS not redownloaded|Unavailable unless exact existingGTFSIDmatch; preserve schedule|

[Official vehicles extension](https://github.com/tmlmobilidade/docs/blob/production/docs/reference/gtfs/schedule/vehicles.mdx) documents make/model/license_plate and operator-localvehicle_id. [Legacy typology validation](https://github.com/tmlmobilidade/docs/blob/7ba875de2c387e336629f23be0a5b21e9364c3fe/docs/reference/gtfs/validation-rules/typology_in_allowed_vehicle_types.mdx) says its code list is incomplete; CM and Carris/Mobi schemes differ. Display raw sourcecode, not invented common names. [CM legacy propulsion mapping](https://github.com/carrismetropolitana/api/blob/v2/packages/types/src/api/vehicles.ts#L143) conflicts with currentv30documentation: legacy6electricity/8naturalgas vs newdefinitions. Preserve raw published codes/stringvalues and explicitlylabel them; do not guess semanticlabels.

No inspected feed provides vehicle-to-depot allocation. Passenger stops/stations are not vehicle allocations. Descriptive [CP](https://www.cp.pt/info/pt/automotoras), [Fertagus](https://www.fertagus.pt/Fertagus-pt/Fertagus/Newsletter/Newsletter-n%C2%BA-54), [TTSL](https://ttsl.pt/terminais-e-frota/frota/) fleet pages do not establish exact liveIDjoins; no scraping expansion.

Historical metadata is what was recorded with the observation. New fields appear from collection after this update; prior missing snapshot fields remain explicitly unknown. This preserves frozen historical pages and avoids implying present specifications were historical allocations. Duplicate model text can be safely normalized for display. CM metadata is retained on partial refresh, and nonempty existing verified fields are not erased by blank Hub rows.
