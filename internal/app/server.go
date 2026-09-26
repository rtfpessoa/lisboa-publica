package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"go.uber.org/zap"
	"google.golang.org/api/idtoken"
	"lisboapublica/internal/api"
)

// Options configures the public origin, authentication and request limits.
type Options struct {
	Origin, GoogleClientID, Environment, FrontendDir string
	DevAuth, PublicReads                             bool
	RateLimit                                        int
	TrustedProxyCIDRs                                string
}

// Server implements the generated API using cached feeds and retained observations.
type Server struct {
	Metro          *MetroClient
	Store          *Store
	Cache          *Cache
	Options        Options
	Log            *zap.Logger
	VerifyGoogle   func(context.Context, string, string) (*idtoken.Payload, error)
	rate           *limiter
	trustedProxies []netip.Prefix
}
type apiError struct {
	Status        int
	Code, Message string
}

func (e *apiError) Error() string                 { return e.Message }
func fail(status int, code, message string) error { return &apiError{status, code, message} }

// NewServer validates configuration and constructs an API server.
func NewServer(store *Store, cache *Cache, options Options, log *zap.Logger) (*Server, error) {
	u, e := url.Parse(options.Origin)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" {
		return nil, fmt.Errorf("PUBLIC_ORIGIN must be an origin without a path")
	}
	if options.Environment == "" {
		options.Environment = "production"
	}
	if options.DevAuth && options.Environment != "development" {
		return nil, fmt.Errorf("DEV_AUTH requires ENVIRONMENT=development")
	}
	if options.Environment == "production" && u.Scheme != "https" {
		return nil, fmt.Errorf("production PUBLIC_ORIGIN requires HTTPS")
	}
	if options.RateLimit <= 0 {
		options.RateLimit = defaultReadRate
	}
	s := &Server{Store: store, Cache: cache, Options: options, Log: log, VerifyGoogle: idtoken.Validate, rate: &limiter{entries: map[string]rateEntry{}, limit: options.RateLimit}}
	for _, cidr := range strings.Split(options.TrustedProxyCIDRs, ",") {
		if strings.TrimSpace(cidr) == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS: %w", err)
		}
		s.trustedProxies = append(s.trustedProxies, prefix)
	}
	return s, nil
}
func (s *Server) error(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		s.Log.Error("API operation failed", zap.String("path", r.URL.Path), zap.Error(err))
		ae = &apiError{http.StatusInternalServerError, "internal", "Erro interno."}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if ae.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	w.WriteHeader(ae.Status)
	_ = json.NewEncoder(w).Encode(api.Error{Code: ae.Code, Message: ae.Message})
}

// Handler returns the generated API with validation, authentication and rate limiting.
func (s *Server) Handler() (http.Handler, error) {
	spec, e := api.GetSwagger()
	if e != nil {
		return nil, e
	}
	spec.Servers = nil
	if e = spec.Validate(context.Background()); e != nil {
		return nil, e
	}
	router, e := legacy.NewRouter(spec)
	if e != nil {
		return nil, e
	}
	strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, e error) {
		s.error(w, r, fail(http.StatusBadRequest, "request", e.Error()))
	}, ResponseErrorHandlerFunc: s.error})
	generated := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, e error) {
		s.error(w, r, fail(http.StatusBadRequest, "request", e.Error()))
	}})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			s.serveUI(w, r)
			return
		}
		started := time.Now()
		defer func() {
			s.Log.Debug("API request", zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.Duration("duration", time.Since(started)))
		}()
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		actor, err := s.validateAPI(w, r, router)
		if err != nil {
			s.error(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), requestKey, r)
		ctx = context.WithValue(ctx, writerKey, w)
		ctx = context.WithValue(ctx, identityKey, actor)
		w.Header().Set("Cache-Control", "no-store")
		generated.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}
