package app

import (
	"fmt"
	"time"

	"lisboapublica/internal/api"
)

type tripEndpoint struct {
	Stop, Name string
	Sequence   int
}

type tripEndpoints struct {
	Origin, Destination tripEndpoint
}

func (g *gtfsReader) rememberStop(m map[string]string) error {
	if g.provider.ID != "cp" {
		return nil
	}
	id, name := m["stop_id"], m["stop_name"]
	if len(id) == 0 || len(id) > 128 || len(name) > 512 || len(g.endpointNames) >= maxReadResults {
		return fmt.Errorf("CP endpoint metadata limit")
	}
	g.endpointNames[id] = name
	return nil
}

func (g *gtfsReader) rememberEndpoint(t *ScheduledTrip, stop string, seq int) {
	if g.provider.ID != "cp" {
		return
	}
	endpoint := tripEndpoint{Stop: stop, Name: g.endpointNames[stop], Sequence: seq}
	if t.Endpoints == nil {
		t.Endpoints = &tripEndpoints{Origin: endpoint, Destination: endpoint}
	}
	if seq < t.Endpoints.Origin.Sequence {
		t.Endpoints.Origin = endpoint
	}
	if seq > t.Endpoints.Destination.Sequence {
		t.Endpoints.Destination = endpoint
	}
}

func (t ScheduledTrip) scheduledEndpoints(source string, day time.Time) *api.ScheduledEndpoints {
	e := t.Endpoints
	if e == nil || e.Origin.Stop == "" || e.Origin.Name == "" || e.Destination.Stop == "" || e.Destination.Name == "" || e.Origin.Sequence >= e.Destination.Sequence {
		return nil
	}
	return &api.ScheduledEndpoints{OriginSourceStopId: e.Origin.Stop, OriginName: e.Origin.Name, DestinationSourceStopId: e.Destination.Stop, DestinationName: e.Destination.Name, SourceUrl: source, ServiceDate: day.In(lisbon).Format("2006-01-02")}
}
