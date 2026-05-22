package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
)

// AuditStore persists audit.Record rows in the kiff_audit table.
type AuditStore struct {
	pool *pgxpool.Pool
}

// NewAuditStore returns a Postgres-backed audit store.
func NewAuditStore(pool *pgxpool.Pool) *AuditStore {
	return &AuditStore{pool: pool}
}

// Append validates and stores an audit record.
func (s *AuditStore) Append(ctx context.Context, r audit.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}

	dataJSON, err := marshalJSONOrEmpty(r.Data)
	if err != nil {
		return fmt.Errorf("marshal audit data: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO kiff_audit (
			id, kind, entity_id, entity_type, actor_id, message, data,
			trace_id, correlation_id, causation_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`,
		r.ID, string(r.Kind), r.EntityID, r.EntityType,
		nullableString(r.ActorID), nullableString(r.Message), dataJSON,
		nullableString(r.TraceID), nullableString(r.CorrelationID), nullableString(r.CausationID),
		r.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}

// List returns audit records for an entity in chronological order, matching
// the in-memory store. An empty entity id returns all records.
func (s *AuditStore) List(ctx context.Context, entityID string) ([]audit.Record, error) {
	return s.Query(ctx, audit.Filter{EntityID: entityID})
}

// Query returns audit records matching the filter in chronological order.
func (s *AuditStore) Query(ctx context.Context, f audit.Filter) ([]audit.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	const baseQuery = `
		SELECT id, kind, entity_id, entity_type, actor_id, message, data,
		       trace_id, correlation_id, causation_id, created_at
		FROM kiff_audit
	`

	clauses := []string{}
	args := []any{}
	if f.EntityID != "" {
		args = append(args, f.EntityID)
		clauses = append(clauses, fmt.Sprintf("entity_id = $%d", len(args)))
	}
	if f.Kind != "" {
		args = append(args, string(f.Kind))
		clauses = append(clauses, fmt.Sprintf("kind = $%d", len(args)))
	}
	if f.ActorID != "" {
		args = append(args, f.ActorID)
		clauses = append(clauses, fmt.Sprintf("actor_id = $%d", len(args)))
	}
	if f.TraceID != "" {
		args = append(args, f.TraceID)
		clauses = append(clauses, fmt.Sprintf("trace_id = $%d", len(args)))
	}
	if f.CorrelationID != "" {
		args = append(args, f.CorrelationID)
		clauses = append(clauses, fmt.Sprintf("correlation_id = $%d", len(args)))
	}

	query := baseQuery
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at, inserted_at, id"

	rows, err := queryAll(ctx, s.pool, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit: %w", err)
	}
	defer rows.Close()

	out := []audit.Record{}
	for rows.Next() {
		var (
			r             audit.Record
			kindStr       string
			actorID       *string
			message       *string
			traceID       *string
			correlationID *string
			causationID   *string
			dataJSON      []byte
		)
		if err := rows.Scan(
			&r.ID, &kindStr, &r.EntityID, &r.EntityType, &actorID, &message, &dataJSON,
			&traceID, &correlationID, &causationID, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit record: %w", err)
		}
		r.Kind = audit.Kind(kindStr)
		if actorID != nil {
			r.ActorID = *actorID
		}
		if message != nil {
			r.Message = *message
		}
		if traceID != nil {
			r.TraceID = *traceID
		}
		if correlationID != nil {
			r.CorrelationID = *correlationID
		}
		if causationID != nil {
			r.CausationID = *causationID
		}
		if len(dataJSON) > 0 {
			if err := unmarshalToMap(dataJSON, &r.Data); err != nil {
				return nil, fmt.Errorf("unmarshal audit data: %w", err)
			}
		}
		r.CreatedAt = r.CreatedAt.UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit: %w", err)
	}
	return out, nil
}

// Compile-time guard: keep pgxpool used so go mod tidy keeps it.
var _ = (*pgxpool.Pool)(nil)
