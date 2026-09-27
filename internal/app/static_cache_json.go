package app

import (
	"encoding/json"
	"io"

	"lisboapublica/internal/api"
)

// Keep the existing cache JSON format, but never buffer an entire timetable or
// network as one JSON value. Each array element uses the standard encoder.
func writeStaticCacheJSON(w io.Writer, d *StaticData) error {
	if d == nil {
		return json.NewEncoder(w).Encode(nil)
	}
	fields := []cacheJSONField{
		{"routes", d.Routes, false}, {"stops", d.Stops, false},
		{"schedule", d.Schedule, d.Schedule == nil},
		{"cm_paths", d.CMPaths, len(d.CMPaths) == 0}, {"cm_path_error", d.CMPathError, d.CMPathError == nil},
		{"models", d.Models, len(d.Models) == 0},
		{"plan_id", d.PlanID, false}, {"valid_from", d.ValidFrom, false},
		{"valid_until", d.ValidUntil, false}, {"source", d.Source, false},
		{"updated", d.Updated, false}, {"shapes", d.Shapes, len(d.Shapes) == 0},
		{"geometry_updated", d.GeometryUpdated, d.GeometryUpdated == nil},
		{"geometry_error", d.GeometryError, d.GeometryError == nil},
		{"geometry_partial", d.GeometryPartial, !d.GeometryPartial},
		{"cp_journey_endpoints", d.CPJourneyEndpoints, !d.CPJourneyEndpoints},
		{"arrival_metadata", d.ArrivalMetadata, !d.ArrivalMetadata},
		{"has_frequencies", d.HasFrequencies, !d.HasFrequencies},
		{"cp_prediction_metadata", d.CPPredictionMetadata, !d.CPPredictionMetadata},
		{"cp_has_frequencies", d.CPHasFrequencies, !d.CPHasFrequencies},
	}
	return writeCacheJSONObject(w, fields)
}

type cacheJSONField struct {
	name  string
	value any
	omit  bool
}

func writeCacheJSONObject(w io.Writer, fields []cacheJSONField) error {
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	comma := ""
	for _, field := range fields {
		if field.omit {
			continue
		}
		if _, err := io.WriteString(w, comma+`"`+field.name+`":`); err != nil {
			return err
		}
		err := writeCacheJSONValue(w, field.value)
		if err != nil {
			return err
		}
		comma = ","
	}
	_, err := io.WriteString(w, "}")
	return err
}

func writeCacheJSONArray[T any](w io.Writer, rows []T) error {
	if rows == nil {
		_, err := io.WriteString(w, "null")
		return err
	}
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	err := writeCacheJSONElements(w, rows)
	if err == nil {
		_, err = io.WriteString(w, "]")
	}
	return err
}

func writeCacheJSONElements[T any](w io.Writer, rows []T) error {
	encoder := json.NewEncoder(w)
	for i := range rows {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err := encoder.Encode(rows[i]); err != nil {
			return err
		}
	}
	return nil
}

func writeCacheJSONValue(w io.Writer, value any) error {
	var err error
	switch rows := value.(type) {
	case []CMPath:
		err = writeCacheJSONArray(w, rows)
	case []ScheduledTrip:
		err = writeCacheJSONArray(w, rows)
	case []api.RouteDetail:
		err = writeCacheJSONArray(w, rows)
	case []api.Stop:
		err = writeCacheJSONArray(w, rows)
	case []api.RouteShape:
		err = writeCacheJSONArray(w, rows)
	case *Schedule:
		err = writeCacheJSONObject(w, []cacheJSONField{{"Trips", rows.Trips, false}, {"Calendars", rows.Calendars, false}, {"Exceptions", rows.Exceptions, false}, {"Parents", rows.Parents, false}})
	default:
		err = json.NewEncoder(w).Encode(value)
	}
	return err
}
