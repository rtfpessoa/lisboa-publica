package app

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"lisboapublica/internal/api"
)

const hubBase = "https://go.tmlmobilidade.pt/hub/api/v1"
const cmBase = "https://api.carrismetropolitana.pt/v2"

var lisbon, _ = time.LoadLocation("Europe/Lisbon")

type provider struct{ ID, Name, Agency, Code, Color, Mode, Note string }

var providers = []provider{
	{"carris", "Carris", "IA9T6", "1", "#f5b800", "bus", "Posições reportadas. Velocidade amostral; dados de frota podem estar indisponíveis."},
	{"cm", "Carris Metropolitana", "", "", "#f2c600", "bus", "API oficial v2. Campos de modelo e matrícula podem estar indisponíveis."},
	{"tcb", "TCB", "A3H3M", "8", "#72b739", "bus", "Posições e horários publicados pelo operador através da TML."},
	{"mobi", "MobiCascais", "HF16N", "21", "#46896e", "bus", "Metadados de veículos publicados pela TML; inventário completo não garantido."},
	{"metro", "Metro de Lisboa", "IA2N9", "2", "#ec493a", "metro", "Posições estimadas a partir de tempos de espera e horários. Não são GPS; excluídas dos cálculos de velocidade e distância."},
	{"cp", "CP", "N18KL", "3", "#278044", "train", "Serviços na área de Lisboa. Posições reportadas podem ter lacunas; horários são planeados."},
	{"ttsl", "TTSL", "LTP61", "4", "#388aca", "ferry", "GTFS através da TML. O feed direto apresentou certificado TLS expirado; a verificação TLS mantém-se ativa."},
	{"fertagus", "Fertagus", "7NTB1", "15", "#236caa", "train", "Posições reportadas e horários. Modelo e matrícula só quando existe correspondência verificada."},
}

func providerByID(id string) (provider, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}
	return provider{}, false
}
func qualify(p, id string) string { return p + ":" + id }
func verifiedHubID(raw, agency string) string {
	prefix := "[" + agency + "]"
	if strings.HasPrefix(raw, prefix) && !strings.HasPrefix(strings.TrimPrefix(raw, prefix), "[") {
		return strings.TrimPrefix(raw, prefix)
	}
	return raw
}
func verifiedHubTrip(raw, agency, activePlan string) (string, *string) {
	if !strings.HasPrefix(raw, "[") {
		return raw, nil
	}
	i := strings.Index(raw, "]")
	if i < 0 {
		return raw, nil
	}
	plan := raw[1:i]
	if activePlan != "" && plan == activePlan {
		rest := raw[i+1:]
		normalized := verifiedHubID(rest, agency)
		if normalized != rest {
			return normalized, optional(plan)
		}
	}
	return raw, optional(plan)
}
func ptr[T any](v T) *T { return &v }
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// StaticData contains one operator’s normalized published routes, stops and schedule.
type StaticData struct {
	Routes     []api.RouteDetail   `json:"routes"`
	Stops      []api.Stop          `json:"stops"`
	Schedule   *Schedule           `json:"schedule,omitempty"`
	Models     map[string]Metadata `json:"models,omitempty"`
	PlanID     string              `json:"plan_id"`
	ValidFrom  string              `json:"valid_from"`
	ValidUntil string              `json:"valid_until"`
	Source     string              `json:"source"`
	Updated    time.Time           `json:"updated"`
}

// Metadata contains published vehicle model and registration fields.
type Metadata struct{ Model, Plate string }

// LiveData holds provider positions and their collection time.
type LiveData struct {
	Vehicles  []api.Vehicle `json:"vehicles"`
	Collected time.Time     `json:"collected"`
}

// State is an immutable collection version shared by paginated readers.
type State struct {
	Metro     *MetroData
	Revision  string
	Created   time.Time
	Static    map[string]*StaticData
	Live      map[string]*LiveData
	Operators map[string]api.Operator
}

// Cache publishes immutable states and retains recent versions for pagination.
type Cache struct {
	mu       sync.RWMutex
	current  *State
	versions map[string]*State
	sequence uint64
}

