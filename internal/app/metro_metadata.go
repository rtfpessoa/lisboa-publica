package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"lisboapublica/internal/api"
)

type metroMetadataState struct {
	metadataMu             sync.RWMutex
	metadataStarted        bool
	metadataStations       []MetroStation
	metadataDestinations   []MetroDestination
	metadataLines          []api.MetroLine
	metadataAt             time.Time
	metadataStationsAt     time.Time
	metadataDestinationsAt time.Time
	metadataError          string
	tokenMu                sync.Mutex
}

// MetroDestination retains an official destination catalogue identifier and name.
type MetroDestination struct {
	ID   string `json:"id_destino"`
	Name string `json:"nome_destino"`
}

func (m *MetroClient) metadataLoop(ctx context.Context) {
	m.refreshMetadata(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refreshMetadata(ctx)
		}
	}
}
func (m *MetroClient) refreshMetadata(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	m.refreshMetroLineState(bounded)
	stationsDue, destinationsDue := m.metroCataloguesDue()
	if stationsDue {
		m.refreshMetroStations(bounded)
	}
	if destinationsDue {
		m.refreshMetroDestinations(bounded)
	}
}
func (m *MetroClient) refreshMetroLineState(ctx context.Context) {
	var states map[string]string
	err := m.get(ctx, "/estadoLinha/todos", &states)
	var lines []api.MetroLine
	if err == nil {
		lines, err = metroLines(states)
	}
	m.metadataMu.Lock()
	defer m.metadataMu.Unlock()
	if err != nil {
		m.metadataError = err.Error()
		return
	}
	m.metadataLines, m.metadataAt, m.metadataError = lines, time.Now().UTC(), ""
}
func (m *MetroClient) metroCataloguesDue() (bool, bool) {
	m.metadataMu.RLock()
	defer m.metadataMu.RUnlock()
	return metroCatalogueDue(m.metadataStationsAt), metroCatalogueDue(m.metadataDestinationsAt)
}
func metroCatalogueDue(at time.Time) bool { return at.IsZero() || time.Since(at) >= 24*time.Hour }
func (m *MetroClient) refreshMetroStations(ctx context.Context) {
	var stations []MetroStation
	if m.get(ctx, "/infoEstacao/todos", &stations) != nil || len(stations) == 0 {
		return
	}
	m.metadataMu.Lock()
	defer m.metadataMu.Unlock()
	m.metadataStations, m.metadataStationsAt = stations, time.Now().UTC()
}
func (m *MetroClient) refreshMetroDestinations(ctx context.Context) {
	var destinations []MetroDestination
	if m.get(ctx, "/infoDestinos/todos", &destinations) != nil || len(destinations) == 0 {
		return
	}
	m.metadataMu.Lock()
	defer m.metadataMu.Unlock()
	m.metadataDestinations, m.metadataDestinationsAt = destinations, time.Now().UTC()
}

func (m *MetroClient) fetchWaitLane(ctx context.Context, previous *MetroData, now time.Time) *MetroData {
	raw, err := m.readWaits(ctx)
	var waits []MetroWait
	if err == nil {
		err = decodeMetroWaitRows(raw, &waits)
	}
	data := m.metroMetadataSnapshot(now)
	data.Waits, data.RawWaits = waits, raw
	if len(data.Stations) == 0 && previous != nil {
		data.Stations = previous.Stations
	}
	if err != nil {
		completeMetroFetch(data, previous, nil, err)
	}
	return data
}
func (m *MetroClient) metroMetadataSnapshot(now time.Time) *MetroData {
	m.metadataMu.RLock()
	defer m.metadataMu.RUnlock()
	lines := append([]api.MetroLine{}, m.metadataLines...)
	lineError := metroLineSnapshotError(lines, m.metadataAt, m.metadataError, now)
	status := api.MetroStatus{Status: "ok", CheckedAt: &now, SourceUrl: metroBase, Lines: lines, Message: "Previsões oficiais; estado das linhas consultado em ciclo separado."}
	status.LineStateUpdatedAt, status.LineStateError = optionalTime(m.metadataAt), optional(lineError)
	return &MetroData{Status: status, Stations: append([]MetroStation{}, m.metadataStations...), Destinations: append([]MetroDestination{}, m.metadataDestinations...)}
}
func metroLineSnapshotError(lines []api.MetroLine, at time.Time, sourceError string, now time.Time) string {
	if now.Sub(at) <= sourceFreshness {
		return sourceError
	}
	for n := range lines {
		lines[n].State = "Estado por confirmar"
	}
	if sourceError == "" {
		sourceError = "Estado das linhas expirado"
	}
	return sourceError
}

func (m *MetroClient) readWaits(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := m.get(ctx, "/tempoEspera/Estacao/todos", &raw)
	return raw, err
}
func decodeMetroWaitRows(raw []byte, waits *[]MetroWait) error { return json.Unmarshal(raw, waits) }

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
