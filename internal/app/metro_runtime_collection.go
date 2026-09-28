package app

import (
	"encoding/json"
	"lisboapublica/internal/patterns"
	"strings"
	"time"
)

type metroPointBatch struct {
	groups     map[string][]metroPoint
	paths      map[string]patterns.Pattern
	references map[string]map[string]bool
	rowCounts  map[string]int
	rejected   map[string]bool
	localOnly  []metroLocalForecast
}

type metroLocalForecast struct {
	Row       MetroWait
	Reference string
	Clock     time.Time
	Seconds   int
}

func collectMetroPoints(data *MetroData, topology patterns.Topology, now time.Time) *metroPointBatch {
	b := &metroPointBatch{groups: map[string][]metroPoint{}, paths: map[string]patterns.Pattern{}, references: map[string]map[string]bool{}, rowCounts: map[string]int{}, rejected: map[string]bool{}}
	for _, row := range data.Waits {
		b.rowCounts[metroWaitRowKey(row)]++
	}
	for _, row := range data.Waits {
		b.collectRow(row, topology, now)
	}
	return b
}
func metroWaitRowKey(row MetroWait) string {
	return row.Stop + "|" + row.Platform + "|" + row.Destination
}
func (b *metroPointBatch) collectRow(row MetroWait, topology patterns.Topology, now time.Time) {
	clock, err := time.ParseInLocation("20060102150405", row.At, lisbon)
	if err != nil || now.Sub(clock) > sourceFreshness || clock.After(now.Add(providerClockSkew)) {
		return
	}
	path, pathOK := uniqueMetroPath(topology, row.Stop, row.Destination)
	values := []json.RawMessage{row.Wait1, row.Wait2, row.Wait3}
	refs := []string{row.Train, row.Train2, row.Train3}
	counts := map[string]int{}
	for _, v := range refs {
		counts[strings.TrimSpace(v)]++
	}
	for n, ref := range refs {
		if !pathOK {
			ref = strings.TrimSpace(ref)
			seconds, valid := metroWaitSeconds(values[n])
			if metroReference(ref) && valid && counts[ref] == 1 && b.rowCounts[metroWaitRowKey(row)] == 1 {
				b.localOnly = append(b.localOnly, metroLocalForecast{row, ref, clock, seconds})
			}
			continue
		}
		b.collectReference(metroRowReference{row: row, path: path, clock: clock, train: strings.TrimSpace(ref), wait: values[n], counts: counts})
	}
}

type metroRowReference struct {
	row    MetroWait
	path   patterns.Pattern
	clock  time.Time
	train  string
	wait   json.RawMessage
	counts map[string]int
}

func (b *metroPointBatch) collectReference(v metroRowReference) {
	if !metroReference(v.train) {
		return
	}
	key := metroTrackKey(metroTrackIdentity{v.path.Route, v.row.Destination, v.train})
	scope := v.path.Route + "|" + v.train
	if b.references[scope] == nil {
		b.references[scope] = map[string]bool{}
	}
	b.references[scope][key] = true
	b.paths[key] = v.path
	if b.rowCounts[metroWaitRowKey(v.row)] != 1 || v.counts[v.train] != 1 {
		b.rejected[key] = true
		return
	}
	seconds, valid := metroWaitSeconds(v.wait)
	var eta *int
	if valid {
		eta = ptr(seconds)
	}
	b.groups[key] = append(b.groups[key], metroPoint{v.row.Stop, v.row.Platform, v.clock, eta})
}
