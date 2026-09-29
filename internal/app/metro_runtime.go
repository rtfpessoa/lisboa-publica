package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

const metroArrivalModel = "metro-publication-transition-v1"
const maxMetroPopupVisits = 256

type metroPoint struct {
	Stop, Platform string
	Clock          time.Time
	Seconds        *int
}
type metroTrack struct {
	LifecycleGroup        string
	ModelStops            map[string]metroModelStop
	FixedModelSign        int
	FixedModelSupport     map[int64]metroModelPublication
	ModelMotion           *metroOperationalMotion
	ModelDepartureSupport map[string]map[int64]metroModelPublication
	HistoricalCorrection  bool
	ModelGeneration       uint64
	Priors                []metroVisitPrior
	Geometry              [][][]float64
	ProviderDirection     string
	Train                 api.MetroTrain
	Profile               string
	Codes                 []string
	Points                map[string]metroPoint
	Proofs                map[string]metroEventProof
	ProofBytes            int
	ProofOverflow         bool
	HistoricalOnly        bool
	BarrierBefore         *api.MetroTrain
	BarrierRevision       uint64
	PendingSuccessor      string
	Movement              *metroTrackMovement
	Revision              uint64
	CommittedRevision     uint64
	Generation            string
	CommittedAt           time.Time
	CheckpointHash        string
	CheckpointUnavailable bool
}
type metroEventProof struct {
	Train         api.MetroTrain `json:"train"`
	CallID        string         `json:"call_id"`
	Before, After metroPoint
}

// metroRuntime observes every original publication before display coalescing.
// It never treats an approximate Hub trip assignment as journey identity.
type metroRuntime struct {
	mu sync.Mutex
	metroRuntimeTopology
	metroRuntimeTracks
	metroRuntimeJournal
	metroRuntimePublication
	recoveryMisses map[string]metroRecoveryMiss
	models         map[string]patterns.MetroQualifiedModel
	candidates     map[string]string
}

// Publication and its classified point batch describe one coherent source update.
type metroRuntimePublication struct {
	forecastCache      []api.MetroForecastContext
	forecastCacheUntil time.Time
	forecastTracks     map[string]metroForecastTrack
	forecastSequence   uint64
	publication        *MetroData
	batch              *metroPointBatch
}

type metroRuntimeTopology struct {
	hubError          string
	hubCorrections    []hubPosition
	operational       map[string]*metroOperationalMotion
	operationalAxes   map[string]metroOperationalAxisData
	priorCache        map[string][]metroVisitPrior
	plan              *StaticData
	topology          patterns.Topology
	stationsSignature string
}
type metroRuntimeTracks struct {
	tracks            map[string]*metroTrack
	active            map[string]string
	session           string
	sequence          uint64
	inventoryOverflow bool
}
type metroRuntimeJournal struct {
	capture            metroInputCapture
	lifecycleGroups    map[string]map[string]bool
	writer             sync.Mutex
	pending            map[string]patterns.MetroEventRecord
	pendingBytes       int
	historyDeliveryGap bool
	historyStatus      string
	archiveAvailable   bool
	dirty              map[string]patterns.MetroJourneyCheckpoint
	dirtyBytes         int
}

func newMetroRuntime() *metroRuntime {
	return &metroRuntime{
		recoveryMisses: map[string]metroRecoveryMiss{},
		models:         map[string]patterns.MetroQualifiedModel{}, candidates: map[string]string{},
		metroRuntimeTracks:  metroRuntimeTracks{tracks: map[string]*metroTrack{}, active: map[string]string{}, session: time.Now().UTC().Format(time.RFC3339Nano)},
		metroRuntimeJournal: metroRuntimeJournal{pending: map[string]patterns.MetroEventRecord{}, dirty: map[string]patterns.MetroJourneyCheckpoint{}, historyStatus: "unavailable"},
	}
}
func metroReference(v string) bool {
	return strings.TrimSpace(v) != "" && strings.TrimSpace(v) != "0" && len(v) <= 128 && !strings.Contains(v, "|")
}

type metroTrackIdentity struct{ route, direction, train string }

func metroTrackKey(i metroTrackIdentity) string { return i.route + "|" + i.direction + "|" + i.train }

