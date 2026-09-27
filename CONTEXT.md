# Lisbon transit

Lisbon public transport services and their observed operation.

## Language

**Operator**: An organization providing public transport services in the Lisbon region.

**Route**: A named service with one or more directions and scheduled journeys.

**Direction**: The orientation of travel along a route. Journeys ending at different destinations can share a direction, including a service that terminates before the route's usual terminus.

**Journey destination**: The endpoint of a particular journey. It can differ from the usual terminus for its direction.

**Stop**: A place where passengers board or leave a service.

**Stop visit**: One occurrence of a passenger stop in the ordered journey. Repeated visits to the same stop are distinct occurrences.

**Actual arrival**: An arrival at a stop visit explicitly reported as having occurred. A prediction or a change of position or stop status does not establish its exact time.

**Actual departure**: A departure from a stop visit explicitly reported as having occurred. It is separate from arrival; neither an arrival nor an inferred movement establishes its exact time.

**Vehicle observation**: A provider report of a vehicle's location at a stated time.
_Avoid_: Live vehicle when the report is stale

**Published stop status**: A provider's statement about a vehicle's progress relative to a passenger stop, such as approaching, standing at the stop, or travelling towards it. A statement derived from estimated operation is not physical confirmation.

**Last-known state**: The most recent available position or operational statement, with its original time. It does not necessarily describe the vehicle's current state.

**Position awaiting update**: A last published vehicle position temporarily displayed while a newer observation is unavailable. It does not establish movement during the data gap.

**Data gap**: An interval without a newer observation. A data gap does not establish whether a vehicle is stationary, moving, or out of service.
_Avoid_: Inactive vehicle solely because observations stopped

**Scheduled trip**: A planned journey on a route; it does not prove the journey occurred.

**Arrival prediction**: An estimate of when a service will reach a passenger stop. It does not establish the vehicle's location or prove an arrival occurred.

**Published schedule deviation**: A provider's signed estimate relative to the planned time, including early running, zero deviation and lateness. An absent deviation is unknown.

**Source update time**: The time attached to the provider's underlying update. Publishing or collecting the same information again does not make it newer.

**Passenger-facing name**: A readable name for an operator, station, destination or route, distinct from the technical identifier used to associate records.

**Observed trip**: A journey identified in provider observations; incomplete observations do not prove completion.

**Journey instance**: A particular journey on an operating date and in a particular direction. An outbound journey and its return are distinct instances even when the vehicle is the same.

**Fleet view**: Vehicles detected during the selected period, not an operator's complete registered inventory.

**Snapshot**: A retained observation used to inspect past operation.

**Commercial speed estimate**: Distance between valid consecutive vehicle observations divided by elapsed time, including observed stops.
_Avoid_: Measured road speed