func (s *Server) validateAPI(w http.ResponseWriter, r *http.Request, router routers.Router) (*identity, error) {
	route, params, err := router.FindRoute(r)
	if err != nil {
		return nil, fail(http.StatusNotFound, "not_found", "Endpoint não encontrado.")
	}
	if !s.rate.allow("ip:" + s.clientIP(r)) {
		return nil, fail(http.StatusTooManyRequests, "rate_limit", "Demasiados pedidos. Tente de novo dentro de um minuto.")
	}
	actor, err := s.requestIdentity(w, r, route.Operation)
	if err != nil {
		return nil, err
	}
	if err = s.authorize(r, route.Operation, actor); err != nil {
		return nil, err
	}
	input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil }}}
	if err = openapi3filter.ValidateRequest(r.Context(), input); err != nil {
		return nil, fail(http.StatusBadRequest, "request", err.Error())
	}
	return actor, nil
}
func (s *Server) requestIdentity(w http.ResponseWriter, r *http.Request, operation *openapi3.Operation) (*identity, error) {
	actor, err := s.authenticate(r)
	if err == nil {
		return actor, nil
	}
	var authErr *apiError
	if errors.As(err, &authErr) && authErr.Code == "invalid_session" && s.anonymousOperation(operation) {
		s.clearCookie(w, "lp_session", http.SameSiteLaxMode)
		return nil, nil
	}
	return nil, err
}
func (s *Server) anonymousOperation(operation *openapi3.Operation) bool {
	public, _ := operation.Extensions["x-public-read"].(bool)
	return public && s.Options.PublicReads || operation.Security != nil && len(*operation.Security) == 0
}
func (s *Server) authorize(r *http.Request, operation *openapi3.Operation, actor *identity) error {
	sessionOnly, _ := operation.Extensions["x-session-only"].(bool)
	if sessionOnly && (actor == nil || !actor.Session) {
		return fail(http.StatusUnauthorized, "session_required", "Inicie sessão Google para gerir chaves.")
	}
	publicRead, _ := operation.Extensions["x-public-read"].(bool)
	requiresAuth := operation.Security != nil && len(*operation.Security) > 0 && !publicRead
	if actor == nil {
		if requiresAuth || publicRead && !s.Options.PublicReads {
			return fail(http.StatusUnauthorized, "credentials_required", "Credenciais em falta.")
		}
		return nil
	}
	return s.authorizeIdentity(r, operation, actor)
}
func (s *Server) authorizeIdentity(r *http.Request, operation *openapi3.Operation, actor *identity) error {
	if !s.rate.allow("principal:" + actor.Email + ":" + actor.KeyID) {
		return fail(http.StatusTooManyRequests, "rate_limit", "Limite de pedidos atingido.")
	}
	if err := requiredScopes(operation, actor); err != nil {
		return err
	}
	if actor.Session && r.Method != http.MethodGet && r.Header.Get("Origin") != s.Options.Origin {
		return fail(http.StatusForbidden, "origin", "Origem não autorizada.")
	}
	return nil
}
func requiredScopes(operation *openapi3.Operation, actor *identity) error {
	scopes, _ := operation.Extensions["x-required-scopes"].([]any)
	for _, scope := range scopes {
		needed, _ := scope.(string)
		if !contains(actor.Scopes, needed) {
			return fail(http.StatusForbidden, "scope", "Chave sem o âmbito necessário: "+needed)
		}
	}
	return nil
}
func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := s.Options.FrontendDir
	if root == "" {
		root = "frontend/dist"
	}
	clean := filepath.Clean("/" + r.URL.Path)
	path := filepath.Join(root, strings.TrimPrefix(clean, "/"))
	if info, e := os.Stat(path); e != nil || info.IsDir() {
		if strings.HasPrefix(clean, "/assets/") || filepath.Ext(clean) != "" {
			http.NotFound(w, r)
			return
		}
		path = filepath.Join(root, "index.html")
	}
	if _, e := os.Stat(path); e != nil {
		http.Error(w, "Frontend not built. Run npm run build in frontend.", http.StatusServiceUnavailable)
		return
	}
	http.ServeFile(w, r, path)
}
func contains(s []string, q string) bool {
	for _, v := range s {
		if v == q {
			return true
		}
	}
	return false
}

// GetHealth checks database readiness with a bounded timeout.
func (s *Server) GetHealth(ctx context.Context, _ api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	health, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if e := s.Store.DB.Ping(health); e != nil {
		return nil, fail(http.StatusServiceUnavailable, "database", "Base de dados indisponível.")
	}
	return api.GetHealth200JSONResponse{Status: "ok", Database: "ok"}, nil
}

