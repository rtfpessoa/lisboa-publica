package app

import (
	"context"
	"lisboapublica/internal/api"
	"net/http"
	"time"
)

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
	return api.GetConfig200JSONResponse{GoogleClientId: optional(s.Options.GoogleClientID), DevAuth: s.Options.DevAuth, LoginNonce: nonce, LiveRefreshSeconds: int(providerRefreshInterval.Seconds()), HistoryRetentionDays: s.Store.retentionDays(), HistoryResolutionSeconds: s.Store.historyResolution(), HistoryStorageLimitBytes: s.Store.storageLimit(), HistoryCollectionStatus: s.Store.historyCollectionStatus()}, nil
}
