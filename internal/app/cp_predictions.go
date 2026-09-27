package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

const hoursPerDay = 24
const secondsPerDay = hoursPerDay * secondsPerHour

// CPData is an immutable, ephemeral prediction snapshot. It is never persisted as history.
type CPData struct {
	PlanID       string
	Rows         []api.CPPrediction
	Availability api.CPPredictionAvailability
}

func normalizeCP(feed *cpFeed, data *StaticData, now time.Time) (*CPData, error) {
	if data == nil || data.Schedule == nil {
		return nil, fmt.Errorf("CP network unavailable")
	}
	published := time.Unix(feed.Header.Timestamp, 0).UTC()
	if !cpCurrent(published, now) {
		return nil, fmt.Errorf("CP publication expired")
	}
	out := &CPData{PlanID: data.PlanID, Rows: []api.CPPrediction{}, Availability: api.CPPredictionAvailability{Status: api.CPPredictionAvailabilityStatusOk, Message: "Previsões da fonte pública TML · CP.", SourceUrl: cpSourceURL, CollectedAt: &now, PublishedAt: &published}}
	batch := cpBatch{index: indexCP(data, feed.Updates), now: now, out: out, candidates: map[string]api.CPPrediction{}, conflicts: map[string]bool{}, services: map[string]bool{}}
	err := batch.collect(feed.Updates)
	if err == nil {
		err = finishCP(out)
	}
	return out, err
}

type cpBatch struct {
	index               *cpIndex
	now                 time.Time
	out                 *CPData
	candidates          map[string]api.CPPrediction
	conflicts, services map[string]bool
}

func (b *cpBatch) collect(updates []cpUpdate) error {
	for _, u := range updates {
		if err := b.accept(u); err != nil {
			return err
		}
	}
	for key, row := range b.candidates {
		if b.conflicts[key] || cpSuppressed(b.index, updates, row, b.now) {
			b.out.Availability.ExcludedUpdates++
			continue
		}
		b.out.Rows = append(b.out.Rows, row)
	}
	return nil
}

func (b *cpBatch) accept(u cpUpdate) error {
	t := cpTrip(b.index, u)
	if !cpAdmissibleUpdate(t, u, b.now) {
		b.out.Availability.ExcludedUpdates += len(u.Stops)
		return nil
	}
	b.services[t.ID] = true
	if len(b.services) > cpMaxServices {
		return fmt.Errorf("CP service capacity")
	}
	day, basis := b.index.instance(t, u, b.now)
	instance := cpResolvedUpdate{Trip: t, Update: u, Day: day, Basis: basis}
	for _, s := range u.Stops {
		row, valid := b.index.prediction(instance, s, b.now)
		if !valid {
			b.out.Availability.ExcludedUpdates++
			continue
		}
		chooseCPPrediction(b.candidates, b.conflicts, row)
	}
	return nil
}

func cpAdmissibleUpdate(t *ScheduledTrip, u cpUpdate, now time.Time) bool {
	return t != nil && cpScheduled(u.Trip.Relationship) && cpCurrent(time.Unix(u.Timestamp, 0), now)
}

type cpResolvedUpdate struct {
	Trip   *ScheduledTrip
	Update cpUpdate
	Day    time.Time
	Basis  string
}

func cpCurrent(at, now time.Time) bool {
	return at.Unix() > 0 && !at.After(now.Add(providerClockSkew)) && now.Before(at.Add(sourceFreshness))
}

func (i *cpIndex) prediction(instance cpResolvedUpdate, s cpStopUpdate, now time.Time) (api.CPPrediction, bool) {
	trip, update, day, basis := instance.Trip, instance.Update, instance.Day, instance.Basis
	v := i.visit(trip, s)
	if basis == "invalid" || v == nil || !cpScheduled(s.Relationship) {
		return api.CPPrediction{}, false
	}
	// An explicit invalid/conflicting service descriptor is not an undated fallback.
	if update.Trip.Date != "" && day.IsZero() && cpScheduledJoin(i.Data, trip) {
		return api.CPPrediction{}, false
	}
	var planned time.Time
	if !day.IsZero() {
		planned = serviceStart(day).Add(time.Duration(v.Arrival) * time.Second)
	}
	expected, valid := cpExpected(s, planned)
	if !valid || expected.Before(now) || expected.After(now.Add(2*time.Hour)) {
		return api.CPPrediction{}, false
	}
	return i.predictionRow(instance, s, v, now), true
}

