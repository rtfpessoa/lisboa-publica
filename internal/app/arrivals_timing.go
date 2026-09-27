package app

// Three national timing extrema fit in 57 bits, avoiding an allocation and an
// expanded JSON object per non-CP trip. CP retains its existing metadata format.
const arrivalTimingBits = 19
const arrivalTimingMask = (1 << arrivalTimingBits) - 1
const arrivalTimingPresent = 1 << 57
const arrivalTimingInvalid = 1 << 58

func packArrivalTiming(t *cpTripTiming) uint64 {
	if t == nil {
		return 0
	}
	if t.Invalid || t.FirstArrival < 0 || t.FirstDeparture < 0 || t.LastArrival < 0 || t.FirstArrival > arrivalTimingMask || t.FirstDeparture > arrivalTimingMask || t.LastArrival > arrivalTimingMask {
		return arrivalTimingPresent | arrivalTimingInvalid
	}
	return arrivalTimingPresent | uint64(t.FirstArrival) | uint64(t.FirstDeparture)<<arrivalTimingBits | uint64(t.LastArrival)<<(2*arrivalTimingBits)
}
func unpackArrivalTiming(v uint64) *cpTripTiming {
	if v&arrivalTimingPresent == 0 {
		return nil
	}
	return &cpTripTiming{FirstArrival: int(v & arrivalTimingMask), FirstDeparture: int((v >> arrivalTimingBits) & arrivalTimingMask), LastArrival: int((v >> (2 * arrivalTimingBits)) & arrivalTimingMask), Invalid: v&arrivalTimingInvalid != 0}
}

func (d *StaticData) setArrivalMetadata(p provider, frequencies bool) {
	d.ArrivalMetadata = true
	d.HasFrequencies = frequencies
	if p.ID == "cp" {
		d.CPPredictionMetadata = true
		d.CPHasFrequencies = frequencies
	}
}

func (t *ScheduledTrip) compactArrivalTiming(p provider) {
	if p.ID != "cp" {
		t.ArrivalTiming = packArrivalTiming(t.CPTiming)
		t.CPTiming = nil
	}
}
