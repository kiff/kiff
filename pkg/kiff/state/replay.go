package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/kiff/kiff/pkg/kiff/event"
)

var ErrInvalidReplay = errors.New("invalid state replay")

// ReplayStep records one event-to-state transition during state rebuild.
type ReplayStep struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Version   int    `json:"version"`
}

// ReplayResult captures the rebuilt state and transition path for an entity.
type ReplayResult struct {
	EntityID   string       `json:"entity_id"`
	EntityType string       `json:"entity_type"`
	State      State        `json:"state"`
	Steps      []ReplayStep `json:"steps"`
}

// Rebuild applies stored events in order to reconstruct state.
func Rebuild(ctx context.Context, machine StateMachine, events []event.Event) (ReplayResult, error) {
	if err := ctx.Err(); err != nil {
		return ReplayResult{}, err
	}
	if machine == nil {
		return ReplayResult{}, fmt.Errorf("%w: state machine is required", ErrInvalidReplay)
	}
	if len(events) == 0 {
		return ReplayResult{}, fmt.Errorf("%w: at least one event is required", ErrInvalidReplay)
	}

	entityID := events[0].EntityID
	entityType := events[0].EntityType
	current := State{EntityID: entityID, EntityType: entityType}
	steps := make([]ReplayStep, 0, len(events))

	for _, ev := range events {
		if err := ev.Validate(); err != nil {
			return ReplayResult{}, err
		}
		if ev.EntityID != entityID {
			return ReplayResult{}, fmt.Errorf("%w: event %q belongs to entity %q, expected %q", ErrInvalidReplay, ev.ID, ev.EntityID, entityID)
		}
		if ev.EntityType != entityType {
			return ReplayResult{}, fmt.Errorf("%w: event %q has entity type %q, expected %q", ErrInvalidReplay, ev.ID, ev.EntityType, entityType)
		}

		from := current.Value
		next, err := machine.Apply(ctx, current, ev)
		if err != nil {
			return ReplayResult{}, err
		}
		steps = append(steps, ReplayStep{
			EventID:   ev.ID,
			EventType: ev.Type,
			From:      from,
			To:        next.Value,
			Version:   next.Version,
		})
		current = next
	}

	return ReplayResult{
		EntityID:   entityID,
		EntityType: entityType,
		State:      current,
		Steps:      steps,
	}, nil
}
