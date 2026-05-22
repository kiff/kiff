package permission

import (
	"context"
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/actor"
)

func TestSimplePolicyAllowsActorAndRolePermissions(t *testing.T) {
	policy := NewSimplePolicy()
	policy.GrantActor("agent-1", "mission.propose_move")
	policy.GrantRole("approver", "mission.request_human_approval")

	if !policy.Can(context.Background(), actor.Actor{ID: "agent-1"}, "mission.propose_move") {
		t.Fatal("expected direct actor permission")
	}
	if !policy.Can(context.Background(), actor.Actor{ID: "human-1", Roles: []string{"approver"}}, "mission.request_human_approval") {
		t.Fatal("expected role permission")
	}
	if policy.Can(context.Background(), actor.Actor{ID: "agent-2"}, "mission.propose_move") {
		t.Fatal("did not expect ungranted permission")
	}
}
