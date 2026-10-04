package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/app"
)

const (
	upstreamRequestBudget  = 900
	upstreamRequestTimeout = 45 * time.Second
	httpHeaderTimeout      = 5 * time.Second
	httpReadTimeout        = 30 * time.Second
	httpWriteTimeout       = 60 * time.Second
	httpIdleTimeout        = 60 * time.Second
	httpShutdownTimeout    = 10 * time.Second
)

type backgroundServices struct {
	server *app.Server
	store  *app.Store
	cache  *app.Cache
	log    *zap.Logger
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// newLogger builds the process logger; LOG_LEVEL selects the zap level (default info).
func newLogger() *zap.Logger {
	config := zap.NewProductionConfig()
	invalid := ""
	if level := strings.TrimSpace(os.Getenv("LOG_LEVEL")); level != "" {
		if err := config.Level.UnmarshalText([]byte(level)); err != nil {
			invalid = level
		}
	}
	log, _ := config.Build()
	if invalid != "" {
		log.Warn("ignoring invalid LOG_LEVEL", zap.String("value", invalid))
	}
	return log
}

func main() {
	log := newLogger()
	defer log.Sync()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	database := os.Getenv("DATABASE_URL")
	if database == "" {
		log.Fatal("DATABASE_URL is required")
	}
	store, err := openConfiguredStore(ctx, database)
	if err != nil {
		log.Fatal("database initialization failed", zap.Error(err))
	}
	defer store.DB.Close()
	if err := configureHistory(store); err != nil {
		log.Fatal("history configuration failed", zap.Error(err))
	}
	cache := app.NewCache()
	if err = store.Restore(ctx, cache); err != nil {
		log.Fatal("cache restoration failed", zap.Error(err))
	}
	server := configuredServer(store, cache, log)
	if server.Patterns != nil {
		defer server.Patterns.Close()
	}
	services := backgroundServices{server: server, store: store, cache: cache, log: log}
	services.start(ctx)
	handler, err := server.Handler()
	if err != nil {
		log.Fatal("API initialization failed", zap.Error(err))
	}
	serveHTTP(ctx, handler, log)
}
func runtimeOptions() app.Options {
	limit, _ := strconv.Atoi(env("RATE_LIMIT_PER_MINUTE", "300"))
	return app.Options{Origin: env("PUBLIC_ORIGIN", "http://localhost:8080"), Environment: env("ENVIRONMENT", "development"), DevAuth: env("DEV_AUTH", "false") == "true", PublicReads: env("PUBLIC_READS", "true") == "true", GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), FrontendDir: env("FRONTEND_DIR", "frontend/dist"), RateLimit: limit, TrustedProxyCIDRs: os.Getenv("TRUSTED_PROXY_CIDRS")}
}
func (services backgroundServices) start(ctx context.Context) {
	go services.store.RunReporting(ctx, services.cache, services.log)
	outgoing := &http.Client{Timeout: upstreamRequestTimeout, Transport: app.NewBudgetTransport(upstreamRequestBudget), CheckRedirect: app.CheckUpstreamRedirect}
	services.server.Metro = app.NewMetroClient(outgoing, services.store, services.cache, os.Getenv("METRO_CLIENT_ID"), os.Getenv("METRO_CLIENT_SECRET"))
	services.server.Metro.Log = services.log
	services.server.Metro.History = services.server.Patterns
	if err := services.cache.ConfigureMetroModels(os.Getenv("METRO_MODEL_ALLOWLIST")); err != nil {
		services.log.Fatal("Invalid Metro model allowlist", zap.Error(err))
	}
	metroMillis, err := strconv.Atoi(env("METRO_REFRESH_MILLISECONDS", "500"))
	if err != nil || metroMillis < 500 || metroMillis > 60000 {
		services.log.Fatal("METRO_REFRESH_MILLISECONDS must be between 500 and 60000")
	}
	services.server.Metro.Interval = time.Duration(metroMillis) * time.Millisecond
	if os.Getenv("METRO_CLIENT_ID") != "" && os.Getenv("METRO_CLIENT_SECRET") != "" {
		go services.server.Metro.Run(ctx)
	}
	services.startIngestion(ctx, outgoing)
}
func (services backgroundServices) startIngestion(ctx context.Context, outgoing *http.Client) {
	if env("INGEST_ENABLED", "true") == "true" {
		fetcher := app.NewFetcher(services.store, services.cache, services.log)
		fetcher.Client = outgoing
		fetcher.Patterns = services.server.Patterns
		go fetcher.Run(ctx)
	}
}
func serveHTTP(ctx context.Context, handler http.Handler, log *zap.Logger) {
	srv := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: handler, ReadHeaderTimeout: httpHeaderTimeout, ReadTimeout: httpReadTimeout, WriteTimeout: httpWriteTimeout, IdleTimeout: httpIdleTimeout}
	go shutdownHTTP(ctx, srv)
	log.Info("server started", zap.String("address", srv.Addr))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("HTTP server failed", zap.Error(err))
	}
}

func shutdownHTTP(ctx context.Context, srv *http.Server) {
	<-ctx.Done()
	shutdown, c := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer c()
	_ = srv.Shutdown(shutdown)
}

func configuredServer(store *app.Store, cache *app.Cache, log *zap.Logger) *app.Server {
	server, err := app.NewServer(store, cache, runtimeOptions(), log)
	if err != nil {
		log.Fatal("server configuration failed", zap.Error(err))
	}
	server.Patterns, err = configurePatterns()
	if err != nil {
		log.Fatal("transport archive initialization failed", zap.Error(err))
	}
	return server
}