// GetConfig returns public UI configuration and a fresh browser login nonce.
func (s *Server) GetConfig(ctx context.Context, _ api.GetConfigRequestObject) (api.GetConfigResponseObject, error) {
	nonce, e := randomSecret()
	if e != nil {
		return nil, e
	}
	s.cookie(writer(ctx), "lp_login", nonce, time.Now().Add(loginNonceLifetime), http.SameSiteStrictMode)
	return api.GetConfig200JSONResponse{GoogleClientId: optional(s.Options.GoogleClientID), DevAuth: s.Options.DevAuth, LoginNonce: nonce, HistoryRetentionDays: s.Store.retentionDays()}, nil
}

type Filter struct {
	Limit, Offset        int
	Revision             string
	Operators            []string
	Route, Q, Stop, Sort string
	From, To             time.Time
	HourStart, HourEnd   int
	Weekdays             bool
}

func (s *Server) filter(ctx context.Context, historical bool) (Filter, error) {
	query := request(ctx).URL.Query()
	filter := Filter{Limit: defaultPageSize, Revision: query.Get("revision"), Route: query.Get("route_id"), Q: strings.ToLower(query.Get("q")), Stop: query.Get("stop_id"), Sort: query.Get("sort"), HourEnd: 24}
	if v := query.Get("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}
	if v := query.Get("offset"); v != "" {
		filter.Offset, _ = strconv.Atoi(v)
	}
	if err := readFilterSelectors(&filter, query); err != nil {
		return filter, err
	}
	now := time.Now().UTC()
	if historical {
		local := now.In(lisbon)
		filter.From = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, lisbon)
		filter.To = now
	} else {
		filter.From = now
		filter.To = now.Add(24 * time.Hour)
	}
	if err := readFilterDates(&filter, query, historical); err != nil {
		return filter, err
	}
	if !filter.To.After(filter.From) {
		return filter, fail(http.StatusBadRequest, "range", "Intervalo de datas inválido.")
	}
	if historical {
		if filter.From.Before(now.AddDate(0, 0, -s.Store.retentionDays())) {
			return filter, fail(http.StatusGone, "history_expired", fmt.Sprintf("Histórico disponível apenas nos últimos %d dias.", s.Store.retentionDays()))
		}
		if filter.To.After(now.Add(time.Minute)) || filter.To.Sub(filter.From) > time.Duration(s.Store.retentionDays())*24*time.Hour+time.Hour {
			return filter, fail(http.StatusBadRequest, "range", "Intervalo histórico fora dos limites.")
		}
	} else if filter.To.Sub(filter.From) > maximumScheduleWindow {
		return filter, fail(http.StatusBadRequest, "range", "Horários limitados a48 horas por pedido.")
	}
	if v := query.Get("hour_start"); v != "" {
		filter.HourStart, _ = strconv.Atoi(v)
	}
	if v := query.Get("hour_end"); v != "" {
		filter.HourEnd, _ = strconv.Atoi(v)
	}
	if filter.HourStart >= filter.HourEnd {
		return filter, fail(http.StatusBadRequest, "hours", "Intervalo horário inválido.")
	}
	filter.Weekdays = query.Get("weekdays_only") == "true"
	return filter, nil
}
func (f Filter) selected(p string) bool { return len(f.Operators) == 0 || contains(f.Operators, p) }
func paginate[T any](items []T, f Filter, revision string) (api.Page, []T) {
	total := len(items)
	start := min(f.Offset, total)
	end := min(start+f.Limit, total)
	return api.Page{Limit: f.Limit, Offset: f.Offset, Total: total, HasMore: end < total, Revision: optional(revision)}, items[start:end]
}
func routeSummary(r api.RouteDetail) api.Route {
	return api.Route{Id: r.Id, SourceId: r.SourceId, OperatorId: r.OperatorId, ShortName: r.ShortName, LongName: r.LongName, Color: r.Color, StopIds: r.StopIds, PlanId: r.PlanId}
}

