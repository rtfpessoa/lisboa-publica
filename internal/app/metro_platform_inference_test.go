package app

import (
	"testing"
	"time"
)

func TestMetroArrivalTransitionRequiresSameNonemptyPlatform(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		valid               bool
	}{
		{"same", "1", "1", true},
		{"different", "1", "2", false},
		{"previous missing", "", "1", false},
		{"current missing", "1", "", false},
		{"both missing", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, static, data, _ := metroLiveFixture(t)
			now := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
			first := metroTestRow(now, "RM", "7", "10")
			first.Platform = tc.before
			data.Waits = []MetroWait{first, metroTestRow(now, "CS", "7", "120")}
			publishMetroTest(s, static, data, now)
			if len(data.Trains) != 1 {
				t.Fatal("initial journey unavailable")
			}
			original := data.Trains[0]
			at := now.Add(10 * time.Second)
			zero := metroTestRow(at, "RM", "7", "0")
			zero.Platform = tc.after
			next := *data
			next.Waits = []MetroWait{zero, metroTestRow(at, "CS", "7", "110")}
			publishMetroTest(s, static, &next, at)
			if len(next.Trains) != 1 || (next.Trains[0].Calls[0].Arrival.Inferred != nil) != tc.valid {
				t.Fatal("platform transition produced the wrong arrival evidence")
			}
			if !tc.valid && (next.Trains[0].Association != "suspended" || !next.Trains[0].SourceUpdatedAt.Equal(original.SourceUpdatedAt)) {
				t.Fatal("incompatible platform renewed the episode")
			}
		})
	}
}

func TestMetroRepeatedZeroRequiresOriginalPlatformProof(t *testing.T) {
	for _, tc := range []struct {
		name, platform string
		offset         time.Duration
		missingProof   bool
		valid          bool
	}{
		{"same platform", "1", 0, false, true},
		{"same source clock", "1", -10 * time.Second, false, true},
		{"different platform", "2", 0, false, false},
		{"missing platform", "", 0, false, false},
		{"missing original proof", "1", 0, true, false},
		{"future source clock", "1", 5 * time.Second, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, static, data, _ := metroLiveFixture(t)
			now := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
			data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
			publishMetroTest(s, static, data, now)
			next := *data
			at := now.Add(10 * time.Second)
			next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "110")}
			publishMetroTest(s, static, &next, at)
			if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
				t.Fatal("same-platform arrival missing")
			}
			original := next.Trains[0]
			if tc.missingProof {
				r := s.Cache.metroRuntime
				r.mu.Lock()
				r.tracks[original.JourneyId].Proofs = nil
				r.mu.Unlock()
			}
			at = now.Add(20 * time.Second)
			zero := metroTestRow(at.Add(tc.offset), "RM", "7", "0")
			zero.Platform = tc.platform
			next.Waits = []MetroWait{zero, metroTestRow(at, "CS", "7", "100")}
			publishMetroTest(s, static, &next, at)
			if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
				t.Fatal("repeated zero lost the retained arrival")
			}
			if tc.valid && (next.Trains[0].Association != "supported" || !next.Trains[0].SourceUpdatedAt.Equal(at)) {
				t.Fatal("same-platform zero did not continue the episode")
			}
			if !tc.valid && (next.Trains[0].Association != "suspended" || !next.Trains[0].SourceUpdatedAt.Equal(original.SourceUpdatedAt)) {
				t.Fatal("unproven zero renewed the episode")
			}
		})
	}
}

func TestMetroRepeatedZeroCannotBridgeMissingStop(t *testing.T) {
	s, static, data, _ := metroLiveFixture(t)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "240")}
	publishMetroTest(s, static, data, now)
	next := *data
	at := now.Add(10 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "230")}
	publishMetroTest(s, static, &next, at)
	if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("supported arrival missing")
	}
	for _, seconds := range []int{40, 70, 100, 130} {
		at = now.Add(time.Duration(seconds) * time.Second)
		next.Waits = []MetroWait{metroTestRow(at, "CS", "7", "null")}
		publishMetroTest(s, static, &next, at)
	}
	original := next.Trains[0]
	at = now.Add(140 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "null")}
	publishMetroTest(s, static, &next, at)
	if next.Trains[0].Calls[0].Arrival.Inferred == nil || next.Trains[0].Association != "suspended" || !next.Trains[0].SourceUpdatedAt.Equal(original.SourceUpdatedAt) || next.Trains[0].CurrentIndex != nil {
		t.Fatal("a zero after a stop gap recreated or renewed the old current visit")
	}
}

func TestMetroRepeatedZeroCannotBridgeNullWait(t *testing.T) {
	s, static, data, _ := metroLiveFixture(t)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, static, data, now)
	next := *data
	at := now.Add(10 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "110")}
	publishMetroTest(s, static, &next, at)
	if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("supported arrival missing")
	}
	at = now.Add(20 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "null"), metroTestRow(at, "CS", "7", "null")}
	publishMetroTest(s, static, &next, at)
	original := next.Trains[0]
	at = now.Add(30 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "null")}
	publishMetroTest(s, static, &next, at)
	if next.Trains[0].Calls[0].Arrival.Inferred == nil || next.Trains[0].Association != "suspended" || !next.Trains[0].SourceUpdatedAt.Equal(original.SourceUpdatedAt) || next.Trains[0].CurrentIndex != nil {
		t.Fatal("a zero after a null wait recreated or renewed the old current visit")
	}
}

func TestMetroRepeatedArrivalContinuityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gap, age time.Duration
		valid    bool
	}{
		{"gap boundary", 60 * time.Second, 0, true},
		{"gap exceeded", 61 * time.Second, 0, false},
		{"age boundary", 0, 60 * time.Second, true},
		{"age exceeded", 0, 61 * time.Second, false},
		{"future", 0, -time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, track, now := metroEpisodeScopeFixture()
			now = now.Add(90 * time.Second)
			point := track.Proofs["arrival"].After
			point.Clock = now.Add(-tc.age)
			previous := point
			previous.Clock = point.Clock.Add(-tc.gap)
			track.Points[point.Stop] = previous
			if metroRepeatedArrivalSupported(track, 1, point, now) != tc.valid {
				t.Fatal("repeated zero continuity boundary was not enforced")
			}
		})
	}
}

func TestMetroOrdinaryCorrectionRequiresOriginalPlatformAndContext(t *testing.T) {
	tests := []struct {
		name   string
		valid  bool
		mutate func(*metroRuntime, *metroPointBatch, *metroTrack, time.Time)
	}{
		{"matching proof without current point", true, func(*metroRuntime, *metroPointBatch, *metroTrack, time.Time) {}},
		{"matching proof and current point", true, func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ time.Time) {
			tr.Points["AL"] = tr.Proofs["arrival"].After
		}},
		{"different platform", false, func(_ *metroRuntime, b *metroPointBatch, tr *metroTrack, _ time.Time) {
			tr.Points["AL"] = tr.Proofs["arrival"].After
			b.groups["metro:green|50|24C"][0].Platform = "other"
		}},
		{"platform changed then repeated", false, func(_ *metroRuntime, b *metroPointBatch, tr *metroTrack, _ time.Time) {
			old := tr.Proofs["arrival"].After
			old.Platform = "other"
			tr.Points["AL"] = old
			b.groups["metro:green|50|24C"][0].Platform = "other"
		}},
		{"missing platform", false, func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			b.groups["metro:green|50|24C"][0].Platform = ""
		}},
		{"missing proof", false, func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ time.Time) { tr.Proofs = nil }},
		{"competing direction", false, func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			b.references["metro:green|24C"]["metro:green|other|24C"] = true
		}},
		{"opposite motion", false, func(r *metroRuntime, b *metroPointBatch, _ *metroTrack, now time.Time) {
			r.operational = map[string]*metroOperationalMotion{"metro:green|24C": {PublishedAt: now, Sign: -1, Axis: b.paths["metro:green|50|24C"]}}
		}},
		{"missing downstream anchor", false, func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ time.Time) {
			tr.Points = map[string]metroPoint{}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, batch, track, now := metroOrdinaryCorrectionFixture()
			r.archiveAvailable = true
			tc.mutate(r, batch, track, now)
			clock := track.Train.SourceUpdatedAt
			key := "metro:green|50|24C"
			if selected := r.selectedContexts(batch, now); len(selected) == 1 {
				r.applyPoints(track, batch.episodePoints(key), now)
			}
			if tc.valid {
				if track.Train.Calls[1].Arrival.Inferred != nil || !track.UncommittedRetraction || len(r.pendingCorrections) != 1 {
					t.Fatal("supported same-platform correction was lost")
				}
				return
			}
			if track.Train.Calls[1].Arrival.Inferred == nil || track.Train.Association != "suspended" || !track.Train.SourceUpdatedAt.Equal(clock) || track.UncommittedRetraction {
				t.Fatal("ambiguous correction changed the passage or renewed the episode")
			}
		})
	}
}

func TestMetroLateSamePlatformCorrectionPreservesPassage(t *testing.T) {
	s, static, data, _ := metroLiveFixture(t)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "240")}
	publishMetroTest(s, static, data, now)
	next := *data
	at := now.Add(10 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "230")}
	publishMetroTest(s, static, &next, at)
	if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("supported arrival missing")
	}
	for _, seconds := range []int{40, 70, 100, 130} {
		at = now.Add(time.Duration(seconds) * time.Second)
		next.Waits = []MetroWait{metroTestRow(at, "CS", "7", "120")}
		publishMetroTest(s, static, &next, at)
	}
	original := next.Trains[0]
	at = now.Add(140 * time.Second)
	next.Waits = []MetroWait{metroTestRow(at, "RM", "7", "20"), metroTestRow(at, "CS", "7", "120")}
	publishMetroTest(s, static, &next, at)
	if next.Trains[0].Calls[0].Arrival.Inferred == nil || next.Trains[0].Association != "suspended" || !next.Trains[0].SourceUpdatedAt.Equal(original.SourceUpdatedAt) {
		t.Fatal("late reference reuse withdrew or renewed the old passage")
	}
}

func TestMetroCorrectionsAreValidatedBeforeAnyWithdrawal(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	key := "metro:green|50|24C"
	batch.groups[key] = batch.groups[key][1:]
	batch.groups[key] = append(batch.groups[key], metroPoint{Stop: "AL", Platform: "other", Clock: now, Seconds: ptr(30)})
	current, conflict := latestMetroPoints(batch.groups[key])
	if conflict || r.retractArrivals(track, current, now) {
		t.Fatal("ambiguous correction set was accepted")
	}
	if track.Train.Calls[1].Arrival.Inferred == nil || track.Train.Calls[2].Arrival.Inferred == nil || track.UncommittedRetraction || len(r.pending) != 0 {
		t.Fatal("a correction was withdrawn before the complete set was validated")
	}
}
