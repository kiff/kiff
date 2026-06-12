package action

import (
	"context"
	"errors"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

func TestValidationBlocksActionWhenStateIsNotAllowed(t *testing.T) {
	validator := NewDefaultValidator()
	_, err := validator.Validate(context.Background(), ActionContext{
		ActionName:   "EXECUTE_MOVE",
		CurrentState: "ACTIVE",
		Actor:        actor.Actor{ID: "agent"},
	}, ActionContract{
		Name:          "EXECUTE_MOVE",
		AllowedStates: []string{"WAITING_APPROVAL"},
	}, nil)

	if !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("expected ErrStateNotAllowed, got %v", err)
	}
}

func TestValidationBlocksActionWhenRequiredPermissionIsMissing(t *testing.T) {
	validator := NewDefaultValidator()
	policy := permission.NewSimplePolicy()

	_, err := validator.Validate(context.Background(), ActionContext{
		ActionName:   "EXECUTE_MOVE",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent"},
	}, ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
	}, policy)

	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}

func TestValidationMarksActionAsRequiringApproval(t *testing.T) {
	validator := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")

	result, err := validator.Validate(context.Background(), ActionContext{
		ActionName:   "EXECUTE_MOVE",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent"},
	}, ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: ApprovalRequired,
	}, policy)

	if !result.RequiresApproval {
		t.Fatal("expected validation result to require approval")
	}
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}

func TestActionResultNormalizeSetsSucceededStatus(t *testing.T) {
	result := ActionResult{Executed: true}.Normalize()
	if result.Status != ExecutionSucceeded {
		t.Fatalf("expected succeeded status, got %q", result.Status)
	}
	if result.ExecutedAt.IsZero() {
		t.Fatal("expected executed at timestamp")
	}
}

func TestFailedResultRecordsError(t *testing.T) {
	result := FailedResult("EXECUTE_MOVE", "attempt-1", errors.New("executor failed"))
	if result.Status != ExecutionFailed {
		t.Fatalf("expected failed status, got %q", result.Status)
	}
	if result.Error != "executor failed" {
		t.Fatalf("expected error message, got %q", result.Error)
	}
	if result.Executed {
		t.Fatal("expected failed result not to be executed")
	}
}
