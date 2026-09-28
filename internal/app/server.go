package app

import (
	"context"

	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"

	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"go.uber.org/zap"
	"google.golang.org/api/idtoken"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
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
	Patterns       *patterns.Service
	Store          *Store
	Cache          *Cache
	Options        Options
	Log            *zap.Logger
	VerifyGoogle   func(context.Context, string, string) (*idtoken.Payload, error)
	expensiveReads chan struct{}
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
	options, err := validateServerOptions(options)
	if err != nil {
		return nil, err
	}
	s := &Server{expensiveReads: make(chan struct{}, maxExpensiveReads), Store: store, Cache: cache, Options: options, Log: log, VerifyGoogle: idtoken.Validate, rate: &limiter{entries: map[string]rateEntry{}, limit: options.RateLimit}}
	s.trustedProxies, err = proxyPrefixes(options.TrustedProxyCIDRs)
	return s, err
}

func validateServerOptions(options Options) (Options, error) {
	u, e := url.Parse(options.Origin)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" {
		return options, fmt.Errorf("PUBLIC_ORIGIN must be an origin without a path")
	}
	if options.Environment == "" {
		options.Environment = "production"
	}
	if options.DevAuth && options.Environment != "development" {
		return options, fmt.Errorf("DEV_AUTH requires ENVIRONMENT=development")
	}
	if options.Environment == "production" && u.Scheme != "https" {
		return options, fmt.Errorf("production PUBLIC_ORIGIN requires HTTPS")
	}
	if options.RateLimit <= 0 {
		options.RateLimit = defaultReadRate
	}
	return options, nil
}

func proxyPrefixes(value string) ([]netip.Prefix, error) {
	prefixes := []netip.Prefix{}
	for _, cidr := range strings.Split(value, ",") {
		if strings.TrimSpace(cidr) == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS: %w", err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func expensiveRead(path string) bool {
	if strings.HasPrefix(path, "/api/v1/stops/") || strings.HasPrefix(path, "/api/v1/vehicles/") {
		return true
	}
	switch path {
	case "/api/v1/transport/patterns", "/api/v1/metro/patterns", "/api/v1/cp/predictions", "/api/v1/trips", "/api/v1/arrivals", "/api/v1/metrics", "/api/v1/history", "/api/v1/fleet", "/api/v1/traffic", "/api/v1/rankings", "/api/v1/operator-coverage":
		return true
	}
	return false
}

func readResultLimit() error {
	return fail(http.StatusBadRequest, "result_limit", "Demasiados resultados; selecione um operador, carreira ou intervalo menor.")
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

var _ api.StrictServerInterface = (*Server)(nil)
