package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"lisboapublica/internal/api"
	"sort"
	"time"
)

type metroFrameRequest struct {
	server   *Server
	ctx      context.Context
	interest metroInterest
}
type metroFrameBuilder struct {
	metroFrameRequest
	state    *State
	static   *StaticData
	now      time.Time
	frame    api.MetroLiveFrame
	contexts []api.MetroForecastContext
}

func (s *Server) newMetroFrameBuilder(ctx context.Context, i metroInterest) (*metroFrameBuilder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := s.Cache.state("")
	if err != nil {
		return nil, err
	}
	b := &metroFrameBuilder{metroFrameRequest: metroFrameRequest{server: s, ctx: ctx, interest: i}, state: state, static: state.Static["metro"], now: time.Now().UTC()}
	historyStatus := b.readRuntimeView()

	b.initialize()
	b.frame.HistoryStatus = historyStatus
	err = b.checkInventory()
	return b, err
}
func (b *metroFrameBuilder) readRuntimeView() string {
	state := b.state
	data, plan, contexts, historyStatus := b.server.Cache.metroRuntime.view(b.now)
	if data != nil {
		copy := *state
		copy.Metro = data
		b.state, b.static, b.contexts = &copy, plan, contexts
	} else if state.Metro != nil {
		// Cached source forecasts survive restart, never cached live association.
		copy, cached := *state, *state.Metro
		cached.Trains = []api.MetroTrain{}
		copy.Metro, b.state = &cached, &copy
		if b.static != nil {
			topology := metroTopology(&cached, b.static)
			classifier := &metroRuntime{metroRuntimeTopology: metroRuntimeTopology{plan: b.static, topology: topology}, metroRuntimePublication: metroRuntimePublication{publication: &cached, batch: collectMetroPoints(&cached, topology, b.now)}}
			classifier.selectedContexts(classifier.batch, b.now)
			b.contexts = classifier.forecastContexts(b.now)
		}
	}
	return historyStatus
}

func (b *metroFrameBuilder) initialize() {
	b.frame = api.MetroLiveFrame{PublishedAt: b.now, Vehicles: []api.Vehicle{}, Trains: []api.MetroTrain{}, Directions: []api.BoardDirection{}, UnassociatedForecasts: []api.StopCall{}, ForecastContexts: &[]api.MetroForecastContext{}, Recovery: &api.MetroJourneyRecovery{Status: "none"}, HistoryStatus: "unavailable", Status: api.MetroStatus{Status: "unconfigured", Message: "API direta sem dados", Lines: []api.MetroLine{}, SourceUrl: metroBase}}
	if b.static != nil {
		b.frame.PlanId = b.static.PlanID
	}
	if b.state.Metro != nil {
		b.frame.Status = b.state.Metro.Status
		for _, t := range b.state.Metro.Trains {
			b.frame.Trains = append(b.frame.Trains, cloneMetroTrain(t))
		}
	}
	if b.frame.Status.Status == "ok" && b.expiredStatus() {
		b.frame.Status.Status = "error"
		b.frame.Status.Message = "Dados Metro expirados"
	}
}
func (b *metroFrameBuilder) expiredStatus() bool {
	return b.frame.Status.CheckedAt == nil || b.now.Sub(*b.frame.Status.CheckedAt) > sourceFreshness
}
func (b *metroFrameBuilder) checkInventory() error {
	if b.state.Metro != nil && b.state.Metro.InventoryOverflow {
		return fail(503, "inventory_capacity", "Inventário Metro excede a capacidade; não é possível apresentar uma lista completa.")
	}
	return nil
}
func (b *metroFrameBuilder) scopeTrains() {
	filtered := b.frame.Trains[:0]
	selected := textValue(b.frame.SelectedJourneyId)
	for _, t := range b.frame.Trains {
		if b.interest.Route == "" || sameMetroRoute(b.static, t.RouteId, b.interest.Route) || t.JourneyId == selected {
			filtered = append(filtered, t)
		}
	}
	b.frame.Trains = filtered
	sort.Slice(b.frame.Trains, func(a, c int) bool { return b.frame.Trains[a].JourneyId < b.frame.Trains[c].JourneyId })
	sort.Slice(b.frame.Vehicles, func(a, c int) bool { return b.frame.Vehicles[a].Id < b.frame.Vehicles[c].Id })
	sort.Slice(b.frame.Directions, func(a, c int) bool {
		return boardSelection(b.frame.Directions[a].LineKey, b.frame.Directions[a].DirectionKey) < boardSelection(b.frame.Directions[c].LineKey, b.frame.Directions[c].DirectionKey)
	})
}
func encodeMetroFrame(f api.MetroLiveFrame, limit int) (api.MetroLiveFrame, []byte, error) {
	// Receipt/render clocks and cache cursors cannot change identical source evidence.
	identity := f
	identity.PublishedAt = time.Time{}
	identity.Status.CheckedAt = nil
	raw, err := json.Marshal(identity)
	if err != nil {
		return f, nil, err
	}
	f.Revision = fmt.Sprintf("%x", sha256.Sum256(raw))
	raw, err = json.Marshal(f)
	if err == nil && len(raw) > limit {
		err = fail(413, "frame_limit", "Seleção demasiado grande; escolha uma linha Metro.")
	}
	return f, raw, err
}
