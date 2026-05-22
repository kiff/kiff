package proposal

import (
	"errors"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/evidence"
)

var ErrInvalidProposal = errors.New("invalid proposal")

// ActionProposal captures a proposed action before KIFF validates it.
type ActionProposal struct {
	ID               string
	EntityID         string
	EntityType       string
	ActionName       string
	ActorID          string
	Parameters       map[string]any
	Evidence         []evidence.Ref
	ReasoningSummary string
	Confidence       float64
	CreatedAt        time.Time
}

// Validate checks the minimum fields needed to record a proposal.
func (p ActionProposal) Validate() error {
	if p.ID == "" {
		return errors.Join(ErrInvalidProposal, errors.New("proposal id is required"))
	}
	if p.EntityID == "" {
		return errors.Join(ErrInvalidProposal, errors.New("proposal entity id is required"))
	}
	if p.EntityType == "" {
		return errors.Join(ErrInvalidProposal, errors.New("proposal entity type is required"))
	}
	if p.ActionName == "" {
		return errors.Join(ErrInvalidProposal, errors.New("proposal action name is required"))
	}
	if p.ActorID == "" {
		return errors.Join(ErrInvalidProposal, errors.New("proposal actor id is required"))
	}
	if p.CreatedAt.IsZero() {
		return errors.Join(ErrInvalidProposal, errors.New("proposal created at is required"))
	}
	return nil
}

// Decision converts the proposal into an auditable KIFF decision.
func (p ActionProposal) Decision() (decision.Decision, error) {
	if err := p.Validate(); err != nil {
		return decision.Decision{}, err
	}
	return decision.Decision{
		ID:               p.ID,
		EntityID:         p.EntityID,
		EntityType:       p.EntityType,
		Kind:             decision.KindActionProposal,
		ProposedAction:   p.ActionName,
		Evidence:         p.Evidence,
		ReasoningSummary: p.ReasoningSummary,
		Confidence:       p.Confidence,
		ActorID:          p.ActorID,
		CreatedAt:        p.CreatedAt,
	}, nil
}

// ActionContext converts the proposal into an action context for validation.
func (p ActionProposal) ActionContext(currentState string, proposalActor actor.Actor) (action.ActionContext, error) {
	if err := p.Validate(); err != nil {
		return action.ActionContext{}, err
	}
	if proposalActor.ID == "" {
		proposalActor.ID = p.ActorID
	}
	return action.ActionContext{
		ActionName:   p.ActionName,
		EntityID:     p.EntityID,
		EntityType:   p.EntityType,
		CurrentState: currentState,
		Actor:        proposalActor,
		Parameters:   p.Parameters,
	}, nil
}
