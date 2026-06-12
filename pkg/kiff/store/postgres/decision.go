package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/evidence"
)

// DecisionStore persists decision.Decision rows in the kiff_decisions table.
type DecisionStore struct {
	pool *pgxpool.Pool
}

// NewDecisionStore returns a Postgres-backed decision store.
func NewDecisionStore(pool *pgxpool.Pool) *DecisionStore {
	return &DecisionStore{pool: pool}
}

// Append validates and stores a decision.
func (s *DecisionStore) Append(ctx context.Context, d decision.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}

	evidenceJSON, err := marshalJSONArrayOrEmpty(d.Evidence)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO kiff_decisions (
			id, entity_id, entity_type, kind, proposed_action, evidence,
			reasoning_summary, confidence, actor_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		d.ID, d.EntityID, d.EntityType, string(d.Kind),
		nullableString(d.ProposedAction), evidenceJSON,
		nullableString(d.ReasoningSummary), d.Confidence,
		d.ActorID, d.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert decision: %w", err)
	}
	return nil
}

// List returns decisions for an entity in insertion order. An empty entity id
// returns all decisions.
func (s *DecisionStore) List(ctx context.Context, entityID string) ([]decision.Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	const baseQuery = `
		SELECT id, entity_id, entity_type, kind, proposed_action, evidence,
		       reasoning_summary, confidence, actor_id, created_at
		FROM kiff_decisions
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
		return nil, fmt.Errorf("query decisions: %w", err)
	}
	defer rows.Close()

	out := []decision.Decision{}
	for rows.Next() {
		var (
			d                decision.Decision
			kindStr          string
			proposedAction   *string
			reasoningSummary *string
			evidenceJSON     []byte
			confidence       *float64
		)
		if err := rows.Scan(
			&d.ID, &d.EntityID, &d.EntityType, &kindStr,
			&proposedAction, &evidenceJSON, &reasoningSummary,
			&confidence, &d.ActorID, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		d.Kind = decision.Kind(kindStr)
		if proposedAction != nil {
			d.ProposedAction = *proposedAction
		}
		if reasoningSummary != nil {
			d.ReasoningSummary = *reasoningSummary
		}
		if confidence != nil {
			d.Confidence = *confidence
		}
		if len(evidenceJSON) > 0 && string(evidenceJSON) != "[]" {
			var refs []evidence.Ref
			if err := json.Unmarshal(evidenceJSON, &refs); err != nil {
				return nil, fmt.Errorf("unmarshal evidence: %w", err)
			}
			d.Evidence = refs
		}
		d.CreatedAt = d.CreatedAt.UTC()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate decisions: %w", err)
	}
	return out, nil
}

// nullableString returns a *string for empty values so Postgres stores NULL
// instead of an empty string. This matches the schema's nullable columns and
// keeps queries semantically correct.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
