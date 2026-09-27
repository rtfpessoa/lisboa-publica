package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

// References are response-only selectors, not authentication credentials.
// Every decoded field is checked against the retained immutable observation.
type vehicleNavigation struct {
	Revision string `json:"r"`
	AsOf     int64  `json:"a"`
	ID       string `json:"i"`
	Operator string `json:"o"`
	Observed int64  `json:"t"`
	Plan     string `json:"p,omitempty"`
	Trip     string `json:"v,omitempty"`
	Day      string `json:"d,omitempty"`
	Pattern  string `json:"g,omitempty"`
}

func textValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func navigationFor(state *State, asOf time.Time, v api.Vehicle) vehicleNavigation {
	return vehicleNavigation{state.Revision, asOf.UnixNano(), v.Id, v.OperatorId, v.ObservedAt.UnixNano(), textValue(v.PlanId), textValue(v.TripId), textValue(v.OperationalDate), textValue(v.PatternId)}
}

func encodeNavigation(prefix string, value any) string {
	b, _ := json.Marshal(value)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func decodeNavigation(raw, prefix string, max int, target any) error {
	if len(raw) > max || !strings.HasPrefix(raw, prefix) {
		return fail(http.StatusBadRequest, "reference", "Referência de navegação inválida.")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, prefix))
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(target)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = io.ErrUnexpectedEOF
			}
		}
	}
	if err != nil {
		return fail(http.StatusBadRequest, "reference", "Referência de navegação inválida.")
	}
	return nil
}

func vehicleReference(state *State, asOf time.Time, v api.Vehicle) *api.VehicleReference {
	raw := encodeNavigation("n1.", navigationFor(state, asOf, v))
	if len(raw) > 2048 || len(v.Id) > 256 {
		return nil
	}
	return &api.VehicleReference{VehicleId: v.Id, Reference: raw}
}

// No current static enrichment: a newer plan cannot rename or change a frozen report.
func navigationVehicles(state *State, operator string, asOf time.Time) []api.Vehicle {
	rows, _, _, _, _ := projectLive(state.Live[operator], state.Operators[operator], nil, asOf)
	return rows
}

type serviceIdentity struct{ Operator, Plan, Trip, Day string }

func serviceFor(v api.Vehicle) serviceIdentity {
	return serviceIdentity{v.OperatorId, textValue(v.PlanId), textValue(v.TripId), textValue(v.OperationalDate)}
}
func (k serviceIdentity) complete() bool {
	return k.Operator != "" && k.Plan != "" && k.Trip != "" && k.Day != ""
}

func serviceVehicles(ctx context.Context, state *State, operator string) (map[serviceIdentity]*api.VehicleReference, error) {
	out := map[serviceIdentity]*api.VehicleReference{}
	for _, v := range navigationVehicles(state, operator, state.Created) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := serviceFor(v)
		if !key.complete() {
			continue
		}
		if _, exists := out[key]; exists {
			out[key] = nil
		} else {
			out[key] = vehicleReference(state, state.Created, v)
		}
	}
	return out, nil
}

func attachArrivalVehicles(ctx context.Context, state *State, rows []api.Arrival) error {
	if len(rows) == 0 {
		return nil
	}
	links, err := serviceVehicles(ctx, state, rows[0].OperatorId)
	if err != nil {
		return err
	}
	for i := range rows {
		a := &rows[i]
		if a.ServiceDate == nil {
			continue
		}
		a.VehicleRef = links[serviceIdentity{a.OperatorId, textValue(a.PlanId), textValue(a.SourceTripId), a.ServiceDate.Format("2006-01-02")}]
	}
	return ctx.Err()
}

func attachPredictionVehicles(ctx context.Context, state *State, rows []api.CPPrediction) error {
	links, err := serviceVehicles(ctx, state, "cp")
	if err != nil {
		return err
	}
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := &rows[i]
		if p.ServiceDate != nil {
			p.VehicleRef = links[serviceIdentity{p.OperatorId, p.PlanId, p.SourceTripId, p.ServiceDate.Format("2006-01-02")}]
		}
	}
	return nil
}

func vehicleStopMatches(state *State, v api.Vehicle, stop string) bool {
	d := state.Static[v.OperatorId]
	if v.StopId == nil || !compatibleVehicleStopPlan(v, d) {
		return false
	}
	matched := *v.StopId == stop
	if !matched && d != nil && d.Schedule != nil {
		matched = qualify(v.OperatorId, d.Schedule.Parents[strings.TrimPrefix(*v.StopId, v.OperatorId+":")]) == stop
	}
	return matched
}
func compatibleVehicleStopPlan(v api.Vehicle, d *StaticData) bool {
	return v.PlanId == nil || d == nil || *v.PlanId == d.PlanID
}

func (s *Server) navigationState(raw, id string, now time.Time) (*State, time.Time, api.Vehicle, error) {
	var n vehicleNavigation
	err := decodeNavigation(raw, "n1.", 2048, &n)
	if err == nil && n.ID != id {
		err = fail(http.StatusBadRequest, "reference", "A referência pertence a outro veículo.")
	}
	asOf := time.Unix(0, n.AsOf)
	if err == nil {
		err = validateVehicleClock(asOf, now)
	}
	state := s.Cache.vehicleRevisionState(n.Revision, now)
	if err == nil {
		err = validateVehicleState(state, asOf)
	}
	var v api.Vehicle
	if err == nil {
		v, err = frozenNavigationVehicle(state, n, asOf, now)
	}
	return state, asOf, v, err
}
func frozenNavigationVehicle(state *State, n vehicleNavigation, asOf, now time.Time) (api.Vehicle, error) {
	for _, v := range navigationVehicles(state, n.Operator, asOf) {
		if v.Id != n.ID || navigationFor(state, asOf, v) != n {
			continue
		}
		if !now.Before(v.ObservedAt.Add(lastKnownLifetime)) {
			break
		}
		v.VehicleRef = vehicleReference(state, asOf, v)
		return v, nil
	}
	return api.Vehicle{}, fail(http.StatusGone, "reference_expired", "Esta ligação expirou. Atualize os dados de origem.")
}

func listedVehicles(ctx context.Context, state *State, f Filter, asOf time.Time) ([]api.Vehicle, error) {
	out := []api.Vehicle{}
	for operator := range state.Live {
		if !f.selected(operator) {
			continue
		}
		rows, err := listedProviderVehicles(ctx, state, operator, f, asOf)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}
func listedProviderVehicles(ctx context.Context, state *State, operator string, f Filter, asOf time.Time) ([]api.Vehicle, error) {
	out := []api.Vehicle{}
	for _, v := range navigationVehicles(state, operator, asOf) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.Stop != "" && !vehicleStopMatches(state, v, f.Stop) {
			continue
		}
		if f.Route != "" && textValue(v.RouteId) != f.Route {
			continue
		}
		v.VehicleRef = vehicleReference(state, asOf, v)
		out = append(out, v)
	}
	return out, nil
}