// NewCache creates an empty collection with all eight providers visible.
func NewCache() *Cache {
	c := &Cache{versions: map[string]*State{}}
	s := &State{Static: map[string]*StaticData{}, Live: map[string]*LiveData{}, Operators: map[string]api.Operator{}}
	for _, p := range providers {
		source := hubBase + "/plans"
		live := hubBase + "/vehicles/positions"
		if p.ID == "cm" {
			source = cmBase + "/lines"
			live = cmBase + "/vehicles"
		}
		s.Operators[p.ID] = api.Operator{Id: p.ID, Name: p.Name, Color: p.Color, Mode: api.OperatorMode(p.Mode), StaticSource: source, LiveSource: live, Status: api.OperatorStatusLoading, StaticStatus: api.OperatorStaticStatusLoading, Note: p.Note}
	}
	c.publish(s)
	return c
}
func (c *Cache) publish(s *State) {
	c.sequence++
	s.Revision = time.Now().UTC().Format("20060102T150405.000000000") + "-" + stringID(c.sequence)
	s.Created = time.Now()
	c.current = s
	c.versions[s.Revision] = s
	for r, v := range c.versions {
		if time.Since(v.Created) > staticRefreshInterval {
			delete(c.versions, r)
		}
	}
	if len(c.versions) > maxCachedVersions {
		var oldest *State
		for _, v := range c.versions {
			if oldest == nil || v.Created.Before(oldest.Created) {
				oldest = v
			}
		}
		delete(c.versions, oldest.Revision)
	}
}
func (c *Cache) update(id string, static *StaticData, live *LiveData, op api.Operator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.current
	value := &State{Metro: old.Metro, Static: map[string]*StaticData{}, Live: map[string]*LiveData{}, Operators: map[string]api.Operator{}}
	for k, v := range old.Static {
		value.Static[k] = v
	}
	for k, v := range old.Live {
		value.Live[k] = v
	}
	for k, v := range old.Operators {
		value.Operators[k] = v
	}
	if static != nil {
		value.Static[id] = static
	}
	if live != nil {
		value.Live[id] = live
	}
	value.Operators[id] = op
	c.publish(value)
}
func (c *Cache) state(revision string) (*State, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if revision == "" {
		return c.current, nil
	}
	s := c.versions[revision]
	if s == nil || time.Since(s.Created) > staticRefreshInterval {
		return nil, fail(http.StatusGone, "revision_expired", "A coleção mudou. Volte a carregar a primeira página.")
	}
	return s, nil
}
func (c *Cache) operator(id string) api.Operator {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current.Operators[id]
}
func stringID(v uint64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := 20
	for v > 0 {
		i--
		b[i] = digits[v%10]
		v /= 10
	}
	return string(b[i:])
}
func validPosition(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && !math.IsInf(lat, 0) && !math.IsInf(lon, 0) && lat >= lisbonSouthLatitude && lat <= lisbonNorthLatitude && lon >= -lisbonWestLongitude && lon <= -lisbonEastLongitude
}
func sampledDistance(a, b api.Vehicle) (*float64, *float64) {
	seconds := b.ObservedAt.Sub(a.ObservedAt).Seconds()
	if a.PositionKind != "reported" || b.PositionKind != "reported" || seconds < 5 || seconds > 180 {
		return nil, nil
	}
	r := math.Pi / 180
	dlat := (b.Lat - a.Lat) * r
	dlon := (b.Lon - a.Lon) * r
	h := math.Pow(math.Sin(dlat/2), 2) + math.Cos(a.Lat*r)*math.Cos(b.Lat*r)*math.Pow(math.Sin(dlon/2), 2)
	distance := earthRadiusKm * 2 * math.Asin(math.Min(1, math.Sqrt(h)))
	speed := distance / seconds * secondsPerHour
	if speed > maxSampledSpeedKmh {
		return nil, nil
	}
	return &distance, &speed
}
func sortVehicles(v []api.Vehicle) { sort.Slice(v, func(i, j int) bool { return v[i].Id < v[j].Id }) }
