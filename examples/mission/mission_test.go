package mission

import "testing"

func TestMissionDomainDefinitionDeclaresVocabulary(t *testing.T) {
	definition, err := NewDomainDefinition()
	if err != nil {
		t.Fatalf("new domain definition: %v", err)
	}
	if err := definition.Validate(); err != nil {
		t.Fatalf("validate domain definition: %v", err)
	}
	if !definition.KnowsEntityType(EntityTypeMissionAttempt) {
		t.Fatal("expected mission attempt entity type")
	}
	if !definition.KnowsEventType(EventMissionSubmitted) {
		t.Fatal("expected mission submitted event type")
	}
	if _, ok := definition.Actions.Get(ActionExecuteMove); !ok {
		t.Fatal("expected execute move action contract")
	}
}

func TestMissionHappyPathWorks(t *testing.T) {
	result, err := RunHappyPath()
	if err != nil {
		t.Fatalf("run mission happy path: %v", err)
	}
	if result.FinalState.Value != StateCompleted {
		t.Fatalf("expected final state %q, got %q", StateCompleted, result.FinalState.Value)
	}
	if len(result.Audit) == 0 {
		t.Fatal("expected audit records")
	}
}
