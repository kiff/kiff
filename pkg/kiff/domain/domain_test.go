package domain

import (
	"errors"
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/state"
)

func TestDefinitionValidationRequiresCoreWiring(t *testing.T) {
	if err := (Definition{}).Validate(); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition, got %v", err)
	}

	definition := Definition{
		Name:         "mission",
		StateMachine: state.NewTransitionMachine(),
		Actions:      action.NewCatalog(),
	}
	if err := definition.Validate(); err != nil {
		t.Fatalf("validate definition: %v", err)
	}
}

func TestDefinitionKnowsDeclaredTypes(t *testing.T) {
	definition := Definition{
		Name:        "mission",
		EntityTypes: []string{"MissionAttempt"},
		EventTypes:  []string{"MISSION_SUBMITTED"},
	}

	if !definition.KnowsEntityType("MissionAttempt") {
		t.Fatal("expected known entity type")
	}
	if definition.KnowsEntityType("Order") {
		t.Fatal("did not expect unknown entity type")
	}
	if !definition.KnowsEventType("MISSION_SUBMITTED") {
		t.Fatal("expected known event type")
	}
	if definition.KnowsEventType("ORDER_CREATED") {
		t.Fatal("did not expect unknown event type")
	}
}