func (i *cpIndex) predictionRow(instance cpResolvedUpdate, s cpStopUpdate, v *StopTime, now time.Time) api.CPPrediction {
	update, day, basis := instance.Update, instance.Day, instance.Basis
	var planned time.Time
	if !day.IsZero() {
		planned = serviceStart(day).Add(time.Duration(v.Arrival) * time.Second)
	}
	expected, _ := cpExpected(s, planned)
	row := i.namedPrediction(instance.Trip, v)
	observed := time.Unix(update.Timestamp, 0).UTC()
	row.ExpectedAt, row.DelaySeconds, row.SourceUpdatedAt = expected, s.Arrival.Delay, observed
	row.CollectedAt, row.ValidUntil, row.SourceUrl = now, observed.Add(sourceFreshness), cpSourceURL
	date := "unknown"
	if !day.IsZero() {
		row.ServiceDate, row.DateBasis, row.ScheduledAt = ptr(apiDate(day)), ptr(api.CPPredictionDateBasis(basis)), &planned
		date = day.Format("2006-01-02")
	}
	row.Id = strings.Join([]string{row.PlanId, row.SourceTripId, date, row.StopId, fmt.Sprint(row.StopSequence)}, "|")
	return row
}

func cleanCPName(v string) string {
	if len(v) > cpMaxNameBytes {
		return ""
	}
	return strings.Join(strings.Fields(v), " ")
}

func cleanCPLabel(v string) string {
	if len(v) > cpMaxLabelBytes {
		return ""
	}
	return strings.TrimSpace(v)
}

func chooseCPPrediction(rows map[string]api.CPPrediction, conflicts map[string]bool, row api.CPPrediction) {
	old, exists := rows[row.Id]
	if !exists || row.SourceUpdatedAt.After(old.SourceUpdatedAt) {
		rows[row.Id], conflicts[row.Id] = row, false
		return
	}
	if row.SourceUpdatedAt.Equal(old.SourceUpdatedAt) && !reflect.DeepEqual(row, old) {
		conflicts[row.Id] = true
	}
}

func cpSuppressed(i *cpIndex, updates []cpUpdate, row api.CPPrediction, now time.Time) bool {
	for _, u := range updates {
		t := cpTrip(i, u)
		if t == nil || qualify("cp", t.ID) != row.SourceTripId {
			continue
		}
		at := time.Unix(u.Timestamp, 0)
		if !cpCurrent(at, now) || at.Before(row.SourceUpdatedAt) {
			continue
		}
		if u.Trip.Date != "" && row.ServiceDate != nil && u.Trip.Date != row.ServiceDate.Format("20060102") {
			continue
		}
		if !cpScheduled(u.Trip.Relationship) {
			return true
		}
		for _, s := range u.Stops {
			v := i.visit(t, s)
			if v != nil && v.Sequence == row.StopSequence && !cpScheduled(s.Relationship) {
				return true
			}
		}
	}
	return false
}

func finishCP(out *CPData) error {
	if len(out.Rows) > cpMaxRows {
		return fmt.Errorf("CP row capacity")
	}
	sort.Slice(out.Rows, func(a, b int) bool {
		if out.Rows[a].ExpectedAt.Equal(out.Rows[b].ExpectedAt) {
			return out.Rows[a].Id < out.Rows[b].Id
		}
		return out.Rows[a].ExpectedAt.Before(out.Rows[b].ExpectedAt)
	})
	if out.Availability.ExcludedUpdates > 0 {
		out.Availability.Status, out.Availability.Message = api.CPPredictionAvailabilityStatusPartial, "Cobertura parcial: algumas atualizações estão antigas ou não podem ser associadas com segurança."
	} else if len(out.Rows) == 0 {
		out.Availability.Message = "Sem previsões atuais na área de Lisboa; consulte os horários planeados."
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if len(encoded) > cpMaxBytes {
		return fmt.Errorf("CP snapshot capacity")
	}
	return nil
}

func (i *cpIndex) namedPrediction(t *ScheduledTrip, v *StopTime) api.CPPrediction {
	row := api.CPPrediction{OperatorId: "cp", PlanId: i.Data.PlanID, SourceTripId: qualify("cp", t.ID), RouteId: qualify("cp", t.Route), StopId: qualify("cp", v.Stop), StopSequence: v.Sequence}
	row.StopName = cleanCPName(i.Names[v.Stop])
	row.RouteName = cleanCPName(scheduledRouteName(i.Data, row.RouteId))
	row.DestinationName = cleanCPName(t.Headsign)
	row.ServiceLabel = optional(cleanCPLabel(t.Label))
	return row
}
