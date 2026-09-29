package app

import (
	"encoding/json"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"sort"
	"time"
)

func (b *metroFrameBuilder) selectJourney() {
	id := b.interest.Journey
	if id == "" {
		return
	}
	b.frame.Recovery = &api.MetroJourneyRecovery{Status: "recovering", RequestedJourneyId: ptr(id), Reason: "A recuperar a viagem selecionada"}
	for _, t := range b.frame.Trains {
		if t.JourneyId == id {
			b.frame.SelectedJourneyId = ptr(id)
			b.frame.Recovery.Status, b.frame.Recovery.Reason = "current", "Viagem selecionada disponível"
			if !metroCurrentDirection(t, b.now) {
				b.frame.Recovery.Status, b.frame.Recovery.Reason = "historical", "Viagem selecionada disponível; associação atual por confirmar"
			}
			return
		}
	}
	if t, ok := b.server.Cache.metroRuntime.retained(id, b.now); ok {
		b.frame.Trains = append(b.frame.Trains, t)
		b.frame.SelectedJourneyId = ptr(id)
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "historical", t.Reason
	} else {
		b.restoreJourney()
	}
}
func (b *metroFrameBuilder) restoreJourney() {
	if b.server.Patterns == nil {
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "unavailable", "Histórico indisponível; previsões atuais continuam disponíveis"
		return
	}
	runtime := b.server.Cache.metroRuntime
	if result, exists := runtime.recoveryMiss(b.interest.Journey, b.now); exists {
		b.frame.Recovery = &result
		if result.Status == "partial" || result.Status == "corrupt" {
			b.frame.HistoryStatus = "partial"
		}
		return
	}
	defer func() {
		if b.frame.SelectedJourneyId == nil {
			runtime.retainRecoveryMiss(*b.frame.Recovery, b.now)
		}
	}()
	b.restoreRetainedJourney()
}
func (b *metroFrameBuilder) restoreRetainedJourney() {
	checkpoint, status, err := b.server.Patterns.MetroJourneyCheckpoint(b.ctx, b.interest.Journey, b.now)
	if err != nil {
		b.frame.HistoryStatus = "partial"
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "corrupt", "Não foi possível verificar o histórico selecionado"
		return
	}
	if status == "expired" {
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "expired", "Histórico selecionado expirado"
		return
	}
	if status == "restored" {
		b.restoreCheckpoint(checkpoint)
		return
	}
	b.restoreLegacyJourney()
}
func (b *metroFrameBuilder) restoreCheckpoint(checkpoint patterns.MetroJourneyCheckpoint) {
	runtime := b.server.Cache.metroRuntime
	var p metroCheckpointPayload
	if json.Unmarshal(checkpoint.Payload, &p) != nil || p.Version != 1 || p.Train.JourneyId != b.interest.Journey || len(p.Train.Calls) > maxMetroPopupVisits {
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "corrupt", "Histórico selecionado incompatível"
		return
	}
	t := p.Train
	t.ModelProjection = nil
	t.Association, t.NextIndex, t.CurrentIndex, t.VehicleId = "suspended", nil, nil, nil
	t.Reason = "Último estado confirmado restaurado; continuidade atual não comprovada"
	t.Persistence = &api.MetroJourneyPersistence{State: "committed", Revision: int64(checkpoint.Revision), CommittedRevision: int64(checkpoint.Revision), Generation: ptr(checkpoint.Generation), CommittedAt: ptr(checkpoint.CommittedAt)}
	expireRestoredMetroCalls(&t, b.now)

	b.frame.Trains = append(b.frame.Trains, t)
	b.frame.SelectedJourneyId = ptr(t.JourneyId)
	b.frame.Recovery.Status, b.frame.Recovery.Reason = "historical", t.Reason
	p.Train = t
	runtime.retainCheckpoint(p, checkpoint)
	return
}
func expireRestoredMetroCalls(t *api.MetroTrain, now time.Time) {
	for n := range t.Calls {
		c := &t.Calls[n]
		expireMetroCall(c, "suspended", now)
		c.Phase = "unknown"
		if c.Arrival.Inferred != nil {
			c.Arrival.Inferred.Persistence = "committed"
		}
		if c.Departure.Inferred != nil {
			c.Departure.Inferred.Persistence = "committed"
		}
	}
}

func (b *metroFrameBuilder) restoreLegacyJourney() {

	records, err := b.server.Patterns.MetroJourneyEvents(b.ctx, b.interest.Journey, b.now)
	if err != nil {
		b.frame.HistoryStatus = "partial"
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "partial", "Histórico parcial indisponível"
		return
	}
	if len(records) == 0 {
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "unavailable", "Histórico selecionado indisponível ou removido pelo limite de espaço"
		return
	}
	b.restoreLegacyRecords(records)
}
func (b *metroFrameBuilder) restoreLegacyRecords(records []patterns.MetroEventRecord) {
	latest := b.latestJourneyProofs(records)
	restored := api.MetroTrain{}
	for _, record := range latest {
		b.restoreProof(&restored, record)
	}
	sort.Slice(restored.Calls, func(i, j int) bool { return restored.Calls[i].StopSequence < restored.Calls[j].StopSequence })
	if restored.JourneyId != b.interest.Journey || len(restored.Calls) == 0 {
		b.frame.Recovery.Status, b.frame.Recovery.Reason = "partial", "Histórico parcial sem registo recuperável"
		return
	}
	restored.CurrentIndex = nil
	restored.Association = "suspended"
	restored.NextIndex = nil
	restored.Reason = "Histórico parcial restaurado; sem continuidade atual"
	b.frame.Trains = append(b.frame.Trains, restored)
	b.frame.SelectedJourneyId = ptr(restored.JourneyId)
	b.frame.Recovery.Status, b.frame.Recovery.Reason = "partial", restored.Reason
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
