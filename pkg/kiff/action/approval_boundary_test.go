package action_test

// This file lives in the external test package (action_test) on purpose:
// it sees only the action package's *public* API, exactly like a caller
// that imports the framework. It documents the self-approval trust
// boundary (#12): there is no public path to mark an ActionContext
// approved, so a non-runtime caller cannot bypass approval.

import (
	"context"
	"errors"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

// A caller can build an ActionContext but cannot set the approved bit. The
// separate negative-compilation test proves that the unexported field and the
// internal trust capability cannot be used from an external module. This test
// covers the corresponding runtime behavior: a caller-built context for an
// ApprovalRequired contract fails validation.
func TestCallerCannotSelfApproveViaPublicAPI(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")

	// Everything a caller can legitimately set on the context.
	actionCtx := action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent", Roles: []string{"agent"}},
	}
	if actionCtx.IsApproved() {
		t.Fatal("a caller-built context must never report approved")
	}

	contract := action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
	}

	_, err := action.NewDefaultValidator().Validate(context.Background(), actionCtx, contract, policy)
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired with no runtime-granted approval, got %v", err)
	}
}
