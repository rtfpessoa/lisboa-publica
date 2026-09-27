package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"lisboapublica/internal/api"
	"time"
)

func decodeCMArrivals(ctx context.Context, blob []byte, stop string, now time.Time, v *arrivalSnapshot) error {
	decoder := json.NewDecoder(cpContextReader{ctx: ctx, Reader: bytes.NewReader(blob)})
	if err := arrivalListDelimiter(decoder, '['); err != nil {
		return err
	}
	err := readCMArrivalRows(ctx, decoder, stop, now, v)
	if err == nil {
		err = completeArrivalList(decoder)
	}
	return err
}

func readCMArrivalRows(ctx context.Context, decoder *json.Decoder, stop string, now time.Time, v *arrivalSnapshot) error {
	seen := map[string]bool{}
	var err error
	for decoder.More() {
		var a cmArrival
		err = ctx.Err()
		if err == nil {
			err = decoder.Decode(&a)
		}
		if err != nil {
			break
		}
		appendCMArrival(v, a, stop, now, seen)
	}
	return err
}

func arrivalListDelimiter(d *json.Decoder, want json.Delim) error {
	token, err := d.Token()
	if err != nil || token != want {
		return fmt.Errorf("invalid arrivals list")
	}
	return nil
}

func completeArrivalList(d *json.Decoder) error {
	if err := arrivalListDelimiter(d, ']'); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing arrivals data")
	}
	return nil
}

func validCMDescriptor(a cmArrival) bool {
	return validArrivalUnix(a.Scheduled) && a.Line != "" && len(a.Line) <= cpMaxLabelBytes && len(a.Trip) <= arrivalCMTripBytes && len(a.Headsign) <= cpMaxNameBytes
}

func appendCMArrival(v *arrivalSnapshot, a cmArrival, stop string, now time.Time, seen map[string]bool) {
	if len(v.rows) >= arrivalRowsLimit || !validCMDescriptor(a) {
		v.availability.Status = "partial"
		v.availability.PlannedStatus = "partial"
	} else if !observedCMArrival(v, a, now) {
		row, expected := cmArrivalRow(v, a, stop)
		if expected.Before(now) || expected.After(now.Add(arrivalForecastHorizon)) {
			return
		}
		if seen[row.Id] {
			v.availability.Status = "partial"
		} else {
			seen[row.Id] = true
			v.rows = append(v.rows, row)
		}
	}
}

func observedCMArrival(v *arrivalSnapshot, a cmArrival, now time.Time) bool {
	if a.Observed == 0 {
		return false
	}
	if !validArrivalUnix(a.Observed) || time.Unix(a.Observed, 0).After(now.Add(providerClockSkew)) {
		v.availability.Status = "partial"
	}
	return true
}

func cmArrivalRow(v *arrivalSnapshot, a cmArrival, stop string) (api.Arrival, time.Time) {
	scheduled := time.Unix(a.Scheduled, 0).UTC()
	row := api.Arrival{Id: stop + ":" + a.Line + ":" + a.Trip + ":" + stringID(uint64(a.Scheduled)), OperatorId: "cm", StopId: stop, RouteId: qualify("cm", a.Line), TripId: qualify("cm", a.Trip), Headsign: a.Headsign, ScheduledAt: &scheduled, Kind: "scheduled", SourceUrl: arrivalSource(stop), SourceTripId: optional(a.Trip), ValidUntil: ptr(v.expires), RouteName: cmArrivalRouteName(v.static, a.Line)}
	expected := scheduled
	if a.Estimated != 0 {
		if validArrivalUnix(a.Estimated) {
			expected = time.Unix(a.Estimated, 0).UTC()
			row.ExpectedAt = &expected
			row.Kind = "prediction"
		} else {
			v.availability.Status = "partial"
		}
	}
	return row, expected
}

func cmArrivalRouteName(d *StaticData, line string) *string {
	for _, r := range d.Routes {
		if r.Id == qualify("cm", line) {
			if name := cleanCPName(r.ShortName); name != "" {
				return &name
			}
			break
		}
	}
	return &line
}
