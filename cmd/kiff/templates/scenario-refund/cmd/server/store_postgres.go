package main

import (
	"context"

	"github.com/kiff/kiff/pkg/kiff/store"
	pgstore "github.com/kiff/kiff/pkg/kiff/store/postgres"
)

// This file is scaffolded only for `-store postgres`. It registers the
// Postgres opener so main.go can use it without importing pgx in the
// file/memory builds. Run the database with `docker compose up -d` and set
// DATABASE_URL (see .env.example / README).
func init() {
	postgresOpener = openPostgres
}

func openPostgres(ctx context.Context, url string) (*store.Bundle, func(), error) {
	pool, err := pgstore.Connect(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	// Idempotent: creates the KIFF tables if they do not exist. Production
	// deployments manage schema with their own migration tool.
	if err := pgstore.ApplySchema(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	bundle := pgstore.NewBundleOwnedPool(pool)
	sb := bundle.AsStoreBundle()
	return &sb, bundle.Close, nil
}
