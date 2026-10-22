package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"lisboapublica/internal/api"
)

type snapshotMetadata struct {
	SourceID             string  `json:"source_id"`
	Model                *string `json:"model"`
	Plate                *string `json:"license_plate"`
	Typology             *string `json:"typology,omitempty"`
	Propulsion           *string `json:"propulsion,omitempty"`
	SeatedCapacity       *int    `json:"seated_capacity,omitempty"`
	TotalCapacity        *int    `json:"total_capacity,omitempty"`
	WheelchairAccessible *bool   `json:"wheelchair_accessible,omitempty"`
	Contactless          *bool   `json:"contactless,omitempty"`
}

func (s *Store) prepareHistory(operator string, live *LiveData, distances map[string]*float64) ([]historicalRecord, *historyCollector) {
	if s.HistoryInterval == 0 {
		if live == nil {
			return nil, nil
		}
		return rawHistory(live, distances), nil
	}
	if live == nil {
		live = &LiveData{Collected: time.Now().UTC()}
	}
	collector := s.collector
	if collector == nil {
		collector = newHistoryCollector()
	}
	next, records := collector.propose(operator, live, distances, s.HistoryInterval)
	return records, next
}

func recordBytes(records []historicalRecord) int64 {
	bytes := int64(0)
	for _, record := range records {
		vehicle := record.Vehicle
		fields := len(vehicle.Id) + len(vehicle.SourceId) + len(vehicle.OperatorId) + len(stringValue(vehicle.RouteId)) + len(stringValue(vehicle.TripId)) + len(stringValue(vehicle.Model)) + len(stringValue(vehicle.LicensePlate)) + len(stringValue(vehicle.Typology)) + len(stringValue(vehicle.Propulsion))
		bytes += int64(fields)*storageWriteOverhead + historyRecordOverhead + specificationWriteBytes(vehicle)
	}
	return bytes
}

type historyWrite struct {
	operator    string
	generation  int64
	records     []historicalRecord
	replacement bool
}

func insertHistory(ctx context.Context, tx pgx.Tx, write historyWrite) error {
	if len(write.records) == 0 {
		return nil
	}
	var retainedAfter *time.Time
	if err := tx.QueryRow(ctx, `SELECT cutoff FROM history_pressure WHERE id=1`).Scan(&retainedAfter); err != nil {
		return err
	}
	batch, queued, err := prepareHistoryBatch(write, retainedAfter)
	if err != nil || len(queued) == 0 {
		return err
	}
	inserted, recent, err := executeHistoryBatch(ctx, tx, batch, queued)
	if err != nil {
		return err
	}
	if write.replacement && inserted != recent {
		return fmt.Errorf("replacement history aged beyond the recent window")
	}
	if inserted > 0 {
		_, err := tx.Exec(ctx, `UPDATE history_pressure SET delete_credit=delete_credit+$1,
			insert_credit=GREATEST(insert_credit-$1,0) WHERE id=1 AND enabled`, recent)
		return err
	}
	return nil
}

func prepareHistoryBatch(write historyWrite, retainedAfter *time.Time) (*pgx.Batch, []historicalRecord, error) {
	batch := &pgx.Batch{}
	queued := make([]historicalRecord, 0, len(write.records))
	for _, record := range write.records {
		vehicle := record.Vehicle
		if retainedAfter != nil && vehicle.ObservedAt.Before(*retainedAfter) {
			continue
		}
		blob, err := json.Marshal(snapshotMetadata{SourceID: vehicle.SourceId, Model: vehicle.Model, Plate: vehicle.LicensePlate, Typology: vehicle.Typology, Propulsion: vehicle.Propulsion, SeatedCapacity: vehicle.SeatedCapacity, TotalCapacity: vehicle.TotalCapacity, WheelchairAccessible: vehicle.WheelchairAccessible, Contactless: vehicle.Contactless})
		if err != nil {
			return nil, nil, err
		}
		batch.Queue("INSERT INTO snapshots(operator_id,vehicle_id,observed_at,generation,route_id,trip_id,position_kind,lat,lon,speed_kmh,distance_km,payload,first_observed_at,speed_sample_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(operator_id,vehicle_id,observed_at) DO NOTHING", write.operator, vehicle.Id, vehicle.ObservedAt, write.generation, vehicle.RouteId, vehicle.TripId, string(vehicle.PositionKind), vehicle.Lat, vehicle.Lon, vehicle.SpeedKmh, record.Distance, blob, record.First, record.SpeedSamples)
		queued = append(queued, record)
	}
	return batch, queued, nil
}

func executeHistoryBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch, queued []historicalRecord) (int64, int64, error) {
	results := tx.SendBatch(ctx, batch)
	var inserted, recent int64
	now := time.Now().UTC()
	cutoff := now.Add(-pressureRecentWindow)
	for _, record := range queued {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()
			return 0, 0, err
		}
		inserted += tag.RowsAffected()
		if !record.Vehicle.ObservedAt.Before(cutoff) && !record.Vehicle.ObservedAt.After(now.Add(providerClockSkew)) {
			recent += tag.RowsAffected()
		}
	}
	return inserted, recent, results.Close()
}

func (s *Store) historyResolution() int {
	if s.HistoryInterval == 0 {
		return int(providerRefreshInterval.Seconds())
	}
	return int(s.HistoryInterval.Seconds())
}

func (s *Store) historyCollectionStatus() api.ConfigHistoryCollectionStatus {
	return api.ConfigHistoryCollectionStatus(s.historyStatus())
}

type cacheUpdate struct {
	Static, Live, Health []byte
	Facts                []factWrite
	Reporting            []reportingWrite
}

func prepareCacheUpdate(static *StaticData, live *LiveData, op api.Operator) (cacheUpdate, error) {
	update := cacheUpdate{}
	var err error
	if static != nil {
		update.Static, err = encodeCache(static)
	}
	if err != nil {
		return update, err
	}
	if live != nil {
		update.Live, err = encodeCache(live)
	}
	if err != nil {
		return update, err
	}
	update.Health, err = json.Marshal(op)
	return update, err
}

func (s *Store) persistUpdate(ctx context.Context, id string, update cacheUpdate, records []historicalRecord, replacement bool) error {
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var generation int64
		if err := tx.QueryRow(ctx, "UPDATE app_state SET generation=generation+1 WHERE id=1 RETURNING generation").Scan(&generation); err != nil {
			return err
		}
		parts := []struct {
			Kind string
			Blob []byte
		}{{"static", update.Static}, {"live", update.Live}}
		for _, part := range parts {
			if part.Blob == nil {
				continue
			}
			if err := writeCache(ctx, tx, id, part.Kind, part.Blob); err != nil {
				return err
			}
		}
		if err := insertReporting(ctx, tx, id, update.Reporting); err != nil {
			return err
		}
		if err := insertVehicleFacts(ctx, tx, id, update.Facts); err != nil {
			return err
		}
		if err := insertHistory(ctx, tx, historyWrite{operator: id, generation: generation, records: records, replacement: replacement}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO source_health(operator_id,payload) VALUES($1,$2) ON CONFLICT(operator_id) DO UPDATE SET payload=excluded.payload", id, update.Health)
		return err
	})
}

// Four bounded specification values add at most128JSON bytes, before MVCC reservation.
const snapshotSpecificationBytes = 128

func specificationWriteBytes(vehicle api.Vehicle) int64 {
	if vehicle.SeatedCapacity == nil && vehicle.TotalCapacity == nil && vehicle.WheelchairAccessible == nil && vehicle.Contactless == nil {
		return 0
	}
	return snapshotSpecificationBytes * storageWriteOverhead
}
