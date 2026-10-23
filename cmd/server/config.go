package main

import (
	"context"
	"fmt"
	"lisboapublica/internal/app"
	"os"
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
	guard := env("STORAGE_GUARD", "false") == "true"
	direct, err := strconv.ParseBool(env("STORAGE_POSTGRES_DIRECT", "false"))
	if err != nil || (direct && !guard) {
		return nil, fmt.Errorf("STORAGE_POSTGRES_DIRECT requires a guarded direct PostgreSQL connection")
	}
	path := os.Getenv("STORAGE_FILESYSTEM_PATH")
	var probe *app.StorageFilesystemConfig
	if path != "" {
		freeBytes, parseErr := strconv.ParseUint(env("STORAGE_FILESYSTEM_MIN_FREE_BYTES", "1073741824"), 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid STORAGE_FILESYSTEM_MIN_FREE_BYTES")
		}
		freeInodes, parseErr := strconv.ParseUint(env("STORAGE_FILESYSTEM_MIN_FREE_INODES", "10000"), 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid STORAGE_FILESYSTEM_MIN_FREE_INODES")
		}
		probe = &app.StorageFilesystemConfig{Path: path, MinimumBytes: freeBytes, MinimumInodes: freeInodes}
	}
	return app.OpenStoreWithStorageOptions(ctx, database, guard, probe, direct)
}
