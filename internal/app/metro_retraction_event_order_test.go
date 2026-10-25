package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroCorrectionEventWaitsForItsCheckpoint(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	dir := t.TempDir()
	history, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	if err = r.bindMetroHistory(history); err != nil {
		t.Fatal(err)
	}
	r.archiveAvailable = true
	r.queueCheckpoint(track)
	old := r.dirty["run"]
	oldProof := track.Proofs["earlier"]
	oldProof.Train.Calls = []api.StopCall{track.Train.Calls[1]}
	raw, err := json.Marshal(oldProof)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.RecordMetroEvents([]patterns.MetroEventRecord{{ID: "old", Journey: "run", At: oldProof.After.Clock, Payload: raw}}, now); err != nil {
		t.Fatal(err)
	}
	commitMetroCorrectionBehindEarlierCheckpoint(t, r, batch, track, history, old, now)
	delete(r.pending, "unrelated")
	r.pendingBytes -= len(`{"proof":true}`)
	r.flushEvents(history, now)
	if len(r.pending) != 0 || len(r.pendingCorrections) != 0 {
		t.Fatal("committed correction was not flushed")
	}
	if err = history.Close(); err != nil {
		t.Fatal(err)
	}
	assertMetroCorrectionReopened(t, dir, now)
}

func commitMetroCorrectionBehindEarlierCheckpoint(t *testing.T, r *metroRuntime, batch *metroPointBatch, track *metroTrack, history *patterns.Service, old patterns.MetroJourneyCheckpoint, now time.Time) {
	t.Helper()
	// The earlier checkpoint was already in flight when the correction arrived.
	r.checkpointPending = []patterns.MetroJourneyCheckpoint{old}
	r.selectedContexts(batch, now)
	r.queueCheckpoint(track)
	putPendingLaneEvent(r, "unrelated", now)
	available, _ := r.eventBatch(now)
	if len(available) != 1 || available[0].ID != "unrelated" || !track.UncommittedRetraction {
		t.Fatal("pending correction was written or blocked an unrelated journey", available)
	}
	if _, err := history.CommitMetroJourneys(context.Background(), []patterns.MetroJourneyCheckpoint{old}, now); err != nil {
		t.Fatal(err)
	}
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{old}, "old", now)
	if !track.UncommittedRetraction {
		t.Fatal("an older checkpoint acknowledged a later correction")
	}
	r.checkpointPending = nil
	r.queueCheckpoint(track)
	newer := r.dirty["run"]
	if newer.Revision <= old.Revision {
		t.Fatal("corrected checkpoint was not queued")
	}
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{old}, "stale", now)
	if !track.UncommittedRetraction {
		t.Fatal("a repeated stale acknowledgement opened the event gate")
	}
	if _, err := history.CommitMetroJourneys(context.Background(), []patterns.MetroJourneyCheckpoint{newer}, now); err != nil {
		t.Fatal(err)
	}
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{newer}, "new", now)
	if track.UncommittedRetraction {
		t.Fatal("corrected checkpoint did not open the event gate")
	}
}

func assertMetroCorrectionReopened(t *testing.T, dir string, now time.Time) {
	t.Helper()
	history, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	checkpoint, status, err := history.MetroJourneyCheckpoint(context.Background(), "run", now)
	var payload metroCheckpointPayload
	if err != nil || status != "restored" || json.Unmarshal(checkpoint.Payload, &payload) != nil || payload.Train.Calls[1].Arrival.Inferred != nil {
		t.Fatal("reopened checkpoint resurrected the corrected arrival", status, err)
	}
	records, err := history.MetroJourneyEvents(context.Background(), "run", now)
	if err != nil || len(records) != 2 {
		t.Fatal("event fallback lacks the correction", len(records), err)
	}
	b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Journey: "run"}}, now: now, frame: api.MetroLiveFrame{Recovery: &api.MetroJourneyRecovery{}}}
	b.restoreLegacyRecords(records)
	if len(b.frame.Trains) != 1 || b.frame.Trains[0].Calls[0].Arrival.Inferred != nil {
		t.Fatal("event-only fallback resurrected the corrected arrival")
	}
}

func TestMetroCorrectionEventRemainsBoundedWhenCheckpointFails(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	r.archiveAvailable = true
	r.queueCheckpoint(track)
	old := r.dirty["run"]
	r.selectedContexts(batch, now)
	r.failCheckpointBatch([]patterns.MetroJourneyCheckpoint{old}, patterns.MetroCheckpointCommit{})
	if !r.checkpointFenced || r.historyStatus != "paused" || !track.UncommittedRetraction {
		t.Fatal("checkpoint failure did not preserve the correction fence")
	}
	for n := len(r.pending); n < 1024; n++ {
		id := fmt.Sprintf("occupied-%d", n)
		putPendingLaneEvent(r, id, now)
	}
	before := r.pendingBytes
	call := api.StopCall{Id: "other", Arrival: missingCallTime("correction")}
	r.queueArrival(track, call, metroPoint{}, metroPoint{Clock: now})
	if len(r.pending) != 1024 || r.pendingBytes != before || !r.eventAdmissionLost || r.historyStatus != "paused" {
		t.Fatal("bounded pending capacity did not disclose rejected evidence")
	}
	delete(r.tracks, "run")
	eligible, _ := r.eventBatch(now.AddDate(0, 0, 7))
	if len(eligible) != 1023 || len(r.pendingCorrections) != 1 {
		t.Fatal("missing track or exact retention boundary opened the correction gate")
	}
	r.eventBatch(now.AddDate(0, 0, 7).Add(time.Nanosecond))
	if len(r.pending) != 0 || len(r.pendingCorrections) != 0 || r.pendingBytes != 0 {
		t.Fatal("expired pending correction did not release its bounded reservation")
	}
}

