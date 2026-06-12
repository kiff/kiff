package permission

import (
	"context"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/actor"
)

func TestSimplePolicyAllowsActorAndRolePermissions(t *testing.T) {
	policy := NewSimplePolicy()
	policy.GrantActor("agent-1", "mission.propose_move")
	policy.GrantRole("approver", "mission.request_human_approval")
	policy.AssignRole("human-1", "approver")

	if !policy.Can(context.Background(), actor.Actor{ID: "agent-1"}, "mission.propose_move") {
		t.Fatal("expected direct actor permission")
	}
	if !policy.Can(context.Background(), actor.Actor{ID: "human-1"}, "mission.request_human_approval") {
		t.Fatal("expected role permission from policy-owned membership")
	}
	if policy.Can(context.Background(), actor.Actor{ID: "agent-2"}, "mission.propose_move") {
		t.Fatal("did not expect ungranted permission")
	}
}

// TestSimplePolicyIgnoresCallerSuppliedRoles is the #19 acceptance test:
// a role placed on the caller-built actor carries no authority. Only
// policy-owned assignments (AssignRole) grant permissions.
func TestSimplePolicyIgnoresCallerSuppliedRoles(t *testing.T) {
	policy := NewSimplePolicy()
	policy.GrantRole("admin", "orders.refund")

	// The caller asserts the admin role on the actor it submits, but the
	// policy never assigned admin to this actor.
	forged := actor.Actor{ID: "agent", Roles: []string{"admin"}}
	if policy.Can(context.Background(), forged, "orders.refund") {
		t.Fatal("caller-supplied actor.Roles must not grant a permission the policy did not assign")
	}

	// Once the policy assigns the role by actor ID, authority follows.
	policy.AssignRole("agent", "admin")
	if !policy.Can(context.Background(), actor.Actor{ID: "agent"}, "orders.refund") {
		t.Fatal("policy-assigned role should grant the permission")
	}

	// RevokeRole removes it again.
	policy.RevokeRole("agent", "admin")
	if policy.Can(context.Background(), actor.Actor{ID: "agent"}, "orders.refund") {
		t.Fatal("revoked role must no longer grant the permission")
	}
}
