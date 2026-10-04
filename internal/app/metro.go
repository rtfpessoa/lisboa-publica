package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"golang.org/x/text/unicode/norm"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

const metroBase = "https://api.metrolisboa.pt:8243/estadoServicoML/1.0.1"
const metroTokenURL = "https://api.metrolisboa.pt:8243/token"

// MetroStation describes a station published by the authenticated Metro API.
type MetroStation struct {
	ID    string `json:"stop_id"`
	Name  string `json:"stop_name"`
	Lat   string `json:"stop_lat"`
	Lon   string `json:"stop_lon"`
	Lines string `json:"linha"`
	URLs  string `json:"stop_url"`
	Zone  string `json:"zone_id"`
}

// MetroWait retains the provider’s platform prediction fields without inventing positions.
type MetroWait struct {
	Stop        string          `json:"stop_id"`
	Platform    string          `json:"cais"`
	At          string          `json:"hora"`
	Train       string          `json:"comboio"`
	Train2      string          `json:"comboio2"`
	Train3      string          `json:"comboio3"`
	Wait1       json.RawMessage `json:"tempoChegada1"`
	Wait2       json.RawMessage `json:"tempoChegada2"`
	Wait3       json.RawMessage `json:"tempoChegada3"`
	Destination string          `json:"destino"`
}

// MetroData combines direct service status, station metadata and waiting-time predictions.
type MetroData struct {
	Destinations      []MetroDestination `json:"destinations,omitempty"`
	InventoryOverflow bool               `json:"-"`
	Trains            []api.MetroTrain   `json:"-"`
	Topology          patterns.Topology  `json:"-"`
	Status            api.MetroStatus    `json:"status"`
	Waits             []MetroWait        `json:"waits"`
	Stations          []MetroStation     `json:"stations"`
	// Original wait JSON preserves fields whose meaning is not yet known.
	RawWaits json.RawMessage `json:"raw_waits,omitempty"`
}

// MetroClient serializes OAuth token reuse and cached direct Metro refreshes.
type MetroClient struct {
	Client                           *http.Client
	Store                            *Store
	Cache                            *Cache
	ClientID, Secret, Base, TokenURL string
	Log                              *zap.Logger
	lastUnmapped                     []string
	lastSampleLogged                 time.Time
	ownLoggedAt                      time.Time
	metroRefreshState
	History *patterns.Service
}

// NewMetroClient creates a client for server-side consumer credentials.
func NewMetroClient(client *http.Client, store *Store, cache *Cache, id, secret string) *MetroClient {
	return &MetroClient{Client: client, Store: store, Cache: cache, ClientID: id, Secret: secret, Base: metroBase, TokenURL: metroTokenURL, metroRefreshState: metroRefreshState{Interval: 500 * time.Millisecond}}
}

func (m *MetroClient) interval() time.Duration {
	if m.Interval < 500*time.Millisecond {
		return 500 * time.Millisecond
	}
	return m.Interval
}

// Refresh returns the shared Metro result, refreshing at most once per polling interval.
func (m *MetroClient) Refresh(ctx context.Context) *MetroData {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.lastAttempt.IsZero() && time.Since(m.lastAttempt) < m.interval() {
		return m.data
	}
	now := time.Now().UTC()
	m.lastAttempt = now
	if m.ClientID == "" || m.Secret == "" {
		data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusUnconfigured, Message: "API direta do Metro sem credenciais. Posições estimadas e horários TML continuam disponíveis.", SourceUrl: metroBase, Lines: []api.MetroLine{}}}
		m.data = data
		return data
	}
	ctx, cancel := context.WithTimeout(ctx, metroRefreshTimeout)
	defer cancel()
	return m.refreshConfigured(ctx, now)
}
func (m *MetroClient) refreshConfigured(ctx context.Context, now time.Time) *MetroData {
	previous := m.data
	if previous == nil {
		state, _ := m.Cache.state("")
		previous = state.Metro
	}
	data := m.fetchData(ctx, previous, now)
	// Acquisition may publish a source clock after the cycle start but before receipt.
	// Keep cadence on lastAttempt; inference and collection status use completed receipt.
	received := time.Now().UTC()
	data.Status.CheckedAt = &received
	m.publish(ctx, data, received)
	m.data = data
	return data
}

