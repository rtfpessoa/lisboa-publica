package app

import (
	"encoding/json"
	"lisboapublica/internal/api"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStationCoverageFollowsContributingForecasts(t *testing.T) {
	s, d, _ := popupFixture(t, "metro", 3)
	now := time.Now().UTC()
	d.Routes[0].ShortName = "Vd"
	s.Cache.updateMetro(&MetroData{
		Status:   api.MetroStatus{Status: "ok", CheckedAt: &now},
		Stations: []MetroStation{{ID: "S", Name: "Lisboa", Lines: "[Verde]"}},
		Waits:    []MetroWait{{Stop: "S", At: now.In(lisbon).Format("20060102150405"), Train: "direct", Destination: "54", Wait1: json.RawMessage("120")}},
	}, s.Cache.operator("metro"))
	stale := now.Add(-5 * time.Minute)
	s.Cache.updateProviderPredictions(map[string]*StaticData{"metro": d}, map[string]*CPData{
		"metro": {PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "stale", PublishedAt: &stale, Message: "Previsões TML; cobertura parcial e tempos reais anteriores sem fonte comprovada."}},
	})
	board := popupGET[api.StopBoard](t, s, "/api/v1/stops/metro%3AS/board")
	calls := popupGET[api.StopCallPage](t, s, "/api/v1/stops/metro%3AS/board/calls")
	if !hasStationForecast(calls.Data, now) {
		t.Fatal("fixture did not provide a direct Metro forecast")
	}
	if board.Coverage.Status != "partial" || !strings.Contains(board.Coverage.Message, "Previsões disponíveis") {
		t.Fatalf("unrelated stale TML publication hid a usable Metro forecast: %+v", board.Coverage)
	}
	if board.Coverage.SourceUpdatedAt == nil || now.Sub(*board.Coverage.SourceUpdatedAt) > time.Second {
		t.Fatal("coverage did not preserve the contributing Metro source clock")
	}
	for _, direction := range board.Directions {
		if direction.DirectionKey == nil {
			continue
		}
		selected := popupGET[api.StopCallPage](t, s, "/api/v1/stops/metro%3AS/board/calls?line_key="+url.QueryEscape(direction.LineKey)+"&direction_key="+url.QueryEscape(*direction.DirectionKey))
		if hasStationForecast(selected.Data, now) {
			continue
		}
		if selected.Coverage.SourceUpdatedAt != nil || strings.Contains(selected.Coverage.Message, "Previsões disponíveis") {
			t.Fatal("selected results inherited another direction's forecast coverage")
		}
	}
	s.Cache.updateProviderPredictions(map[string]*StaticData{"metro": d}, map[string]*CPData{
		"metro": {PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "ok", PublishedAt: &now, Message: "Previsões TML; cobertura parcial e tempos reais anteriores sem fonte comprovada."}},
	})
	next := popupGET[api.StopBoard](t, s, "/api/v1/stops/metro%3AS/board")
	if next.Coverage.Message != board.Coverage.Message || next.Coverage.Status != board.Coverage.Status {
		t.Fatal("irrelevant source status changed the displayed forecast coverage")
	}
}

func TestStationCoverageDoesNotRenewExpiredEvidence(t *testing.T) {
	s, d, _ := popupFixture(t, "metro", 3)
	state, _ := s.Cache.state("")
	now := time.Now().UTC()
	expired := now.Add(-time.Second)
	evidence := &api.CallTimeEvidence{At: now.Add(time.Minute), SourceUrl: metroBase, SourceUpdatedAt: &expired, ValidUntil: &expired}
	calls := []api.StopCall{{Arrival: api.CallTime{Kind: "prediction", Prediction: evidence}}}
	coverage := s.stationCoverage(state, "metro", calls, arrivalSnapshot{})
	if strings.Contains(coverage.Message, "Previsões disponíveis") || coverage.SourceUpdatedAt != nil {
		t.Fatal("expired evidence was advertised as a current forecast")
	}
	d.Updated = now.Add(-13 * time.Hour)
	coverage = s.stationCoverage(state, "metro", nil, arrivalSnapshot{})
	if coverage.Status != "stale" || !strings.Contains(coverage.Message, "Rede desatualizada") {
		t.Fatal("stale published network lost its warning")
	}
	state.Static["metro"] = nil
	coverage = s.stationCoverage(state, "metro", nil, arrivalSnapshot{})
	if coverage.Status != "unavailable" {
		t.Fatal("missing evidence claimed a usable timetable")
	}
}

