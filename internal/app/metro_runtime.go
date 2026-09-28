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
	ProviderDirection string
	Train             api.MetroTrain
	Profile           string
	Codes             []string
	Points            map[string]metroPoint
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
}
type metroRuntimeTopology struct {
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
	pending          map[string]patterns.MetroEventRecord
	pendingBytes     int
	historyStatus    string
	archiveAvailable bool
}

func newMetroRuntime() *metroRuntime {
	return &metroRuntime{
		metroRuntimeTracks:  metroRuntimeTracks{tracks: map[string]*metroTrack{}, active: map[string]string{}, session: time.Now().UTC().Format(time.RFC3339Nano)},
		metroRuntimeJournal: metroRuntimeJournal{pending: map[string]patterns.MetroEventRecord{}, historyStatus: "unavailable"},
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
	r.inventoryOverflow = false
	if history != nil && r.historyStatus == "unavailable" {
		r.historyStatus = "pending"
	}
	if data.Status.Status != "ok" || static == nil || static.Schedule == nil {
		r.suspendAll("Fonte ou topologia indisponível")
		data.Trains = r.current(now)
		data.Topology = r.topology
		return
	}
	r.updateTopology(data, static)
	batch := collectMetroPoints(data, r.topology, now)
	for key, points := range batch.groups {
		track := r.admitTrack(metroTrackAdmission{key: key, batch: batch, data: data, static: static, now: now})
		if track != nil {
			r.applyPoints(track, points, now)
		}
	}
	r.suspendAbsent(batch)
	r.prune(now)
	data.Trains = r.current(now)
}
func (r *metroRuntime) updateTopology(data *MetroData, static *StaticData) {
	raw, _ := json.Marshal(data.Stations)
	signature := string(raw)
	if r.plan != static || r.stationsSignature != signature {
		r.plan = static
		r.stationsSignature = signature
		r.topology = metroTopology(data, static)
	}
	data.Topology = r.topology
}
func uniqueMetroPath(topology patterns.Topology, stop, direction string) (patterns.Pattern, bool) {
	var selected *patterns.Pattern
	for _, path := range topology.Patterns {
		if path.Direction != direction || !metroPathContains(path, stop) {
			continue
		}
		if selected != nil {
			if selected.Route != path.Route {
				return patterns.Pattern{}, false
			}
			// Different stop orders are ambiguous, even if an approximate trip ID matches.
			if strings.Join(selected.Stops, "|") != strings.Join(path.Stops, "|") {
				return patterns.Pattern{}, false
			}
		} else {
			copy := path
			selected = &copy
		}
	}
	if selected == nil {
		return patterns.Pattern{}, false
	}
	return *selected, true
}
func metroPathCalls(path patterns.Pattern, data *MetroData, static *StaticData, id string) []api.StopCall {
	calls := []api.StopCall{}
	stations := map[string]MetroStation{}
	for _, s := range data.Stations {
		stations[s.ID] = s
	}
	for n, code := range path.Stops {
		stopID := "metro:" + code
		candidates := []api.Stop{}
		for _, s := range static.Stops {
			if metroTargetMatches(ptr(stations[code]), &s) {
				candidates = append(candidates, s)
			}
		}
		if len(candidates) == 1 {
			stopID = candidates[0].Id
		}
		calls = append(calls, api.StopCall{Id: id + ":" + strconv.Itoa(n), JourneyId: ptr(id), StopId: stopID, StopName: stations[code].Name, StopSequence: n, StopPlanId: optional(static.PlanID), StopStaticUpdatedAt: ptr(static.Updated), LineKey: path.Route, DirectionKey: ptr(path.Direction), Destination: stations[path.Destination].Name, Phase: "unknown", Arrival: missingCallTime("Sem dados de chegada"), Departure: missingCallTime("Sem dados de partida: modelo de movimento não calibrado")})
	}
	return calls
}
func suspendMetroTrack(t *metroTrack, reason string) {
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
}
func (r *metroRuntime) suspendAll(reason string) {
	for _, id := range r.active {
		if t := r.tracks[id]; t != nil {
			suspendMetroTrack(t, reason)
		}
	}
}
func (r *metroRuntime) applyPoints(t *metroTrack, points []metroPoint, now time.Time) {
	current, conflict := latestMetroPoints(points)
	conflict = conflict || conflictingMetroPoints(t, current, now)
	if !conflict {
		r.retractArrivals(t, current)
	}
	if !conflict && metroRegressiveIndex(t.Train.NextIndex, firstMetroCurrent(t, current, now)) {
		regressMetroTrack(t, now)
		return
	}
	if conflict {
		suspendMetroTrack(t, "Plataformas contraditórias")
		return
	}
	r.projectPoints(t, current, now)
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
