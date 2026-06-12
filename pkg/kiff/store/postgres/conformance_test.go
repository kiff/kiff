package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/store/postgres"
	"github.com/kiff/kiff/pkg/kiff/store/storetest"
)

// envURL is the env var that gates the Postgres conformance suite. When
// unset, the tests in this file are skipped, so `go test ./...` remains
// usable on machines without a running Postgres.
//
// Example: KIFF_POSTGRES_TEST_URL=postgres://kiff:kiff@localhost:5432/kiff_test
const envURL = "KIFF_POSTGRES_TEST_URL"

func TestPostgresEventStore_Conformance(t *testing.T) {
	storetest.RunEventStore(t, func(t *testing.T) (event.Store, func()) {
		pool, cleanup := newTestPool(t)
		return postgres.NewEventStore(pool), cleanup
	})
}

func TestPostgresDecisionStore_Conformance(t *testing.T) {
	storetest.RunDecisionStore(t, func(t *testing.T) (decision.Store, func()) {
		pool, cleanup := newTestPool(t)
		return postgres.NewDecisionStore(pool), cleanup
	})
}

func TestPostgresApprovalStore_Conformance(t *testing.T) {
	storetest.RunApprovalStore(t, func(t *testing.T) (approval.Store, func()) {
		pool, cleanup := newTestPool(t)
		return postgres.NewApprovalStore(pool), cleanup
	})
}

func TestPostgresAuditStore_Conformance(t *testing.T) {
	storetest.RunAuditStore(t, func(t *testing.T) (audit.Store, func()) {
		pool, cleanup := newTestPool(t)
		return postgres.NewAuditStore(pool), cleanup
	})
}

// newTestPool returns a connection pool that points at an isolated schema in
// the configured test database, plus a cleanup func that drops the schema.
//
// Schema isolation lets every subtest in the conformance suite run against a
// clean state without dropping/recreating the entire database. We use
// search_path so the table names inside the package don't have to know about
// the schema.
func newTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	url := os.Getenv(envURL)
	if url == "" {
		t.Skipf("set %s to run Postgres conformance tests (e.g. postgres://kiff:kiff@localhost:5432/kiff_test)", envURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	schema := "kiff_test_" + randHex(t, 8)
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		pool.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		pool.Close()
		t.Fatalf("set search_path: %v", err)
	}
	if err := postgres.ApplySchema(ctx, pool); err != nil {
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		pool.Close()
		t.Fatalf("apply schema: %v", err)
	}

	cleanup := func() {
		// New short-lived context so cleanup runs even if the test's context
		// has been cancelled.
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dropCancel()
		_, _ = pool.Exec(dropCtx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		pool.Close()
	}
	return pool, cleanup
}

// randHex returns 2*n hex characters seeded from crypto/rand. It is used to
// keep test schemas unique across parallel test invocations.
func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}
