// Package postgres provides Postgres-backed implementations of the four KIFF
// store interfaces (event, decision, approval, audit).
//
// The package follows the same shape as pkg/kiff/store/file:
//
//   - one type per store, all implementing their respective package's Store
//     interface;
//   - a Bundle that owns a single connection pool and exposes the four
//     stores as a unit;
//   - a small Connect helper that returns a pgxpool.Pool with sensible
//     defaults.
//
// The schema lives in schema.sql alongside this package. Apply it once
// against your target database (or call ApplySchema for tests). The store
// does not run migrations on its own — production deployments should manage
// schema with their own tool.
//
// All four stores share one pool. The pool is lazy and concurrent-safe; you
// can use the same Bundle from many goroutines.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidConfig is returned when a connection string is missing or empty.
var ErrInvalidConfig = errors.New("invalid postgres config")

// Connect opens a pgxpool.Pool against the given Postgres URL.
//
// The URL accepts both libpq-style ("postgres://user:pass@host:port/db")
// and key-value forms ("host=... user=... ..."). Defaults are conservative:
// a small pool is created, ready for serverless and small services.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, fmt.Errorf("%w: connection string is required", ErrInvalidConfig)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

//go:embed schema.sql
var schemaSQL string

// SchemaSQL returns the raw DDL used to create KIFF's Postgres tables.
// Production deployments should manage migrations with their own tool;
// SchemaSQL exists so test setup, demos, and starter projects can apply
// the schema without copying SQL around.
func SchemaSQL() string { return schemaSQL }

// ApplySchema executes the embedded schema against pool. It is idempotent:
// the schema uses CREATE TABLE IF NOT EXISTS for all tables, so calling it
// twice is safe.
func ApplySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("%w: pool is required", ErrInvalidConfig)
	}
	_, err := pool.Exec(ctx, schemaSQL)
	if err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}
