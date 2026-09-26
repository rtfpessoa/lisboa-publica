package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

// Fetcher fetches official provider feeds and publishes normalized snapshots.
type Fetcher struct {
	Client  *http.Client
	Store   *Store
	Cache   *Cache
	Log     *zap.Logger
	Hub, CM string
	mu      sync.Mutex
	etag    map[string]string
	blobs   map[string][]byte
}

// NewFetcher creates a provider fetcher with bounded HTTP requests.
func NewFetcher(s *Store, c *Cache, log *zap.Logger) *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: upstreamTimeout}, Store: s, Cache: c, Log: log, Hub: hubBase, CM: cmBase, etag: map[string]string{}, blobs: map[string][]byte{}}
}
func (f *Fetcher) fetch(ctx context.Context, u string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LisboaPublica/1.0 (independent transit dashboard)")
	f.mu.Lock()
	tag := f.etag[u]
	f.mu.Unlock()
	if tag != "" {
		req.Header.Set("If-None-Match", tag)
	}
	res, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		f.mu.Lock()
		right := f.blobs[u]
		f.mu.Unlock()
		if right == nil {
			return nil, fmt.Errorf("304 without cached body")
		}
		return right, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream HTTP%d", res.StatusCode)
	}
	right, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(right)) > max {
		return nil, fmt.Errorf("upstream response exceeds size limit")
	}
	if tag = res.Header.Get("ETag"); tag != "" {
		f.mu.Lock()
		if len(f.blobs) < maxConditionalBodies {
			f.etag[u] = tag
			f.blobs[u] = right
		}
		f.mu.Unlock()
	}
	return right, nil
}
func (f *Fetcher) fetchJSON(ctx context.Context, u string, dst any) error {
	blob, e := f.fetch(ctx, u, providerJSONBytes)
	if e != nil {
		return e
	}
	return json.Unmarshal(blob, dst)
}

type hubPosition struct {
	Agency    string   `json:"agency_id"`
	ID        string   `json:"vehicle_id"`
	Route     string   `json:"route_id"`
	RouteName string   `json:"route_short_name"`
	Trip      string   `json:"trip_id"`
	Lat       float64  `json:"latitude"`
	Lon       float64  `json:"longitude"`
	At        int64    `json:"created_at"`
	Bearing   *float64 `json:"bearing"`
	Plate     *string  `json:"license_plate"`
}
type hubPlan struct {
	ID     string `json:"_id"`
	Agency string `json:"agency_id"`
	Active bool   `json:"is_active"`
	From   int    `json:"active_from"`
	Until  int    `json:"active_until"`
	URL    string `json:"operation_gtfs_normalized_url"`
}

