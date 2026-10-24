package app

import (
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func metroPastStationFixture(t *testing.T, retain bool) (*Server, *StaticData, *MetroData, time.Time) {
	t.Helper()
	s, static, data, now := metroLiveFixture(t)
	data.Stations = append(data.Stations, MetroStation{ID: "AA", Name: "Amadora", Lat: "38.69", Lon: "-9.14", Lines: "[Verde]"})
	static.Stops = append(static.Stops, api.Stop{Id: "metro:gtfs-aa", SourceId: "aa", Name: "Amadora", Lat: 38.69, Lon: -9.14})
	static.Schedule.Trips[0].Times = append(static.Schedule.Trips[0].Times, StopTime{Stop: "aa", Sequence: 3, Arrival: 241, Departure: 241})
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "1"), metroTestRow(now, "CS", "7", "5"), metroTestRow(now, "AA", "7", "120")}
	publishMetroTest(s, static, data, now)
	if len(data.Trains) != 1 || len(data.Trains[0].Calls) != 3 {
		t.Fatal("initial topology unavailable")
	}
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "0"), metroTestRow(now.Add(time.Second), "AA", "7", "119")}
	if retain {
		next.Waits = append(next.Waits, metroTestRow(now.Add(time.Second), "RM", "7", "null"))
	}
	publishMetroTest(s, static, &next, now.Add(time.Second))
	if len(next.Trains) != 1 || next.Trains[0].Calls[1].Arrival.Inferred == nil {
		t.Fatal("supported downstream arrival missing")
	}
	if retain {
		r := s.Cache.metroRuntime
		r.mu.Lock()
		point, exists := r.tracks[next.Trains[0].JourneyId].Points["RM"]
		r.mu.Unlock()
		if !exists || point.Seconds != nil {
			t.Fatal("retained upstream baseline missing")
		}
	}
	return s, static, &next, now
}

func TestMetroPastNullWaitCannotRenewEpisode(t *testing.T) {
	s, static, data, now := metroPastStationFixture(t, false)
	before := data.Trains[0].SourceUpdatedAt
	data.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "RM", "7", "null")}
	publishMetroTest(s, static, data, now.Add(2*time.Second))
	if data.Trains[0].Association == "supported" || !data.Trains[0].SourceUpdatedAt.Equal(before) {
		t.Fatal("a past station's null wait renewed the episode")
	}
}

// A reused reference at a retained upstream station is not proof of the same
// episode, even when its source clock advances or another prior is active.
func TestMetroRetainedPastStationCannotRenewEpisode(t *testing.T) {
	for _, prior := range []struct {
		name      string
		ambiguous bool
	}{{"unique", false}, {"ambiguous", true}} {
		for _, tc := range []struct{ name, wait string }{{"positive", "45"}, {"zero", "0"}, {"null", "null"}} {
			t.Run(prior.name+"/"+tc.name, func(t *testing.T) {
				s, static, data, now := metroPastStationFixture(t, true)
				original := data.Trains[0]
				if prior.ambiguous {
					addMetroAmbiguousTestPrior(s.Cache.metroRuntime, original.JourneyId)
				}
				data.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "RM", "7", tc.wait)}
				publishMetroTest(s, static, data, now.Add(3*time.Second))
				assertMetroEpisodeNotRenewed(t, data.Trains, original)
			})
		}
	}
}

func addMetroAmbiguousTestPrior(r *metroRuntime, journey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	other := *r.tracks[journey]
	other.Train = cloneMetroTrain(other.Train)
	other.Train.JourneyId = "other"
	r.tracks["other"] = &other
	r.active[other.Train.RouteId+"|opposite|"+other.Train.Reference] = "other"
}

func assertMetroEpisodeNotRenewed(t *testing.T, trains []api.MetroTrain, original api.MetroTrain) {
	t.Helper()
	for _, train := range trains {
		if train.JourneyId == original.JourneyId {
			if train.Association == "supported" || !train.SourceUpdatedAt.Equal(original.SourceUpdatedAt) {
				t.Fatal("a retained past station renewed the episode without downstream support")
			}
			return
		}
	}
	t.Fatal("original episode missing")
}
