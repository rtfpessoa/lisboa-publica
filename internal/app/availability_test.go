package app

import (
	"context"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStagedLiveBatchSurvivesFailedCommit(t *testing.T) {
	s := testStore(t)
	if err := s.ConfigureHistory(30, staticRefreshInterval, false); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-15 * time.Minute).Truncate(staticRefreshInterval)
	for i := 0; i < 6; i++ {
		v := historyVehicle("staged", base.Add(time.Duration(i+1)*5*time.Second), float64(10+i))
		live := &LiveData{Collected: v.ObservedAt, Vehicles: []api.Vehicle{v}}
		s.stageLive(live, map[string]*float64{v.Id: ptr(.1)})
		s.stageLive(live, map[string]*float64{v.Id: ptr(.1)})
	}
	checkSnapshotCount(t, s, 0)
	if _, err := s.DB.Exec(context.Background(), "DROP TABLE source_health"); err != nil {
		t.Fatal(err)
	}
	closeAt := base.Add(staticRefreshInterval + sourceFreshness + time.Second)
	if s.Save(context.Background(), "carris", nil, &LiveData{Collected: closeAt}, NewCache().operator("carris"), nil) == nil {
		t.Fatal("expected transaction failure")
	}
	if len(s.collector.Pending) != 1 {
		t.Fatal("staged batch lost on failure")
	}
	if _, err := s.DB.Exec(context.Background(), "CREATE TABLE source_health(operator_id TEXT PRIMARY KEY,payload JSONB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	saveHistory(t, s, closeAt, nil, nil)
	saveHistory(t, s, closeAt, nil, nil)
	var samples int
	var distance, speed float64
	if err := s.DB.QueryRow(context.Background(), "SELECT speed_sample_count,distance_km,speed_kmh FROM snapshots").Scan(&samples, &distance, &speed); err != nil {
		t.Fatal(err)
	}
	if samples != 6 || math.Abs(distance-.6) > 1e-9 || speed != 12.5 {
		t.Fatalf("batch lost/doubled: %d %f %f", samples, distance, speed)
	}
	checkSnapshotCount(t, s, 1)
}

func TestLivePublicationBoundsSuccessAndErrorDurability(t *testing.T) {
	s := testStore(t)
	_ = s.ConfigureHistory(30, staticRefreshInterval, false)
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	p, _ := providerByID("carris")
	now := time.Now().UTC()
	v := historyVehicle("fast", now, 10)
	f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
	first, _ := s.generation(context.Background())
	for i := 0; i < 10; i++ {
		v.ObservedAt = now.Add(time.Duration(i) * time.Millisecond)
		f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
	}
	successful, _ := s.generation(context.Background())
	if successful != first {
		t.Fatalf("unchanged reporting state multiplied durable writes %d->%d", first, successful)
	}
	f.markError(context.Background(), p, false, context.DeadlineExceeded)
	failed, _ := s.generation(context.Background())
	if failed != first+1 {
		t.Fatalf("first reporting failure was not committed immediately: %d->%d", first, failed)
	}
	for i := 0; i < 10; i++ {
		f.markError(context.Background(), p, false, context.DeadlineExceeded)
	}
	repeatedFailure, _ := s.generation(context.Background())
	if repeatedFailure != failed {
		t.Fatalf("same reporting failure bypassed batch cadence %d->%d", failed, repeatedFailure)
	}
	if c.operator("carris").Status != "error" {
		t.Fatal("error not published immediately")
	}
	if len(s.collector.Pending) == 0 {
		t.Fatal("intermediate samples not staged")
	}
	f.saveLive(context.Background(), p, []api.Vehicle{v}, now.Add(time.Second))
	recovered, _ := s.generation(context.Background())
	if recovered != failed+1 {
		t.Fatalf("reporting recovery was not committed immediately: %d->%d", failed, recovered)
	}
	if c.operator("carris").Status != "ok" {
		t.Fatal("reporting recovery not published")
	}

}

func TestOperatorCoverageCommittedWindowAndRevision(t *testing.T) {
	s := testStore(t)
	c := NewCache()
	now := time.Now().UTC()
	v := historyVehicle("covered", now.Add(-time.Minute), 12)
	v.Model = ptr("Volvo")
	v.LicensePlate = ptr("AB12CD")
	v.Typology = ptr("3.3")
	putLive(t, s, c, "carris", []api.Vehicle{v})
	server, err := NewServer(s, c, Options{Origin: "https://example.com", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	h, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	q := url.Values{"from": {now.Add(-time.Hour).Format(time.RFC3339Nano)}, "to": {now.Format(time.RFC3339Nano)}, "limit": {"1"}}
	status, body := req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/operator-coverage?"+q.Encode(), nil, nil)
	expectStatus(t, status, 200, body)
	page := decode[api.OperatorCoveragePage](t, body)
	if page.Page.Total != 8 || page.Data[0].OperatorId != "carris" || page.Data[0].Vehicles != 1 || page.Data[0].SpeedSamples != 1 || page.Data[0].TypologyVehicles != 1 {
		t.Fatalf("bad coverage %s", body)
	}
	v.Id = "carris:late"
	putLive(t, s, c, "carris", []api.Vehicle{v})
	q.Set("revision", *page.Page.Revision)
	q.Set("limit", "500")
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/operator-coverage?"+q.Encode(), nil, nil)
	expectStatus(t, status, 200, body)
	if decode[api.OperatorCoveragePage](t, body).Data[0].Vehicles != 1 {
		t.Fatal("coverage revision shifted")
	}
	q.Del("revision")
	q.Set("weekdays_only", "true")
	q.Set("hour_start", "0")
	q.Set("hour_end", "1")
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/operator-coverage?"+q.Encode(), nil, nil)
	expectStatus(t, status, 200, body)
	if decode[api.OperatorCoveragePage](t, body).Data[0].Vehicles != 0 {
		t.Fatal("hour/weekday coverage filter ignored")
	}
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/operator-coverage", nil, map[string]string{"Authorization": "Bearer invalid"})
	expectStatus(t, status, 401, body)
}

func TestPublishedModelsAndUnknownCodes(t *testing.T) {
	if publishedModel("Volvo B7R", "Volvo B7R") != "Volvo B7R" || publishedModel("Volvo", "Volvo B7R") != "Volvo B7R" || publishedModel("", "Volvo B7R Volvo B7R") != "Volvo B7R" {
		t.Fatal("published model duplication")
	}
	m := metadataRow(map[string]string{"make": "IVECO", "model": "BUS", "license_plate": " AB12CD ", "typology": "3.3", "propulsion": "8"})
	m = mergeMetadata(m, Metadata{})
	updated := mergeMetadata(m, Metadata{Model: "New model", Plate: "NEW12AB"})
	if updated.Model != "New model" || updated.Plate != "NEW12AB" || updated.Typology != "3.3" {
		t.Fatal("new verified fields did not replace older values")
	}
	v := api.Vehicle{SourceId: "[LA77N]123"}
	enrichVehicle(&v, &StaticData{Models: map[string]Metadata{"[LA77N]123": m}})
	if v.Typology == nil || *v.Typology != "3.3" || v.Propulsion == nil || *v.Propulsion != "8" || *v.LicensePlate != "AB12CD" {
		t.Fatalf("published codes lost/invented %+v", v)
	}
	wrong := api.Vehicle{SourceId: "[BNA17]123"}
	enrichVehicle(&wrong, &StaticData{Models: map[string]Metadata{"[LA77N]123": m}})
	if wrong.Model != nil {
		t.Fatal("cross-agency enrichment")
	}
}

func TestFastPublicationKeepsFrozenCollections(t *testing.T) {
	c := NewCache()
	now := time.Now().UTC()
	d := fixtureStatic("metro", now)
	op := c.operator("metro")
	c.update("metro", d, nil, op)
	frozen, _ := c.state("")
	for i := 0; i < maxCachedVersions-1; i++ {
		c.update("metro", nil, &LiveData{Collected: now.Add(time.Duration(i) * time.Millisecond)}, op)
	}
	page, err := c.state(frozen.Revision)
	if err != nil || page.Static["metro"] != d {
		t.Fatal("within-cap publications expired/shared-static revision", err)
	}
	c.update("metro", nil, &LiveData{Collected: now}, op)
	if _, err := c.state(frozen.Revision); err == nil {
		t.Fatal("over-cap revision did not expire")
	}
}

func TestPausedStageDoesNotAccumulate(t *testing.T) {
	s := &Store{HistoryInterval: staticRefreshInterval, collector: newHistoryCollector(), budget: &storageBudget{state: "paused"}}
	s.stageLive(&LiveData{Collected: time.Now(), Vehicles: []api.Vehicle{historyVehicle("x", time.Now(), 1)}}, nil)
	if len(s.collector.Pending) != 0 {
		t.Fatal("paused stage grew")
	}
}

func TestProviderClockSkewIndependentOfPoll(t *testing.T) {
	f := NewFetcher(nil, NewCache(), zap.NewNop())
	p, _ := providerByID("cp")
	now := time.Now().UTC()
	_, err := f.hubVehicles(p, []hubPosition{{Agency: p.Agency, ID: "x", At: now.Add(20 * time.Second).UnixMilli(), Lat: 38.72, Lon: -9.15}}, now)
	if err != nil || providerRefreshInterval != 5*time.Second || providerClockSkew != 30*time.Second {
		t.Fatal("poll interval tightened provider skew", err)
	}
	if strings.Contains(p.Note, "complete inventory") {
		t.Fatal("invented coverage")
	}
}

func TestCMArchiveMetadataUsesAgencyIdentity(t *testing.T) {
	p, _ := providerByID("cm")
	data, err := readCMNetwork(shapeArchive(t, true), &hubPlan{ID: "plan", Agency: "LA77N"}, p, hubBase, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Models) != 1 || data.Models["[LA77N]123"].Typology != "3.3" || data.Models["[LA77N]123"].Plate != "AB12CD" {
		t.Fatal("archive metadata discarded or joined to wrong identity", data.Models)
	}
}
