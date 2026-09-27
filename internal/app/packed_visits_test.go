package app

import (
	"reflect"
	"testing"
)

func TestPackedVisitsPreserveIndependentClocksRepeatedStopsAndSequences(t *testing.T) {
	visits := []StopTime{{"S", 90000, 90001, 1}, {"S", -1, 90100, 4000000}, {"N", 90200, -1, 4000001}, {"S", -1, -1, 4000002}}
	trip := ScheduledTrip{PackedTimes: packVisits(visits), PackedCount: len(visits)}
	if !reflect.DeepEqual(localJourneyTimes(&trip), visits) {
		t.Fatal("packed visits changed published evidence")
	}
	for _, blob := range [][]byte{{2}, {1, 255}, {1, 129, 1}} {
		trip.PackedTimes = blob
		if localJourneyTimes(&trip) != nil {
			t.Fatal("malformed visits accepted")
		}
	}
}
