# Lisbon transit

Lisbon public transport services and their observed operation.

## Language

**Operator**: An organization providing public transport services in the Lisbon region.

**Route**: A named service with one or more directions and scheduled journeys.

**Stop**: A place where passengers board or leave a service.

**Vehicle observation**: A provider report of a vehicle's location at a stated time.
_Avoid_: Live vehicle when the report is stale

**Published stop status**: A provider's statement about a vehicle's progress relative to a passenger stop, such as approaching, standing at the stop, or travelling towards it. A statement derived from estimated operation is not physical confirmation.

**Last-known state**: The most recent available position or operational statement, with its original time. It does not necessarily describe the vehicle's current state.

**Data gap**: An interval without a newer observation. A data gap does not establish whether a vehicle is stationary, moving, or out of service.
_Avoid_: Inactive vehicle solely because observations stopped

**Scheduled trip**: A planned journey on a route; it does not prove the journey occurred.

**Observed trip**: A journey identified in provider observations; incomplete observations do not prove completion.

**Fleet view**: Vehicles detected during the selected period, not an operator's complete registered inventory.

**Snapshot**: A retained observation used to inspect past operation.

**Commercial speed estimate**: Distance between valid consecutive vehicle observations divided by elapsed time, including observed stops.
_Avoid_: Measured road speed
