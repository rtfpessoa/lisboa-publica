package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func securityServer(t *testing.T, store *Store, cache *Cache) (*Server, http.Handler) {
	t.Helper()
	s, err := NewServer(store, cache, Options{Origin: "https://example.test", Environment: "production", PublicReads: true, RateLimit: 1000}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}

func securityRequest(h http.Handler, method, path, session string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Origin", "https://example.test")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		r.AddCookie(&http.Cookie{Name: "lp_session", Value: session})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func seedSecuritySession(t *testing.T, store *Store) string {
	t.Helper()
	token := "synthetic-session"
	_, err := store.DB.Exec(context.Background(), "INSERT INTO sessions(token_hash,email,name,auth_kind,expires_at) VALUES($1,$2,'fixture','google',$3)", tokenHash(token), "fixture@example.test", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestConcurrentKeyQuota(t *testing.T) {
	store := testStore(t)
	session := seedSecuritySession(t, store)
	for i := 0; i < 19; i++ {
		_, err := store.DB.Exec(context.Background(), "INSERT INTO api_keys(id,owner_email,name,token_hash,scopes,created_at,expires_at) VALUES($1,$2,'fixture',$3,$4,$5,$6)", fmt.Sprint(i), "fixture@example.test", fmt.Sprint(i), []string{"read:transit"}, time.Now(), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, h := securityServer(t, store, NewCache())
	start := make(chan struct{})
	var wg sync.WaitGroup
	var created, limited atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := securityRequest(h, "POST", "/api/v1/keys", session, []byte(`{"name":"fixture","scopes":["read:transit"]}`))
			switch w.Code {
			case 201:
				created.Add(1)
			case 400:
				if strings.Contains(w.Body.String(), `"key_limit"`) {
					limited.Add(1)
				} else {
					t.Error(w.Body.String())
				}
			default:
				t.Errorf("create: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	// Hold the existing write lock so concurrent handlers reach the same quota boundary.
	store.writeMu.Lock()
	close(start)
	time.Sleep(100 * time.Millisecond)
	store.writeMu.Unlock()
	wg.Wait()
	var count int
	if err := store.DB.QueryRow(context.Background(), "SELECT count(*) FROM api_keys WHERE NOT revoked").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if created.Load() != 1 || limited.Load() != 7 || count != 20 {
		t.Fatalf("quota: created=%d limited=%d count=%d", created.Load(), limited.Load(), count)
	}
}

func TestCredentialCleanupHeadroom(t *testing.T) {
	for _, test := range []struct {
		name    string
		bytes   int64
		failure bool
		want    int
	}{
		{"operational ceiling", operationalDatabaseBytes, false, 204},
		{"full ceiling", maximumDatabaseBytes, false, 500},
		{"measurement unavailable", 0, true, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t)
			session := seedSecuritySession(t, store)
			_, err := store.DB.Exec(context.Background(), "INSERT INTO api_keys(id,owner_email,name,token_hash,scopes,created_at,expires_at) VALUES('fixture','fixture@example.test','fixture','fixture',$1,$2,$3)", []string{"read:transit"}, time.Now(), time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			store.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) {
				if test.failure {
					return 0, errors.New("unavailable")
				}
				return test.bytes, nil
			}}
			_, h := securityServer(t, store, NewCache())
			w := securityRequest(h, "POST", "/api/v1/keys", session, []byte(`{"name":"fixture","scopes":["read:transit"]}`))
			if w.Code == 201 {
				t.Fatal("issuance bypassed operational guard")
			}
			w = securityRequest(h, "DELETE", "/api/v1/keys/fixture", session, nil)
			if w.Code != test.want {
				t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
			}
			var revoked bool
			if err = store.DB.QueryRow(context.Background(), "SELECT revoked FROM api_keys WHERE id='fixture'").Scan(&revoked); err != nil {
				t.Fatal(err)
			}
			if revoked != (test.want == 204) {
				t.Fatalf("revoked=%v", revoked)
			}
			w = securityRequest(h, "POST", "/api/v1/auth/logout", session, nil)
			if w.Code != test.want {
				t.Fatalf("logout: %d %s", w.Code, w.Body.String())
			}
			cleared := false
			for _, c := range w.Result().Cookies() {
				if c.Name == "lp_session" && c.MaxAge < 0 && c.Secure && c.HttpOnly {
					cleared = true
				}
			}
			if !cleared {
				t.Fatal("browser cookie not cleared on valid-origin logout")
			}
			var remaining int
			if err = store.DB.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if (remaining == 0) != (test.want == 204) {
				t.Fatalf("remaining=%d", remaining)
			}
			foreign := httptest.NewRequest("DELETE", "/api/v1/keys/fixture", nil)
			foreign.Header.Set("Origin", "https://foreign.example")
			foreign.AddCookie(&http.Cookie{Name: "lp_session", Value: session})
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, foreign)
			if test.want != 204 && recorder.Code != 403 {
				t.Fatalf("foreign origin: %d", recorder.Code)
			}
		})
	}
}

func TestArchiveBudgetAndNames(t *testing.T) {
	for _, test := range []struct {
		name                string
		extra               int
		overflow, duplicate bool
	}{
		{"overflow", 0, true, false}, {"entry count", 257, false, false}, {"duplicate", 0, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			for i, name := range []string{"routes.txt", "stops.txt", "trips.txt", "stop_times.txt", "calendar.txt"} {
				size := uint64(0)
				if test.overflow {
					if i == 0 {
						size = 128
					}
					if i == 1 {
						size = ^uint64(0) - 100
					}
				}
				if _, err := z.CreateRaw(&zip.FileHeader{Name: name, UncompressedSize64: size, Method: zip.Store}); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < test.extra; i++ {
				if _, err := z.Create(fmt.Sprintf("extra%d.txt", i)); err != nil {
					t.Fatal(err)
				}
			}
			if test.duplicate {
				if _, err := z.Create("./routes.txt"); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := openGTFS(b.Bytes()); err == nil {
				t.Fatal("malformed archive accepted")
			}
		})
	}
	if _, err := openGTFS(shapeArchive(t, false)); err != nil {
		t.Fatal("valid archive:", err)
	}
}

func TestScheduleCancellationAndStopValidation(t *testing.T) {
	now := time.Now().UTC()
	cache := NewCache()
	for _, p := range []string{"carris", "tcb"} {
		cache.update(p, fixtureStatic(p, now), nil, cache.operator(p))
	}
	_, h := securityServer(t, nil, cache)
	for _, path := range []string{"/api/v1/arrivals?stop_id=carris:missing", "/api/v1/arrivals?stop_id=carris:S&operators=tcb", "/api/v1/arrivals?stop_id=metro:missing"} {
		w := securityRequest(h, "GET", path, "", nil)
		if w.Code != 404 && w.Code != 400 {
			t.Fatalf("unknown/incompatible stop: %d %s", w.Code, w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/v1/arrivals?stop_id=carris:S", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("canceled schedule returned success")
	}
	w = securityRequest(h, "GET", "/api/v1/arrivals?stop_id=carris:S", "", nil)
	if w.Code != 200 {
		t.Fatalf("known stop: %d %s", w.Code, w.Body.String())
	}
	result := decode[api.ArrivalPage](t, w.Body.Bytes())
	for _, a := range result.Data {
		if a.OperatorId != "carris" {
			t.Fatal("unrelated schedule scanned")
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	_, h := securityServer(t, nil, NewCache())
	w := securityRequest(h, "GET", "/api/v1/operators", "", nil)
	if w.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("missing HTTPS security headers")
	}
	if w.Header().Get("Permissions-Policy") != "camera=(), microphone=(), geolocation=(self)" {
		t.Fatal("unexpected permissions policy")
	}
	s, err := NewServer(nil, NewCache(), Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	h, err = s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	if securityRequest(h, "GET", "/api/v1/operators", "", nil).Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS on HTTP development origin")
	}
}

func TestExpensiveReadAdmissionAndRelease(t *testing.T) {
	s, h := securityServer(t, nil, NewCache())
	for range maxExpensiveReads {
		s.expensiveReads <- struct{}{}
	}
	for _, path := range []string{"trips", "arrivals?stop_id=carris:S", "vehicles/cp:v/calls", "metrics", "history", "fleet", "traffic", "rankings", "operator-coverage"} {
		w := securityRequest(h, "GET", "/api/v1/"+path, "", nil)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"busy"`) || w.Header().Get("Retry-After") == "" {
			t.Fatalf("admission %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := securityRequest(h, "GET", "/api/v1/operators", "", nil); w.Code != 200 {
		t.Fatalf("cheap endpoint blocked: %d", w.Code)
	}
	for range maxExpensiveReads {
		<-s.expensiveReads
	}
	for range maxExpensiveReads + 1 {
		w := securityRequest(h, "GET", "/api/v1/arrivals?stop_id=carris:missing", "", nil)
		if w.Code != 404 {
			t.Fatalf("error slot not released: %d", w.Code)
		}
	}
	if w := securityRequest(h, "GET", "/api/v1/trips", "", nil); w.Code != 200 {
		t.Fatalf("slot not released: %d", w.Code)
	}
}

func TestDatabaseStatementTimeoutAndCancellation(t *testing.T) {
	store := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	var slept any
	if err := store.DB.QueryRow(ctx, "SELECT pg_sleep(1)").Scan(&slept); err == nil || ctx.Err() == nil {
		t.Fatalf("query did not exit on its context deadline: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("canceled query did not exit promptly")
	}
	var value string
	if err := store.DB.QueryRow(context.Background(), "SHOW statement_timeout").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "0" && value != "0s" {
		t.Fatalf("pool timeout changed: %s", value)
	}
	tx, err := store.readTransaction(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(context.Background(), "SHOW statement_timeout").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "15s" && value != "15000" {
		t.Fatalf("read statement timeout: %s", value)
	}
	if err = tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Explicitly set a small timeout on one borrowed connection and prove server cancellation.
	conn, err := store.DB.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(context.Background(), "SET statement_timeout='20ms'"); err != nil {
		t.Fatal(err)
	}
	err = conn.QueryRow(context.Background(), "SELECT pg_sleep(1)").Scan(&slept)
	var canceled *pgconn.PgError
	if !errors.As(err, &canceled) || canceled.Code != "57014" {
		t.Fatalf("expected database statement cancellation, got: %v", err)
	}
	if _, err = conn.Exec(context.Background(), "SET statement_timeout='0'"); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectRetainsProviderOrigin(t *testing.T) {
	for _, tc := range []struct {
		initial, next string
		allowed       bool
	}{
		{"https://objectstorage.eu-frankfurt-1.oraclecloud.com/a", "https://objectstorage.eu-frankfurt-1.oraclecloud.com/b", true},
		{"https://objectstorage.eu-frankfurt-1.oraclecloud.com/a", "https://objectstorage.eu-frankfurt-1.oraclecloud.com:443/b", true},
		{"https://api.metrolisboa.pt:8243/token", "https://api.metrolisboa.pt:8243/new", true},
		{"https://api.metrolisboa.pt:8243/token", "https://api.metrolisboa.pt/new", false},
		{"https://trusted.test/a", "https://different.test/b", false},
		{"https://trusted.test/a", "http://trusted.test/b", false},
		{"https://trusted.test/a", "https://user@trusted.test/b", false},
		{"https://trusted.test/a", "https://trusted.test:444/b", false},
	} {
		t.Run(tc.next, func(t *testing.T) {
			initial, _ := http.NewRequest("GET", tc.initial, nil)
			next, _ := http.NewRequest("GET", tc.next, nil)
			err := CheckUpstreamRedirect(next, []*http.Request{initial})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, err)
			}
			if CheckUpstreamRedirect(next, []*http.Request{initial, initial, initial}) == nil {
				t.Fatal("redirect count ignored")
			}
		})
	}
}

func TestArchiveInitialOriginPolicy(t *testing.T) {
	fetcher := NewFetcher(nil, NewCache(), zap.NewNop())
	for _, raw := range []string{"https://user@objectstorage.eu-frankfurt-1.oraclecloud.com/a", "https://objectstorage.eu-frankfurt-1.oraclecloud.com:8243/a", "https://other.test/a", "http://objectstorage.eu-frankfurt-1.oraclecloud.com/a"} {
		if _, err := fetcher.fetchHubArchive(context.Background(), &hubPlan{URL: raw}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestParentStopUsesPinnedNetworkRevision(t *testing.T) {
	now := time.Now()
	cache := NewCache()
	data := fixtureStatic("carris", now)
	data.Schedule.Parents["S"] = "parent"
	cache.update("carris", data, nil, cache.operator("carris"))
	_, h := securityServer(t, nil, cache)
	from := serviceStart(now).Add(12 * time.Hour)
	path := "/api/v1/arrivals?operators=carris&stop_id=carris:parent&from=" + url.QueryEscape(from.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(from.Add(time.Hour).Format(time.RFC3339Nano)) + "&limit=1"
	first := securityRequest(h, "GET", path, "", nil)
	if first.Code != 200 {
		t.Fatalf("parent first page: %d %s", first.Code, first.Body.String())
	}
	page := decode[api.ArrivalPage](t, first.Body.Bytes())
	if page.Page.Total != 2 || !page.Page.HasMore {
		t.Fatalf("parent page: %+v", page)
	}
	cache.update("carris", &StaticData{}, nil, cache.operator("carris"))
	next := securityRequest(h, "GET", path+"&offset=1&revision="+url.QueryEscape(*page.Page.Revision), "", nil)
	if next.Code != 200 {
		t.Fatalf("pinned parent page: %d %s", next.Code, next.Body.String())
	}
	second := decode[api.ArrivalPage](t, next.Body.Bytes())
	if second.Page.Total != 2 || second.Data[0].Id == page.Data[0].Id {
		t.Fatal("parent pagination changed")
	}
	if w := securityRequest(h, "GET", path, "", nil); w.Code != 404 {
		t.Fatalf("current removed stop: %d", w.Code)
	}
}

func TestScheduleResultCeiling(t *testing.T) {
	now := time.Now()
	data := fixtureStatic("carris", now)
	trip := data.Schedule.Trips[0]
	data.Schedule.Trips = make([]ScheduledTrip, maxReadResults+1)
	for i := range data.Schedule.Trips {
		data.Schedule.Trips[i] = trip
		data.Schedule.Trips[i].ID = fmt.Sprint(i)
	}
	from := serviceStart(now).Add(12 * time.Hour)
	_, _, err := (scheduleQuery{ctx: context.Background(), data: data, operator: "carris", filter: Filter{From: from, To: from.Add(time.Hour)}}).run()
	var ae *apiError
	if !errors.As(err, &ae) || ae.Code != "result_limit" {
		t.Fatalf("result ceiling: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = (scheduleQuery{ctx: canceled, data: data, operator: "carris", filter: Filter{From: from, To: from.Add(time.Hour)}}).run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestLogoutClearsCookieOnAuthenticationFailure(t *testing.T) {
	store := testStore(t)
	session := seedSecuritySession(t, store)
	_, h := securityServer(t, store, NewCache())
	store.DB.Close()
	w := securityRequest(h, "POST", "/api/v1/auth/logout", session, nil)
	if w.Code == 204 {
		t.Fatal("reported successful server invalidation with closed database")
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "lp_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("authentication failure prevented cookie clearing")
	}
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.Header.Set("Origin", "https://foreign.test")
	r.AddCookie(&http.Cookie{Name: "lp_session", Value: session})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("foreign origin cleared cookie")
	}
}

func TestHistoricalResultCeiling(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	// Valid Lisbon coordinates, one distinct vehicle/route/spatial cell per synthetic observation.
	_, err := store.DB.Exec(ctx, `INSERT INTO snapshots(vehicle_id,operator_id,route_id,observed_at,lat,lon,speed_kmh,position_kind,payload,generation)
 SELECT 'carris:'||i::TEXT,'carris','carris:'||i::TEXT,$1,38.3001+(i%500)*0.001,-9.6001+floor(i/500.0)*0.001,10,'reported','{"source_id":"fixture"}'::JSONB,0 FROM generate_series(1,$2) AS g(i)`, now, maxReadResults+1)
	if err != nil {
		t.Fatal(err)
	}
	_, h := securityServer(t, store, NewCache())
	for _, path := range []string{"fleet", "fleet?q=no-matches", "traffic", "rankings"} {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		target := "/api/v1/" + path + sep + "operators=carris&limit=1&from=" + url.QueryEscape(now.Add(-time.Minute).Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Add(time.Second).Format(time.RFC3339))
		w := securityRequest(h, "GET", target, "", nil)
		t.Logf("%s oversized response: %d %s", path, w.Code, w.Body.String())
		bounded := w.Code == 400 && strings.Contains(w.Body.String(), `"result_limit"`)
		timedOut := w.Code == 503 && strings.Contains(w.Body.String(), `"request_timeout"`)
		if !bounded && !timedOut {
			t.Fatalf("%s ceiling: %d %s", path, w.Code, w.Body.String())
		}
	}
	if _, err = store.DB.Exec(ctx, "DELETE FROM snapshots WHERE vehicle_id=$1", fmt.Sprintf("carris:%d", maxReadResults+1)); err != nil {
		t.Fatal(err)
	}
	w := securityRequest(h, "GET", "/api/v1/fleet?operators=carris&limit=1&from="+url.QueryEscape(now.Add(-time.Minute).Format(time.RFC3339))+"&to="+url.QueryEscape(now.Add(time.Second).Format(time.RFC3339)), "", nil)
	if w.Code != 200 {
		t.Fatalf("ceiling boundary: %d %s", w.Code, w.Body.String())
	}
	page := decode[api.FleetVehiclePage](t, w.Body.Bytes())
	if page.Page.Total != maxReadResults || len(page.Data) != 1 || !page.Page.HasMore {
		t.Fatal("within-bound pagination lost accurate total")
	}
}

func TestDirectMetroStationValidation(t *testing.T) {
	cache := NewCache()
	now := time.Now()
	cache.updateMetro(&MetroData{Stations: []MetroStation{{ID: "RM", Name: "Roma"}}, Status: api.MetroStatus{Status: "ok", CheckedAt: &now}}, cache.operator("metro"))
	_, h := securityServer(t, nil, cache)
	if w := securityRequest(h, "GET", "/api/v1/arrivals?stop_id=metro:RM", "", nil); w.Code != 200 {
		t.Fatalf("direct station lost fallback: %d %s", w.Code, w.Body.String())
	}
	if w := securityRequest(h, "GET", "/api/v1/arrivals?stop_id=metro:unknown", "", nil); w.Code != 404 {
		t.Fatalf("unknown direct station: %d", w.Code)
	}
}

func TestRedirectPolicyBudgetsActualAttempts(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		budget := NewBudgetTransport(900)
		calls := 0
		budget.Base = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path == "/first" {
				target := "https://provider.test/second"
				if foreign {
					target = "https://different.test/second"
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
		})
		client := &http.Client{Transport: budget, CheckRedirect: CheckUpstreamRedirect}
		res, err := client.Get("https://provider.test/first")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := 2
		if foreign {
			want = 1
		}
		if calls != want || len(budget.requests) != want {
			t.Fatalf("foreign=%v attempts=%d budget=%d", foreign, calls, len(budget.requests))
		}
	}
}

func TestMetroReadsDoNotWaitForCollectorLocks(t *testing.T) {
	now := time.Now()
	cache := NewCache()
	store := &Store{}
	cache.update("metro", fixtureStatic("metro", now), nil, cache.operator("metro"))
	cache.updateMetro(&MetroData{Status: api.MetroStatus{Status: "ok", CheckedAt: &now, Lines: []api.MetroLine{}}}, cache.operator("metro"))
	s, h := securityServer(t, store, cache)
	s.Metro = NewMetroClient(nil, store, cache, "synthetic-id", "synthetic-secret")
	s.Metro.mu.Lock()
	defer s.Metro.mu.Unlock()
	store.PublishMu.Lock()
	defer store.PublishMu.Unlock()
	for _, path := range []string{"/api/v1/arrivals?stop_id=metro:S", "/api/v1/metro/status"} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { h.ServeHTTP(w, r); close(done) }()
		select {
		case <-done:
			if w.Code != 200 && w.Code != 503 {
				t.Errorf("cached read: %d %s", w.Code, w.Body.String())
			}
			if len(s.expensiveReads) != 0 {
				t.Error("collector lock retained expensive-read slot")
			}
		case <-time.After(200 * time.Millisecond):
			cancel()
			t.Fatal("request waited on the collector lock despite cancellation")
		}
		cancel()
	}
}

func TestCachedMetroStatusFreshness(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		checked *time.Time
		want    api.MetroStatusStatus
	}{
		{"fresh", ptr(now), api.MetroStatusStatusOk},
		{"old", ptr(now.Add(-sourceFreshness - time.Minute)), api.MetroStatusStatusError},
		{"unknown time", nil, api.MetroStatusStatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewCache()
			original := api.MetroStatus{Status: api.MetroStatusStatusOk, Message: "Normal", CheckedAt: tc.checked, Lines: []api.MetroLine{{}}}
			cache.updateMetro(&MetroData{Status: original}, cache.operator("metro"))
			s, h := securityServer(t, nil, cache)
			s.Metro = NewMetroClient(nil, nil, cache, "synthetic-id", "synthetic-secret")
			w := securityRequest(h, "GET", "/api/v1/metro/status", "", nil)
			if w.Code != 200 {
				t.Fatalf("cached status: %d %s", w.Code, w.Body.String())
			}
			status := decode[api.MetroStatus](t, w.Body.Bytes())
			if status.Status != tc.want {
				t.Fatalf("status=%s want%s", status.Status, tc.want)
			}
			if tc.want == api.MetroStatusStatusError && (len(status.Lines) != 0 || !strings.Contains(status.Message, "desatualizados")) {
				t.Fatal("stale normal service exposed")
			}
			if tc.want == api.MetroStatusStatusOk && (len(status.Lines) != 1 || status.Message != "Normal") {
				t.Fatal("fresh service changed")
			}
			state, _ := cache.state("")
			if state.Metro.Status.Status != api.MetroStatusStatusOk || state.Metro.Status.Message != "Normal" || len(state.Metro.Status.Lines) != 1 {
				t.Fatal("HTTP status mutated immutable cached revision")
			}
		})
	}
}
