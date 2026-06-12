package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/state"
)

func TestBuilderProducesValidDefinition(t *testing.T) {
	def, err := New("orders").
		Entity("Order").
		Event("ORDER_CREATED").
		Event("ORDER_PAID").
		Transition("ORDER_CREATED", "", "CREATED").
		Transition("ORDER_PAID", "CREATED", "PAID").
		Allow("CREATED", "MARK_PAID").
		Action(action.ActionContract{
			Name: "MARK_PAID", AllowedStates: []string{"CREATED"},
		}).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !def.KnowsEntityType("Order") {
		t.Fatal("expected Order entity type")
	}
	if !def.KnowsEventType("ORDER_CREATED") {
		t.Fatal("expected ORDER_CREATED event type")
	}
	contract, ok := def.Actions.Get("MARK_PAID")
	if !ok || contract.Name != "MARK_PAID" {
		t.Fatal("expected MARK_PAID contract registered")
	}
	// State machine accepts the declared transition
	next, err := def.StateMachine.Apply(context.Background(),
		state.State{EntityID: "o-1", EntityType: "Order"},
		event.Event{
			ID: "e-1", Type: "ORDER_CREATED", EntityID: "o-1", EntityType: "Order",
			Source: "t", ActorID: "a", OccurredAt: time.Now().UTC(),
		})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if next.Value != "CREATED" {
		t.Fatalf("expected CREATED, got %q", next.Value)
	}
}

func TestBuilderRejectsMissingName(t *testing.T) {
	_, err := New("").Build()
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition, got %v", err)
	}
}

func TestBuilderSurfacesDuplicateActionRegistration(t *testing.T) {
	_, err := New("dup").
		Action(action.ActionContract{Name: "X"}).
		Action(action.ActionContract{Name: "X"}).
		Build()
	if !errors.Is(err, action.ErrDuplicateAction) {
		t.Fatalf("expected ErrDuplicateAction, got %v", err)
	}
}
