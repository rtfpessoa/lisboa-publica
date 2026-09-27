package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Read the timetable one trip at a time, avoiding a second full JSON timetable
// in the decoder buffer during restart or refresh validation.
func decodeStaticCacheJSON(reader io.Reader, data *StaticData) error {
	decoder := json.NewDecoder(reader)
	return decodeCacheObject(decoder, data, func(name string) (bool, error) {
		if name != "schedule" {
			return false, nil
		}
		data.Schedule = &Schedule{}
		err := decodeScheduleCache(decoder, data.Schedule)
		if errors.Is(err, errNullCacheObject) {
			data.Schedule = nil
			err = nil
		}
		return true, err
	})
}

func decodeScheduleCache(decoder *json.Decoder, schedule *Schedule) error {
	identities := map[string]string{}
	sources := map[scheduledTripSource]*scheduledTripSource{}
	return decodeCacheObject(decoder, schedule, func(name string) (bool, error) {
		if name != "Trips" {
			return false, nil
		}
		token, err := decoder.Token()
		if err != nil || token == nil {
			return true, err
		}
		if token != json.Delim('[') {
			return true, fmt.Errorf("invalid cached trip array")
		}
		schedule.Trips = []ScheduledTrip{}
		for decoder.More() {
			var trip ScheduledTrip
			if err = decoder.Decode(&trip); err != nil {
				return true, err
			}
			internCachedSource(&trip, sources)
			internCachedVisits(trip.Times, identities)
			internCachedVisits(trip.JourneyTimes, identities)
			schedule.Trips = append(schedule.Trips, trip)
		}
		_, err = decoder.Token()
		return true, err
	})
}

func internCachedVisits(visits []StopTime, identities map[string]string) {
	for n := range visits {
		id := visits[n].Stop
		canonical, found := identities[id]
		if !found {
			canonical = strings.Clone(id)
			identities[canonical] = canonical
		}
		visits[n].Stop = canonical
	}
}

func decodeCacheObject(decoder *json.Decoder, target any, special func(string) (bool, error)) error {
	token, err := decoder.Token()
	if err == nil {
		err = cacheObjectToken(token)
	}
	if err != nil {
		return err
	}
	fields := cacheDecodeFields(target)
	for decoder.More() {
		err = decodeCacheField(decoder, fields, special)
		if err != nil {
			break
		}
	}
	if err == nil {
		_, err = decoder.Token()
	}
	return err
}

func cacheObjectToken(token json.Token) error {
	if token == nil {
		return errNullCacheObject
	}
	if token != json.Delim('{') {
		return fmt.Errorf("invalid cached object")
	}
	return nil
}

func decodeCacheField(decoder *json.Decoder, fields map[string]any, special func(string) (bool, error)) error {
	name, err := decoder.Token()
	if err != nil {
		return err
	}
	handled, err := special(name.(string))
	if !handled && err == nil {
		field := fields[name.(string)]
		if field == nil {
			var ignored json.RawMessage
			field = &ignored
		}
		err = decoder.Decode(field)
	}
	return err
}

// Resolve the authoritative struct tags rather than duplicating the cache schema.
func cacheDecodeFields(target any) map[string]any {
	value := reflect.ValueOf(target).Elem()
	fields := map[string]any{}
	for n := 0; n < value.NumField(); n++ {
		definition := value.Type().Field(n)
		if !definition.IsExported() {
			continue
		}
		name := strings.Split(definition.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = definition.Name
		}
		fields[name] = value.Field(n).Addr().Interface()
	}
	return fields
}

var errNullCacheObject = errors.New("null cached object")

func decodeStoredCacheJSON(reader io.Reader, target any) error {
	if static, ok := target.(*StaticData); ok {
		return decodeStaticCacheJSON(reader, static)
	}
	return json.NewDecoder(reader).Decode(target)
}

func internCachedSource(trip *ScheduledTrip, sources map[scheduledTripSource]*scheduledTripSource) {
	if trip.Source == nil {
		return
	}
	shared := sources[*trip.Source]
	if shared == nil {
		shared = trip.Source
		sources[*shared] = shared
	}
	trip.Source = shared
}
