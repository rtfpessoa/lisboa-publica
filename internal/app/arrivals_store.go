package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"lisboapublica/internal/api"
)

const (
	arrivalCandidateLimit    = 512
	arrivalRetainedRowsLimit = 1024
	arrivalRetainedNameBytes = 1024
	arrivalSourceURLBytes    = 2048
	arrivalSnapshotOverhead  = 2048
	arrivalRowOverhead       = 512
	arrivalEncodedSlack      = 3
	arrivalSelectorOverhead  = 256
	arrivalOperatorOverhead  = 16
	arrivalResultLimit       = 64
	arrivalCMMinuteLimit     = 48
	arrivalCMRequestLimit    = 2
	arrivalForecastHorizon   = 24 * time.Hour
	arrivalLoadingLifetime   = 5 * time.Second
	arrivalEarliestTimestamp = 946684800 // 2000-01-01 UTC; reject milliseconds and invalid epochs.
	arrivalCMTripBytes       = 256

	arrivalDemandLimit  = 32 // per source, no neighbour prefetch
	arrivalInterest     = 30 * time.Second
	arrivalRowsLimit    = 256
	arrivalEncodedLimit = 256 * 1024
	arrivalBodyLimit    = 512 * 1024
	arrivalResultBudget = 1 * 1024 * 1024
	arrivalTMLBudget    = 3 * 1024 * 1024
	arrivalCMBudget     = 1 * 1024 * 1024
	arrivalMappingLimit = 4096
)

type arrivalSnapshot struct {
	rows         []api.Arrival
	availability api.ArrivalAvailability
	static       *StaticData
	expires      time.Time
	bytes        int
}
type arrivalDemand struct {
	static              *StaticData
	interest, attempted time.Time
	busy                bool
}
type arrivalResult struct {
	snapshot arrivalSnapshot
	filter   Filter
	created  time.Time
}

// Independent bounded retention: the 64 network revisions never own ETA rows.
type arrivalStore struct {
	mu         sync.Mutex
	demands    map[string]*arrivalDemand
	latest     map[string]arrivalSnapshot
	results    map[string]arrivalResult
	sequence   uint64
	epoch      string
	entropyErr error
	cmAttempts []time.Time
}

func newArrivalStore() *arrivalStore {
	epoch, err := randomSecret()
	return &arrivalStore{epoch: epoch, entropyErr: err, demands: map[string]*arrivalDemand{}, latest: map[string]arrivalSnapshot{}, results: map[string]arrivalResult{}}
}
func arrivalSource(stop string) string {
	if strings.HasPrefix(stop, "cm:") {
		return cmBase + "/arrivals/by_stop/" + strings.TrimPrefix(stop, "cm:")
	}
	return cpSourceURL
}
func (s *arrivalStore) prune(now time.Time) {
	for k, d := range s.demands {
		if !d.busy && now.After(d.interest) {
			delete(s.demands, k)
			delete(s.latest, k)
		}
	}
	for k, r := range s.results {
		if !now.Before(r.snapshot.expires) {
			delete(s.results, k)
		}
	}
}
func (s *arrivalStore) request(stop string, static *StaticData, now time.Time) arrivalSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	d := s.demands[stop]
	if d == nil {
		if s.sourceDemandCount(stop) >= arrivalDemandLimit {
			return unavailableArrivalDemand(static, stop, now)
		}
		d = &arrivalDemand{static: static}
		s.demands[stop] = d
	}
	if d.static != static {
		d.static = static
		delete(s.latest, stop)
	}
	d.interest = now.Add(arrivalInterest)
	if v, ok := s.latest[stop]; ok && v.static == static && now.Before(v.expires) {
		return v
	}
	return arrivalSnapshot{static: static, availability: api.ArrivalAvailability{Status: "loading", PlannedStatus: "unavailable", Message: "A carregar próximas passagens…", SourceUrl: arrivalSource(stop)}, expires: now.Add(arrivalLoadingLifetime)}
}

func (s *arrivalStore) sourceDemandCount(stop string) int {
	count := 0
	cm := strings.HasPrefix(stop, "cm:")
	for k := range s.demands {
		if strings.HasPrefix(k, "cm:") == cm {
			count++
		}
	}
	return count
}

func unavailableArrivalDemand(static *StaticData, stop string, now time.Time) arrivalSnapshot {
	return arrivalSnapshot{static: static, availability: api.ArrivalAvailability{Status: "partial", PlannedStatus: "unavailable", Message: "Previsões indisponíveis; mostramos horários planeados.", SourceUrl: arrivalSource(stop)}, expires: now.Add(arrivalLoadingLifetime)}
}
func (s *arrivalStore) wanted(cm bool, now time.Time) map[string]*StaticData {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	out := map[string]*StaticData{}
	for k, d := range s.demands {
		if strings.HasPrefix(k, "cm:") == cm && now.Before(d.interest) {
			out[k] = d.static
		}
	}
	return out
}
func arrivalBytes(rows []api.Arrival) int {
	// Account for structs, backing arrays, pointers, strings and encoded-size slack.
	if len(rows) > arrivalRetainedRowsLimit {
		return arrivalResultBudget + 1
	}
	bytes := arrivalSnapshotOverhead
	for _, r := range rows {
		if len(r.Id) > arrivalRetainedNameBytes || len(r.Headsign) > arrivalRetainedNameBytes || len(r.SourceUrl) > arrivalSourceURLBytes || (r.RouteName != nil && len(*r.RouteName) > arrivalRetainedNameBytes) {
			return arrivalResultBudget + 1
		}
		b, _ := json.Marshal(r)
		bytes += arrivalRowOverhead + len(b)*arrivalEncodedSlack
	}
	return bytes
}
func (s *arrivalStore) publish(stop string, v arrivalSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.demands[stop]
	if d == nil || d.static != v.static {
		return
	}
	v = boundedArrivalSnapshot(v)
	cm := strings.HasPrefix(stop, "cm:")
	budget := arrivalTMLBudget
	if cm {
		budget = arrivalCMBudget
	}
	if s.retainedSourceBytes(stop, cm)+v.bytes > budget {
		v.rows = nil
		v.bytes = arrivalSnapshotOverhead
		markArrivalPartial(&v)
	}
	s.latest[stop] = v
}

