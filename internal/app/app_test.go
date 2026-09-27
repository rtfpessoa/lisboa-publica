package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"google.golang.org/api/idtoken"
	"lisboapublica/internal/api"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for Postgres/Cockroach integration")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	name := fmt.Sprintf("lp_test_%d", time.Now().UnixNano())
	if _, e = base.Exec(ctx, "CREATE SCHEMA "+name); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	s, e := OpenStore(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close(); _, _ = base.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); base.Close() })
	return s
}
func fixtureStatic(p string, now time.Time) *StaticData {
	return &StaticData{Routes: []api.RouteDetail{{Id: qualify(p, "1"), SourceId: "1", OperatorId: p, ShortName: "1", LongName: "Centro — Estação", Color: "#f5b800", StopIds: []string{qualify(p, "S")}}}, Stops: []api.Stop{{Id: qualify(p, "S"), SourceId: "S", OperatorId: p, Name: "Estação", Lat: 38.72, Lon: -9.15, RouteIds: []string{qualify(p, "1")}}}, Models: map[string]Metadata{}, Updated: now, ValidFrom: now.In(lisbon).Format("20060102"), ValidUntil: now.In(lisbon).AddDate(0, 0, 1).Format("20060102"), Schedule: &Schedule{Trips: []ScheduledTrip{{ID: "T", Route: "1", Service: "daily", Headsign: "Centro", Times: []StopTime{{Stop: "S", Arrival: 43200, Departure: 43200, Sequence: 1}, {Stop: "S", Arrival: 44000, Departure: 44000, Sequence: 2}}}}, Calendars: map[string]Calendar{"daily": {Start: "20200101", End: "20301231", Days: [7]bool{true, true, true, true, true, true, true}}}, Exceptions: map[string]map[string]int{}, Parents: map[string]string{}}}
}
func putLive(t *testing.T, s *Store, c *Cache, p string, vs []api.Vehicle) {
	t.Helper()
	op := c.operator(p)
	op.Status = api.OperatorStatusOk
	op.LiveUpdatedAt = ptr(time.Now().UTC())
	live := &LiveData{Vehicles: vs, Collected: time.Now().UTC()}
	if e := s.Save(context.Background(), p, nil, live, op, map[string]*float64{vs[0].Id: ptr(.2)}); e != nil {
		t.Fatal(e)
	}
	c.update(p, nil, live, op)
}
func req(t *testing.T, client *http.Client, method, u string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var data io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		data = bytes.NewReader(b)
	}
	r, e := http.NewRequest(method, u, data)
	if e != nil {
		t.Fatal(e)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	res, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, b
}
func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if e := json.Unmarshal(b, &v); e != nil {
		t.Fatalf("decode %s: %v", b, e)
	}
	return v
}
func expectStatus(t *testing.T, status, want int, b []byte) {
	t.Helper()
	if status != want {
		t.Fatalf("status=%d want%d body=%s", status, want, b)
	}
}

