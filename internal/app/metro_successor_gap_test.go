package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// The private reverse candidate must not infer an arrival across a missing sample.
func TestMetroSuccessorCannotBridgePublicationGap(t *testing.T) {
	for _, kind := range []string{"absent", "error", "rejected", "null", "superseded wait"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _, now := metroLiveFixture(t)
			data, static := metroPlatformFixture()
			data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
			static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
			for i := range data.Stations {
				data.Stations[i].Lines = "[Azul]"
			}
			data.Destinations = []MetroDestination{{ID: "42", Name: "Santa Apolónia"}, {ID: "33", Name: "Reboleira"}}
			row := func(at time.Time, stop, dest, value string) MetroWait {
				return MetroWait{Stop: stop, Destination: dest, Platform: "1", At: at.In(lisbon).Format("20060102150405"), Train: "7", Wait1: json.RawMessage(value)}
			}
			data.Waits = []MetroWait{row(now, "RB", "42", "120")}
			publishMetroTest(s, static, data, now)
			if len(data.Trains) != 1 {
				t.Fatal("fixture did not admit a forward journey")
			}
			r := s.Cache.metroRuntime
			r.models[r.topology.Profile+"|33"] = patterns.MetroQualifiedModel{}
			at := now.Add(time.Second)
			data.Waits = []MetroWait{row(now, "RB", "42", "120"), row(at, "SP", "33", "10")}
			publishMetroTest(s, static, data, at)
			candidate := r.tracks[r.candidates["metro:4_0|33|7"]]
			if candidate == nil || candidate.Points["SP"].Seconds == nil {
				t.Fatal("fixture did not create the reverse candidate")
			}
			at = now.Add(2 * time.Second)
			data.Waits = []MetroWait{row(now, "RB", "42", "120")}
			switch kind {
			case "error":
				data.Status.Status = "error"
			case "rejected":
				bad := row(at, "SP", "33", "10")
				bad.Train2, bad.Wait2 = "7", json.RawMessage("11")
				data.Waits = append(data.Waits, bad)
			case "null":
				data.Waits = append(data.Waits, row(at, "SP", "33", "null"))
			case "superseded wait":
				data.Waits = append(data.Waits, row(now.Add(time.Second), "SP", "33", "10"), row(at, "SP", "33", "null"))
			}
			publishMetroTest(s, static, data, at)
			at = now.Add(3 * time.Second)
			data.Status.Status = "ok"
			data.Waits = []MetroWait{row(now, "RB", "42", "120"), row(at, "SP", "33", "0")}
			publishMetroTest(s, static, data, at)
			if candidate.Train.Calls[0].Arrival.Inferred != nil {
				t.Fatal("the candidate inferred an arrival across a publication gap")
			}
		})
	}
}

func metroSuccessorContinuityFixture(t *testing.T) (*Server, *StaticData, *MetroData, time.Time, *metroTrack) {
	t.Helper()
	s, _, _, now := metroLiveFixture(t)
	data, static := metroPlatformFixture()
	data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
	static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
	for i := range data.Stations {
		data.Stations[i].Lines = "[Azul]"
	}
	data.Destinations = []MetroDestination{{ID: "42", Name: "Santa Apolónia"}, {ID: "33", Name: "Reboleira"}}
	data.Waits = []MetroWait{metroSuccessorTestRow(now, "RB", "42", "120")}
	publishMetroTest(s, static, data, now)
	r := s.Cache.metroRuntime
	r.models[r.topology.Profile+"|33"] = patterns.MetroQualifiedModel{}
	at := now.Add(time.Second)
	data.Waits = []MetroWait{metroSuccessorTestRow(now, "RB", "42", "120"), metroSuccessorTestRow(at, "SP", "33", "10")}
	publishMetroTest(s, static, data, at)
	candidate := r.tracks[r.candidates["metro:4_0|33|7"]]
	if candidate == nil || candidate.Points["SP"].Seconds == nil {
		t.Fatal("fixture did not create the reverse candidate")
	}
	return s, static, data, now, candidate
}

