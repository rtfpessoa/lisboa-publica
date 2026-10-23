package app

import (
	"encoding/hex"
	"fmt"
	"lisboapublica/internal/patterns"
	"time"
)

type metroCutProvenance struct {
	Nonce    string `json:"nonce"`
	Revision uint64 `json:"revision"`
}

func metroCutOrigin(cut patterns.MetroCutSnapshot) *metroCutProvenance {
	if !cut.Valid {
		return nil
	}
	return &metroCutProvenance{Nonce: hex.EncodeToString(cut.Nonce[:]), Revision: cut.Revision}
}

// snapshotMetroTrack does not change live state. When its forecast origin is
// unknown or no longer current, derive schedules only from independent facts.
func (r *metroRuntime) snapshotMetroTrack(t *metroTrack, cut patterns.MetroCutSnapshot, safe bool, now time.Time) metroTrack {
	copy := *t
	copy.Train = cloneMetroTrain(t.Train)
	if safe || r.historyCutMismatch || r.historyCut == nil || !cut.Valid || cut.Pending || cut != r.forecastCut {
		clearMetroOwnPredictions(&copy)
		projectMetroScheduled(&copy, now)
	}
	return copy
}

func (r *metroRuntime) claimMetroSnapshot(cut patterns.MetroCutSnapshot) bool {
	return !r.historyCutMismatch && r.historyCut != nil && r.historyCut.Claim(cut)
}

// BindMetroHistory binds the runtime to the archive's cut before publishers run.
func (c *Cache) BindMetroHistory(history *patterns.Service) error {
	r := c.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bindMetroHistory(history)
}

// bindMetroHistory requires the runtime lock. A different owner cannot silently
// replace a cut while earlier projections or writes remain in flight.
func (r *metroRuntime) bindMetroHistory(history *patterns.Service) error {
	var cut *patterns.MetroCut
	if history != nil {
		cut = history.MetroCut()
	}
	if r.historyCutBound && r.historyCut != cut {
		r.historyCutMismatch = true
		r.historyStatus = "unavailable"
		return fmt.Errorf("Metro history owner changed")
	}
	r.historyCutBound, r.historyCut = true, cut
	return nil
}

// fenceMetroForecasts keeps independently anchored schedule estimates while
// removing projections produced under a different archive continuity cut.
func (r *metroRuntime) fenceMetroForecasts(now time.Time) patterns.MetroCutSnapshot {
	cut := r.historySnapshot()
	if r.historyCut != nil {
		r.updateMetroCutStatus(cut)
	}
	if cut == r.forecastCut && !r.historyCutMismatch {
		return cut
	}
	for _, track := range r.tracks {
		if track.HistoricalOnly {
			continue
		}
		resetMetroTrackForecasts(track, now)
		track.ForecastCut = cut
	}
	r.forecastCut = cut
	r.forecastCacheUntil = time.Time{}
	return cut
}

func (r *metroRuntime) updateMetroCutStatus(cut patterns.MetroCutSnapshot) {
	if !cut.Valid || r.historyCutMismatch {
		r.historyStatus = "unavailable"
	} else if cut.Pending {
		r.historyStatus = "paused"
	}
}

func resetMetroTrackForecasts(track *metroTrack, now time.Time) {
	clearMetroOwnPredictions(track)
	projectMetroScheduled(track, now)
	resetMetroBarrierForecasts(track, now)
}

func resetMetroBarrierForecasts(track *metroTrack, now time.Time) {
	if track.BarrierBefore == nil {
		return
	}
	before := *track
	before.Train = cloneMetroTrain(*track.BarrierBefore)
	clearMetroOwnPredictions(&before)
	projectMetroScheduled(&before, now)
	track.BarrierBefore = &before.Train
}

func clearMetroOwnPredictions(t *metroTrack) {
	for n := range t.Train.Calls {
		t.Train.Calls[n].OwnPrediction = nil
		t.Train.Calls[n].OwnDeparturePrediction = nil
	}
}

func (r *metroRuntime) historySnapshot() patterns.MetroCutSnapshot {
	if r.historyCut == nil {
		return patterns.MetroCutSnapshot{}
	}
	return r.historyCut.Snapshot()
}

// serverMetroCut detects an unbound or replaced owner without waiting for I/O.
func (s *Server) serverMetroCut() (*patterns.MetroCut, bool) {
	r := s.Cache.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Patterns == nil {
		return nil, !r.historyCutMismatch && (!r.historyCutBound || r.historyCut == nil)
	}
	cut := s.Patterns.MetroCut()
	return cut, !r.historyCutMismatch && r.historyCutBound && r.historyCut == cut
}

func (s *Server) claimMetroPublication(cut patterns.MetroCutSnapshot, safe bool) bool {
	owner, matched := s.serverMetroCut()
	if safe {
		if owner != nil {
			_ = owner.Snapshot()
		}
		return true
	}
	if !matched {
		return false
	}
	return owner == nil || owner.Claim(cut)
}
