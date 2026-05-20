package approval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApprovalValidationFailsWhenRequiredFieldsMissing(t *testing.T) {
	err := Approval{}.Validate()
	if !errors.Is(err, ErrInvalidApproval) {
		t.Fatalf("expected ErrInvalidApproval, got %v", err)
	}
}

func TestInMemoryApprovalStoreSavesListsAndChecksGranted(t *testing.T) {
	store := NewInMemoryStore()
	approval := Approval{
		ID:          "approval-1",
		EntityID:    "attempt-1",
		EntityType:  "MissionAttempt",
		ActionName:  "EXECUTE_MOVE",
		RequestedBy: "agent-1",
		ReviewedBy:  "human-1",
		Status:      StatusGranted,
		Reason:      "bounded move is acceptable",
		CreatedAt:   time.Now().UTC(),
		ReviewedAt:  time.Now().UTC(),
	}

	if err := store.Save(context.Background(), approval); err != nil {
		t.Fatalf("save approval: %v", err)
	}

	approvals, err := store.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(approvals))
	}

	granted, err := store.IsGranted(context.Background(), "approval-1", "attempt-1", "EXECUTE_MOVE")
	if err != nil {
		t.Fatalf("check granted approval: %v", err)
	}
	if !granted {
		t.Fatal("expected approval to be granted")
	}
}

func TestInMemoryApprovalStoreOnlyGrantsMatchingEntityAndAction(t *testing.T) {
	store := NewInMemoryStore()
	approval := Approval{
		ID:          "approval-1",
		EntityID:    "attempt-1",
		EntityType:  "MissionAttempt",
		ActionName:  "EXECUTE_MOVE",
		RequestedBy: "agent-1",
		ReviewedBy:  "human-1",
		Status:      StatusGranted,
		CreatedAt:   time.Now().UTC(),
		ReviewedAt:  time.Now().UTC(),
	}
	if err := store.Save(context.Background(), approval); err != nil {
		t.Fatalf("save approval: %v", err)
	}

	granted, err := store.IsGranted(context.Background(), "approval-1", "attempt-2", "EXECUTE_MOVE")
	if err != nil {
		t.Fatalf("check granted approval: %v", err)
	}
	if granted {
		t.Fatal("expected approval not to grant a different entity")
	}
}
