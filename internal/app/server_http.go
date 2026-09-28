package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"net/http"
)

func (s *Server) error(w http.ResponseWriter, r *http.Request, err error) {
	var pg *pgconn.PgError
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &pg) && pg.Code == "57014" {
		err = fail(http.StatusServiceUnavailable, "request_timeout", "Pedido interrompido; reduza o intervalo e tente novamente.")
	}
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
	return s.middleware(generated, router), nil
}