func TestIntegration(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	now := time.Now().UTC()
	for _, p := range []string{"carris", "tcb"} {
		d := fixtureStatic(p, now)
		op := cache.operator(p)
		op.StaticStatus = api.OperatorStaticStatusOk
		op.StaticUpdatedAt = &now
		if e := store.Save(context.Background(), p, d, nil, op, nil); e != nil {
			t.Fatal(e)
		}
		cache.update(p, d, nil, op)
	}
	base := now.Add(-15 * time.Minute)
	a := api.Vehicle{Id: "carris:A", SourceId: "A", OperatorId: "carris", RouteId: ptr("carris:1"), TripId: ptr("carris:T"), RouteName: "1", Lat: 38.72, Lon: -9.15, ObservedAt: base, CollectedAt: now, PositionKind: api.VehiclePositionKindReported, SourceUrl: hubBase, SpeedKmh: ptr(12.0)}
	b := a
	b.Id = "carris:B"
	b.SourceId = "B"
	putLive(t, store, cache, "carris", []api.Vehicle{a, b})
	a.ObservedAt = now.Add(-time.Second)
	b.ObservedAt = a.ObservedAt
	putLive(t, store, cache, "carris", []api.Vehicle{a, b})
	s, e := NewServer(store, cache, Options{Origin: "http://localhost", Environment: "development", DevAuth: true, PublicReads: true, GoogleClientID: "google-fixture", RateLimit: 1000}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	h, e := s.Handler()
	if e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	s.Options.Origin = ts.URL
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	origin := map[string]string{"Origin": ts.URL}
	status, body := req(t, client, "GET", ts.URL+"/api/v1/vehicles?operators=carris&limit=1", nil, nil)
	expectStatus(t, status, 200, body)
	first := decode[api.VehiclePage](t, body)
	if len(first.Data) != 1 || first.Page.Total != 2 || !first.Page.HasMore {
		t.Fatal("bad first page")
	}
	oldRevision := *first.Page.Revision
	c := a
	c.Id = "carris:0"
	c.SourceId = "0"
	putLive(t, store, cache, "carris", []api.Vehicle{c, a, b})
	status, body = req(t, client, "GET", ts.URL+"/api/v1/vehicles?operators=carris&limit=1&offset=1&revision="+url.QueryEscape(oldRevision), nil, nil)
	expectStatus(t, status, 200, body)
	second := decode[api.VehiclePage](t, body)
	if second.Data[0].Id != "carris:B" || second.Page.Total != 2 {
		t.Fatal("live page mixed revisions")
	}
	status, body = req(t, client, "GET", ts.URL+"/api/v1/routes?limit=1", nil, nil)
	expectStatus(t, status, 200, body)
	routes := decode[api.RoutePage](t, body)
	if routes.Page.Total != 2 || routes.Data[0].Id != "carris:1" {
		t.Fatal("qualified IDs failed")
	}
	window := url.Values{"operators": {"carris"}, "from": {now.Add(-time.Hour).Format(time.RFC3339Nano)}, "to": {now.Format(time.RFC3339Nano)}, "limit": {"1"}}
	status, body = req(t, client, "GET", ts.URL+"/api/v1/fleet?"+window.Encode(), nil, nil)
	expectStatus(t, status, 200, body)
	fleet := decode[api.FleetVehiclePage](t, body)
	if fleet.Page.Total != 3 {
		t.Fatal("bad fleet total")
	}
	c.Id = "carris:-late"
	c.ObservedAt = now.Add(-10 * time.Minute)
	putLive(t, store, cache, "carris", []api.Vehicle{c})
	window.Set("revision", *fleet.Page.Revision)
	window.Set("offset", "1")
	status, body = req(t, client, "GET", ts.URL+"/api/v1/fleet?"+window.Encode(), nil, nil)
	expectStatus(t, status, 200, body)
	next := decode[api.FleetVehiclePage](t, body)
	if next.Page.Total != 3 || next.Data[0].Id != fleet.Data[0].Id && next.Data[0].Id == c.Id {
		t.Fatal("delayed snapshot shifted page")
	}
	if e = store.prune(context.Background()); e != nil {
		t.Fatal(e)
	}
	status, body = req(t, client, "GET", ts.URL+"/api/v1/fleet?"+window.Encode(), nil, nil)
	expectStatus(t, status, 200, body)
	if decode[api.FleetVehiclePage](t, body).Page.Total != 3 {
		t.Fatal("pruning shifted active window")
	}
	// Every historical SQL query is exercised against both engines, not mocked.
	for _, path := range []string{"metrics", "history", "traffic", "rankings"} {
		status, body = req(t, client, "GET", ts.URL+"/api/v1/"+path+"?operators=carris&from="+url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339Nano))+"&to="+url.QueryEscape(now.Format(time.RFC3339Nano)), nil, nil)
		expectStatus(t, status, 200, body)
	}
	for _, path := range []string{"/vehicles?limit=0", "/vehicles?limit=501", "/vehicles?offset=-1", "/vehicles?operators=bogus", "/vehicles?revision=gone", "/history?from=2000-01-01T00:00:00Z&to=2000-01-02T00:00:00Z"} {
		status, body = req(t, client, "GET", ts.URL+"/api/v1"+path, nil, nil)
		want := 400
		if strings.Contains(path, "revision") || strings.Contains(path, "2000-") {
			want = 410
		}
		expectStatus(t, status, want, body)
	}
	status, body = req(t, client, "GET", ts.URL+"/api/v1/vehicles", nil, map[string]string{"Authorization": "Bearer lp_invalid"})
	expectStatus(t, status, 401, body)
	status, body = req(t, client, "GET", ts.URL+"/api/v1/config", nil, nil)
	config := decode[api.Config](t, body)
	expectStatus(t, status, 200, body)
	status, body = req(t, client, "POST", ts.URL+"/api/v1/auth/development", api.DevLogin{LoginNonce: config.LoginNonce}, map[string]string{"Origin": "https://attacker.example"})
	expectStatus(t, status, 403, body)
	status, body = req(t, client, "POST", ts.URL+"/api/v1/auth/development", api.DevLogin{LoginNonce: "missing-or-mismatched"}, origin)
	expectStatus(t, status, 403, body)
	status, body = req(t, client, "POST", ts.URL+"/api/v1/auth/development", api.DevLogin{LoginNonce: config.LoginNonce}, origin)
	expectStatus(t, status, 200, body)
	status, body = req(t, client, "POST", ts.URL+"/api/v1/keys", api.CreateKey{Name: "fixture", Scopes: []api.CreateKeyScopes{"read:transit"}}, origin)
	expectStatus(t, status, 201, body)
	key := decode[api.KeySecret](t, body)
	var storedHash string
	if e = store.DB.QueryRow(context.Background(), "SELECT token_hash FROM api_keys WHERE id=$1", key.Key.Id).Scan(&storedHash); e != nil || storedHash == key.Secret || storedHash != tokenHash(key.Secret) {
		t.Fatal("key secret storage failure")
	}
	keyHeaders := map[string]string{"Authorization": "Bearer " + key.Secret}
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/route-shapes", nil, keyHeaders)
	expectStatus(t, status, 200, body)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/vehicles", nil, keyHeaders)
	expectStatus(t, status, 200, body)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/operator-coverage", nil, keyHeaders)
	expectStatus(t, status, 403, body)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/history", nil, keyHeaders)
	expectStatus(t, status, 403, body)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/keys", nil, keyHeaders)
	expectStatus(t, status, 401, body)
	status, body = req(t, client, "DELETE", ts.URL+"/api/v1/keys/"+key.Key.Id, nil, origin)
	expectStatus(t, status, 204, body)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/vehicles", nil, keyHeaders)
	expectStatus(t, status, 401, body)
	status, body = req(t, client, "POST", ts.URL+"/api/v1/keys", api.CreateKey{Name: "expires", Scopes: []api.CreateKeyScopes{"read:history"}}, origin)
	expectStatus(t, status, 201, body)
	expired := decode[api.KeySecret](t, body)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/route-shapes", nil, map[string]string{"Authorization": "Bearer " + expired.Secret})
	expectStatus(t, status, 403, body)
	_, _ = store.DB.Exec(context.Background(), "UPDATE api_keys SET expires_at=$1 WHERE id=$2", now.Add(-time.Hour), expired.Key.Id)
	status, body = req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/history", nil, map[string]string{"Authorization": "Bearer " + expired.Secret})
	expectStatus(t, status, 401, body)
	status, body = req(t, client, "POST", ts.URL+"/api/v1/auth/logout", nil, origin)
	expectStatus(t, status, 204, body)
	status, body = req(t, client, "GET", ts.URL+"/api/v1/auth/me", nil, nil)
	expectStatus(t, status, 401, body)
	// The Google adapter must check signed nonce, issuer, audience, expiry and verified email.
	status, body = req(t, client, "GET", ts.URL+"/api/v1/config", nil, nil)
	config = decode[api.Config](t, body)
	s.VerifyGoogle = func(context.Context, string, string) (*idtoken.Payload, error) {
		return &idtoken.Payload{Audience: "google-fixture", Issuer: "https://accounts.google.com", Expires: time.Now().Add(time.Hour).Unix(), Claims: map[string]any{"email": "person@example.test", "name": "Person", "email_verified": true, "nonce": "wrong"}}, nil
	}
	status, body = req(t, client, "POST", ts.URL+"/api/v1/auth/google", api.GoogleLogin{Credential: "fixture"}, origin)
	expectStatus(t, status, 403, body)
	s.VerifyGoogle = func(context.Context, string, string) (*idtoken.Payload, error) {
		return &idtoken.Payload{Audience: "google-fixture", Issuer: "https://accounts.google.com", Expires: time.Now().Add(time.Hour).Unix(), Claims: map[string]any{"email": "person@example.test", "name": "Person", "email_verified": true, "nonce": config.LoginNonce}}, nil
	}
	status, body = req(t, client, "POST", ts.URL+"/api/v1/auth/google", api.GoogleLogin{Credential: "fixture"}, origin)
	expectStatus(t, status, 200, body)
	restored := NewCache()
	if e = store.Restore(context.Background(), restored); e != nil {
		t.Fatal(e)
	}
	state, _ := restored.state("")
	if len(state.Static["carris"].Routes) != 1 || state.Live["carris"].Vehicles[0].Id != c.Id {
		t.Fatal("restart did not restore cache")
	}
	var snapshots int
	_ = store.DB.QueryRow(context.Background(), "SELECT count(*) FROM snapshots").Scan(&snapshots)
	putLive(t, store, cache, "carris", []api.Vehicle{c})
	var after int
	_ = store.DB.QueryRow(context.Background(), "SELECT count(*) FROM snapshots").Scan(&after)
	if after != snapshots {
		t.Fatal("duplicate poll added snapshots")
	}
}