// publish updates live health immediately and limits durable writes independently.
func (m *MetroClient) publish(ctx context.Context, data *MetroData, now time.Time) {
	state, _ := m.Cache.state("")
	m.Cache.metroRuntime.observe(data, state.Static["metro"], m.History, now)
	m.reportUnmappedStops()
	m.publishMetroHistory(data, now)
	m.Store.PublishMu.Lock()
	defer m.Store.PublishMu.Unlock()
	op := m.metroPublicationOperator(data, now)
	if now.Sub(m.lastPersist) >= livePersistenceInterval {
		m.lastPersist = now
		_ = m.Store.SaveMetro(ctx, data, op)
	}
	m.Cache.updateMetro(data, op)
}

// logOwnForecastStats emits a cumulative own-forecast summary at most once per minute
// at debug level: tracks queried and call values produced by each source since start.
func (m *MetroClient) logOwnForecastStats() {
	if m.Log == nil {
		return
	}
	now := time.Now().UTC()
	if !m.ownLoggedAt.IsZero() && now.Sub(m.ownLoggedAt) < time.Minute {
		return
	}
	m.ownLoggedAt = now
	queries, historical, schedule := m.Cache.metroRuntime.ownForecastStats()
	m.Log.Debug("metro own forecasts", zap.Int64("queries", queries), zap.Int64("historical", historical), zap.Int64("schedule_prior", schedule))
}

// reportUnmappedStops logs a changed set of static stops that no longer resolve to a
// published station. A GTFS revision change must not shrink the network silently.
func (m *MetroClient) reportUnmappedStops() {
	unmapped := m.Cache.metroRuntime.unmappedStaticStops()
	if len(unmapped) == len(m.lastUnmapped) {
		equal := true
		for n := range unmapped {
			if unmapped[n] != m.lastUnmapped[n] {
				equal = false
				break
			}
		}
		if equal {
			return
		}
	}
	previous := len(m.lastUnmapped)
	m.lastUnmapped = unmapped
	if m.Log == nil {
		return
	}
	if len(unmapped) > 0 {
		m.Log.Warn("metro topology left static stops unmapped", zap.Int("count", len(unmapped)), zap.Strings("stops", unmapped))
	} else if previous > 0 {
		m.Log.Info("metro topology resolved every static stop")
	}
}

func (m *MetroClient) publishMetroHistory(data *MetroData, now time.Time) {
	if m.History != nil {
		state, _ := m.Cache.state("")
		if state != nil {
			m.enqueuePatterns(data, state.Static["metro"], now)
		}
	}
	m.historyMu.Lock()
	queued := m.historyQueue != nil
	m.historyMu.Unlock()
	if !queued {
		m.Cache.metroRuntime.projectOwn(data, m.History, now)
		m.logOwnForecastStats()
	}
}
func (m *MetroClient) metroPublicationOperator(data *MetroData, now time.Time) api.Operator {
	op := m.Cache.operator("metro")
	op.DirectStatus, op.DirectUpdatedAt, op.DirectError = ptr(api.OperatorDirectStatus(data.Status.Status)), ptr(now), nil
	if data.Status.Status == "error" {
		op.DirectError = ptr(data.Status.Message)
	}
	return op
}

// Run refreshes provider data until its context is cancelled.
func (m *MetroClient) Run(ctx context.Context) {
	m.metadataMu.Lock()
	m.metadataStarted = true
	m.metadataMu.Unlock()
	if m.History != nil {
		m.historyMu.Lock()
		m.historyQueue = make(chan metroHistoryTask, 64)
		m.historyMu.Unlock()
		go m.historyLoop(ctx)
	}
	go m.metadataLoop(ctx)
	go m.Cache.metroRuntime.runJournal(ctx, m.History)
	m.Refresh(ctx)
	for {
		// Measure from the previous start, discarding missed ticks and catch-up work.
		m.mu.Lock()
		lastAttempt := m.lastAttempt
		m.mu.Unlock()
		delay := max(time.Until(lastAttempt.Add(m.interval())), time.Millisecond)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			m.Refresh(ctx)
		}
	}
}

