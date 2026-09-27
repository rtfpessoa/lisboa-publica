# OpenFreeMap

OpenFreeMap supplies the browser's background map style and the resources that style references. Transport entities, predictions and official network geometry come from the application API and its transport sources, not from the background-map integration.

## Resource and collection boundary

[Map initialization](../../frontend/src/Map.tsx) passes `https://tiles.openfreemap.org/styles/positron` to MapLibre, with initial center `[-9.145, 38.731]` and zoom `12.3`. There are no source credentials or backend collection loop for this resource. MapLibre fetches the style and required resources as the map loads and the viewport changes. Browser/network caching follows the returned resource policies; the application does not impose its five-second transport cadence or provider budget here.

The style is interpreted by MapLibre. Resource URLs resolved from it can change; this reference does not claim an exhaustively audited current set of tile, sprite or glyph hosts. No style/tile fields are normalized into transport entities or persisted in the application database.

## Consumed data and application layers

| Input | Use/output | Storage |
|---|---|---|
| Positron style document | Background map rendering and style-resolved resources | Browser/renderer-managed, not application SQL |
| Tile/style image/glyph resources as referenced | Background cartography and labels | Browser/renderer-managed |
| Application API vehicle/stop coordinates | Local GeoJSON point layers and click selection | Transport cache/history rules apply to the source API data |
| Application route/network shape coordinates | Local GeoJSON line overlays | Source static cache; see [GTFS geometry](tml-hub.md#gtfs-field-inventory) |
| Application traffic/CP arrival projections | Local rendered layers | Their application dataset lifecycle, not OpenFreeMap transport records |
| Generated operator markers | Browser-rendered images | In-memory renderer state |

The app adds GeoJSON sources for vehicles, stops, CP arrivals, route, traffic and network. These are overlay consumers, not additional OpenFreeMap API endpoints. Metro estimated markers, stopped-state markers and stale opacity are application presentation decisions.

## Availability and attribution

The map installs a compact attribution control. Preserve attribution when changing styles/layers. External resource errors surface a map notice; absence of a background resource does not prove transport-source failure. The UI's map rendering and source-health reporting remain separate concerns.

Synthetic example: a usable GTFS route shape is drawn above the Positron background. If the background resource fails, that is a presentation dependency failure; it must not turn scheduled route data into an unavailable provider observation.

## Evidence

Implementation: [Map.tsx](../../frontend/src/Map.tsx), [MapLibre dependency](../../frontend/package.json), [API query consumers](../../frontend/src/App.tsx). Dated reference/rendering evidence is in [source research](../research/SOURCES.md) and [validation](../VALIDATION.md). No new remote-style availability or licensing audit was performed for this code reference.
