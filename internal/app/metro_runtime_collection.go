package app

import (
	"encoding/json"
	"lisboapublica/internal/patterns"
	"strings"
	"time"
)

type metroPointBatch struct {
	provenance          map[string]metroSourceSlot
	groups              map[string][]metroPoint
	paths               map[string]patterns.Pattern
	references          map[string]map[string]bool
	rejected            map[string]bool
	localOnly           []metroLocalForecast
	topologyUnavailable bool
}

type metroSourceSlot struct {
	Revision string
	Slot     int
}

type metroLocalForecast struct {
	Row       MetroWait
	Reference string
	Clock     time.Time
	Seconds   int
	Slot      int
	Valid     bool
}

func collectMetroPoints(data *MetroData, topology patterns.Topology, static *StaticData, now time.Time) *metroPointBatch {
	b := &metroPointBatch{provenance: map[string]metroSourceSlot{}, groups: map[string][]metroPoint{}, paths: map[string]patterns.Pattern{}, references: map[string]map[string]bool{}, rejected: map[string]bool{}}
	resolver, routeIDs := newMetroDestinationResolver(data, topology), metroRouteIDs(static)
	for _, row := range data.Waits {
		b.collectRow(row, topology, resolver, routeIDs, now)
	}
	return b
}
func metroWaitRowKey(row MetroWait) string {
	return row.Stop + "|" + row.Platform + "|" + row.Destination
}
func (b *metroPointBatch) collectRow(row MetroWait, topology patterns.Topology, resolver *metroDestinationResolver, routeIDs map[string]string, now time.Time) {
	clock, err := time.ParseInLocation("20060102150405", row.At, lisbon)
	if err != nil || now.Sub(clock) > sourceFreshness || clock.After(now.Add(providerClockSkew)) {
		return
	}
	path, pathOK := uniqueMetroPath(topology, row.Stop, row.Destination)
	if pathOK && resolver.admittedRoute(row, routeIDs) != path.Route {
		pathOK = false
	}
	values := []json.RawMessage{row.Wait1, row.Wait2, row.Wait3}
	refs := []string{row.Train, row.Train2, row.Train3}
	counts := map[string]int{}
	for _, v := range refs {
		counts[strings.TrimSpace(v)]++
	}
	for n, ref := range refs {
		if !pathOK || !metroReference(strings.TrimSpace(ref)) {
			b.collectLocalForecast(row, ref, values[n], clock, n)
			continue
		}
		b.collectReference(metroRowReference{row: row, path: path, clock: clock, train: strings.TrimSpace(ref), wait: values[n], counts: counts, slot: n})
	}
}

func (b *metroPointBatch) collectLocalForecast(row MetroWait, ref string, wait json.RawMessage, clock time.Time, slot int) {
	ref = strings.TrimSpace(ref)
	seconds, valid := metroWaitSeconds(wait)
	if !metroReference(ref) && ref != "" && ref != "0" {
		return
	}
	if !metroReference(ref) {
		ref = ""
	}
	b.localOnly = append(b.localOnly, metroLocalForecast{Row: row, Reference: ref, Clock: clock, Seconds: seconds, Slot: slot, Valid: valid})
}

type metroRowReference struct {
	row    MetroWait
	path   patterns.Pattern
	clock  time.Time
	train  string
	wait   json.RawMessage
	slot   int
	counts map[string]int
}

func (b *metroPointBatch) collectReference(v metroRowReference) {
	if !metroReference(v.train) {
		return
	}
	key := metroTrackKey(metroTrackIdentity{v.path.Route, v.row.Destination, v.train})
	b.collectReferencePath(key, v)
	seconds, valid := metroWaitSeconds(v.wait)
	var eta *int
	if valid {
		eta = ptr(seconds)
	}
	point := metroPoint{v.row.Stop, v.row.Platform, v.clock, eta}
	b.groups[key] = append(b.groups[key], point)
	if eta != nil {
		b.collectReferenceProvenance(key, point, v)
	}
}
func (b *metroPointBatch) collectReferencePath(key string, v metroRowReference) {
	scope := v.path.Route + "|" + v.train
	if b.references[scope] == nil {
		b.references[scope] = map[string]bool{}
	}
	b.references[scope][key] = true
	b.paths[key] = v.path
	if v.counts[v.train] != 1 {
		b.rejected[key] = true
	}
}
func (b *metroPointBatch) collectReferenceProvenance(key string, point metroPoint, v metroRowReference) {
	id := metroForecastPointID(key, point)
	source := metroSourceSlot{metroWaitRevision(v.row), v.slot + 1}
	old, exists := b.provenance[id]
	if !exists || source.Revision < old.Revision {
		b.provenance[id] = source
	}
}