// ListOperators lists provider capabilities and source freshness.
func (s *Server) ListOperators(ctx context.Context, _ api.ListOperatorsRequestObject) (api.ListOperatorsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Operator{}
	now := time.Now()
	for _, p := range providers {
		v := state.Operators[p.ID]
		if v.Status == "ok" && (v.LiveUpdatedAt == nil || now.Sub(*v.LiveUpdatedAt) > 90*time.Second) {
			v.Status = api.OperatorStatusStale
		}
		if v.ObservedAt != nil && now.Sub(*v.ObservedAt) > 180*time.Second && v.Status == "ok" {
			v.Status = api.OperatorStatusStale
		}
		if v.StaticStatus == "ok" && (v.StaticUpdatedAt == nil || now.Sub(*v.StaticUpdatedAt) > 12*time.Hour) {
			v.StaticStatus = api.OperatorStaticStatusStale
		}
		out = append(out, v)
	}
	page, data := paginate(out, filter, state.Revision)
	return api.ListOperators200JSONResponse{Data: data, Page: page}, nil
}

// ListRoutes searches published routes within an immutable collection.
func (s *Server) ListRoutes(ctx context.Context, _ api.ListRoutesRequestObject) (api.ListRoutesResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Route{}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		for _, r := range d.Routes {
			if filter.Q != "" && !strings.Contains(strings.ToLower(r.ShortName+" "+r.LongName+" "+r.SourceId), filter.Q) {
				continue
			}
			out = append(out, routeSummary(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	page, data := paginate(out, filter, state.Revision)
	return api.ListRoutes200JSONResponse{Data: data, Page: page}, nil
}

// GetRoute returns a qualified route and its published geometry.
func (s *Server) GetRoute(ctx context.Context, r api.GetRouteRequestObject) (api.GetRouteResponseObject, error) {
	state, _ := s.Cache.state("")
	for _, d := range state.Static {
		for _, v := range d.Routes {
			if v.Id == r.RouteId {
				return api.GetRoute200JSONResponse(v), nil
			}
		}
	}
	return nil, fail(http.StatusNotFound, "not_found", "Carreira não encontrada.")
}

// ListStops searches published stops within an immutable collection.
func (s *Server) ListStops(ctx context.Context, _ api.ListStopsRequestObject) (api.ListStopsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Stop{}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		for _, v := range d.Stops {
			if filter.Route != "" && !contains(v.RouteIds, filter.Route) {
				continue
			}
			if filter.Q != "" && !strings.Contains(strings.ToLower(v.Name+" "+v.SourceId), filter.Q) {
				continue
			}
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	page, data := paginate(out, filter, state.Revision)
	return api.ListStops200JSONResponse{Data: data, Page: page}, nil
}

// ListVehicles lists reported or explicitly estimated positions.
func (s *Server) ListVehicles(ctx context.Context, _ api.ListVehiclesRequestObject) (api.ListVehiclesResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Vehicle{}
	for p, d := range state.Live {
		if !filter.selected(p) {
			continue
		}
		for _, v := range d.Vehicles {
			if filter.Route != "" && (v.RouteId == nil || *v.RouteId != filter.Route) {
				continue
			}
			v.Stale = time.Since(v.ObservedAt) > 180*time.Second
			out = append(out, v)
		}
	}
	sortVehicles(out)
	page, data := paginate(out, filter, state.Revision)
	return api.ListVehicles200JSONResponse{Data: data, Page: page}, nil
}

// ListTrips lists calendar-aware planned trips with stable schedule pagination.
func (s *Server) ListTrips(ctx context.Context, _ api.ListTripsRequestObject) (api.ListTripsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, revision, err := s.scheduleState(ctx, &filter)
	if err != nil {
		return nil, err
	}
	out := []api.Trip{}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		t, _ := scheduled(d, p, filter.From, filter.To, filter.Route, "")
		out = append(out, t...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PlannedDeparture.Equal(out[j].PlannedDeparture) {
			return out[i].Id < out[j].Id
		}
		return out[i].PlannedDeparture.Before(out[j].PlannedDeparture)
	})
	page, data := paginate(out, filter, revision)
	return api.ListTrips200JSONResponse{Data: data, Page: page}, nil
}

// ListArrivals merges labeled planned and predicted arrivals with stable pagination.
func (s *Server) ListArrivals(ctx context.Context, _ api.ListArrivalsRequestObject) (api.ListArrivalsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	if filter.Stop == "" {
		return nil, fail(http.StatusBadRequest, "stop_required", "Selecione uma paragem.")
	}
	if filter.Revision == "" && strings.HasPrefix(filter.Stop, "metro:") && s.Metro != nil {
		s.Metro.Refresh(ctx)
	}
	state, revision, err := s.scheduleState(ctx, &filter)
	if err != nil {
		return nil, err
	}
	out := []api.Arrival{}
	if filter.selected("metro") && strings.HasPrefix(filter.Stop, "metro:") {
		out = append(out, predictedArrivals(state, filter.From, filter.To, filter.Route, filter.Stop)...)
	}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		_, a := scheduled(d, p, filter.From, filter.To, filter.Route, filter.Stop)
		out = append(out, a...)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].ScheduledAt, out[j].ScheduledAt
		if ai == nil {
			ai = out[i].ExpectedAt
		}
		if aj == nil {
			aj = out[j].ExpectedAt
		}
		if ai.Equal(*aj) {
			return out[i].Id < out[j].Id
		}
		return ai.Before(*aj)
	})
	page, data := paginate(out, filter, revision)
	return api.ListArrivals200JSONResponse{Data: data, Page: page}, nil
}

