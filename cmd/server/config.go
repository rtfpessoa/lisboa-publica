package main

import (
	"context"
	"fmt"
	"lisboapublica/internal/app"
	"strconv"
	"time"
)

func configureHistory(store *app.Store) error {
	days, err := strconv.Atoi(env("SNAPSHOT_RETENTION_DAYS", "30"))
	if err != nil {
		return fmt.Errorf("invalid SNAPSHOT_RETENTION_DAYS")
	}
	seconds, err := strconv.Atoi(env("HISTORY_INTERVAL_SECONDS", "0"))
	if err != nil {
		return fmt.Errorf("invalid HISTORY_INTERVAL_SECONDS")
	}
	return store.ConfigureHistory(days, time.Duration(seconds)*time.Second, env("STORAGE_GUARD", "false") == "true")
}

func openConfiguredStore(ctx context.Context, database string) (*app.Store, error) {
	return app.OpenStoreWithStorageGuard(ctx, database, env("STORAGE_GUARD", "false") == "true")
}
