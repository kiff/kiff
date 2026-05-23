package main

import (
	"time"

	aicafeops "github.com/kiffhq/kiff/examples/ai-cafe-ops"
	"github.com/kiffhq/kiff/pkg/kiff/proposal"
)

// proposalFromRequest builds a KIFF action proposal from the agent's
// tool call so the runtime can record reasoning + confidence as a
// first-class decision before any execution happens.
func proposalFromRequest(id string, req agentDecideRequest, actionName string) proposal.ActionProposal {
	return proposal.ActionProposal{
		ID:               id,
		EntityID:         req.ShiftID,
		EntityType:       aicafeops.EntityShift,
		ActionName:       actionName,
		ReasoningSummary: req.Reasoning,
		Confidence:       req.Confidence,
		ActorID:          aicafeops.AgentActor.ID,
		CreatedAt:        time.Now().UTC(),
		Parameters:       req.Parameters,
	}
}