func (r *metroRuntime) observe(data *MetroData, static *StaticData, history *patterns.Service, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() { data.InventoryOverflow = r.inventoryOverflow }()
	r.archiveAvailable = history != nil
	copy := *data
	r.publication = &copy
	r.forecastCacheUntil = time.Time{}
	r.inventoryOverflow = false
	if history != nil && r.historyStatus == "unavailable" {
		r.historyStatus = "pending"
	}
	if data.Status.Status != "ok" || static == nil || static.Schedule == nil {
		r.suspendAll("Fonte ou topologia indisponível")
		r.markCaptureGap()
		r.captureChanged(now)
		r.queueCurrentCheckpoints()
		data.Trains = r.current(now)
		data.Topology = r.topology
		return
	}
	r.observeSupported(data, static, now)
}
func (r *metroRuntime) observeSupported(data *MetroData, static *StaticData, now time.Time) {
	r.forecastCacheUntil = time.Time{}
	r.updateTopology(data, static)
	batch := collectMetroPoints(data, r.topology, now)
	r.batch = batch
	for _, key := range r.selectedContexts(batch, now) {
		points := batch.groups[key]
		track := r.admitTrack(metroTrackAdmission{key: key, batch: batch, data: data, static: static, now: now})
		if track != nil {
			r.applyPoints(track, points, now)
		}
	}
	r.observeSuccessorCandidates(batch, data, static, now)
	r.suspendAbsent(batch)
	r.prune(now)
	r.queueCurrentCheckpoints()
	r.captureChanged(now)
	data.Trains = r.current(now)
}

func (r *metroRuntime) updateTopology(data *MetroData, static *StaticData) {
	raw, _ := json.Marshal(data.Stations)
	signature := string(raw)
	if r.plan != static || r.stationsSignature != signature {
		r.plan = static
		r.priorCache = map[string][]metroVisitPrior{}
		r.operational = map[string]*metroOperationalMotion{}
		r.operationalAxes = map[string]metroOperationalAxisData{}
		r.stationsSignature = signature
		r.topology = metroTopology(data, static)
	}
	data.Topology = r.topology
}