// GetMetroStatus returns cached direct Metro availability without waiting on collection.
func (s *Server) GetMetroStatus(ctx context.Context, _ api.GetMetroStatusRequestObject) (api.GetMetroStatusResponseObject, error) {
	if s.Metro == nil || s.Metro.ClientID == "" || s.Metro.Secret == "" {
		return api.GetMetroStatus200JSONResponse{Status: api.MetroStatusStatusUnconfigured, Message: "API direta do Metro sem credenciais; utilize horários planeados.", SourceUrl: metroBase, Lines: []api.MetroLine{}}, nil
	}
	state, err := s.Cache.state("")
	if err != nil {
		return nil, err
	}
	if state.Metro == nil {
		return api.GetMetroStatus200JSONResponse{Status: api.MetroStatusStatusError, Message: "A aguardar dados da API direta do Metro; utilize horários planeados.", SourceUrl: metroBase, Lines: []api.MetroLine{}}, nil
	}
	status := state.Metro.Status
	if status.Status == api.MetroStatusStatusOk && (status.CheckedAt == nil || time.Since(*status.CheckedAt) > sourceFreshness) {
		status.Status = api.MetroStatusStatusError
		status.Message = "Dados da API direta do Metro desatualizados; serviço por confirmar."
		status.Lines = []api.MetroLine{}
	}
	return api.GetMetroStatus200JSONResponse(status), nil
}
func (c *Cache) updateMetro(d *MetroData, op api.Operator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := *c.current
	copy.Operators = make(map[string]api.Operator, len(c.current.Operators))
	for k, v := range c.current.Operators {
		copy.Operators[k] = v
	}
	copy.Operators["metro"] = op
	copy.Metro = d
	c.publish(&copy)
}

// SaveMetro persists direct Metro predictions and source health atomically.
func (s *Store) SaveMetro(ctx context.Context, d *MetroData, op api.Operator) error {
	blob, e := encodeCache(d)
	if e != nil {
		return e
	}
	opJSON, e := json.Marshal(op)
	if e != nil {
		return e
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.reserveStorage(ctx, int64(len(blob)+len(opJSON))*storageWriteOverhead+historyRecordOverhead, operationalDatabaseBytes); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if e := writeCache(ctx, tx, "metro", "direct", blob); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, "INSERT INTO source_health(operator_id,payload) VALUES($1,$2) ON CONFLICT(operator_id) DO UPDATE SET payload=excluded.payload", "metro", opJSON)
		return e
	})
}

var destinations = map[string]string{"33": "RB", "34": "AS", "35": "PO", "36": "CM", "37": "LA", "38": "SS", "39": "AV", "40": "BC", "41": "TP", "42": "SP", "43": "OD", "44": "LU", "45": "CG", "46": "CP", "48": "RA", "50": "TE", "51": "AL", "52": "AM", "53": "MM", "54": "CS", "56": "BV", "57": "CH", "59": "MO", "60": "AP"}

func normalizeName(v string) string {
	decomp := norm.NFD.String(strings.ToLower(v))
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, decomp)
}
func predictedArrivals(state *State, from, to time.Time, route, stop string) []api.Arrival {
	out := []api.Arrival{}
	data := state.Metro
	asOf := state.Created
	if asOf.IsZero() {
		asOf = time.Now()
	}
	if data == nil || data.Status.Status != "ok" || data.Status.CheckedAt == nil || asOf.Sub(*data.Status.CheckedAt) > sourceFreshness {
		return out
	}
	stations := map[string]MetroStation{}
	for _, station := range data.Stations {
		stations[station.ID] = station
	}
	stationID := metroStationID(state.Static["metro"], data.Stations, stations, stop)
	routeIDs := metroRouteIDs(state.Static["metro"])
	latest := latestMetroPublications(data.Waits, stationID, asOf)
	seen := currentMetroArrivals(metroArrivalSelection{data: data, stations: stations, routeIDs: routeIDs, asOf: asOf, from: from, to: to, route: route, stop: stop, stationID: stationID, latest: latest})
	for _, arrival := range seen {
		out = append(out, arrival)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExpectedAt.Equal(*out[j].ExpectedAt) {
			return out[i].Id < out[j].Id
		}
		return out[i].ExpectedAt.Before(*out[j].ExpectedAt)
	})
	return out
}