func TestMetroCorrectionSurvivesEarlierEventAcknowledgement(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	r.archiveAvailable = true
	putPendingLaneEvent(r, "earlier", now)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		r.flushEventsWithWrite(now, func([]patterns.MetroEventRecord, time.Time) error {
			close(started)
			<-release
			return nil
		})
		close(done)
	}()
	<-started
	r.mu.Lock()
	r.selectedContexts(batch, now)
	r.queueCheckpoint(track)
	putPendingLaneEvent(r, "unrelated", now)
	r.mu.Unlock()
	close(release)
	<-done
	r.mu.Lock()
	defer r.mu.Unlock()
	eligible, _ := r.eventBatch(now)
	if len(eligible) != 1 || eligible[0].ID != "unrelated" || !track.UncommittedRetraction || len(r.pendingCorrections) != 1 {
		t.Fatal("old event acknowledgement cleared or bypassed a newer correction", eligible)
	}
}

func metroOrdinaryCorrectionFixture() (*metroRuntime, *metroPointBatch, *metroTrack, time.Time) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	batch.groups[key] = []metroPoint{{Stop: "AL", Platform: "AL3ALO", Clock: now, Seconds: ptr(30)}, batch.groups[key][1], batch.groups[key][2]}
	return r, batch, track, now
}

func TestMetroOrdinaryCorrectionWithoutPointSurvivesEventOnlyReopen(t *testing.T) {
	r, batch, track, now := metroOrdinaryCorrectionFixture()
	dir := t.TempDir()
	history, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	if err = r.bindMetroHistory(history); err != nil {
		t.Fatal(err)
	}
	r.archiveAvailable = true
	original := track.Proofs["arrival"]
	original.Train.Calls = []api.StopCall{track.Train.Calls[1]}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.RecordMetroEvents([]patterns.MetroEventRecord{{ID: "old", Journey: "run", At: original.After.Clock, Payload: raw}}, now); err != nil {
		t.Fatal(err)
	}
	key := "metro:green|50|24C"
	if selected := r.selectedContexts(batch, now); len(selected) != 1 || selected[0] != key {
		t.Fatalf("normal correction path was not selected: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	if track.Train.Calls[1].Arrival.Inferred != nil || len(r.pendingCorrections) != 1 {
		t.Fatal("ordinary correction did not withdraw and retain a correction event")
	}
	r.queueCheckpoint(track)
	r.flushCheckpoints(context.Background(), history, now)
	r.flushEvents(history, now)
	if err = history.Close(); err != nil {
		t.Fatal(err)
	}
	history, err = patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	records, err := history.MetroJourneyEvents(context.Background(), "run", now)
	if err != nil || len(records) != 2 {
		t.Fatal("event-only reopen lacks the correction", len(records), err)
	}
	b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Journey: "run"}}, now: now, frame: api.MetroLiveFrame{Recovery: &api.MetroJourneyRecovery{}}}
	b.restoreLegacyRecords(records)
	if len(b.frame.Trains) != 1 || b.frame.Trains[0].Calls[0].Arrival.Inferred != nil {
		t.Fatal("ordinary correction event reopened an older inferred arrival")
	}
}

func TestMetroOrdinaryCorrectionWithoutUniqueProofPreservesArrival(t *testing.T) {
	for _, variant := range []string{"missing", "ambiguous", "different platform"} {
		t.Run(variant, func(t *testing.T) {
			r, batch, track, now := metroOrdinaryCorrectionFixture()
			r.archiveAvailable = true
			switch variant {
			case "missing":
				track.Proofs = nil
			case "ambiguous":
				proof := track.Proofs["arrival"]
				proof.Before.Platform, proof.After.Platform = "other", "other"
				track.Proofs["duplicate"] = proof
			case "different platform":
				batch.groups["metro:green|50|24C"][0].Platform = "other"
			}
			key := "metro:green|50|24C"
			if selected := r.selectedContexts(batch, now); len(selected) != 1 {
				t.Fatalf("normal correction path was not selected: %v", selected)
			}
			r.applyPoints(track, batch.episodePoints(key), now)
			if track.Train.Calls[1].Arrival.Inferred == nil || track.Train.Association != "suspended" || track.UncommittedRetraction || len(r.pendingCorrections) != 0 {
				t.Fatal("unprovable correction did not preserve the inferred arrival")
			}
		})
	}
}
