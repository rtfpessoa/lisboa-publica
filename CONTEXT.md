# Lisbon transit

Lisbon public transport services and their observed operation.

## Language

**Operator**: An organization providing public transport services in the Lisbon region.

**Route**: A named service with one or more directions and scheduled journeys.

**Direction**: The orientation of travel along a route. Journeys ending at different destinations can share a direction, including a service that terminates before the route's usual terminus.

**Journey destination**: The endpoint of a particular journey. It can differ from the usual terminus for its direction.

**Published route**: An ordered sequence of passenger stops for a line and direction. Associating it with a vehicle does not identify a dated journey or establish that vehicle's arrival and departure times.

**Stop**: A place where passengers board or leave a service.

**Station**: A passenger stop encompassing one or more boarding places and their lines and directions.

**Platform**: A boarding place within a station. Separate platforms do not imply separate stations or uniquely identify a train.

**Stop visit**: One occurrence of a passenger stop in the ordered journey. Repeated visits to the same stop are distinct occurrences.

**Actual arrival**: An arrival at a stop visit explicitly reported as having occurred. A prediction or a change of position or stop status does not establish its exact time.

**Actual departure**: A departure from a stop visit explicitly reported as having occurred. It is separate from arrival; neither an arrival nor an inferred movement establishes its exact time.

**Vehicle observation**: A provider report of a vehicle's location at a stated time.
_Avoid_: Live vehicle when the report is stale

**Vehicle reporting state**: The availability of up-to-date provider reporting for a vehicle. It is separate from physical movement, published stop status and whether the vehicle is in service.
_Avoid_: Stopped vehicle solely because reporting is missing

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


**Inferred stop event**: An estimated passage, arrival or departure at a passenger stop, supported by indirect evidence. It retains its uncertainty and is distinct from a physically confirmed event.

**Inference consistency check**: A check that inferred stop events and journey associations agree with the available transport reports, chronology and route context. It does not confirm that the inferred physical operation occurred.

**Independent event reference**: A record of a service's passage, arrival or departure obtained independently of the predictions or estimated positions under evaluation. Its event convention and timing uncertainty determine what it can validate.

**Physical event validation**: Comparison of inferred stop events or journey associations with an admissible independent record of the service's physical operation. Agreement with predictions derived from the same source is not this validation.

**Stop arrival**: The event at which a stopping service becomes stationary at its passenger stop or platform. It is distinct from opening the doors or passing the stop without stopping.

**Stop departure**: The event at which a service starts moving again to continue its journey after stopping at a passenger stop.

**Stop dwell time**: The interval from a vehicle becoming stationary at a passenger stop until it starts moving again. It is distinct from the time its doors remain open.

**Segment travel time**: The interval from departure at one passenger stop to arrival at the next stop on the journey. It excludes dwell at both stops.

**Arrival-to-arrival elapsed time**: The interval between arrival at one passenger stop and arrival at the next stop on the same journey. It includes dwell at the origin and travel to the next stop, and excludes dwell at that next stop.

**Hourly operating pattern**: A summary of a service's passages, intervals, stop dwell times or travel times grouped by hour for a route, direction and passenger stop or segment, on comparable days. Data gaps remain distinct from an hour without service.

**Hourly passage occurrence proportion**: The share of comparable days with at least one admissible passage in a selected hour among days with sufficient evidence coverage for that hour. It describes occurrence across days, not when a particular journey will arrive or how long a passenger will wait.

**Hybrid arrival prediction**: An arrival estimate combining an official prediction to a reference stop with historical travel and intermediate dwell times for the remaining route to the target stop. It depends on the official prediction and a supported association with the same operational journey.

**Waiting time prediction**: An estimate of how long until the next service arrives at a selected passenger stop in a selected direction.

**Onward arrival prediction**: An estimate of when the same identified journey will reach its subsequent passenger stops. Associating a journey does not by itself establish the identity of the physical vehicle.

**Remaining journey time**: The time from a journey's current progress to arrival at a selected downstream stop, including the unfinished travel and intermediate stop dwell times. It excludes travel and stops already completed, and dwell after arrival at the target stop.

**Operational journey**: A service's run along a route in one direction. Reversing direction at a terminal starts a new journey, even if the physical vehicle stays the same.

**Service day**: The operational date assigned to a journey by its operator. It can differ from the local civil date of an event after midnight; an unpublished and unsupported service day remains unknown.

**Journey continuity**: The supported association of updates with the same operational journey. An ambiguous association leaves continuity unknown.