// A shared axis may support downstream ordering without proving the origin.
func uniqueMetroPath(topology patterns.Topology, stop, direction string) (patterns.Pattern, bool) {
	paths := []patterns.Pattern{}
	var selected patterns.Pattern
	for _, path := range topology.Patterns {
		if path.Direction != direction || !metroPathContains(path, stop) {
			continue
		}
		paths = append(paths, path)
		if len(path.Stops) > len(selected.Stops) || len(path.Stops) == len(selected.Stops) && strings.Join(path.Stops, "|") < strings.Join(selected.Stops, "|") {
			selected = path
		}
	}
	if len(paths) == 0 {
		return patterns.Pattern{}, false
	}
	for _, path := range paths {
		seen := map[string]bool{}
		for _, code := range path.Stops {
			if seen[code] {
				return patterns.Pattern{}, false
			}
			seen[code] = true
		}
		if path.Route != selected.Route || metroPathEmbeddings(selected.Stops, path.Stops) != 1 {
			return patterns.Pattern{}, false
		}
	}
	return selected, true
}
func metroPathEmbeddings(axis, stops []string) int {
	count := 0
	for start := 0; start+len(stops) <= len(axis); start++ {
		if strings.Join(axis[start:start+len(stops)], "|") == strings.Join(stops, "|") {
			count++
		}
	}
	return count
}
func metroPathCalls(path patterns.Pattern, data *MetroData, static *StaticData, id string) []api.StopCall {
	calls := []api.StopCall{}
	stations := map[string]MetroStation{}
	for _, s := range data.Stations {
		stations[s.ID] = s
	}
	catalog := metroStopCatalog(static)
	for n, code := range path.Stops {
		stopID := metroPopupStationID(stations[code], catalog)
		calls = append(calls, api.StopCall{Id: id + ":" + strconv.Itoa(n), JourneyId: ptr(id), StopId: stopID, StopName: stations[code].Name, StopSequence: n, StopPlanId: optional(static.PlanID), StopStaticUpdatedAt: ptr(static.Updated), LineKey: path.Route, DirectionKey: ptr(path.Direction), Destination: stations[path.Destination].Name, Phase: "unknown", Arrival: missingCallTime("Sem dados de chegada"), Departure: missingCallTime("Sem dados de partida: modelo de movimento não calibrado")})
	}
	return calls
}
func suspendMetroTrack(t *metroTrack, reason string) {
	if t.BarrierRevision != 0 {
		return
	}
	t.Train.Association = "suspended"
	t.Train.Reason = reason
	t.Train.NextIndex = nil
	t.Train.CurrentIndex = nil
	for n := range t.Train.Calls {
		c := &t.Train.Calls[n]
		if c.Arrival.Kind == "prediction" {
			c.Arrival = missingCallTime("Sem previsão atual")
		}
		c.OwnPrediction = nil
		c.Phase = "unknown"
	}
	// No source transition may bridge a support gap.
	t.Points = map[string]metroPoint{}
	t.Train.ModelProjection = nil
	t.Movement = nil
	t.ModelStops = nil
	t.FixedModelSign = 0
	t.Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "unknown", Reason: "Continuidade de movimento interrompida: " + reason}
}
func (r *metroRuntime) suspendAll(reason string) {
	for _, id := range r.active {
		if t := r.tracks[id]; t != nil {
			suspendMetroTrack(t, reason)
		}
	}
}
func (r *metroRuntime) applyPoints(t *metroTrack, points []metroPoint, now time.Time) {
	if t.BarrierRevision != 0 || metroTrackClosed(t) {
		return
	}
	before := cloneMetroTrain(t.Train)
	clearMetroSourceGap(t, points)
	current, conflict := latestMetroPoints(points)
	conflict = conflict || conflictingMetroPoints(t, current, now)
	if !r.acceptMetroCurrent(t, current, conflict, now) {
		return
	}
	r.projectPoints(t, current, now)
	r.applyMetroMovement(t, current, now)
	r.projectMetroOperationalState(t, now)
	r.closeAtTerminal(t, before)
}
func (r *metroRuntime) acceptMetroCurrent(t *metroTrack, current map[string]metroPoint, conflict bool, now time.Time) bool {
	if conflict {
		retractMetroMovementCorrections(t, current)
		suspendMetroTrack(t, "Plataformas contraditórias")
		return false
	}
	r.retractArrivals(t, current)
	if metroRegressiveCurrent(t, current, now) {
		regressMetroTrack(t, now)
		return false
	}
	return true
}
func (r *metroRuntime) projectMetroOperationalState(t *metroTrack, now time.Time) {
	if t.Train.DirectionEvidence == nil || t.Train.DirectionEvidence.State != "confirmed" {
		r.operationalEvidence(t, now)
	}
	projectMetroScheduled(t, now)
	r.projectOperationalSegment(t, now)
	r.projectMetroExperimentalDepartures(t, now)
}

func clearMetroSourceGap(t *metroTrack, points []metroPoint) {
	latest := t.Train.SourceUpdatedAt
	for _, p := range points {
		if p.Clock.After(latest) {
			latest = p.Clock
		}
	}
	if !t.Train.SourceUpdatedAt.IsZero() && latest.Sub(t.Train.SourceUpdatedAt) > 60*time.Second {
		suspendMetroTrack(t, "Intervalo entre publicações superior a 60 segundos; continuidade por confirmar")
	}
}
func equalMetroSeconds(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// The cache is smaller than retained history. Eviction never claims durable TTL coverage.

// Bind existing own forecasts through shared transition evidence; no second forecast engine is introduced.

// A short-turn path belongs to the unique containing ordered direction, without borrowing its destination.
func canonicalMetroDirection(topology patterns.Topology, path patterns.Pattern) string {
	longest := len(path.Stops)
	directions := map[string]bool{}
	for _, candidate := range topology.Patterns {
		if candidate.Route != path.Route || len(candidate.Stops) < longest {
			continue
		}
		contains := false
		for start := 0; start+len(path.Stops) <= len(candidate.Stops); start++ {
			if strings.Join(candidate.Stops[start:start+len(path.Stops)], "|") == strings.Join(path.Stops, "|") {
				contains = true
				break
			}
		}
		if !contains {
			continue
		}
		if len(candidate.Stops) > longest {
			longest = len(candidate.Stops)
			directions = map[string]bool{}
		}
		directions[candidate.Direction] = true
	}
	if len(directions) == 1 {
		for direction := range directions {
			return direction
		}
	}
	return path.Direction
}
