package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMetroArrivalEventRecordsTransitionClock(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, static, data, now)
	id := data.Trains[0].JourneyId
	at := now.Add(10 * time.Second)
	data.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "110")}
	publishMetroTest(s, static, data, at)
	s.Cache.metroRuntime.flushEvents(s.Patterns, at)
	records, err := s.Patterns.MetroJourneyEvents(context.Background(), id, at)
	if err != nil || len(records) != 1 {
		t.Fatalf("arrival event missing: %d records, %v", len(records), err)
	}
	var proof metroEventProof
	if err := json.Unmarshal(records[0].Payload, &proof); err != nil {
		t.Fatal(err)
	}
	if !proof.Train.SourceUpdatedAt.Equal(at) || !proof.Train.ValidUntil.Equal(at.Add(sourceFreshness)) || !proof.After.Clock.Equal(at) {
		t.Fatal("persisted arrival evidence has an outdated source clock")
	}
}

func TestMetroNullOnlyPublicationsCannotRenewJourney(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "240")}
	publishMetroTest(s, static, data, now)
	at := now.Add(10 * time.Second)
	next := *data
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "230")}
	publishMetroTest(s, static, &next, at)
	if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("setup did not infer a supported passage")
	}
	original := next.Trains[0]
	for _, seconds := range []int{40, 70, 100, 130, 160, 190, 220} {
		at = now.Add(time.Duration(seconds) * time.Second)
		next.Waits = []MetroWait{metroTestRow(at, "CS", "7", "null")}
		publishMetroTest(s, static, &next, at)
		if seconds == 40 {
			assertMetroEpisodeNotRenewed(t, next.Trains, original)
		}
	}
	r := s.Cache.metroRuntime
	r.mu.Lock()
	track := r.tracks[original.JourneyId]
	if track == nil || track.Train.Association == "supported" || !track.Train.SourceUpdatedAt.Equal(original.SourceUpdatedAt) || track.Train.Calls[0].Arrival.Inferred == nil {
		r.mu.Unlock()
		t.Fatal("null-only publications renewed or lost the historical passage")
	}
	r.mu.Unlock()
}

func TestMetroNullWaitBreaksTransitionAndLongGapSplitsJourney(t *testing.T) {
	for _, elapsed := range []time.Duration{20 * time.Second, 70 * time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			s, static, data, now := metroLiveFixture(t)
			data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10")}
			publishMetroTest(s, static, data, now)
			original := data.Trains[0]
			at := now.Add(10 * time.Second)
			data.Waits = []MetroWait{metroTestRow(at, "RM", "7", "null")}
			publishMetroTest(s, static, data, at)
			assertMetroEpisodeNotRenewed(t, data.Trains, original)
			at = now.Add(elapsed)
			wait := "0"
			if elapsed > time.Minute {
				wait = "10"
			}
			data.Waits = []MetroWait{metroTestRow(at, "RM", "7", wait)}
			publishMetroTest(s, static, data, at)
			if len(data.Trains) != 1 || data.Trains[0].Calls[0].Arrival.Inferred != nil {
				t.Fatal("a zero after a null wait created a passage")
			}
			if (data.Trains[0].JourneyId == original.JourneyId) != (elapsed < time.Minute) {
				t.Fatal("journey identity did not respect the continuity window")
			}
		})
	}
}

func TestMetroNewerNullDoesNotRenewOlderWait(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120"), metroTestRow(now, "CS", "7", "240")}
	publishMetroTest(s, static, data, now)
	original := data.Trains[0]
	at := now.Add(30 * time.Second)
	next := *data
	next.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120"), metroTestRow(at, "CS", "7", "null")}
	publishMetroTest(s, static, &next, at)
	if len(next.Trains) != 1 || next.Trains[0].JourneyId != original.JourneyId {
		t.Fatal("the older finite wait did not retain its episode")
	}
	train := next.Trains[0]
	if !train.SourceUpdatedAt.Equal(original.SourceUpdatedAt) || !train.ValidUntil.Equal(original.ValidUntil) {
		t.Fatal("the newer null wait advanced the episode clock")
	}
	r := s.Cache.metroRuntime
	r.mu.Lock()
	point, exists := r.tracks[original.JourneyId].Points["CS"]
	r.mu.Unlock()
	if !exists || point.Seconds != nil {
		t.Fatal("the null wait did not invalidate the retained station baseline")
	}
}

func TestMetroNullOnlyReferenceDoesNotAdmitJourney(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "null")}
	publishMetroTest(s, static, data, now)
	if len(data.Trains) != 0 {
		t.Fatal("a null-only reference admitted a supported journey")
	}
	at := now.Add(time.Second)
	data.Waits = []MetroWait{metroTestRow(at, "RM", "7", "10")}
	publishMetroTest(s, static, data, at)
	if len(data.Trains) != 1 || data.Trains[0].Association != "supported" || !data.Trains[0].SourceUpdatedAt.Equal(at) {
		t.Fatal("a later usable wait did not admit a journey")
	}
}
