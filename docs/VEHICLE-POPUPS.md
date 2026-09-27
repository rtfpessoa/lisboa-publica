# Directions and independent stop times

Station popups show a matrix of lines and directions, with the selected direction beside “Próximas chegadas e partidas”. The default destination is not repeated in every row; a different short-service destination is shown on that visit. Arrival and departure have independent evidence and missing values.

Vehicle popups show the complete safely associated journey, including visits outside the map area and repeated visits to the same stop. The initial page contains the next uniquely identified visit; users can navigate to the beginning and subsequent pages. Existing map paths, nearby-stop navigation, verified vehicle links and published specifications remain available. CM vehicles without an identified journey retain the separate verified published-pattern path or next-stop fallback.

## Cached reads and identity

The additive station board and vehicle journey operations are defined in [OpenAPI](../api/openapi.yaml). Generated Go and TypeScript contracts must be regenerated from that file. Boards default to a two-hour interval and retain all known direction groups independently of the displayed page. Vehicles default to 100 visits per page, up to the existing 500-row limit. No browser read fetches an upstream source.

Board revisions freeze the network and, for requested-stop sources, a publication held by the existing bounded arrival lease. Expiry, eviction or restart requires refreshing the first page. Journey revisions freeze the network/observation, read instant and committed stop-event generation. An omitted journey offset focuses the next visit; an explicit offset selects that page. Visible popups refresh every five seconds and honor `Retry-After`.

A vehicle journey requires a unique trip, matching operator/route/plan and explicit operating date. Frequency-based schedules without a uniquely identified instance and duplicate or unordered visits fail closed. Repeated stops without a published progress sequence do not produce a guessed next visit. The approximate Metro Hub trip assignment does not qualify; an independently identified direct Metro destination can still be shown.

Metro variants share a direction only when the entire shorter parent-stop sequence occurs in the same order in the longer route. Ordinary directions remain scoped to their published route. Journey destination and direction are separate concepts.

## Sources, clocks and gaps

GTFS parsing retains full visits alongside local map visits, sharing the sequence when every visit is local, including independent missing arrival/departure clocks. Service times above 24:00 follow the operating day in Europe/Lisbon, including daylight-saving transitions. The existing four CM archives also provide merged schedules, with explicit line mapping and plan/agency-qualified trips and services. Old static caches refresh through the normal collector. Stop identities share catalog strings, cached schedules restore one trip at a time, and station-to-trip lookups retain at most 32 selected stops within a 4 MiB index budget per network revision. Route/direction catalogs and CM source metadata are shared across trips. Exact GTFS stop-to-line membership narrows station reads. Road-operator local visits use lossless compressed variable-length deltas for stop identity, sequence and independent clocks; they decode only for reads. Complete visits are never removed to meet these budgets.

The existing shared TML ETA request is decoded once for CP, bounded operator publications and requested-stop arrivals. Operator capacity exhaustion marks that operator partial without invalidating other operators. The native CM requested-stop collector and its existing budgets remain active. Predictions without a safe dated instance remain separate station calls and do not inherit vehicle timetables. Collection never resets the original source clock or expiry; source publication time is absent where not supplied.

No current adapter is certified to collect actual stop arrival/departure occurrences. Historical gaps show “Sem registo real”. An elapsed forecast, a GPS position or STOPPED_AT status never creates a real event. Arrival does not establish departure.

The internal stop-event boundary can accept explicit occurrences from future validated adapters. Events become visible only after durable commit; versions are deduplicated and corrections require explicit higher source revisions. Contradictory occurrences remain unavailable. The history budget, retention and collection pause apply to these records. See [history](data/history.md) and [associations](data/associations.md).

## Verification scope

Go tests cover all eight operator identities, safe joins, repeated visits, missing clocks, service dates, cache serialization and durable PostgreSQL event versions. Browser fixtures cover directions, short destinations, independent times, long paginated journeys, expiry and pauses at desktop/mobile widths. Synthetic events validate the contract and durability, not production source coverage.

Dated acceptance evidence, including the enforced Linux memory workload, is in [vehicle popup validation](research/vehicles-ui-validation/VALIDATION.md).
