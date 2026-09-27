package app

import "encoding/binary"

// Lossless road-operator visit storage uses small deltas while retaining each published
// stop identity, sequence, arrival and departure independently.
func packVisits(visits []StopTime) []byte {
	result := []byte{1}
	var arrival, sequence int64
	for _, visit := range visits {
		result = binary.AppendUvarint(result, uint64(len(visit.Stop)))
		result = append(result, visit.Stop...)
		result = binary.AppendVarint(result, int64(visit.Arrival)-arrival)
		result = binary.AppendVarint(result, int64(visit.Departure)-int64(visit.Arrival))
		result = binary.AppendVarint(result, int64(visit.Sequence)-sequence)
		arrival, sequence = int64(visit.Arrival), int64(visit.Sequence)
	}
	return compressPackedVisits(result)
}

type packedVisitReader struct {
	remaining         []byte
	arrival, sequence int64
}

func (reader *packedVisitReader) next() (StopTime, bool) {
	var visit StopTime
	stop, ok := reader.stop()
	if !ok {
		return visit, false
	}
	values, ok := reader.deltas()
	if !ok {
		return visit, false
	}
	reader.arrival += values[0]
	reader.sequence += values[2]
	departure := reader.arrival + values[1]
	if !reader.valid(departure) {
		return visit, false
	}
	return StopTime{stop, int32(reader.arrival), int32(departure), int(reader.sequence)}, true
}

func (reader *packedVisitReader) stop() (string, bool) {
	size, length := binary.Uvarint(reader.remaining)
	if length <= 0 || size > 128 || size > uint64(len(reader.remaining)-length) {
		return "", false
	}
	stop := string(reader.remaining[length : length+int(size)])
	reader.remaining = reader.remaining[length+int(size):]
	return stop, true
}

func (reader *packedVisitReader) deltas() ([3]int64, bool) {
	values := [3]int64{}
	for n := range values {
		value, used := binary.Varint(reader.remaining)
		if used <= 0 {
			return values, false
		}
		values[n] = value
		reader.remaining = reader.remaining[used:]
	}
	return values, true
}

func (reader *packedVisitReader) valid(departure int64) bool {
	return packedClock(reader.arrival) && packedClock(departure) && reader.sequence >= 0 && reader.sequence <= int64(^uint(0)>>1)
}

func packedClock(value int64) bool {
	return value >= -1 && value <= maxGTFSServiceHours*secondsPerHour+secondsPerHour-1
}

func localJourneyTimes(trip *ScheduledTrip) []StopTime {
	if len(trip.PackedTimes) == 0 {
		return trip.Times
	}
	visits := decodePackedVisits(trip.PackedTimes)
	if len(visits) == trip.PackedCount {
		return visits
	}
	return nil
}

func decodePackedVisits(blob []byte) []StopTime {
	blob = expandPackedVisits(blob)
	if len(blob) == 0 || blob[0] != 1 {
		return nil
	}
	reader := packedVisitReader{remaining: blob[1:]}
	visits := []StopTime{}
	for len(reader.remaining) > 0 {
		visit, ok := reader.next()
		if !ok {
			return nil
		}
		visits = append(visits, visit)
	}
	return visits
}

func journeyLocalCount(trip *ScheduledTrip) int {
	if len(trip.PackedTimes) > 0 {
		return trip.PackedCount
	}
	return len(trip.Times)
}

func journeyVisitCount(trip *ScheduledTrip) int {
	if len(trip.JourneyTimes) > 0 {
		return len(trip.JourneyTimes)
	}
	return journeyLocalCount(trip)
}
