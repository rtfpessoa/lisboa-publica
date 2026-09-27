package app

import (
	"fmt"
	"lisboapublica/internal/api"
	"sort"
	"time"
)

type cmStaticLine struct {
	ID    string   `json:"id"`
	Name  string   `json:"long_name"`
	Short string   `json:"short_name"`
	Color string   `json:"color"`
	Stops []string `json:"stop_ids"`
}
type cmStaticStop struct {
	ID    string   `json:"id"`
	Name  string   `json:"long_name"`
	Lat   float64  `json:"lat"`
	Lon   float64  `json:"lon"`
	Lines []string `json:"line_ids"`
}

func buildCMStatic(p provider, source string, lines []cmStaticLine, stops []cmStaticStop) (*StaticData, error) {
	if len(lines) == 0 || len(stops) == 0 {
		return nil, fmt.Errorf("empty CM static feed")
	}
	data := &StaticData{Operator: p.ID, Routes: []api.RouteDetail{}, Stops: []api.Stop{}, Source: source, Updated: time.Now().UTC(), Models: map[string]Metadata{}}
	var err error
	for _, line := range lines {
		err = appendCMStaticLine(data, p, line)
		if err != nil {
			break
		}
	}
	for _, stop := range stops {
		appendCMStaticStop(data, p, stop)
	}
	sort.Slice(data.Routes, func(i, j int) bool { return data.Routes[i].Id < data.Routes[j].Id })
	sort.Slice(data.Stops, func(i, j int) bool { return data.Stops[i].Id < data.Stops[j].Id })
	return data, err
}
func appendCMStaticLine(data *StaticData, p provider, r cmStaticLine) error {
	if r.ID == "" {
		return fmt.Errorf("CM route missing ID")
	}
	ids := []string{}
	for _, s := range r.Stops {
		ids = append(ids, qualify(p.ID, s))
	}
	data.Routes = append(data.Routes, api.RouteDetail{Id: qualify(p.ID, r.ID), SourceId: r.ID, OperatorId: p.ID, ShortName: r.Short, LongName: r.Name, Color: r.Color, StopIds: ids})
	return nil
}
func appendCMStaticStop(data *StaticData, p provider, s cmStaticStop) {
	if !validPosition(s.Lat, s.Lon) {
		return
	}
	ids := []string{}
	for _, l := range s.Lines {
		ids = append(ids, qualify(p.ID, l))
	}
	data.Stops = append(data.Stops, api.Stop{Id: qualify(p.ID, s.ID), SourceId: s.ID, OperatorId: p.ID, Name: s.Name, Lat: s.Lat, Lon: s.Lon, RouteIds: ids})
}
