package securityincident

import (
	"context"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/evidence"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

type Agent interface {
	Propose(context.Context, AgentRequest) (AgentProposal, error)
}

type AgentRequest struct {
	IncidentID     string
	CurrentState   string
	AllowedActions []string
	OperatorInput  string
	Facts          map[string]any
}

type AgentProposal struct {
	ActionName       string
	Parameters       map[string]any
	ReasoningSummary string
	Confidence       float64
}

type ScriptedAgent struct {
	Proposals []AgentProposal
}

func (a *ScriptedAgent) Propose(context.Context, AgentRequest) (AgentProposal, error) {
	if len(a.Proposals) == 0 {
		return AgentProposal{
			ActionName:       "NO_ACTION",
			ReasoningSummary: "scripted security incident agent has no remaining proposals",
			Confidence:       1,
		}, nil
	}
	next := a.Proposals[0]
	a.Proposals = a.Proposals[1:]
	return next, nil
}

func RecordAgentProposal(ctx context.Context, rt *runtime.Runtime, incidentID, operatorInput string, proposal AgentProposal) error {
	actionName := proposal.ActionName
	if actionName == "NO_ACTION" {
		actionName = ""
	}
	return rt.ProposeDecision(ctx, decision.Decision{
		ID:             fmt.Sprintf("dec-%s-%d", incidentID, time.Now().UnixNano()),
		EntityID:       incidentID,
		EntityType:     EntitySecurityIncident,
		Kind:           decision.KindActionProposal,
		ProposedAction: actionName,
		Evidence: []evidence.Ref{
			{
				ID:        fmt.Sprintf("input-%d", time.Now().UnixNano()),
				Kind:      evidence.KindSystemData,
				Source:    "operator-input",
				Summary:   operatorInput,
				CreatedAt: time.Now().UTC(),
			},
		},
		ReasoningSummary: proposal.ReasoningSummary,
		Confidence:       proposal.Confidence,
		ActorID:          SecurityAgentActor.ID,
		CreatedAt:        time.Now().UTC(),
	})
}

func ApplyAgentProposal(ctx context.Context, rt *runtime.Runtime, incidentID, currentState string, proposal AgentProposal) (action.ActionResult, error) {
	contract, err := Contract(rt, proposal.ActionName)
	if err != nil {
		return action.ActionResult{}, err
	}
	actionCtx := action.ActionContext{
		ActionName:     proposal.ActionName,
		EntityID:       incidentID,
		EntityType:     EntitySecurityIncident,
		CurrentState:   currentState,
		Actor:          actorForAction(proposal.ActionName),
		Parameters:     proposal.Parameters,
		IdempotencyKey: stringParam(proposal.Parameters, "idempotency_key"),
	}
	return rt.ExecuteAction(ctx, actionCtx, contract)
}

func actorForAction(actionName string) actor.Actor {
	switch actionName {
	case ActionExecuteSessionReset, ActionRevokeUserAccess:
		return IdentityServiceActor
	default:
		return SecurityAgentActor
	}
}
