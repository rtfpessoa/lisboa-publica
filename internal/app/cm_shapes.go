package app

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"lisboapublica/internal/api"
)

func (f *Fetcher) fetchHubArchive(ctx context.Context, plan *hubPlan) ([]byte, error) {
	parsed, err := url.Parse(plan.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "objectstorage.eu-frankfurt-1.oraclecloud.com" || parsed.User != nil || parsed.Port() != "" && parsed.Port() != "443" {
		return nil, fmt.Errorf("URL GTFS fora do armazenamento oficial autorizado")
	}
	return f.fetch(ctx, plan.URL, maxGTFSCompressedBytes)
}

func (f *Fetcher) cmShapes(ctx context.Context, plans []hubPlan, today int) (*StaticData, error) {
	network := &StaticData{Models: map[string]Metadata{}}
	p, _ := providerByID("cm")
	for _, agency := range []string{"LA77N", "BNA17", "YA15B", "A2L1N"} {
		plan := activeHubPlan(plans, agency, today)
		if plan == nil {
			return nil, fmt.Errorf("Plano de percursos CM indisponível")
		}
		blob, err := f.fetchHubArchive(ctx, plan)
		if err != nil {
			return nil, err
		}
		data, err := readCMNetwork(blob, plan, p, f.Hub+"/plans", time.Now().UTC())
		if err != nil {
			return nil, err
		}
		network.Shapes = append(network.Shapes, data.Shapes...)
		network.CMPaths = append(network.CMPaths, data.CMPaths...)
		if data.CMPathError != nil {
			network.CMPathError = data.CMPathError
		}
		for id, metadata := range data.Models {
			network.Models[id] = metadata
		}
	}
	sort.Slice(network.CMPaths, func(i, j int) bool { return network.CMPaths[i].ID < network.CMPaths[j].ID })
	return network, nil
}

func (f *Fetcher) updateCMShapes(ctx context.Context, data *StaticData, plans []hubPlan, planErr error, today int) {
	network, err := (*StaticData)(nil), planErr
	if err == nil {
		network, err = f.cmShapes(ctx, plans, today)
	}
	if err == nil {
		mergeCMNetwork(data, network)
		err = admitCMGeometry(data, staticHealth(f.Cache.operator("cm"), data))
	}
	if err != nil {
		f.retainGeometry(data, "Falha ao atualizar percursos oficiais CM; a rede de carreiras mantém-se disponível.")
	}
}

func mergeCMNetwork(data, network *StaticData) {
	colors := map[string]string{}
	for _, r := range data.Routes {
		colors[r.Id] = r.Color
	}
	for id, metadata := range network.Models {
		data.Models[id] = mergeMetadata(data.Models[id], metadata)
	}
	for _, shape := range network.Shapes {
		if color, ok := colors[shape.RouteId]; ok {
			shape.Color = color
			data.Shapes = append(data.Shapes, shape)
		}
	}
	data.CMPaths = network.CMPaths
	data.CMPathError = network.CMPathError
	data.GeometryUpdated = ptr(time.Now().UTC())
	attachCMRouteGeometry(data)
}
func admitCMGeometry(data *StaticData, op api.Operator) error {
	if geometryCacheFits(data, op) {
		return nil
	}
	data.CMPaths = nil
	data.CMPathError = ptr("Percursos completos indisponíveis: limite de armazenamento por atualização.")
	if !geometryCacheFits(data, op) {
		return fmt.Errorf("Percursos excedem o limite de armazenamento por atualização")
	}
	return nil
}

func geometryCacheFits(data *StaticData, op api.Operator) bool {
	update, err := prepareCacheUpdate(data, nil, op)
	return err == nil && int64(len(update.Static)+len(update.Health))*storageWriteOverhead+historyRecordOverhead <= maximumWriteBytes
}

func (f *Fetcher) retainGeometry(data *StaticData, message string) {
	state, _ := f.Cache.state("")
	data.Shapes = nil
	data.CMPaths = nil
	data.CMPathError = nil
	data.GeometryUpdated = nil
	if old := state.Static["cm"]; old != nil {
		data.Shapes = old.Shapes
		data.CMPaths = old.CMPaths
		data.CMPathError = old.CMPathError
		data.GeometryUpdated = old.GeometryUpdated
	}
	data.GeometryError = ptr(message)
	attachCMRouteGeometry(data)
	if !geometryCacheFits(data, staticHealth(f.Cache.operator("cm"), data)) {
		data.Shapes = nil
		data.CMPaths = nil
		data.CMPathError = nil
		data.GeometryUpdated = nil
		for i := range data.Routes {
			data.Routes[i].Geometry = nil
		}
		data.GeometryError = ptr("Percursos indisponíveis: excedem o limite de armazenamento por atualização.")
	}
}

func attachCMRouteGeometry(data *StaticData) {
	routes := map[string]*api.RouteDetail{}
	for i := range data.Routes {
		routes[data.Routes[i].Id] = &data.Routes[i]
		data.Routes[i].Geometry = nil
	}
	for _, shape := range data.Shapes {
		if route := routes[shape.RouteId]; route != nil && route.Geometry == nil {
			route.Geometry = &shape.Geometry
		}
	}
}

func (f *Fetcher) refreshCMStatic(ctx context.Context, p provider, plans []hubPlan, planErr error, today int) {
	data, err := f.cmStatic(ctx, p)
	if err != nil {
		f.markError(ctx, p, true, err)
		return
	}
	state, _ := f.Cache.state("")
	if old := state.Static[p.ID]; old != nil {
		for id, metadata := range old.Models {
			data.Models[id] = metadata
		}
	}
	f.updateCMShapes(ctx, data, plans, planErr, today)
	f.saveStatic(ctx, p, data)
}
