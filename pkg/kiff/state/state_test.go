package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/event"
)

func TestTransitionMachineAppliesValidTransition(t *testing.T) {
	machine := NewTransitionMachine(Transition{EventType: "ATTEMPT_CREATED", From: "SUBMITTED", To: "ACTIVE"})

	next, err := machine.Apply(context.Background(), State{EntityID: "attempt-1", EntityType: "MissionAttempt", Value: "SUBMITTED"}, event.Event{
		ID:         "evt-1",
		Type:       "ATTEMPT_CREATED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("apply transition: %v", err)
	}
	if next.Value != "ACTIVE" {
		t.Fatalf("expected ACTIVE, got %q", next.Value)
	}
}

func TestTransitionMachineReturnsErrorForInvalidTransition(t *testing.T) {
	machine := NewTransitionMachine(Transition{EventType: "ATTEMPT_CREATED", From: "SUBMITTED", To: "ACTIVE"})

	_, err := machine.Apply(context.Background(), State{EntityID: "attempt-1", EntityType: "MissionAttempt", Value: "COMPLETED"}, event.Event{
		ID:         "evt-1",
		Type:       "ATTEMPT_CREATED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		OccurredAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestRebuildAppliesEventsInOrder(t *testing.T) {
	machine := NewTransitionMachine(
		Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"},
		Transition{EventType: "ATTEMPT_CREATED", From: "SUBMITTED", To: "ACTIVE"},
	)
	events := []event.Event{
		testEvent("evt-1", "MISSION_SUBMITTED", "attempt-1"),
		testEvent("evt-2", "ATTEMPT_CREATED", "attempt-1"),
	}

	result, err := Rebuild(context.Background(), machine, events)
	if err != nil {
		t.Fatalf("rebuild state: %v", err)
	}
	if result.State.Value != "ACTIVE" {
		t.Fatalf("expected ACTIVE, got %q", result.State.Value)
	}
	if result.State.Version != 2 {
		t.Fatalf("expected version 2, got %d", result.State.Version)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 replay steps, got %d", len(result.Steps))
	}
	if result.Steps[1].From != "SUBMITTED" || result.Steps[1].To != "ACTIVE" {
		t.Fatalf("unexpected second replay step: %#v", result.Steps[1])
	}
}

func TestRebuildRejectsMixedEntityEvents(t *testing.T) {
	machine := NewTransitionMachine(Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"})
	events := []event.Event{
		testEvent("evt-1", "MISSION_SUBMITTED", "attempt-1"),
		testEvent("evt-2", "MISSION_SUBMITTED", "attempt-2"),
	}

	_, err := Rebuild(context.Background(), machine, events)
	if !errors.Is(err, ErrInvalidReplay) {
		t.Fatalf("expected ErrInvalidReplay, got %v", err)
	}
}

func testEvent(id, eventType, entityID string) event.Event {
	return event.Event{
		ID:         id,
		Type:       eventType,
		EntityID:   entityID,
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "actor",
		OccurredAt: time.Now().UTC(),
	}
}
