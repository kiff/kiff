package main

import (
	"time"

	supportops "github.com/kiff/kiff/examples/support-ops"
	"github.com/kiff/kiff/pkg/kiff/proposal"
)

// proposalFromRequest builds a KIFF action proposal from the agent's
// tool call so the runtime can record reasoning + confidence as a
// first-class decision before any execution happens.
func proposalFromRequest(id string, req agentDecideRequest, actionName string) proposal.ActionProposal {
	return proposal.ActionProposal{
		ID:               id,
		EntityID:         req.TicketID,
		EntityType:       supportops.EntityTicket,
		ActionName:       actionName,
		ReasoningSummary: req.Reasoning,
		Confidence:       req.Confidence,
		ActorID:          supportops.AgentActor.ID,
		CreatedAt:        time.Now().UTC(),
		Parameters:       req.Parameters,
	}
}
