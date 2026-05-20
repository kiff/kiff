package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
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
