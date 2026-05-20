package action

import (
	"context"
	"errors"
	"testing"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
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