func TestPublicAndRestrictedRateLimits(t *testing.T) {
	store := testStore(t)
	s, e := NewServer(store, NewCache(), Options{Origin: "http://localhost", Environment: "development", PublicReads: true, RateLimit: 2}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	h, e := s.Handler()
	if e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	for _, want := range []int{200, 200, 429} {
		status, body := req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/vehicles", nil, nil)
		expectStatus(t, status, want, body)
	}
	s.Options.PublicReads = false
	s.rate = &limiter{entries: map[string]rateEntry{}, limit: 100}
	status, body := req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/vehicles", nil, nil)
	expectStatus(t, status, 401, body)
	if _, e = NewServer(store, NewCache(), Options{Origin: "https://example.test", Environment: "production", DevAuth: true}, zap.NewNop()); e == nil {
		t.Fatal("production accepted development login")
	}
}

func TestGTFSCalendarAndMidnight(t *testing.T) {
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	files := map[string]string{"routes.txt": "route_id,route_short_name,route_long_name\n1,1,Centro\n", "stops.txt": "stop_id,stop_name,stop_lat,stop_lon,parent_station\nS,Estação,38.72,-9.15,\nP,Plataforma,38.72,-9.15,S\n", "trips.txt": "route_id,service_id,trip_id,trip_headsign,shape_id\n1,svc,T,Centro,shape\n", "stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT,25:10:00,25:10:00,P,1\nT,25:20:00,25:20:00,P,2\n", "calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nsvc,1,1,1,1,1,1,1,20261001,20261031\n", "calendar_dates.txt": "service_id,date,exception_type\nsvc,20261025,2\nsvc,20260930,1\n", "shapes.txt": "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\nshape,38.72,-9.15,1\nshape,38.73,-9.15,2\n"}
	for name, data := range files {
		w, _ := z.Create(name)
		_, _ = io.WriteString(w, data)
	}
	_ = z.Close()
	p, _ := providerByID("carris")
	date := time.Date(2026, 10, 24, 12, 0, 0, 0, lisbon)
	d, e := readGTFS(archive.Bytes(), p, "plan", "20260901", "20261101", hubBase, date)
	if e != nil {
		t.Fatal(e)
	}
	if d.Schedule.active("svc", date.AddDate(0, 0, 1)) {
		t.Fatal("removal exception ignored")
	}
	if !d.Schedule.active("svc", time.Date(2026, 9, 30, 12, 0, 0, 0, lisbon)) {
		t.Fatal("addition exception ignored")
	}
	start := serviceStart(date).Add(25 * time.Hour)
	trips, _, err := (scheduleQuery{ctx: context.Background(), data: d, operator: "carris", filter: Filter{From: start, To: start.Add(time.Hour)}}).run()
	if err != nil {
		t.Fatal(err)
	}
	_, arrivals, err := (scheduleQuery{ctx: context.Background(), data: d, operator: "carris", filter: Filter{From: start, To: start.Add(time.Hour), Stop: "carris:S"}}).run()
	if err != nil {
		t.Fatal(err)
	}
	if len(trips) != 1 || len(arrivals) != 2 || trips[0].PlannedDeparture.In(lisbon).Hour() != 1 {
		t.Fatalf("midnight/DST parent arrival failure %+v %+v", trips, arrivals)
	}
	if seconds, e := parseClock("25:10:00"); e != nil || seconds != 90600 {
		t.Fatal("extended time parse")
	}
	if _, e = parseClock("01:75:00"); e == nil {
		t.Fatal("invalid time accepted")
	}
}
func TestSampledSpeedProvenance(t *testing.T) {
	now := time.Now()
	a := api.Vehicle{Lat: 38.72, Lon: -9.15, ObservedAt: now, PositionKind: api.VehiclePositionKindReported}
	b := a
	b.ObservedAt = now.Add(30 * time.Second)
	b.Lat += .001
	d, speed := sampledDistance(a, b)
	if d == nil || speed == nil || *speed < 12 || *speed > 14 {
		t.Fatal("distance/speed incorrect")
	}
	b.PositionKind = api.VehiclePositionKindEstimated
	if d, s := sampledDistance(a, b); d != nil || s != nil {
		t.Fatal("Metro estimate contaminated speed")
	}
	b.PositionKind = api.VehiclePositionKindReported
	b.Lat = 39
	if d, s := sampledDistance(a, b); d != nil || s != nil {
		t.Fatal("teleport accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUpstreamBudgetConcurrent(t *testing.T) {
	transport := NewBudgetTransport(1000)
	now := time.Now()
	transport.now = func() time.Time { return now }
	var count atomic.Int32
	transport.Base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		count.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := http.NewRequest("GET", "https://provider.test", nil)
			res, e := transport.RoundTrip(r)
			if e == nil {
				res.Body.Close()
			}
		}()
	}
	wg.Wait()
	if count.Load() != 900 {
		t.Fatalf("upstream budget allowed%d want900", count.Load())
	}
	r, _ := http.NewRequest("GET", "https://provider.test/token", nil)
	now = now.Add(59 * time.Second)
	if _, err := transport.RoundTrip(r); err == nil {
		t.Fatal("minute boundary must not reset the rolling budget")
	}
	now = now.Add(time.Second)
	res, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if count.Load() != 901 {
		t.Fatal("budget must recover after 60 seconds")
	}
}

func TestMetroTokenAndPredictions(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	now := time.Now()
	var calls atomic.Int32
	var tokens atomic.Int32
	failReads := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			user, password, ok := r.BasicAuth()
			if !ok || user != "fixture-id" || password != "fixture-secret" {
				t.Error("Basic credential flow")
			}
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" {
				t.Error("wrong grant")
			}
			tokens.Add(1)
			fmt.Fprint(w, `{"access_token":"fixture-token","expires_in":38,"token_type":"Bearer"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing bearer")
		}
		if failReads {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"code":"900908"}`)
			return
		}
		switch r.URL.Path {
		case "/estadoLinha/todos":
			fmt.Fprint(w, `{"codigo":"200","resposta":{"azul":" Ok","amarela":" Ok","verde":" Ok","vermelha":" Ok","azul_curta":"normal","amarela_curta":"normal","verde_curta":"normal","vermelha_curta":"normal"}}`)
		case "/tempoEspera/Estacao/todos":
			fmt.Fprintf(w, `{"codigo":"200","resposta":[{"stop_id":"RM","cais":"one","hora":"%s","comboio":"26C","tempoChegada1":395,"comboio2":"21C","tempoChegada2":"--","comboio3":"22C","tempoChegada3":1346,"destino":"54"},{"stop_id":"RM","cais":"duplicate","hora":"%s","comboio":"26C","tempoChegada1":395,"destino":"54"},{"stop_id":"RM","hora":"20260101000000","comboio":"old","tempoChegada1":120,"destino":"54"}]}`, now.In(lisbon).Format("20060102150405"), now.In(lisbon).Format("20060102150405"))
		case "/infoEstacao/todos":
			fmt.Fprint(w, `{"codigo":"200","resposta":[{"stop_id":"RM","stop_name":"Roma","stop_lat":"38.748","stop_lon":"-9.14","linha":"[Verde]"},{"stop_id":"CS","stop_name":"Cais do Sodré","stop_lat":"38.705","stop_lon":"-9.144","linha":"[Verde]"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	m := NewMetroClient(upstream.Client(), store, cache, "fixture-id", "fixture-secret")
	m.Base = upstream.URL
	m.TokenURL = upstream.URL + "/token"
	d := m.Refresh(context.Background())
	if d.Status.Status != "ok" || len(d.Status.Lines) != 4 || tokens.Load() != 1 || calls.Load() != 4 {
		t.Fatalf("Metro flow failed %+v calls%d", d.Status, calls.Load())
	}
	for i := 0; i < 100; i++ {
		m.Refresh(context.Background())
	}
	if calls.Load() != 4 {
		t.Fatal("UI request multiplied upstream traffic")
	}
	state, _ := cache.state("")
	out := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:RM")
	if len(out) != 2 || out[0].Kind != "prediction" || out[0].ScheduledAt != nil || out[0].Headsign != "Cais do Sodré" {
		t.Fatalf("invalid prediction/stale/dedup result %+v", out)
	}
	m.lastAttempt = time.Now().Add(-time.Minute)
	m.expires = time.Now().Add(-time.Second)
	m.Refresh(context.Background())
	if tokens.Load() != 2 {
		t.Fatal("expired token was reused")
	}
	failReads = true
	m.lastAttempt = time.Now().Add(-time.Minute)
	d = m.Refresh(context.Background())
	if d.Status.Status != "error" {
		t.Fatal("403 did not mark direct API unavailable")
	}
	state, _ = cache.state("")
	if len(predictedArrivals(state, now, now.Add(time.Hour), "", "metro:RM")) != 0 {
		t.Fatal("failed refresh claimed current predictions")
	}
}
func TestFrozenPredictionEligibility(t *testing.T) {
	asOf := time.Now().Add(-2 * time.Minute)
	state := &State{Created: asOf, Metro: &MetroData{Status: api.MetroStatus{Status: "ok", CheckedAt: &asOf}, Stations: []MetroStation{{ID: "RM", Name: "Roma", Lines: "[Verde]"}}, Waits: []MetroWait{{Stop: "RM", At: asOf.In(lisbon).Format("20060102150405"), Train: "1", Destination: "54", Wait1: json.RawMessage("120")}}}}
	if out := predictedArrivals(state, asOf, asOf.Add(time.Hour), "", "metro:RM"); len(out) != 1 || out[0].RouteName == nil || *out[0].RouteName != "Linha Verde" {
		t.Fatal("frozen prediction aged out with wall clock", out)
	}
	state.Created = asOf.Add(91 * time.Second)
	if out := predictedArrivals(state, asOf, asOf.Add(time.Hour), "", "metro:RM"); len(out) != 0 {
		t.Fatal("fresh collection accepted stale prediction")
	}
}
func TestSpecScopePolicy(t *testing.T) {
	spec, e := api.GetSwagger()
	if e != nil {
		t.Fatal(e)
	}
	if e = spec.Validate(context.Background()); e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for path, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			ids = append(ids, op.OperationID)
			if strings.Contains(path, "/keys") && op.Extensions["x-session-only"] != true {
				t.Fatal("key management missing session policy")
			}
			if op.Extensions["x-public-read"] == true {
				if _, ok := op.Extensions["x-required-scopes"]; !ok {
					t.Fatal("public read missing optional-key scope enforcement")
				}
			}
		}
	}
	sort.Strings(ids)
	if len(ids) != 29 {
		t.Fatalf("unexpected operation count%d", len(ids))
	}
}

func TestPublishedFleetMetadataCrosswalk(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	now := time.Now()
	for _, id := range []string{"mobi", "cm"} {
		op := cache.operator(id)
		op.StaticStatus = api.OperatorStaticStatusError
		op.StaticError = ptr("GTFS failed")
		cache.update(id, &StaticData{Models: map[string]Metadata{}, Updated: now}, nil, op)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"agency_id":"HF16N","vehicle_id":"21-3001","make":"MERCEDES BENZ","model":"SPRINTER","license_plate":"AG64UI"},{"agency_id":"BNA17","vehicle_id":"42-123","make":"IVECO","model":"65SG"},{"agency_id":"UNKNOWN","vehicle_id":"21-bad","model":"FAKE"},{"agency_id":"HF16N","vehicle_id":"42-conflict","model":"FAKE"}]`)
	}))
	defer upstream.Close()
	fetcher := NewFetcher(store, cache, zap.NewNop())
	fetcher.Hub = upstream.URL
	fetcher.refreshMetadata(context.Background())
	state, _ := cache.state("")
	if state.Operators["mobi"].StaticStatus != "error" || state.Operators["mobi"].StaticError == nil {
		t.Fatal("metadata masked GTFS failure")
	}
	if len(state.Static["mobi"].Models) != 1 || state.Static["mobi"].Models["3001"].Plate != "AG64UI" {
		t.Fatal("Mobi crosswalk failed")
	}
	if len(state.Static["cm"].Models) != 1 || state.Static["cm"].Models["[BNA17]123"].Model != "IVECO 65SG" {
		t.Fatal("CM crosswalk failed")
	}
}