// Run refreshes provider data until its context is cancelled.
func (f *Fetcher) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); f.staticLoop(ctx) }()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(providerRefreshInterval)
		defer ticker.Stop()
		prune := time.NewTicker(time.Hour)
		defer prune.Stop()
		f.refreshLive(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.refreshLive(ctx)
			case <-prune.C:
				if e := f.Store.prune(ctx); e != nil {
					f.Log.Warn("retention cleanup failed", zap.Error(e))
				}
			}
		}
	}()
	wg.Wait()
}
func (f *Fetcher) staticLoop(ctx context.Context) {
	f.refreshStatic(ctx)
	timer := time.NewTimer(staticRefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			f.refreshStatic(ctx)
			timer.Reset(staticRefreshInterval)
		}
	}
}
func (f *Fetcher) markError(ctx context.Context, p provider, static bool, err error) {
	f.Store.PublishMu.Lock()
	defer f.Store.PublishMu.Unlock()
	op := f.Cache.operator(p.ID)
	message := err.Error()
	if strings.Contains(message, "objectstorage.") {
		message = "Falha ao obter GTFS no armazenamento oficial; os dados anteriores mantêm-se."
	}
	if static {
		op.StaticStatus = api.OperatorStaticStatusError
		op.StaticError = ptr(message)
	} else {
		op.Status = api.OperatorStatusError
		op.Error = ptr(message)
	}
	if e := f.Store.Save(ctx, p.ID, nil, nil, op, nil); e == nil {
		f.Cache.update(p.ID, nil, nil, op)
	}
	f.Log.Warn("provider refresh failed", zap.String("operator", p.ID), zap.Bool("static", static), zap.String("reason", message))
}
func (f *Fetcher) refreshStatic(ctx context.Context) {
	defer f.refreshMetadata(ctx)
	var plans struct {
		Data  []hubPlan `json:"data"`
		Error any       `json:"error"`
	}
	err := f.fetchJSON(ctx, f.Hub+"/plans", &plans)
	if err == nil && plans.Error != nil {
		err = fmt.Errorf("hub plans returned error")
	}
	today, _ := strconv.Atoi(time.Now().In(lisbon).Format("20060102"))
	for _, p := range providers {
		existing := f.Cache.operator(p.ID)
		if existing.StaticStatus == "ok" && existing.StaticUpdatedAt != nil && time.Since(*existing.StaticUpdatedAt) < staticCacheLifetime {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if p.ID == "cm" {
			d, e := f.cmStatic(ctx, p)
			if e != nil {
				f.markError(ctx, p, true, e)
			} else {
				f.saveStatic(ctx, p, d)
			}
			continue
		}
		if err != nil {
			f.markError(ctx, p, true, err)
			continue
		}
		selected := activeHubPlan(plans.Data, p.Agency, today)
		if selected == nil {
			f.markError(ctx, p, true, fmt.Errorf("Nenhum plano GTFS ativo para a data de serviço"))
			continue
		}
		d, fetchErr := f.loadHubPlan(ctx, p, selected)
		if fetchErr != nil {
			f.markError(ctx, p, true, fetchErr)
			continue
		}
		f.saveStatic(ctx, p, d)
	}
}

// The hub explicitly publishes the numeric-agency to raw-vehicle crosswalk.
func (f *Fetcher) refreshMetadata(ctx context.Context) {
	var rows []struct {
		ID     string `json:"vehicle_id"`
		Agency string `json:"agency_id"`
		Make   string `json:"make"`
		Model  string `json:"model"`
		Plate  string `json:"license_plate"`
	}
	if e := f.fetchJSON(ctx, f.Hub+"/vehicles/metadata", &rows); e != nil {
		f.Log.Warn("fleet metadata unavailable", zap.Error(e))
		return
	}
	codes := map[string]string{"LA77N": "41", "BNA17": "42", "YA15B": "43", "A2L1N": "44", "HF16N": "21"}
	state, _ := f.Cache.state("")
	for _, id := range []string{"mobi", "cm"} {
		d := state.Static[id]
		if d == nil {
			continue
		}
		copyData := *d
		copyData.Models = map[string]Metadata{}
		for k, v := range d.Models {
			copyData.Models[k] = v
		}
		for _, row := range rows {
			code, known := codes[row.Agency]
			if !known || !strings.HasPrefix(row.ID, code+"-") {
				continue
			}
			if (id == "mobi") != (row.Agency == "HF16N") {
				continue
			}
			key := strings.TrimPrefix(row.ID, code+"-")
			if id == "cm" {
				key = "[" + row.Agency + "]" + key
			}
			copyData.Models[key] = Metadata{strings.TrimSpace(row.Make + " " + row.Model), row.Plate}
		}
		f.Store.PublishMu.Lock()
		op := f.Cache.operator(id)
		if e := f.Store.Save(ctx, id, &copyData, nil, op, nil); e == nil {
			f.Cache.update(id, &copyData, nil, op)
		}
		f.Store.PublishMu.Unlock()
	}
}
func (f *Fetcher) saveStatic(ctx context.Context, p provider, d *StaticData) {
	f.Store.PublishMu.Lock()
	defer f.Store.PublishMu.Unlock()
	op := f.Cache.operator(p.ID)
	op.StaticStatus = api.OperatorStaticStatusOk
	op.StaticUpdatedAt = ptr(d.Updated)
	op.StaticError = nil
	op.PlanId = optional(d.PlanID)
	op.ValidFrom = optional(d.ValidFrom)
	op.ValidUntil = optional(d.ValidUntil)
	if e := f.Store.Save(ctx, p.ID, d, nil, op, nil); e != nil {
		f.Log.Error("static persistence failed", zap.String("operator", p.ID), zap.Error(e))
		return
	}
	f.Cache.update(p.ID, d, nil, op)
	f.Log.Info("static provider refreshed", zap.String("operator", p.ID), zap.Int("routes", len(d.Routes)), zap.Int("stops", len(d.Stops)))
}
func (f *Fetcher) refreshLive(ctx context.Context) {
	var result struct {
		Data  []hubPosition `json:"data"`
		Error any           `json:"error"`
	}
	err := f.fetchJSON(ctx, f.Hub+"/vehicles/positions", &result)
	if err == nil && result.Error != nil {
		err = fmt.Errorf("hub returned error")
	}
	if err == nil && result.Data == nil {
		err = fmt.Errorf("hub returned missing data")
	}
	now := time.Now().UTC()
	for _, p := range providers {
		if ctx.Err() != nil {
			return
		}
		if p.ID == "cm" {
			v, e := f.cmLive(ctx, p, now)
			if e != nil {
				f.markError(ctx, p, false, e)
			} else {
				f.saveLive(ctx, p, v, now)
			}
			continue
		}
		if err != nil {
			f.markError(ctx, p, false, err)
			continue
		}
		v, conversionErr := f.hubVehicles(p, result.Data, now)
		if conversionErr != nil {
			f.markError(ctx, p, false, conversionErr)
			continue
		}
		f.saveLive(ctx, p, v, now)
	}
}
func (f *Fetcher) saveLive(ctx context.Context, p provider, vehicles []api.Vehicle, now time.Time) {
	ids := map[string]bool{}
	for _, v := range vehicles {
		if ids[v.Id] {
			f.markError(ctx, p, false, fmt.Errorf("duplicate vehicle identifier"))
			return
		}
		ids[v.Id] = true
	}
	f.Store.PublishMu.Lock()
	defer f.Store.PublishMu.Unlock()
	state, _ := f.Cache.state("")
	old := map[string]api.Vehicle{}
	if data := state.Live[p.ID]; data != nil {
		for _, v := range data.Vehicles {
			old[v.Id] = v
		}
	}
	seen := map[string]bool{}
	dist := map[string]*float64{}
	latest := time.Time{}
	reported, estimated := 0, 0
	for i := range vehicles {
		v := &vehicles[i]
		if seen[v.Id] {
			return
		}
		seen[v.Id] = true
		dist[v.Id] = updateObservedVehicle(v, old)
		enrichVehicle(v, state.Static[p.ID])
		if v.ObservedAt.After(latest) {
			latest = v.ObservedAt
		}
		if now.Sub(v.ObservedAt) <= 180*time.Second {
			if v.PositionKind == "estimated" {
				estimated++
			} else {
				reported++
			}
		}
	}
	sortVehicles(vehicles)
	op := state.Operators[p.ID]
	op.Status = api.OperatorStatusOk
	op.Error = nil
	op.LiveUpdatedAt = ptr(now)
	op.ReportedPositions = ptr(reported)
	op.EstimatedPositions = ptr(estimated)
	op.ObservedAt = nil
	if !latest.IsZero() {
		op.ObservedAt = ptr(latest)
		if now.Sub(latest) > 180*time.Second {
			op.Status = api.OperatorStatusStale
		}
	}
	live := &LiveData{Vehicles: vehicles, Collected: now}
	if e := f.Store.Save(ctx, p.ID, nil, live, op, dist); e != nil {
		f.Log.Error("live persistence failed", zap.String("operator", p.ID), zap.Error(e))
		return
	}
	f.Cache.update(p.ID, nil, live, op)
}
func (f *Fetcher) cmStatic(ctx context.Context, p provider) (*StaticData, error) {
	var lines []struct {
		ID    string   `json:"id"`
		Name  string   `json:"long_name"`
		Short string   `json:"short_name"`
		Color string   `json:"color"`
		Stops []string `json:"stop_ids"`
	}
	var stops []struct {
		ID    string   `json:"id"`
		Name  string   `json:"long_name"`
		Lat   float64  `json:"lat"`
		Lon   float64  `json:"lon"`
		Lines []string `json:"line_ids"`
	}
	if e := f.fetchJSON(ctx, f.CM+"/lines", &lines); e != nil {
		return nil, e
	}
	if e := f.fetchJSON(ctx, f.CM+"/stops", &stops); e != nil {
		return nil, e
	}
	if len(lines) == 0 || len(stops) == 0 {
		return nil, fmt.Errorf("empty CM static feed")
	}
	data := &StaticData{Routes: []api.RouteDetail{}, Stops: []api.Stop{}, Source: f.CM, Updated: time.Now().UTC(), Models: map[string]Metadata{}}
	for _, r := range lines {
		if r.ID == "" {
			return nil, fmt.Errorf("CM route missing ID")
		}
		ids := []string{}
		for _, s := range r.Stops {
			ids = append(ids, qualify(p.ID, s))
		}
		data.Routes = append(data.Routes, api.RouteDetail{Id: qualify(p.ID, r.ID), SourceId: r.ID, OperatorId: p.ID, ShortName: r.Short, LongName: r.Name, Color: r.Color, StopIds: ids})
	}
	for _, s := range stops {
		if !validPosition(s.Lat, s.Lon) {
			continue
		}
		ids := []string{}
		for _, l := range s.Lines {
			ids = append(ids, qualify(p.ID, l))
		}
		data.Stops = append(data.Stops, api.Stop{Id: qualify(p.ID, s.ID), SourceId: s.ID, OperatorId: p.ID, Name: s.Name, Lat: s.Lat, Lon: s.Lon, RouteIds: ids})
	}
	sort.Slice(data.Routes, func(i, j int) bool { return data.Routes[i].Id < data.Routes[j].Id })
	sort.Slice(data.Stops, func(i, j int) bool { return data.Stops[i].Id < data.Stops[j].Id })
	return data, nil
}
func (f *Fetcher) cmLive(ctx context.Context, p provider, now time.Time) ([]api.Vehicle, error) {
	var raw []struct {
		ID      string   `json:"id"`
		Line    string   `json:"line_id"`
		Trip    string   `json:"trip_id"`
		Lat     float64  `json:"lat"`
		Lon     float64  `json:"lon"`
		At      int64    `json:"timestamp"`
		Bearing *float64 `json:"bearing"`
		Model   *string  `json:"model"`
		Plate   *string  `json:"license_plate"`
	}
	if e := f.fetchJSON(ctx, f.CM+"/vehicles", &raw); e != nil {
		return nil, e
	}
	if raw == nil {
		return nil, fmt.Errorf("missing CM vehicle array")
	}
	out := []api.Vehicle{}
	for _, r := range raw {
		if r.ID == "" || r.At <= 0 {
			return nil, fmt.Errorf("invalid CM observation")
		}
		if !validPosition(r.Lat, r.Lon) {
			continue
		}
		at := time.UnixMilli(r.At).UTC()
		if at.After(now.Add(providerRefreshInterval)) {
			return nil, fmt.Errorf("future CM observation")
		}
		v := api.Vehicle{Id: qualify(p.ID, r.ID), SourceId: r.ID, OperatorId: p.ID, RouteName: r.Line, Lat: r.Lat, Lon: r.Lon, ObservedAt: at, CollectedAt: now, PositionKind: api.VehiclePositionKindReported, SourceUrl: f.CM + "/vehicles", Bearing: r.Bearing, Model: r.Model, LicensePlate: r.Plate, Stale: now.Sub(at) > 180*time.Second}
		if r.Line != "" {
			v.RouteId = ptr(qualify(p.ID, r.Line))
		}
		if r.Trip != "" {
			v.TripId = ptr(qualify(p.ID, r.Trip))
		}
		out = append(out, v)
	}
	return out, nil
}

func activeHubPlan(plans []hubPlan, agency string, today int) *hubPlan {
	var selected *hubPlan
	for index := range plans {
		plan := &plans[index]
		if plan.Agency == agency && plan.Active && plan.From <= today && plan.Until >= today && (selected == nil || plan.From > selected.From) {
			selected = plan
		}
	}
	return selected
}
func (f *Fetcher) loadHubPlan(ctx context.Context, p provider, selected *hubPlan) (*StaticData, error) {

	u, e := url.Parse(selected.URL)
	if e != nil || u.Scheme != "https" || u.Hostname() != "objectstorage.eu-frankfurt-1.oraclecloud.com" {
		return nil, fmt.Errorf("URL GTFS fora do armazenamento oficial autorizado")
	}
	blob, e := f.fetch(ctx, selected.URL, maxGTFSCompressedBytes)
	if e != nil {
		return nil, e
	}
	d, e := readGTFS(blob, p, selected.ID, strconv.Itoa(selected.From), strconv.Itoa(selected.Until), f.Hub+"/plans", time.Now().UTC())
	if e != nil {
		return nil, e
	}
	return d, nil
}

func (f *Fetcher) hubVehicles(p provider, positions []hubPosition, now time.Time) ([]api.Vehicle, error) {
	v := []api.Vehicle{}
	invalid := false
	for _, raw := range positions {
		if raw.Agency != p.Agency {
			continue
		}
		if raw.ID == "" || raw.At <= 0 {
			invalid = true
			continue
		}
		if !validPosition(raw.Lat, raw.Lon) {
			continue
		}
		at := time.UnixMilli(raw.At).UTC()
		if at.After(now.Add(providerRefreshInterval)) {
			invalid = true
			continue
		}
		item := f.hubVehicle(p, raw, at, now)
		v = append(v, item)
	}
	if invalid {
		return nil, fmt.Errorf("payload contém observações inválidas")
	}
	return v, nil
}

func (f *Fetcher) hubVehicle(p provider, raw hubPosition, at, now time.Time) api.Vehicle {
	kind := api.VehiclePositionKindReported
	if p.ID == "metro" {
		kind = api.VehiclePositionKindEstimated
	}
	sourceID := verifiedHubID(raw.ID, p.Agency)
	route := verifiedHubID(raw.Route, p.Agency)
	activePlan := ""
	if opPlan := f.Cache.operator(p.ID).PlanId; opPlan != nil {
		activePlan = *opPlan
	}
	trip, plan := verifiedHubTrip(raw.Trip, p.Agency, activePlan)
	item := api.Vehicle{Id: qualify(p.ID, sourceID), SourceId: sourceID, OperatorId: p.ID, PlanId: plan, RouteName: raw.RouteName, Lat: raw.Lat, Lon: raw.Lon, ObservedAt: at, CollectedAt: now, PositionKind: kind, SourceUrl: f.Hub + "/vehicles/positions", Bearing: raw.Bearing, LicensePlate: raw.Plate, Stale: now.Sub(at) > 180*time.Second}
	if route != "" {
		item.RouteId = ptr(qualify(p.ID, route))
	}
	if trip != "" {
		item.TripId = ptr(qualify(p.ID, trip))
	}
	return item
}

func updateObservedVehicle(v *api.Vehicle, old map[string]api.Vehicle) *float64 {
	var distance *float64
	if prev, ok := old[v.Id]; ok {
		if v.ObservedAt.Before(prev.ObservedAt) {
			*v = prev
		} else if v.ObservedAt.Equal(prev.ObservedAt) {
			v.SpeedKmh = prev.SpeedKmh
		} else {
			d, speed := sampledDistance(prev, *v)
			distance = d
			v.SpeedKmh = speed
		}
	}
	return distance
}

func enrichVehicle(v *api.Vehicle, data *StaticData) {

	if data != nil {
		if v.PlanId != nil && *v.PlanId != data.PlanID {
			v.RouteId = nil
		}
		if m, ok := data.Models[v.SourceId]; ok {
			if v.Model == nil {
				v.Model = optional(m.Model)
			}
			if v.LicensePlate == nil {
				v.LicensePlate = optional(m.Plate)
			}
		}
	}
}