func boundedArrivalSnapshot(v arrivalSnapshot) arrivalSnapshot {
	encoded, kept := 2, 0
	for _, row := range v.rows {
		b, _ := json.Marshal(row)
		if encoded+len(b)+1 > arrivalEncodedLimit {
			break
		}
		encoded += len(b) + 1
		kept++
	}
	if kept < len(v.rows) {
		v.rows = append([]api.Arrival(nil), v.rows[:kept]...)
		markArrivalPartial(&v)
	}
	v.bytes = arrivalBytes(v.rows)
	return v
}

func markArrivalPartial(v *arrivalSnapshot) {
	v.availability.Status = "partial"
	v.availability.Message = "Dados publicados incompletos para este intervalo."
}

func (s *arrivalStore) retainedSourceBytes(stop string, cm bool) int {
	used := 0
	for k, r := range s.latest {
		if k != stop && strings.HasPrefix(k, "cm:") == cm {
			used += r.bytes
		}
	}
	return used
}

func (s *arrivalStore) claimCM(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	s.trimCMAttempts(now)
	keys := s.readyCMDemands(now)
	n := min(len(keys), max(0, min(s.availableCMRequests(), arrivalCMMinuteLimit-len(s.cmAttempts))))
	keys = keys[:n]
	for _, k := range keys {
		d := s.demands[k]
		d.busy = true
		d.attempted = now
		s.cmAttempts = append(s.cmAttempts, now)
	}
	return keys
}

func (s *arrivalStore) trimCMAttempts(now time.Time) {
	first := 0
	for first < len(s.cmAttempts) && now.Sub(s.cmAttempts[first]) >= time.Minute {
		first++
	}
	s.cmAttempts = append([]time.Time(nil), s.cmAttempts[first:]...)
}

func (s *arrivalStore) readyCMDemands(now time.Time) []string {
	keys := []string{}
	for k, d := range s.demands {
		if strings.HasPrefix(k, "cm:") && !d.busy && now.Before(d.interest) && now.Sub(d.attempted) >= providerRefreshInterval {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return s.demands[keys[i]].attempted.Before(s.demands[keys[j]].attempted) })
	return keys
}

func (s *arrivalStore) availableCMRequests() int {
	available := arrivalCMRequestLimit
	for _, d := range s.demands {
		if d.busy {
			available--
		}
	}
	return available
}
func (s *arrivalStore) releaseCM(stop string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.demands[stop]; d != nil {
		d.busy = false
	}
}
func (s *arrivalStore) pin(f Filter, v arrivalSnapshot, now time.Time) (string, error) {
	if s.entropyErr != nil {
		return "", fail(http.StatusServiceUnavailable, "revision_unavailable", "Coleção temporariamente indisponível.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	v.bytes = arrivalResultBytes(v.rows, f)
	if v.bytes > arrivalResultBudget {
		return "", readResultLimit()
	}
	s.makeResultRoom(v.bytes)
	s.sequence++
	token := fmt.Sprintf("a:%s-%d:%d:%d", s.epoch, s.sequence, f.From.UnixNano(), f.To.UnixNano())
	s.results[token] = arrivalResult{snapshot: v, filter: f, created: now}
	return token, nil
}
func arrivalResultBytes(rows []api.Arrival, f Filter) int {
	bytes := arrivalBytes(rows) + arrivalSelectorOverhead + len(f.Stop) + len(f.Route) + len(f.Q) + len(f.Sort) + len(f.Revision)
	for _, id := range f.Operators {
		bytes += len(id) + arrivalOperatorOverhead
	}
	return bytes
}

func (s *arrivalStore) resultUsage() (int, string) {
	used := 0
	oldest := ""
	var at time.Time
	for k, r := range s.results {
		used += r.snapshot.bytes
		if oldest == "" || r.created.Before(at) {
			oldest, at = k, r.created
		}
	}
	return used, oldest
}

func (s *arrivalStore) makeResultRoom(bytes int) {
	for {
		used, oldest := s.resultUsage()
		if used+bytes <= arrivalResultBudget && len(s.results) < arrivalResultLimit {
			return
		}
		delete(s.results, oldest)
	}
}

func (s *arrivalStore) page(f Filter, now time.Time) (arrivalSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	r, ok := s.results[f.Revision]
	if !ok {
		return arrivalSnapshot{}, fail(http.StatusGone, "revision_expired", "A coleção mudou. Volte a carregar a primeira página.")
	}
	if f.Stop != r.filter.Stop || f.Route != r.filter.Route || strings.Join(f.Operators, ",") != strings.Join(r.filter.Operators, ",") || !f.From.Equal(r.filter.From) || !f.To.Equal(r.filter.To) {
		return arrivalSnapshot{}, fail(http.StatusBadRequest, "revision", "A seleção mudou entre páginas.")
	}
	return r.snapshot, nil
}

func (s *arrivalStore) previous(stop string, static *StaticData, now time.Time) (arrivalSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.latest[stop]
	return v, ok && v.static == static && now.Before(v.expires)
}
