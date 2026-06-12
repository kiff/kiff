package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kiff/kiff/pkg/kiff/event"
)

// EventStore persists event.Event rows in the kiff_events table.
type EventStore struct {
	pool *pgxpool.Pool
}

// NewEventStore returns a Postgres-backed event store.
func NewEventStore(pool *pgxpool.Pool) *EventStore {
	return &EventStore{pool: pool}
}

// Append validates and stores an event. Duplicate ids return an error from
// Postgres (PRIMARY KEY violation), which is the same contract the in-memory
// store has implicitly via the conformance suite assumptions.
func (s *EventStore) Append(ctx context.Context, e event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return err
	}

	metadata, err := json.Marshal(e.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	payload, err := marshalJSONOrEmpty(e.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO kiff_events (
			id, type, entity_id, entity_type, source, actor_id,
			occurred_at, metadata, payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		e.ID, e.Type, e.EntityID, e.EntityType, e.Source, e.ActorID,
		e.OccurredAt.UTC(), metadata, payload,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// List returns events for an entity in insertion order. An empty entity id
// returns all events.
func (s *EventStore) List(ctx context.Context, entityID string) ([]event.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	const baseQuery = `
		SELECT id, type, entity_id, entity_type, source, actor_id,
		       occurred_at, metadata, payload
		FROM kiff_events
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
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	out := []event.Event{}
	for rows.Next() {
		var (
			ev          event.Event
			metaJSON    []byte
			payloadJSON []byte
		)
		if err := rows.Scan(
			&ev.ID, &ev.Type, &ev.EntityID, &ev.EntityType, &ev.Source, &ev.ActorID,
			&ev.OccurredAt, &metaJSON, &payloadJSON,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if len(metaJSON) > 0 {
			if err := json.Unmarshal(metaJSON, &ev.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		if len(payloadJSON) > 0 {
			if err := unmarshalToMap(payloadJSON, &ev.Payload); err != nil {
				return nil, fmt.Errorf("unmarshal payload: %w", err)
			}
		}
		ev.OccurredAt = ev.OccurredAt.UTC()
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}
