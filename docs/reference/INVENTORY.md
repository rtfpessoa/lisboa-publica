# Reference feature inventory

Read-only Playwright observations of [Mover Lisboa](https://moverlisboa.com/) on2026-09-26.

[Desktop](desktop.png): full-height light map, white left sidebar, icon navigation, route/stop search, Carris main card, six realtime operator tiles and Fertagus static card, map attribution/layer controls.
[Mobile](mobile.png): map fills screen, sidebar hidden, compact top search and menu, lower map controls; operator controls open in a drawer.
[History](history.png): selectable historical view and trends; expandable commercial-speed/fleet-volume panels with chart/vehicle views, route ranking tables/distribution charts and metric tabs. Supported rankings use sampled speed, detected trips and partial distance; completion and headway tabs explicitly unavailable.
[Traffic](traffic.png): last-hour/day/month windows, time-of-day interval, configurable speed bins, non-workday exclusion and terminal toggle.
[Fleet](fleet.png): overview/vehicles/models/depots/types subviews, tables and distributions; the original site's private fleet/assignment assets are not acceptable production sources.
Metric strip contains active vehicles, commercial speed, recent speed, trips, frequency and daily distance.
Route context opens fleet and journeys tables with vehicle, registration, model, first/last activity, distance and journeys; stop context opens stop details.

Implementation boundaries: recreate these controls and information hierarchy with independently supported data.
Exact commercial speed/completed trips/headway and depot assignments are explicitly unavailable; replace invented values with clear labels.
Retained local snapshots support sampled speed, partial distance, detected trip IDs and fleet trends.
Show unknown model/registration as unavailable; use only verified matching published metadata.
Traffic shows transit observation estimates; terminal metadata is unavailable unless static feeds publish it.