func (s *Server) scheduleState(ctx context.Context, f *Filter) (*State, string, error) {
	rev := f.Revision
	if rev != "" {
		parts := strings.Split(rev, ":")
		if len(parts) != 4 || parts[0] != "t" {
			return nil, "", fail(http.StatusBadRequest, "revision", "Revisão de horários inválida.")
		}
		left, ea := strconv.ParseInt(parts[2], 10, numericBitSize)
		right, eb := strconv.ParseInt(parts[3], 10, numericBitSize)
		start, end := time.Unix(0, left).UTC(), time.Unix(0, right).UTC()
		if ea != nil || eb != nil || !end.After(start) || end.Sub(start) > maximumScheduleWindow {
			return nil, "", fail(http.StatusBadRequest, "revision", "Intervalo de horários inválido.")
		}
		query := request(ctx).URL.Query()
		if (query.Get("from") != "" && !f.From.Equal(start)) || (query.Get("to") != "" && !f.To.Equal(end)) {
			return nil, "", fail(http.StatusBadRequest, "revision", "O intervalo mudou entre páginas.")
		}
		f.From, f.To = start, end
		rev = parts[1]
	}
	state, err := s.Cache.state(rev)
	if err != nil {
		return nil, "", err
	}
	return state, fmt.Sprintf("t:%s:%d:%d", state.Revision, f.From.UnixNano(), f.To.UnixNano()), nil
}

var _ api.StrictServerInterface = (*Server)(nil)

func readFilterSelectors(filter *Filter, query url.Values) error {
	if v := query.Get("operators"); v != "" {
		for _, id := range strings.Split(v, ",") {
			if _, ok := providerByID(id); !ok {
				return fail(http.StatusBadRequest, "operator", "Operador desconhecido.")
			}
			filter.Operators = append(filter.Operators, id)
		}
	}
	if filter.Route != "" {
		parts := strings.SplitN(filter.Route, ":", 2)
		if len(parts) != 2 {
			return fail(http.StatusBadRequest, "route", "ID de carreira inválido.")
		}
		if _, ok := providerByID(parts[0]); !ok {
			return fail(http.StatusBadRequest, "route", "Operador da carreira desconhecido.")
		}
	}
	return nil
}

func readFilterDates(filter *Filter, query url.Values, historical bool) error {
	// Restore frozen defaults before validating requests that omit one bound.
	if parts := strings.Split(filter.Revision, ":"); len(parts) == 4 && ((historical && parts[0] == "s") || (!historical && parts[0] == "t")) {
		left, ea := strconv.ParseInt(parts[2], 10, numericBitSize)
		right, eb := strconv.ParseInt(parts[3], 10, numericBitSize)
		if ea != nil || eb != nil {
			return fail(http.StatusBadRequest, "revision", "Revisão inválida.")
		}
		filter.From, filter.To = time.Unix(0, left).UTC(), time.Unix(0, right).UTC()
	}
	var err error
	if v := query.Get("from"); v != "" {
		filter.From, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return fail(http.StatusBadRequest, "from", "Data inválida.")
		}
	}
	if v := query.Get("to"); v != "" {
		filter.To, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return fail(http.StatusBadRequest, "to", "Data inválida.")
		}
	}
	return nil
}
