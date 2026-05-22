package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
)

// ApprovalStore persists approval.Approval rows in the kiff_approvals table.
type ApprovalStore struct {
	pool *pgxpool.Pool
}

// NewApprovalStore returns a Postgres-backed approval store.
func NewApprovalStore(pool *pgxpool.Pool) *ApprovalStore {
	return &ApprovalStore{pool: pool}
}

// Save validates and upserts an approval record. Re-saving the same id
// updates the existing row, preserving the in-memory store's behavior.
func (s *ApprovalStore) Save(ctx context.Context, a approval.Approval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}

	var reviewedAt any
	if !a.ReviewedAt.IsZero() {
		reviewedAt = a.ReviewedAt.UTC()
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO kiff_approvals (
			id, entity_id, entity_type, action_name, requested_by,
			reviewed_by, status, reason, created_at, reviewed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			entity_id     = EXCLUDED.entity_id,
			entity_type   = EXCLUDED.entity_type,
			action_name   = EXCLUDED.action_name,
			requested_by  = EXCLUDED.requested_by,
			reviewed_by   = EXCLUDED.reviewed_by,
			status        = EXCLUDED.status,
			reason        = EXCLUDED.reason,
			created_at    = EXCLUDED.created_at,
			reviewed_at   = EXCLUDED.reviewed_at
	`,
		a.ID, a.EntityID, a.EntityType, a.ActionName, a.RequestedBy,
		nullableString(a.ReviewedBy), string(a.Status), nullableString(a.Reason),
		a.CreatedAt.UTC(), reviewedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert approval: %w", err)
	}
	return nil
}

// Get returns an approval by id. The boolean is false when no row matches.
func (s *ApprovalStore) Get(ctx context.Context, id string) (approval.Approval, bool, error) {
	if err := ctx.Err(); err != nil {
		return approval.Approval{}, false, err
	}

	row := s.pool.QueryRow(ctx, `
		SELECT id, entity_id, entity_type, action_name, requested_by,
		       reviewed_by, status, reason, created_at, reviewed_at
		FROM kiff_approvals
		WHERE id = $1
	`, id)

	a, err := scanApproval(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return approval.Approval{}, false, nil
		}
		return approval.Approval{}, false, fmt.Errorf("get approval: %w", err)
	}
	return a, true, nil
}

// List returns approvals for an entity. An empty entity id returns all
// approvals in insertion order, matching the in-memory store.
func (s *ApprovalStore) List(ctx context.Context, entityID string) ([]approval.Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	const baseQuery = `
		SELECT id, entity_id, entity_type, action_name, requested_by,
		       reviewed_by, status, reason, created_at, reviewed_at
		FROM kiff_approvals
	`
	const orderClause = ` ORDER BY inserted_at, id`

	var (
		rows rowReader
		err  error
	)
	if entityID == "" {
		rows, err = queryAll(ctx, s.pool, baseQuery+orderClause)
	} else {
		rows, err = queryAll(ctx, s.pool, baseQuery+` WHERE entity_id = $1`+orderClause, entityID)
	}
	if err != nil {
		return nil, fmt.Errorf("query approvals: %w", err)
	}
	defer rows.Close()

	out := []approval.Approval{}
	for rows.Next() {
		a, err := scanApprovalFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approvals: %w", err)
	}
	return out, nil
}

// IsGranted returns true when the approval exists, is granted, and matches
// the entity and action. This is the runtime's hot path during action
// validation; keeping it as a single round-trip matters.
func (s *ApprovalStore) IsGranted(ctx context.Context, id, entityID, actionName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	var found bool
	err := s.pool.QueryRow(ctx, `
		SELECT TRUE
		FROM kiff_approvals
		WHERE id = $1
		  AND entity_id = $2
		  AND action_name = $3
		  AND status = $4
	`, id, entityID, actionName, string(approval.StatusGranted)).Scan(&found)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check granted: %w", err)
	}
	return found, nil
}

// scanApproval scans a pgx.Row into an approval.Approval, normalizing
// nullable columns and timestamps.
func scanApproval(row pgx.Row) (approval.Approval, error) {
	var (
		a              approval.Approval
		reviewedBy     *string
		reason         *string
		reviewedAt     *time.Time
		statusStr      string
	)
	err := row.Scan(
		&a.ID, &a.EntityID, &a.EntityType, &a.ActionName, &a.RequestedBy,
		&reviewedBy, &statusStr, &reason, &a.CreatedAt, &reviewedAt,
	)
	if err != nil {
		return approval.Approval{}, err
	}
	a.Status = approval.Status(statusStr)
	if reviewedBy != nil {
		a.ReviewedBy = *reviewedBy
	}
	if reason != nil {
		a.Reason = *reason
	}
	if reviewedAt != nil {
		a.ReviewedAt = reviewedAt.UTC()
	}
	a.CreatedAt = a.CreatedAt.UTC()
	return a, nil
}

// scanApprovalFromRows is the rowReader variant of scanApproval.
func scanApprovalFromRows(r rowReader) (approval.Approval, error) {
	var (
		a          approval.Approval
		reviewedBy *string
		reason     *string
		reviewedAt *time.Time
		statusStr  string
	)
	err := r.Scan(
		&a.ID, &a.EntityID, &a.EntityType, &a.ActionName, &a.RequestedBy,
		&reviewedBy, &statusStr, &reason, &a.CreatedAt, &reviewedAt,
	)
	if err != nil {
		return approval.Approval{}, fmt.Errorf("scan approval: %w", err)
	}
	a.Status = approval.Status(statusStr)
	if reviewedBy != nil {
		a.ReviewedBy = *reviewedBy
	}
	if reason != nil {
		a.Reason = *reason
	}
	if reviewedAt != nil {
		a.ReviewedAt = reviewedAt.UTC()
	}
	a.CreatedAt = a.CreatedAt.UTC()
	return a, nil
}
