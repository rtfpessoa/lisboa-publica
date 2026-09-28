package app

import (
	"context"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func historyString[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
func (f *Fetcher) recordProviderHistory(operator string, vehicles []api.Vehicle, at time.Time, failure string) {
	if f.Patterns == nil || operator == "metro" || f.historyOperators != nil && !f.historyOperators[operator] {
		return
	}
	var static *StaticData
	stops := map[string]api.Stop{}
	if f.Cache != nil {
		state, _ := f.Cache.state("")
		if state != nil {
			static = state.Static[operator]
		}
	}
	if static != nil {
		for _, stop := range static.Stops {
			stops[stop.Id] = stop
		}
	}
	receipt := providerHistoryReceipt(patterns.ProviderReceipt{Operator: operator, ReceivedAt: at, Error: failure}, vehicles, static, stops)

	f.enqueueProviderHistory(receipt)
}

func (f *Fetcher) enqueueProviderHistory(receipt patterns.ProviderReceipt) {
	operator := receipt.Operator
	if f.Patterns == nil || operator == "metro" || f.historyOperators != nil && !f.historyOperators[operator] {
		return
	}
	// Direct helper use in tests/maintenance is synchronous; Run installs the
	// bounded writer before starting collectors. No file write blocks publication.
	if f.history == nil {
		f.writeProviderHistory(receipt)
		return
	}
	f.mu.Lock()
	// Independent collectors can finish out of order. Archive receipt time is
	// assigned under the queue lock; the snapshot's original collection time and
	// all row/prediction source clocks remain separate and unchanged.
	collected := receipt.ReceivedAt
	receipt.CollectedAt = &collected
	receipt.ReceivedAt = time.Now().UTC()
	receipt.Gap = f.historyGaps[operator]
	select {
	case f.history <- receipt:
		delete(f.historyGaps, operator)
	default:
		f.historyGaps[operator] = true
	}
	f.mu.Unlock()
}

func (f *Fetcher) historyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-f.history:
			f.writeProviderHistory(r)
		}
	}
}
func (f *Fetcher) writeProviderHistory(r patterns.ProviderReceipt) {
	if err := f.Patterns.RecordProvider(r); err != nil {
		f.mu.Lock()
		if f.historyGaps != nil {
			f.historyGaps[r.Operator] = true
		}
		f.mu.Unlock()
		f.Log.Warn("provider historical capture skipped", zap.String("operator", r.Operator), zap.Error(err))
	}
}

func providerHistoryReceipt(receipt patterns.ProviderReceipt, vehicles []api.Vehicle, static *StaticData, stops map[string]api.Stop) patterns.ProviderReceipt {
	journeys := map[string]patterns.ProviderJourney{}
	identities := map[string]string{}
	rows := make([]patterns.Observation, 0, len(vehicles))
	limited := false
	for _, v := range vehicles {
		if v.LastKnown {
			continue
		}
		pathID, capped := providerHistoryPath(v, static, stops, journeys, identities)
		limited = limited || capped
		rows = append(rows, patterns.Observation{Journey: pathID, ID: v.Id, SourceID: v.SourceId, SourceURL: v.SourceUrl, Pattern: historyString(v.PatternId), OperationalDate: historyString(v.OperationalDate), ObservedAt: v.ObservedAt, Route: historyString(v.RouteId), Trip: historyString(v.TripId), Plan: historyString(v.PlanId), Stop: historyString(v.StopId), SourceStop: historyString(v.SourceStopId), Status: historyString(v.CurrentStatus), PositionKind: string(v.PositionKind), Lat: v.Lat, Lon: v.Lon, SpeedKmh: v.SpeedKmh})
	}

	receipt.Rows, receipt.Journeys, receipt.Limited = rows, journeys, limited
	return receipt
}

func providerHistoryPath(v api.Vehicle, static *StaticData, stops map[string]api.Stop, journeys map[string]patterns.ProviderJourney, identities map[string]string) (string, bool) {
	limited := false
	context := historyString(v.RouteId) + "|" + historyString(v.PatternId) + "|" + historyString(v.PlanId) + "|" + historyString(v.TripId) + "|" + historyString(v.OperationalDate) + "|" + historyString(v.StopId)
	pathID, seen := identities[context]
	if !seen {
		if path, ok := providerJourney(v, static, stops); ok {
			candidate := patterns.ProviderJourneyID(path)
			if _, known := journeys[candidate]; known || len(journeys) < 256 {
				pathID = candidate
				journeys[pathID] = path
			} else {
				limited = true
			}
		}
		identities[context] = pathID
	}
	return pathID, limited
}
