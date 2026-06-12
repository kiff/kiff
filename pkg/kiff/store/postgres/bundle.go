package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kiff/kiff/pkg/kiff/store"
)

// Bundle owns the four Postgres-backed stores around a shared connection
// pool. It mirrors pkg/kiff/store/file.Bundle so applications can swap
// backends with one line.
type Bundle struct {
	Pool      *pgxpool.Pool
	Events    *EventStore
	Decisions *DecisionStore
	Approvals *ApprovalStore
	Audit     *AuditStore

	ownsPool bool
}

// NewBundle wires the four Postgres stores around an existing pgxpool.Pool.
// Use Connect (or your own pool factory) to build the pool, then hand it to
// NewBundle.
//
// The returned Bundle does not close the pool when Close is called. If you
// want NewBundle to manage the pool's lifecycle, use NewBundleOwnedPool.
func NewBundle(pool *pgxpool.Pool) *Bundle {
	return &Bundle{
		Pool:      pool,
		Events:    NewEventStore(pool),
		Decisions: NewDecisionStore(pool),
		Approvals: NewApprovalStore(pool),
		Audit:     NewAuditStore(pool),
	}
}

// NewBundleOwnedPool is like NewBundle but takes ownership of the pool so
// Close shuts it down. Useful in main.go style wiring where the bundle's
// lifetime matches the process lifetime.
func NewBundleOwnedPool(pool *pgxpool.Pool) *Bundle {
	b := NewBundle(pool)
	b.ownsPool = true
	return b
}

// AsStoreBundle adapts the Postgres bundle to the package-level store.Bundle
// expected by runtime.Config.Stores.
func (b *Bundle) AsStoreBundle() store.Bundle {
	return store.Bundle{
		Events:    b.Events,
		Decisions: b.Decisions,
		Approvals: b.Approvals,
		Audit:     b.Audit,
	}
}

// Close shuts down the connection pool when the bundle owns it. Calling
// Close on a bundle created via NewBundle (without ownership) is a no-op.
func (b *Bundle) Close() {
	if b.ownsPool && b.Pool != nil {
		b.Pool.Close()
	}
}
