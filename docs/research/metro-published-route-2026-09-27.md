# Metro published route evidence

Source inspection dated 2026-09-27; this is implementation evidence, not a verified production deployment revision.

The official Metro tracker chooses an approximate scheduled ride using the train's destination.
Its `findRideForTrain` looks up the destination station, queries rides by that headsign within one hour before/after
the reference instant, and chooses the middle returned ride.
This establishes the role of the trip as a destination/path hint, not a physical train's scheduled allocation.
[Official selector](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/apps/pt-tml-ml-api-fetch/src/find-ride-for-train.ts).

The tracker combines that ride's shape with the next published stop to infer a position.
It emits the selected `trip_id`, the next `stop_id` and `IN_TRANSIT_TO` status.
The emitted position is consequently approximate even when its trip ID matches static GTFS exactly.
[Official tracker](https://github.com/tmlmobilidade/go/blob/prd/modules/tracker/apps/pt-tml-ml-api-fetch/src/index.ts).

The application uses this hint solely for an explicitly separate published-route association.
It preserves exact plan/route matching, ordered visits, missing stop clocks and independent direct-destination checks.
It does not claim a verified train timetable, completed journey or real stop occurrence.
[Implemented selector](../../internal/app/popup_metro_route.go),
[contract](../../api/openapi.yaml), [behavior](../VEHICLE-POPUPS.md).

The evidence was obtained by reading the two official source files; no authenticated Metro probe or deployment check
was performed for this change.
