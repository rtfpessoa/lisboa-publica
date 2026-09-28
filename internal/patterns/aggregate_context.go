package patterns

import "time"

// Segment identifies the route, direction and endpoints of an adjacent component.
type Segment struct{ Route, Direction, Origin, Target string }

type groupIdentity struct{ Train, Direction, Route string }
type plannedOrigin struct{ Stop, Direction, Route string }
type aggregateRequest struct {
	at                                                         time.Time
	route, direction, stop, platform, profile, condition, kind string
}
