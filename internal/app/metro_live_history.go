package app

import (
	"encoding/json"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"sort"
)

func (b *metroFrameBuilder) selectJourney() {
	id := b.interest.Journey
	if id == "" {
		return
	}
	b.frame.SelectedJourneyId = ptr(id)
	for _, t := range b.frame.Trains {
		if t.JourneyId == id {
			return
		}
	}
	if t, ok := b.server.Cache.metroRuntime.retained(id); ok {
		b.frame.Trains = append(b.frame.Trains, t)
	} else {
		b.restoreJourney()
	}
}
func (b *metroFrameBuilder) restoreJourney() {
	if b.server.Patterns == nil {
		return
	}
	records, err := b.server.Patterns.MetroJourneyEvents(b.ctx, b.interest.Journey, b.now)
	if err != nil {
		b.frame.HistoryStatus = "partial"
		return
	}
	if len(records) == 0 {
		return
	}
	latest := b.latestJourneyProofs(records)
	restored := api.MetroTrain{}
	for _, record := range latest {
		b.restoreProof(&restored, record)
	}
	sort.Slice(restored.Calls, func(i, j int) bool { return restored.Calls[i].StopSequence < restored.Calls[j].StopSequence })
	restored.CurrentIndex = nil
	restored.Association = "suspended"
	restored.NextIndex = nil
	restored.Reason = "Histórico parcial restaurado; sem continuidade atual"
	b.frame.Trains = append(b.frame.Trains, restored)
}
func (b *metroFrameBuilder) latestJourneyProofs(records []patterns.MetroEventRecord) map[string]patterns.MetroEventRecord {
	latest := map[string]patterns.MetroEventRecord{}
	for _, record := range records {
		var proof metroEventProof
		if json.Unmarshal(record.Payload, &proof) != nil {
			b.frame.HistoryStatus = "partial"
			continue
		}
		if old, ok := latest[proof.CallID]; !ok || record.At.After(old.At) {
			latest[proof.CallID] = record
		}
	}
	return latest
}
func (b *metroFrameBuilder) restoreProof(restored *api.MetroTrain, record patterns.MetroEventRecord) {
	var p metroEventProof
	if json.Unmarshal(record.Payload, &p) != nil {
		b.frame.HistoryStatus = "partial"
		return
	}
	if restored.JourneyId == "" {
		*restored = p.Train
		restored.Calls = []api.StopCall{}
	}
	for _, c := range p.Train.Calls {
		if c.Arrival.Inferred != nil {
			c.Arrival.Inferred.Persistence = "committed"
		}
		restored.Calls = append(restored.Calls, c)
	}
}
