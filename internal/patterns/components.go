package patterns

import ()

func segmentID(segment Segment) string {
	return digest([]string{segment.Route, segment.Direction, segment.Origin, segment.Target})
}
func (e *engine) componentKey(segment Segment) string {
	if key := e.Topology.Segments[segmentID(segment)]; key != "" {
		return digest([]any{engineReference(e), calendarVersion, samplingProfile(e.Profile), key})
	}
	return "profile:" + e.Profile
}

// SegmentID identifies an adjacent planned segment for compatibility evidence.
func SegmentID(segment Segment) string {
	return segmentID(segment)
}
