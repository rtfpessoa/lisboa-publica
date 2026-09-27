package app

import (
	"net/url"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func TestMetroPublishedRouteWithoutTimedJourney(t *testing.T) {
	s, _, v := popupFixture(t, "metro", 3)
	v.OperationalDate = nil
	v.PositionKind = "estimated"
	s.Cache.update("metro", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: time.Now()}, s.Cache.operator("metro"))
	out := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/metro%3Av/journey?limit=2&offset=0")
	if out.Association != "published_route" || out.JourneyId != nil || !out.Complete || out.Page.Total != 3 || len(out.Data) != 2 || !out.Page.HasMore || out.Progress != "estimated" {
		t.Fatalf("published line/direction route unavailable: %+v", out)
	}
	for _, call := range out.Data {
		if call.JourneyId != nil || call.Arrival.Kind != "unavailable" || call.Departure.Kind != "unavailable" || call.Arrival.Schedule != nil || call.Arrival.Prediction != nil || call.Arrival.Actual != nil {
			t.Fatalf("route gained invented journey/time evidence: %+v", call)
		}
		if call.Stop != nil && (call.StopPlanId == nil || *call.StopPlanId != "plan" || call.StopStaticUpdatedAt == nil) {
			t.Fatalf("route stop lost navigation provenance: %+v", call)
		}
	}
	next := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/metro%3Av/journey?limit=2&offset=2&revision="+url.QueryEscape(*out.Page.Revision))
	if next.Association != "published_route" || len(next.Data) != 1 || next.Data[0].StopSequence != 3 || *next.Page.Revision != *out.Page.Revision {
		t.Fatalf("route pagination changed: %+v", next)
	}
}

func TestMetroPublishedRouteRejectsUnsafeEvidence(t *testing.T) {
	for _, scenario := range []string{"plan", "missing_plan", "route", "missing_route", "trip", "duplicate", "sequence", "direction", "old_progress", "repeated_stop"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, v := popupFixture(t, "metro", 3)
			switch scenario {
			case "plan":
				v.PlanId = ptr("different")
			case "missing_plan":
				v.PlanId = nil
			case "route":
				v.RouteId = ptr("metro:different")
			case "missing_route":
				v.RouteId = nil
			case "trip":
				v.TripId = nil
			case "duplicate":
				d.Schedule.Trips = append(d.Schedule.Trips, d.Schedule.Trips[0])
			case "sequence":
				d.Schedule.Trips[0].JourneyTimes[1].Sequence = 1
			case "direction":
				d.Schedule.Trips[0].Headsign = ""
				d.Schedule.StopNames = map[string]string{}
			case "old_progress":
				v.ObservedAt = time.Now().Add(-4 * time.Minute)
			case "repeated_stop":
				d.Schedule.Trips[0].JourneyTimes[0].Stop = "S"
			}
			s.Cache.update("metro", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: time.Now()}, s.Cache.operator("metro"))
			out := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/metro%3Av/journey")
			if scenario == "old_progress" || scenario == "repeated_stop" {
				if out.Association != "published_route" || out.Progress != "unknown" || out.NextIndex != nil {
					t.Fatalf("old observation established progress: %+v", out)
				}
			} else if out.Association == "published_route" || len(out.Data) != 0 {
				t.Fatalf("unsafe route evidence admitted: %+v", out)
			}
		})
	}
}

func TestMetroDestinationSelectsOnlyAnUnambiguousPublishedRoute(t *testing.T) {
	for _, scenario := range []string{"unique", "same_path", "different_path", "conflicting_hint", "stale_destination"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, v := popupFixture(t, "metro", 3)
			v.TripId, v.OperationalDate = nil, nil
			at := time.Now()
			metro := &MetroData{Status: api.MetroStatus{Status: "ok"}, Stations: []MetroStation{{ID: "AP", Name: "Porto"}}, Waits: []MetroWait{{Train: v.SourceId, Destination: "60", At: at.In(lisbon).Format("20060102150405")}}}
			switch scenario {
			case "same_path", "different_path":
				other := d.Schedule.Trips[0]
				other.ID = "another"
				other.JourneyTimes = append([]StopTime{}, other.JourneyTimes...)
				if scenario == "different_path" {
					other.JourneyTimes[0].Stop = "other"
				}
				d.Schedule.Trips = append(d.Schedule.Trips, other)
			case "conflicting_hint":
				v.TripId = ptr("metro:T")
				metro.Stations[0].Name = "Another destination"
			case "stale_destination":
				metro.Waits[0].At = at.Add(-2 * time.Minute).In(lisbon).Format("20060102150405")
			}
			s.Cache.updateMetro(metro, s.Cache.operator("metro"))
			s.Cache.update("metro", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: at}, s.Cache.operator("metro"))
			out := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/metro%3Av/journey")
			if scenario == "unique" || scenario == "same_path" {
				if out.Association != "published_route" || len(out.Data) != 3 || out.Destination != "Porto" || out.JourneyId != nil {
					t.Fatalf("unambiguous destination path missing: %+v", out)
				}
			} else if out.Association == "published_route" || len(out.Data) != 0 {
				t.Fatalf("conflicting or stale destination admitted: %+v", out)
			}
		})
	}
}
