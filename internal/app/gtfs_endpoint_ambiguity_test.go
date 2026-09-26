package app

import (
	"testing"
	"time"
)

func conflictingEndpointFixture(t *testing.T, rows string) *StaticData {
	t.Helper()
	blob := replaceGTFS(t, shapeArchive(t, false), map[string]string{
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nO,Porto,41.15,-8.61\nP,Other origin,40,-8\nS,Oriente,38.72,-9.15\nD,Faro,37.01,-7.94\nE,Other destination,37,-8\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" + rows,
	})
	p, _ := providerByID("cp")
	d, err := readGTFS(blob, p, "plan", "20260101", "20261231", hubBase, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCPConflictingEndpointSequencesAreUnavailableInEitherOrder(t *testing.T) {
	o := "A,12:00:00,12:00:00,O,1\n"
	p := "A,12:00:00,12:00:00,P,1\n"
	s := "A,13:00:00,13:00:00,S,2\n"
	d := "A,14:00:00,14:00:00,D,3\n"
	e := "A,14:00:00,14:00:00,E,3\n"
	for _, rows := range []string{o + p + s + d, p + o + s + d, o + s + d + e, o + s + e + d} {
		data := conflictingEndpointFixture(t, rows)
		trip := data.Schedule.Trips[0]
		if len(data.Stops) != 1 || len(trip.Times) != 1 || trip.scheduledEndpoints(hubBase, time.Now()) != nil {
			t.Fatal("ambiguous complete endpoints published or local service lost")
		}
	}
}

func TestCPEndpointIdenticalDuplicatesAndInteriorConflictsStayUsable(t *testing.T) {
	o := "A,12:00:00,12:00:00,O,1\n"
	s := "A,13:00:00,13:00:00,S,2\n"
	p := "A,13:00:00,13:00:00,P,2\n"
	d := "A,14:00:00,14:00:00,D,3\n"
	for _, rows := range []string{o + o + s + d + d, o + s + p + d, p + s + d + o} {
		data := conflictingEndpointFixture(t, rows)
		service := data.Schedule.Trips[0].scheduledEndpoints(hubBase, time.Now())
		if service == nil || service.OriginName != "Porto" || service.DestinationName != "Faro" {
			t.Fatal("identical duplicate or interior conflict hid valid extrema")
		}
	}
}
