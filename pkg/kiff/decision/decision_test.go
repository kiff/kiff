package decision

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryDecisionStoreAppendsAndListsDecisions(t *testing.T) {
	store := NewInMemoryStore()
	dec := Decision{
		ID:         "dec-1",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Kind:       KindActionProposal,
		ActorID:    "agent-1",
		CreatedAt:  time.Now().UTC(),
	}

	if err := store.Append(context.Background(), dec); err != nil {
		t.Fatalf("append decision: %v", err)
	}
	decisions, err := store.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
}
