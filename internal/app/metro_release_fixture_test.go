package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// Opt-in synthetic fixture for the Go/proxy/browser release assessment; never contacts Metro.
func TestMetroLiveReleaseFixture(t *testing.T) {
	if os.Getenv("RUN_METRO_LIVE_FIXTURE") != "1" {
		t.Skip("opt-in transport fixture")
	}
	s, d, template, _ := metroLiveFixture(t)
	s.Options.FrontendDir = "../../frontend/dist"
	s.trustedProxies, _ = proxyPrefixes("127.0.0.0/8,172.16.0.0/12,192.168.0.0/16")
	archive, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	s.Patterns = archive
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Cache.metroRuntime.runJournal(ctx, archive)
	// Synthetic large scope: 50 ordered stations, 96 identified references, 1600 platform rows.
	stations := []MetroStation{}
	stops := []api.Stop{}
	visits := []StopTime{}
	for n := 0; n < 50; n++ {
		code := fmt.Sprintf("S%02d", n)
		name := fmt.Sprintf("Synthetic station %02d", n)
		id := "metro:" + code
		if n == 0 {
			code = "RM"
			name = "Roma"
			id = "metro:gtfs-rm"
		}
		if n == 49 {
			code = "CS"
			name = "Cais do Sodré"
			id = "metro:gtfs-cs"
		}
		lat := 38.71 + float64(n)*.0008
		stations = append(stations, MetroStation{ID: code, Name: name, Lat: strconv.FormatFloat(lat, 'f', 6, 64), Lon: "-9.14", Lines: "[Verde]"})
		stops = append(stops, api.Stop{Id: id, SourceId: code, Name: name, Lat: lat, Lon: -9.14, OperatorId: "metro", RouteIds: []string{"metro:r"}})
		visits = append(visits, StopTime{Stop: code, Sequence: n + 1, Arrival: int32(n * 60), Departure: int32(n * 60)})
	}
	template.Stations = stations
	d.Stops = stops
	d.Schedule.Trips[0].Times = visits
	s.Cache.update("metro", d, nil, s.Cache.operator("metro"))
	controls := newMetroReplayControls()
	started := time.Now()
	publish := func() {
		now := time.Now().UTC()
		mode, forget, moving := controls.settings(now)
		if forget {
			r := s.Cache.metroRuntime
			r.mu.Lock()
			if len(r.pending) == 0 {
				r.active = map[string]string{}
				r.tracks = map[string]*metroTrack{}
				controls.mu.Lock()
				controls.forgotten++
				controls.mu.Unlock()
			} else {
				controls.mu.Lock()
				controls.forget = true
				controls.mu.Unlock()
			}
			r.mu.Unlock()
		}
		if mode == "freeze" {
			return
		}
		data := *template
		data.Status = template.Status
		data.Status.CheckedAt = &now
		data.Waits = []MetroWait{}
		for stationIndex, station := range stations {
			for platform := 0; platform < 32; platform++ {
				row := metroTestRow(now, station.ID, strconv.Itoa(platform*3+1), strconv.Itoa(60+stationIndex*60+platform*3))
				if stationIndex == 0 && platform == 0 && mode == "arrival" {
					row.Wait1 = json.RawMessage("0")
				}
				row.Platform = strconv.Itoa(platform + 1)
				row.Train2 = strconv.Itoa(platform*3 + 2)
				row.Train3 = strconv.Itoa(platform*3 + 3)
				row.Wait2 = json.RawMessage(strconv.Itoa(61 + stationIndex*60 + platform*3))
				row.Wait3 = json.RawMessage(strconv.Itoa(62 + stationIndex*60 + platform*3))
				data.Waits = append(data.Waits, row)
			}
		}

		s.Cache.metroRuntime.observe(&data, d, archive, now)
		s.Cache.updateMetro(&data, s.Cache.operator("metro"))
		op := s.Cache.operator("metro")
		op.Status = "ok"
		op.LiveUpdatedAt = &now
		op.EstimatedPositions = ptr(1)
		motion := 0.0
		if moving {
			motion = math.Sin(time.Since(started).Seconds()/20) * .0001
		}
		vehicles := []api.Vehicle{}
		for n := 1; n <= 96; n++ {
			ref := strconv.Itoa(n)
			vehicles = append(vehicles, api.Vehicle{Id: "metro:" + ref, SourceId: ref, OperatorId: "metro", RouteId: ptr("metro:r"), RouteName: "Linha Verde", PositionKind: "estimated", SourceUrl: metroBase, Lat: 38.731 + float64(n%10)*.00005 + motion, Lon: -9.145 + float64(n/10)*.00005, ObservedAt: now, CollectedAt: now})
		}
		op.EstimatedPositions = ptr(len(vehicles))
		s.Cache.update("metro", nil, &LiveData{Collected: now, Vehicles: vehicles}, op)

	}
	publish()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				publish()
			}
		}
	}()
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/test-replay/control", controls.control)
	mux.HandleFunc("/test-replay/state", controls.state)
	mux.HandleFunc("/test-replay/ping", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "synthetic relay") })
	mux.HandleFunc("/test-runtime-metrics", func(w http.ResponseWriter, _ *http.Request) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		s.metroStreams.mu.Lock()
		streams := s.metroStreams.total
		s.metroStreams.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"heap_bytes": mem.HeapAlloc, "sys_bytes": mem.Sys, "goroutines": runtime.NumGoroutine(), "streams": streams})
	})
	listener, err := net.Listen("tcp", "127.0.0.1:18082")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: controls.wrap(mux), WriteTimeout: 60 * time.Second, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	t.Log("Synthetic Metro fixture ready at 127.0.0.1:18082; no provider calls")
	<-time.After(45 * time.Minute)
}
