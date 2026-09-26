package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/routers"
	"go.uber.org/zap"
)

func (s *Server) securityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(self)")
	if strings.HasPrefix(s.Options.Origin, "https://") {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
}

func (s *Server) middleware(generated http.Handler, router routers.Router) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			s.serveUI(w, r)
			return
		}
		started := time.Now()
		defer func() {
			s.Log.Debug("API request", zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.Duration("duration", time.Since(started)))
		}()
		// Clear valid-origin logout cookies before DB-dependent authentication.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/logout" && r.Header.Get("Origin") == s.Options.Origin {
			s.clearCookie(w, "lp_session", http.SameSiteLaxMode)
		}
		if r.Method == http.MethodGet && expensiveRead(r.URL.Path) {
			release, err := s.admitRead()
			if err != nil {
				w.Header().Set("Retry-After", "1")
				s.error(w, r, err)
				return
			}
			defer release()
			ctx, cancel := context.WithTimeout(r.Context(), expensiveReadTimeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		s.dispatchAPI(w, r, generated, router)
	})
}

func (s *Server) admitRead() (func(), error) {
	select {
	case s.expensiveReads <- struct{}{}:
		return func() { <-s.expensiveReads }, nil
	default:
		return nil, fail(http.StatusServiceUnavailable, "busy", "Pedidos em curso; tente novamente dentro de um segundo.")
	}
}

func (s *Server) dispatchAPI(w http.ResponseWriter, r *http.Request, generated http.Handler, router routers.Router) {
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
	if err := ctx.Err(); err != nil {
		s.error(w, r, err)
		return
	}
	generated.ServeHTTP(w, r.WithContext(ctx))
}
