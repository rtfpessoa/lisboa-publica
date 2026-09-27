package app

import (
	"lisboapublica/internal/api"
	"strings"
)

func (b *tmlArrivalBatch) finish(err error) map[string]arrivalSnapshot {
	out := map[string]arrivalSnapshot{}
	for stop, d := range b.wanted {
		operator, _, _ := strings.Cut(stop, ":")
		i := b.indices[operator]
		v := arrivalSnapshot{static: d, rows: []api.Arrival{}, expires: b.now.Add(arrivalInterest), availability: api.ArrivalAvailability{Status: "ok", PlannedStatus: "unavailable", SourceUrl: cpSourceURL, CollectedAt: &b.now, CoverageUntil: ptr(b.now.Add(arrivalForecastHorizon))}}
		if err != nil {
			v.availability.Status = "error"
		} else if b.overflow || i.partial {
			v.availability.Status = "partial"
		}
		if err == nil && !b.overflow {
			selection := tmlArrivalSelection{stop: stop, index: i, snapshot: &v}
			selection.fill()
		}
		out[stop] = v
	}
	return out
}

type tmlArrivalSelection struct {
	stop     string
	index    *tmlArrivalIndex
	snapshot *arrivalSnapshot
}

func (s tmlArrivalSelection) fill() {
	chosen := map[string]api.Arrival{}
	conflict := map[string]bool{}
	for _, c := range s.index.candidates {
		_, raw, _ := strings.Cut(c.row.StopId, ":")
		if !s.index.stopMatches(raw, s.stop) {
			continue
		}
		if !s.validCandidate(c, raw) {
			s.snapshot.availability.Status = "partial"
		} else {
			chooseArrivalCandidate(chosen, conflict, c.row)
		}
	}
	for k, r := range chosen {
		if conflict[k] || len(s.snapshot.rows) >= arrivalRowsLimit {
			s.snapshot.availability.Status = "partial"
		} else {
			s.append(r)
		}
	}
	s.staleAvailability()
}

func (s tmlArrivalSelection) validCandidate(c tmlArrivalCandidate, raw string) bool {
	i := s.index
	return !i.index.BadHub[c.sourceStop] && !i.index.BadStop[raw] && i.index.Crosswalk[c.sourceStop] == raw && !i.cancelled[*c.row.SourceTripId]
}

func chooseArrivalCandidate(chosen map[string]api.Arrival, conflict map[string]bool, r api.Arrival) {
	prior, ok := chosen[r.Id]
	if !ok || r.SourceUpdatedAt.After(*prior.SourceUpdatedAt) {
		chosen[r.Id] = r
		conflict[r.Id] = false
	} else if r.SourceUpdatedAt.Equal(*prior.SourceUpdatedAt) && !r.ExpectedAt.Equal(*prior.ExpectedAt) {
		conflict[r.Id] = true
	}
}

func (s tmlArrivalSelection) append(r api.Arrival) {
	v := s.snapshot
	v.rows = append(v.rows, r)
	if v.availability.SourceUpdatedAt == nil || r.SourceUpdatedAt.After(*v.availability.SourceUpdatedAt) {
		v.availability.SourceUpdatedAt = r.SourceUpdatedAt
	}
	if v.availability.ValidUntil == nil || r.ValidUntil.Before(*v.availability.ValidUntil) {
		v.availability.ValidUntil = r.ValidUntil
	}
	if r.ValidUntil.Before(v.expires) {
		v.expires = *r.ValidUntil
	}
}

func (s tmlArrivalSelection) staleAvailability() {
	stale := s.index.stale[s.stop]
	if len(s.snapshot.rows) == 0 && !stale.IsZero() && s.snapshot.availability.Status == "ok" {
		s.snapshot.availability.Status = "stale"
		s.snapshot.availability.SourceUpdatedAt = ptr(stale)
		s.snapshot.availability.ValidUntil = ptr(stale.Add(sourceFreshness))
	}
}