func metroSuccessorTestRow(at time.Time, stop, dest, value string) MetroWait {
	return MetroWait{Stop: stop, Destination: dest, Platform: "1", At: at.In(lisbon).Format("20060102150405"), Train: "7", Wait1: json.RawMessage(value)}
}

func TestMetroSuccessorCannotBridgeTopologyChange(t *testing.T) {
	for _, withModel := range []bool{false, true} {
		t.Run(map[bool]string{false: "without new model", true: "with new model"}[withModel], func(t *testing.T) {
			s, static, data, now, old := metroSuccessorContinuityFixture(t)
			r := s.Cache.metroRuntime
			oldProfile := r.topology.Profile
			changed := *static
			schedule := &Schedule{Parents: static.Schedule.Parents, Trips: append([]ScheduledTrip{}, static.Schedule.Trips...)}
			schedule.Trips[0].Times = append([]StopTime{}, static.Schedule.Trips[0].Times...)
			for i := range schedule.Trips[0].Times {
				schedule.Trips[0].Times[i].Sequence += 10
			}
			changed.Schedule = schedule
			if withModel {
				r.models[metroTopology(data, &changed).Profile+"|33"] = patterns.MetroQualifiedModel{}
			}
			at := now.Add(2 * time.Second)
			data.Waits = []MetroWait{metroSuccessorTestRow(now, "RB", "42", "120"), metroSuccessorTestRow(at, "SP", "33", "10")}
			publishMetroTest(s, &changed, data, at)
			if r.topology.Profile == oldProfile || r.candidates["metro:4_0|33|7"] == old.Train.JourneyId {
				t.Fatal("profile change did not retire the old candidate")
			}
			at = now.Add(3 * time.Second)
			data.Waits = []MetroWait{metroSuccessorTestRow(now, "RB", "42", "120"), metroSuccessorTestRow(at, "SP", "33", "0")}
			publishMetroTest(s, static, data, at)
			if old.Train.Calls[0].Arrival.Inferred != nil || old.Train.Association != "suspended" || r.candidates["metro:4_0|33|7"] == old.Train.JourneyId {
				t.Fatal("candidate bridged the changed topology")
			}
		})
	}
}

func TestMetroSuccessorContinuityGapBoundary(t *testing.T) {
	for _, gap := range []time.Duration{60 * time.Second, 61 * time.Second} {
		t.Run(gap.String(), func(t *testing.T) {
			s, static, data, now, old := metroSuccessorContinuityFixture(t)
			at := now.Add(2 * time.Second)
			data.Waits = []MetroWait{metroSuccessorTestRow(now, "RB", "42", "120"), metroSuccessorTestRow(at, "SP", "33", "0")}
			publishMetroTest(s, static, data, at)
			if old.Train.Calls[0].Arrival.Inferred == nil {
				t.Fatal("candidate passage missing")
			}
			if len(s.Cache.metroRuntime.pending) != 1 {
				t.Fatal("candidate arrival was not pending")
			}
			at = at.Add(gap)
			data.Waits = []MetroWait{metroSuccessorTestRow(now, "RB", "42", "120"), metroSuccessorTestRow(at, "SS", "33", "120")}
			publishMetroTest(s, static, data, at)
			r := s.Cache.metroRuntime
			current := r.tracks[r.candidates["metro:4_0|33|7"]]
			if current == nil || (current == old) != (gap == 60*time.Second) {
				t.Fatal("candidate identity did not respect the source-clock gap")
			}
			if gap > 60*time.Second && (current.Train.Calls[0].Arrival.Inferred != nil || old.Train.Calls[0].Arrival.Inferred == nil || old.Train.Association != "suspended") {
				t.Fatal("candidate gap lost or carried historical passage")
			}
			r.flushEvents(s.Patterns, at)
			events, err := s.Patterns.MetroJourneyEvents(context.Background(), old.Train.JourneyId, at)
			if err != nil || len(events) != 1 || events[0].Journey != old.Train.JourneyId {
				t.Fatal("old candidate event was not retained separately")
			}
		})
	}
}