func TestScheduleRevisionAndLegacyCache(t *testing.T) {
	for _, raw := range []string{`{"Stop":"S","Arrival":43200,"Departure":43300,"Sequence":1}`, `{"s":"S","a":43200,"d":43300,"q":1}`} {
		var st StopTime
		if e := json.Unmarshal([]byte(raw), &st); e != nil || st.Arrival != 43200 || st.Stop != "S" {
			t.Fatalf("schedule cache migration %s %+v", raw, st)
		}
	}
	store := testStore(t)
	cache := NewCache()
	now := time.Now()
	op := cache.operator("carris")
	d := fixtureStatic("carris", now)
	base := serviceStart(now.In(lisbon))
	secs := int(now.Sub(base).Seconds()) + 10
	d.Schedule.Trips[0].Times = []StopTime{{Stop: "S", Arrival: int32(secs), Departure: int32(secs), Sequence: 1}, {Stop: "S", Arrival: int32(secs + 60), Departure: int32(secs + 60), Sequence: 2}}
	cache.update("carris", d, nil, op)
	server, e := NewServer(store, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true, RateLimit: 1000}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	handler, e := server.Handler()
	if e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	status, body := req(t, http.DefaultClient, "GET", ts.URL+"/api/v1/arrivals?operators=carris&stop_id=carris:S&limit=1", nil, nil)
	expectStatus(t, status, 200, body)
	first := decode[api.ArrivalPage](t, body)
	if first.Page.Total != 2 || first.Page.Revision == nil {
		t.Fatal(string(body))
	}
	cache.update("carris", fixtureStatic("carris", now), nil, op)
	u := ts.URL + "/api/v1/arrivals?operators=carris&stop_id=carris:S&limit=1&offset=1&revision=" + url.QueryEscape(*first.Page.Revision)
	status, body = req(t, http.DefaultClient, "GET", u, nil, nil)
	expectStatus(t, status, 200, body)
	second := decode[api.ArrivalPage](t, body)
	if second.Page.Total != 2 || len(second.Data) != 1 || second.Data[0].Id == first.Data[0].Id || *second.Page.Revision != *first.Page.Revision {
		t.Fatal("schedule page membership changed", string(body))
	}
	status, body = req(t, http.DefaultClient, "GET", u+"&from="+url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339Nano)), nil, nil)
	expectStatus(t, status, 400, body)
}
func TestRevisionRestoresOmittedBoundsBeforeValidation(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	cache.update("metro", &StaticData{Stops: []api.Stop{{Id: "metro:RM", OperatorId: "metro"}}}, nil, cache.operator("metro"))
	server, e := NewServer(store, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true, RateLimit: 1000}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	handler, e := server.Handler()
	if e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	from, to := time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	for _, endpoint := range []string{"arrivals?operators=metro&stop_id=metro:RM&", "history?operators=carris&"} {
		firstURL := ts.URL + "/api/v1/" + endpoint + "from=" + url.QueryEscape(from) + "&to=" + url.QueryEscape(to)
		status, body := req(t, http.DefaultClient, "GET", firstURL, nil, nil)
		expectStatus(t, status, 200, body)
		var response struct{ Page api.Page }
		if e := json.Unmarshal(body, &response); e != nil {
			t.Fatal(e)
		}
		nextURL := ts.URL + "/api/v1/" + endpoint + "to=" + url.QueryEscape(to) + "&revision=" + url.QueryEscape(*response.Page.Revision)
		status, body = req(t, http.DefaultClient, "GET", nextURL, nil, nil)
		expectStatus(t, status, 200, body)
	}
}
func TestVerifiedHubPrefixes(t *testing.T) {
	if verifiedHubID("[IA9T6]118_0", "IA9T6") != "118_0" || verifiedHubID("[OTHER]118_0", "IA9T6") != "[OTHER]118_0" {
		t.Fatal("agency crosswalk")
	}
	id, plan := verifiedHubTrip("[P][IA9T6]T", "IA9T6", "P")
	if id != "T" || plan == nil || *plan != "P" {
		t.Fatal("verified plan")
	}
	for _, raw := range []string{"[OLD][IA9T6]T", "[P][OTHER]T", "[P][IA9T6][EXTRA]T"} {
		id, plan = verifiedHubTrip(raw, "IA9T6", "P")
		if id != raw || plan == nil {
			t.Fatal("unverified join", raw)
		}
	}
}
