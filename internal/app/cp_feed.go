package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	cpMaxRows              = 1024
	cpMaxServices          = 256
	cpMaxBytes             = 256 * 1024
	cpMaxEntities          = 512
	cpMaxDeviation         = 6 * secondsPerHour
	cpMaxIdentifierBytes   = 256
	cpMaxStopBytes         = 128
	cpDescriptorClockBytes = 8
	cpRelationshipBytes    = 32
	cpMaxInputRows         = cpMaxRows * 4
	cpMaxNameBytes         = 512
	cpMaxLabelBytes        = 128
	cpLatestTimestamp      = 4102444800
	cpCalendarAnchorHour   = 12
	cpJSONDepth            = 16
	cpJSONNumericBytes     = 64
	cpJSONStringBytes      = 4096
	cpEntityBytes          = 64 * 1024
	cpEntityObjectDepth    = 3
	providerCollectorCount = 4
	cpSourceURL            = hubBase + "/realtime/eta/gtfs"
)

type cpFeed struct {
	Header struct {
		Version        string `json:"gtfs_realtime_version"`
		Incrementality string `json:"incrementality"`
		Timestamp      int64  `json:"timestamp"`
	}
	Updates []cpUpdate
	visit   func(cpEntity)
}

type cpUpdate struct {
	Trip struct {
		ID           string `json:"trip_id"`
		Date         string `json:"start_date"`
		StartTime    string `json:"start_time"`
		Route        string `json:"route_id"`
		Relationship string `json:"schedule_relationship"`
	} `json:"trip"`
	Vehicle struct {
		ID string `json:"id"`
	} `json:"vehicle"`
	Timestamp int64          `json:"timestamp"`
	Stops     []cpStopUpdate `json:"stop_time_update"`
}

type cpStopUpdate struct {
	ID           string `json:"stop_id"`
	Sequence     *int   `json:"stop_sequence"`
	Relationship string `json:"schedule_relationship"`
	Arrival      struct {
		Time  *int64 `json:"time"`
		Delay *int   `json:"delay"`
	} `json:"arrival"`
}

func decodeCPEntities(d *json.Decoder, feed *cpFeed) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('[') {
		return fmt.Errorf("invalid CP entities")
	}
	rows := 0
	for d.More() {
		var entity cpEntity
		err = d.Decode(&entity)
		if err == nil {
			if feed.visit != nil {
				feed.visit(entity)
			}
			err = retainCPEntity(feed, &rows, entity)
		}
		if err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func boundedCPUpdate(u cpUpdate) bool {
	if len(u.Trip.ID) > cpMaxIdentifierBytes || len(u.Trip.Date) > cpDescriptorClockBytes || len(u.Trip.StartTime) > cpDescriptorClockBytes || len(u.Trip.Route) > cpMaxIdentifierBytes {
		return false
	}
	for _, s := range u.Stops {
		if len(s.ID) > cpMaxStopBytes || len(s.Relationship) > cpRelationshipBytes {
			return false
		}
		if s.Sequence != nil && (*s.Sequence < 0 || *s.Sequence > maxReadResults) {
			return false
		}
	}
	return len(u.Trip.Relationship) <= 32
}

type cpEntity struct {
	Deleted bool     `json:"is_deleted"`
	Update  cpUpdate `json:"trip_update"`
}

func retainCPEntity(feed *cpFeed, rows *int, entity cpEntity) error {
	if entity.Deleted {
		return fmt.Errorf("unsupported CP deleted entity")
	}
	if !strings.Contains(entity.Update.Trip.ID, "[N18KL]") {
		return nil
	}
	// CP does not use physical vehicle identity; preserve its previous admission
	// rules and avoid retaining this new field in the CP input snapshot.
	entity.Update.Vehicle.ID = ""
	*rows += len(entity.Update.Stops)
	var err error
	if len(feed.Updates) >= cpMaxEntities || *rows > cpMaxInputRows || !boundedCPUpdate(entity.Update) {
		err = fmt.Errorf("CP input capacity or identifiers")
	}
	if err == nil {
		feed.Updates = append(feed.Updates, entity.Update)
	}
	return err
}