func TestStationCoverageAdmitsPublishedSchedulesWithoutGTFS(t *testing.T) {
	s, d, _ := popupFixture(t, "cm", 3)
	d.Schedule = nil
	state, _ := s.Cache.state("")
	now := time.Now()
	schedule := &api.CallTimeEvidence{At: now.Add(time.Minute), SourceUrl: "https://api.carrismetropolitana.pt/v2"}
	calls := []api.StopCall{{Arrival: api.CallTime{Kind: "schedule", Schedule: schedule}}}
	view := arrivalSnapshot{static: d, availability: api.ArrivalAvailability{Status: "error"}}
	out := s.stationCoverage(state, "cm", calls, view)
	if out.Status != "partial" || !strings.Contains(out.Message, "Horários planeados") || out.SourceUpdatedAt != nil {
		t.Fatalf("native scheduled evidence lost or given an invented update clock: %+v", out)
	}
}

func TestStationScheduleCoverageSurvivesPredictionFailure(t *testing.T) {
	s, d, _ := popupFixture(t, "carris", 3)
	now := time.Now().UTC()
	s.Cache.updateProviderPredictions(map[string]*StaticData{"carris": d}, map[string]*CPData{
		"carris": {PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "stale", Message: "Previsões antigas."}},
	})
	board := popupGET[api.StopBoard](t, s, "/api/v1/stops/carris%3AS/board")
	calls := popupGET[api.StopCallPage](t, s, "/api/v1/stops/carris%3AS/board/calls")
	if len(calls.Data) == 0 || calls.Data[0].Departure.Schedule == nil {
		t.Fatal("fixture did not retain a planned departure")
	}
	if board.Coverage.Status != "partial" || strings.Contains(board.Coverage.Message, "carregar") {
		t.Fatalf("usable planned board inherited a stale/loading forecast state: %+v", board.Coverage)
	}
	if board.Directions[0].Count == nil || *board.Directions[0].Count == 0 {
		t.Fatal("available assembled results were mislabeled as unknown coverage")
	}
	if hasStationForecast(calls.Data, now) {
		t.Fatal("prediction failure invented a current forecast")
	}
}

func TestPopupHistoryDisclaimerAppearsOnce(t *testing.T) {
	s, d, _ := popupFixture(t, "carris", 3)
	now := time.Now().UTC()
	s.Cache.updateProviderPredictions(map[string]*StaticData{"carris": d}, map[string]*CPData{
		"carris": {PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "ok", PublishedAt: &now, Message: "Previsões TML; cobertura parcial e tempos reais anteriores sem fonte comprovada."}},
	})
	state, _ := s.Cache.state("")
	coverage := s.popupCoverage(state, "carris")
	if count := strings.Count(strings.ToLower(coverage.Message), "tempos reais anteriores sem fonte comprovada"); count != 1 {
		t.Fatalf("history disclaimer repeated %d times: %s", count, coverage.Message)
	}
}

func hasStationForecast(calls []api.StopCall, now time.Time) bool {
	for _, call := range calls {
		for _, clock := range []api.CallTime{call.Arrival, call.Departure} {
			p := clock.Prediction
			if clock.Kind == "prediction" && p != nil && !p.At.Before(now) && p.ValidUntil != nil && now.Before(*p.ValidUntil) {
				return true
			}
		}
	}
	return false
}
