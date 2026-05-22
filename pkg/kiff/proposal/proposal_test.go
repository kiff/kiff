package proposal

import (
	"errors"
	"testing"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
)

func TestActionProposalValidationFailsWhenRequiredFieldsMissing(t *testing.T) {
	err := ActionProposal{}.Validate()
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("expected ErrInvalidProposal, got %v", err)
	}
}

func TestActionProposalConvertsToDecision(t *testing.T) {
	proposal := validActionProposal()

	dec, err := proposal.Decision()
	if err != nil {
		t.Fatalf("convert proposal to decision: %v", err)
	}
	if dec.Kind != decision.KindActionProposal {
		t.Fatalf("expected action proposal decision, got %q", dec.Kind)
	}
	if dec.ProposedAction != proposal.ActionName {
		t.Fatalf("expected proposed action %q, got %q", proposal.ActionName, dec.ProposedAction)
	}
}

func TestActionProposalConvertsToActionContext(t *testing.T) {
	proposal := validActionProposal()

	ctx, err := proposal.ActionContext("ACTIVE", actor.Actor{ID: proposal.ActorID, Type: actor.TypeAgent})
	if err != nil {
		t.Fatalf("convert proposal to action context: %v", err)
	}
	if ctx.ActionName != proposal.ActionName {
		t.Fatalf("expected action name %q, got %q", proposal.ActionName, ctx.ActionName)
	}
	if ctx.CurrentState != "ACTIVE" {
		t.Fatalf("expected current state ACTIVE, got %q", ctx.CurrentState)
	}
	if ctx.Parameters["move"] != "draft the first bounded move" {
		t.Fatalf("expected proposal parameters in action context, got %#v", ctx.Parameters)
	}
}

func validActionProposal() ActionProposal {
	return ActionProposal{
		ID:               "proposal-1",
		EntityID:         "attempt-1",
		EntityType:       "MissionAttempt",
		ActionName:       "PROPOSE_MOVE",
		ActorID:          "mission-agent",
		Parameters:       map[string]any{"move": "draft the first bounded move"},
		ReasoningSummary: "The attempt is active and can accept a proposed move.",
		Confidence:       0.82,
		CreatedAt:        time.Now().UTC(),
	}
}
