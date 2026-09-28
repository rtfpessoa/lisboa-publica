package app

import (
	"context"
	"testing"
)

// Synthetic parent/platform family reproduces the public Alameda catalog shape.
func TestMetroStationProjectionCollapsesPublishedPlatformFamily(t *testing.T) {
	server, static, data, now := metroLiveFixture(t)
	parent := static.Stops[0].Id
	for _, id := range []string{"platform-one", "platform-two"} {
		platform := static.Stops[0]
		platform.Id = "metro:" + id
		platform.SourceId = id
		platform.ParentId = &parent
		static.Stops = append(static.Stops, platform)
	}
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(server, static, data, now)
	frame, _, err := server.metroFrame(context.Background(), metroInterest{Stop: parent})
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Directions) != 1 {
		t.Fatalf("station family lost its direction catalogue: %v", frame.Directions)
	}
	if len(frame.Trains) != 1 || len(frame.Trains[0].Calls) != 1 {
		t.Fatalf("station family lost train calls: %v", frame.Trains)
	}
	call := frame.Trains[0].Calls[0]
	if call.StopId != parent || call.Arrival.Kind != "prediction" {
		t.Fatalf("call does not navigate to the published station: %+v", call)
	}
}

func TestMetroStationProjectionDoesNotMergeDistinctStationRoots(t *testing.T) {
	_, static, data, _ := metroLiveFixture(t)
	other := static.Stops[0]
	other.Id = "metro:another-station"
	other.SourceId = "another-station"
	static.Stops = append(static.Stops, other)
	path := metroTopology(data, static).Patterns[0]
	calls := metroPathCalls(path, data, static, "metro:synthetic")
	if calls[0].StopId != "metro:RM" {
		t.Fatalf("ambiguous stations were guessed: %v", calls[0].StopId)
	}
}

func TestMetroStationProjectionRejectsInvalidParentFamilies(t *testing.T) {
	for _, kind := range []string{"missing", "cycle", "other-operator", "different-station"} {
		t.Run(kind, func(t *testing.T) {
			_, static, data, _ := metroLiveFixture(t)
			stop := static.Stops[0]
			parent := stop
			parent.Id = "metro:parent"
			stop.ParentId = &parent.Id
			switch kind {
			case "cycle":
				parent.ParentId = &stop.Id
			case "other-operator":
				parent.Id = "cp:parent"
			case "different-station":
				parent.Name = "Unrelated station"
			}
			static.Stops[0] = stop
			if kind != "missing" {
				static.Stops = append(static.Stops, parent)
			}
			calls := metroPathCalls(metroTopology(data, static).Patterns[0], data, static, "metro:synthetic")
			if calls[0].StopId != "metro:RM" {
				t.Fatalf("invalid %s parent was accepted: %s", kind, calls[0].StopId)
			}
		})
	}
}