// metroStationID joins official station codes to GTFS using verified nearby names.
func metroStationID(static *StaticData, published []MetroStation, stations map[string]MetroStation, stop string) string {
	stationID := strings.TrimPrefix(stop, "metro:")
	if _, exists := stations[stationID]; exists || static == nil {
		return stationID
	}
	for _, gtfs := range static.Stops {
		if gtfs.Id != stop {
			continue
		}
		matches := []string{}
		for _, station := range published {
			if metroStationCandidateMatches(station, gtfs) {
				matches = append(matches, station.ID)
			}
		}
		if len(matches) == 1 {
			return matches[0]
		}
	}
	return stationID
}

func metroRouteIDs(static *StaticData) map[string]string {
	ids := map[string]string{}
	if static == nil {
		return ids
	}
	names := map[string]string{"Az": "azul", "Am": "amarela", "Vd": "verde", "Vm": "vermelha"}
	for _, route := range static.Routes {
		line, exists := names[route.ShortName]
		if exists && (line != "amarela" || ids[line] == "") {
			ids[line] = route.Id
		}
	}
	return ids
}

func platformLine(wait MetroWait, station MetroStation) string {
	lines := map[string]string{"33": "azul", "42": "azul", "43": "amarela", "48": "amarela", "50": "verde", "54": "verde", "38": "vermelha", "60": "vermelha"}
	if line := lines[wait.Destination]; line != "" {
		return line
	}
	members := strings.Split(strings.Trim(station.Lines, "[]"), ",")
	if len(members) == 1 {
		return strings.ToLower(strings.TrimSpace(members[0]))
	}
	return ""
}

func platformArrivals(wait MetroWait, stations map[string]MetroStation, routeIDs map[string]string, asOf, from, to time.Time, route, stop string) []api.Arrival {
	out := []api.Arrival{}
	observed, err := time.ParseInLocation("20060102150405", wait.At, lisbon)
	if err != nil || asOf.Sub(observed) > sourceFreshness || observed.After(asOf.Add(providerClockSkew)) {
		return out
	}
	base := metroArrival(wait, stations, routeIDs, stop)
	if route != "" && route != base.RouteId {
		return out
	}
	base.ObservedAt = &observed
	return metroUpcoming(wait, base, from, to)
}

func metroArrival(wait MetroWait, stations map[string]MetroStation, routeIDs map[string]string, stop string) api.Arrival {
	line := platformLine(wait, stations[wait.Stop])
	routeID := routeIDs[line]
	lineName := map[string]string{"azul": "Linha Azul", "amarela": "Linha Amarela", "verde": "Linha Verde", "vermelha": "Linha Vermelha"}[line]
	headsign := stations[destinations[wait.Destination]].Name
	if headsign == "" {
		headsign = "Destino publicado: " + wait.Destination
	}
	return api.Arrival{OperatorId: "metro", StopId: stop, RouteId: routeID, RouteName: optional(lineName), Headsign: headsign, Kind: api.ArrivalKindPrediction, SourceUrl: metroBase + "/tempoEspera/Estacao/todos"}
}

func metroUpcoming(wait MetroWait, base api.Arrival, from, to time.Time) []api.Arrival {
	out := []api.Arrival{}
	observed := *base.ObservedAt

	trains := []string{wait.Train, wait.Train2, wait.Train3}
	for index, value := range []json.RawMessage{wait.Wait1, wait.Wait2, wait.Wait3} {
		var seconds int
		var valid bool
		seconds, valid = metroWaitSeconds(value)
		train := strings.TrimSpace(trains[index])
		if !valid || !metroReference(train) {
			continue
		}
		expected := observed.Add(time.Duration(seconds) * time.Second)
		if expected.Before(from) || !expected.Before(to) {
			continue
		}
		id := metroForecastID(wait, train)
		row := base
		row.Id, row.TripId, row.ExpectedAt = id, qualify("metro", train), &expected
		out = append(out, row)
	}
	return out
}

