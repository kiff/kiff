package event

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEventValidationFailsWhenRequiredFieldsMissing(t *testing.T) {
	err := Event{}.Validate()
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestInMemoryEventStoreAppendsAndListsEvents(t *testing.T) {
	store := NewInMemoryStore()
	ev := Event{
		ID:         "evt-1",
		Type:       "MISSION_SUBMITTED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "actor-1",
		OccurredAt: time.Now().UTC(),
	}

	if err := store.Append(context.Background(), ev); err != nil {
		t.Fatalf("append event: %v", err)
	}

	events, err := store.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID != ev.ID {
		t.Fatalf("expected event id %q, got %q", ev.ID, events[0].ID)
	}
}
