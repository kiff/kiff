# Postgres store

A Postgres-backed implementation of the four KIFF store interfaces:
`event.Store`, `decision.Store`, `approval.Store`, and `audit.Store`.

This package is the recommended production backend for KIFF v0.3. It also
covers Supabase, Neon, RDS, CloudSQL, Crunchy Bridge, and any other
Postgres-compatible service.

## Wiring

```go
import (
    "context"

    "github.com/kiff/kiff/pkg/kiff/runtime"
    "github.com/kiff/kiff/pkg/kiff/store/postgres"
)

ctx := context.Background()

pool, err := postgres.Connect(ctx, "postgres://user:pass@host:5432/db?sslmode=disable")
if err != nil {
    return err
}
defer pool.Close()

if err := postgres.ApplySchema(ctx, pool); err != nil {
    return err
}

bundle := postgres.NewBundle(pool)
storeBundle := bundle.AsStoreBundle()

rt, err := runtime.NewForDomain(domainDef, runtime.Config{
    Stores: &storeBundle,
    // ... policy, adapters, etc.
})
```

`Connect` returns a `*pgxpool.Pool`. The bundle does not take ownership of
the pool; close it yourself when the process shuts down. Use
`postgres.NewBundleOwnedPool(pool)` if you want the bundle to call
`pool.Close()` on its own `Close()`.

## Schema

The schema lives in [`schema.sql`](./schema.sql) and creates four tables
plus indexes:

- `kiff_events` — append-only event log
- `kiff_decisions` — append-only decision records
- `kiff_approvals` — upsertable approval records (PRIMARY KEY on `id`)
- `kiff_audit` — append-only audit trail

Every `CREATE` is `IF NOT EXISTS` so applying twice is safe. For production
deployments you should manage migrations with your own tool; this package
intentionally does not bundle a migration runner.

JSONB columns store free-form payloads and metadata; `nullable` columns map
to empty strings on the Go side so callers do not need to discriminate
between `NULL` and `""`.

## Running the conformance tests

The conformance suite is gated by an environment variable so the default
`go test ./...` does not require a running Postgres.

To run it:

```bash
# Start a throwaway Postgres (any 14+ image works)
docker run -d --name kiff-pg-test --rm \
  -e POSTGRES_USER=kiff \
  -e POSTGRES_PASSWORD=kiff \
  -e POSTGRES_DB=kiff_test \
  -p 55432:5432 \
  postgres:16-alpine

# Run the suite
KIFF_POSTGRES_TEST_URL="postgres://kiff:kiff@localhost:55432/kiff_test?sslmode=disable" \
  go test ./pkg/kiff/store/postgres/... -v

# Tear it down
docker stop kiff-pg-test
```

Each subtest creates an isolated Postgres schema (`kiff_test_<random>`),
applies the schema there, runs against it, and drops it on cleanup. You
can run the suite repeatedly against the same database without manual
cleanup between runs.

## What the suite proves

The suite is the same `pkg/kiff/store/storetest` suite that runs against
the in-memory and file-backed stores. Passing it means the Postgres
implementation behaves identically for:

- append + list ordering
- entity filtering
- payload and metadata round-tripping
- approval upsert, get, and `IsGranted` semantics
- audit filtering by entity, kind, actor, trace, correlation
- chronological ordering of audit records
- validation rejection of invalid inputs
- context cancellation

If you implement a new store backend later, run it against
`storetest` and it must pass the same suite.

## Performance notes

The package uses `pgx/v5` with `pgxpool` for connection pooling. Default
pool sizing is conservative (good for serverless and small services). For
high-throughput deployments, pass a tuned `pgxpool.Config` to
`pgxpool.NewWithConfig` directly and hand the resulting pool to
`NewBundle`.

The audit `Query` filter generates parameterized SQL with one clause per
non-empty filter field. The schema has indexes on
`entity_id`, `kind`, `actor_id`, `trace_id`, and `correlation_id` so the
common filters do not require sequential scans.

## What this package does not do

- Migrations beyond `CREATE TABLE IF NOT EXISTS`. Use `golang-migrate`,
  `goose`, Atlas, or your existing migration tool for production schema
  evolution.
- Data retention, partitioning, or archival. Add what your operations
  require on top.
- Encryption at rest. That is your database's job.
- Cross-region replication. That is your database's job.

The store stays small. Operational concerns live where they belong — in
your database setup and your deployment automation.