func metroForecastID(wait MetroWait, reference string) string {
	return qualify("metro", "prediction:"+wait.Stop+":"+reference+":"+wait.Destination)
}

// Explicit null, strings and fractional values are absent, never zero waits.
func metroWaitSeconds(value json.RawMessage) (int, bool) {
	var seconds *int
	if json.Unmarshal(value, &seconds) != nil || seconds == nil || *seconds < 0 || *seconds > maxPredictionWaitSeconds {
		return 0, false
	}
	return *seconds, true
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func (m *MetroClient) fetchData(ctx context.Context, previous *MetroData, now time.Time) *MetroData {
	m.metadataMu.RLock()
	started := m.metadataStarted
	m.metadataMu.RUnlock()
	if started {
		return m.fetchWaitLane(ctx, previous, now)
	}
	return m.fetchInitialMetroData(ctx, previous, now)
}
func (m *MetroClient) fetchInitialMetroData(ctx context.Context, previous *MetroData, now time.Time) *MetroData {
	stations := []MetroStation{}
	if previous != nil {
		stations = previous.Stations
	}
	var states map[string]string
	err := m.get(ctx, "/estadoLinha/todos", &states)
	var waits []MetroWait
	var raw json.RawMessage
	if err == nil {
		raw, err = m.readWaits(ctx)
	}
	if err == nil {
		err = json.Unmarshal(raw, &waits)
	}
	if err == nil && len(stations) == 0 {
		err = m.get(ctx, "/infoEstacao/todos", &stations)
	}
	data := initialMetroData(stations, waits, now)
	if err == nil {
		data.RawWaits = raw
	}
	completeMetroFetch(data, previous, states, err)
	return data
}
func initialMetroData(stations []MetroStation, waits []MetroWait, now time.Time) *MetroData {
	status := api.MetroStatus{Status: api.MetroStatusStatusOk, CheckedAt: &now, SourceUrl: metroBase, Message: "Tempos de espera e estado do serviço verificados na API oficial do Metro.", Lines: []api.MetroLine{}}
	return &MetroData{Status: status, Waits: waits, Stations: stations}
}

func metroLines(states map[string]string) ([]api.MetroLine, error) {
	lines := []api.MetroLine{}

	for _, line := range []string{"amarela", "azul", "verde", "vermelha"} {
		value := strings.TrimSpace(states[line])
		if value == "" {
			return lines, fmt.Errorf("missing Metro line state")
		}
		description := strings.TrimSpace(states[line+"_curta"])
		state := "Aviso do fornecedor"
		if strings.EqualFold(value, "ok") && description == "normal" {
			state = "Normal"
		}
		lines = append(lines, api.MetroLine{Line: line, State: state, Description: value + " · " + description})
	}
	return lines, nil
}

func completeMetroFetch(data, previous *MetroData, states map[string]string, err error) {
	if err != nil {
		data.Status.Status = api.MetroStatusStatusError
		data.Status.Message = err.Error() + ". Previsões diretas indisponíveis; horários planeados e posições TML mantêm-se."
		if previous != nil {
			data.Waits = previous.Waits
			data.Stations = previous.Stations
		}
	} else {
		data.Status.Lines, err = metroLines(states)
		if err != nil {
			data.Status.Status = api.MetroStatusStatusError
			data.Status.Message = err.Error()
			data.Waits = nil
		}
	}
}

// metroRefreshState serializes token reuse and direct snapshot publication.
type metroRefreshState struct {
	metroMetadataState
	metroHistoryDelivery
	// Interval is independent of the shared cadence for other providers.
	Interval    time.Duration
	mu          sync.Mutex
	tokenValue  string
	expires     time.Time
	lastAttempt time.Time
	lastPersist time.Time
	data        *MetroData
}