func TestMetroCandidateProfileFenceWaitsForLifecycle(t *testing.T) {
	for _, kind := range []string{"barrier", "group", "pending successor"} {
		t.Run(kind, func(t *testing.T) {
			r := newMetroRuntime()
			r.topology.Profile = "new"
			candidate := &metroTrack{Profile: "old", Train: api.MetroTrain{JourneyId: "candidate", Association: "supported"}}
			r.tracks["candidate"], r.candidates["key"] = candidate, "candidate"
			switch kind {
			case "barrier":
				candidate.BarrierRevision = 1
			case "group":
				candidate.LifecycleGroup = "group"
			case "pending successor":
				r.tracks["old"] = &metroTrack{PendingSuccessor: "candidate"}
			}
			r.fenceMetroCandidateProfiles()
			if r.candidates["key"] != "candidate" || candidate.Train.Association != "supported" || !candidate.CandidateInterrupted {
				t.Fatal("profile fence changed a pending lifecycle transition")
			}
			// Returning to the old profile cannot erase the interrupted state.
			r.topology.Profile = "old"
			r.fenceMetroCandidateProfiles()
			candidate.BarrierRevision, candidate.LifecycleGroup = 0, ""
			delete(r.tracks, "old")
			r.fenceMetroCandidateProfiles()
			if r.candidates["key"] != "" || candidate.Train.Association != "suspended" {
				t.Fatal("profile fence did not retire the resolved candidate")
			}
		})
	}
}

func TestMetroInterruptedCandidateCannotBePromotedOnAck(t *testing.T) {
	for _, kind := range []string{"profile", "source error", "absent", "rejected", "null"} {
		t.Run(kind, func(t *testing.T) {
			r := newMetroRuntime()
			r.topology.Profile = "old"
			old := &metroTrack{BarrierRevision: 1, PendingSuccessor: "candidate", Train: api.MetroTrain{JourneyId: "old", Lifecycle: &api.MetroJourneyLifecycle{State: "superseded"}}}
			candidate := &metroTrack{BarrierRevision: 2, Profile: "old", Train: api.MetroTrain{JourneyId: "candidate", Association: "supported"}}
			r.tracks["old"], r.tracks["candidate"] = old, candidate
			r.active["old"], r.candidates["key"] = "old", "candidate"
			switch kind {
			case "profile":
				r.topology.Profile = "new"
				r.fenceMetroCandidateProfiles()
				r.topology.Profile = "old"
			case "source error":
				r.suspendAll("source error")
			case "absent":
				r.suspendAbsent(&metroPointBatch{groups: map[string][]metroPoint{}})
			case "rejected":
				r.suspendAbsent(&metroPointBatch{groups: map[string][]metroPoint{"key": {}}, rejected: map[string]bool{"key": true}})
			case "null":
				if r.successorHasWait(metroSuccessorObservation{key: "key", batch: &metroPointBatch{groups: map[string][]metroPoint{"key": {{Stop: "SP"}}}}}) {
					t.Fatal("null wait unexpectedly admitted")
				}
			}
			if !candidate.CandidateInterrupted || candidate.Train.Association != "supported" {
				t.Fatal("interruption was not recorded without changing frozen state")
			}
			r.commitLifecycle(old, 1)
			if r.active["key"] != "" || r.candidates["key"] != "candidate" {
				t.Fatal("an interrupted candidate was promoted on predecessor acknowledgement")
			}
			candidate.BarrierRevision = 0
			r.fenceMetroCandidateProfiles()
			if r.candidates["key"] != "" || candidate.Train.Association != "suspended" {
				t.Fatal("candidate was not retired after its own barrier cleared")
			}
		})
	}
}
