package main

import (
	"time"

	refundagno "github.com/kiffhq/kiff/examples/refund-agno"
	"github.com/kiffhq/kiff/pkg/kiff/proposal"
)

// proposalFromRequest builds a KIFF action proposal from the agent's tool
// call so the runtime can record reasoning + confidence as a first-class
// decision. The proposal is recorded BEFORE execution so the audit trail
// captures what the agent intended even if KIFF blocks the action.
func proposalFromRequest(id string, req agentRefundRequest, actionName string) proposal.ActionProposal {
	return proposal.ActionProposal{
		ID:               id,
		EntityID:         req.OrderID,
		EntityType:       refundagno.EntityOrder,
		ActionName:       actionName,
		ReasoningSummary: req.Reasoning,
		Confidence:       req.Confidence,
		ActorID:          refundagno.AgentActor.ID,
		CreatedAt:        time.Now().UTC(),
		Parameters: map[string]any{
			"amount_cents": req.AmountCents,
			"reason":       req.Reason,
		},
	}
}
