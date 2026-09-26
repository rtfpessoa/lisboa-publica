package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/app"
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	database := os.Getenv("DATABASE_URL")
	if database == "" {
		log.Fatal("DATABASE_URL is required")
	}
	store, err := app.OpenStore(ctx, database)
	if err != nil {
		log.Fatal("database initialization failed", zap.Error(err))
	}
	defer store.DB.Close()
	retention, retentionErr := strconv.Atoi(env("SNAPSHOT_RETENTION_DAYS", "30"))
	if retentionErr != nil || retention < 1 || retention > 30 {
		log.Fatal("SNAPSHOT_RETENTION_DAYS must be between 1 and 30")
	}
	store.RetentionDays = retention
	cache := app.NewCache()
	if err = store.Restore(ctx, cache); err != nil {
		log.Fatal("cache restoration failed", zap.Error(err))
	}
	limit, _ := strconv.Atoi(env("RATE_LIMIT_PER_MINUTE", "300"))
	server, err := app.NewServer(store, cache, app.Options{Origin: env("PUBLIC_ORIGIN", "http://localhost:8080"), Environment: env("ENVIRONMENT", "development"), DevAuth: env("DEV_AUTH", "false") == "true", PublicReads: env("PUBLIC_READS", "true") == "true", GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), FrontendDir: env("FRONTEND_DIR", "frontend/dist"), RateLimit: limit, TrustedProxyCIDRs: os.Getenv("TRUSTED_PROXY_CIDRS")}, log)
	if err != nil {
		log.Fatal("server configuration failed", zap.Error(err))
	}
	outgoing := &http.Client{Timeout: 45 * time.Second, Transport: app.NewBudgetTransport(900), CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		if req.URL.Scheme != "https" {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	server.Metro = app.NewMetroClient(outgoing, store, cache, os.Getenv("METRO_CLIENT_ID"), os.Getenv("METRO_CLIENT_SECRET"))
	if os.Getenv("METRO_CLIENT_ID") != "" && os.Getenv("METRO_CLIENT_SECRET") != "" {
		go server.Metro.Run(ctx)
	}
	handler, err := server.Handler()
	if err != nil {
		log.Fatal("API initialization failed", zap.Error(err))
	}
	if env("INGEST_ENABLED", "true") == "true" {
		fetcher := app.NewFetcher(store, cache, log)
		fetcher.Client = outgoing
		go fetcher.Run(ctx)
	}
	srv := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("server started", zap.String("address", srv.Addr))
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("HTTP server failed", zap.Error(err))
	}
}
