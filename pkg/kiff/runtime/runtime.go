package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/decision"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
)

// Config wires the stores and policies used by a Runtime.
type Config struct {
	EventStore       event.Store
	DecisionStore    decision.Store
	AuditStore       audit.Store
	StateMachine     state.StateMachine
	PermissionPolicy permission.Policy
	ActionValidator  action.Validator
}

// Runtime coordinates event ingestion, decisions, action validation, execution, and audit.
type Runtime struct {
	Events      event.Store
	Decisions   decision.Store
	Audit       audit.Store
	States      state.StateMachine
	Permissions permission.Policy
	Validator   action.Validator
}

// New creates a runtime with in-memory defaults for omitted stores.
func New(config Config) *Runtime {
	rt := &Runtime{
		Events:      config.EventStore,
		Decisions:   config.DecisionStore,
		Audit:       config.AuditStore,
		States:      config.StateMachine,
		Permissions: config.PermissionPolicy,
		Validator:   config.ActionValidator,
	}
	if rt.Events == nil {
		rt.Events = event.NewInMemoryStore()
	}
	if rt.Decisions == nil {
		rt.Decisions = decision.NewInMemoryStore()
	}
	if rt.Audit == nil {
		rt.Audit = audit.NewInMemoryStore()
	}
	if rt.Validator == nil {
		rt.Validator = action.NewDefaultValidator()
	}
	return rt
}

// IngestEvent stores an event, applies state when a state machine is present, and audits both facts.
func (r *Runtime) IngestEvent(ev event.Event) error {
	ctx := context.Background()
	if err := r.Events.Append(ctx, ev); err != nil {
		return err
	}
	if err := r.appendAudit(ctx, audit.KindEventIngested, ev.EntityID, ev.EntityType, ev.ActorID, "event ingested", map[string]any{
		"event_id":   ev.ID,
		"event_type": ev.Type,
	}); err != nil {
		return err
	}

	if r.States == nil {
		return nil
	}

	current, ok, err := r.States.Current(ctx, ev.EntityID)
	if err != nil {
		return err
	}
	if !ok {
		current = state.State{EntityID: ev.EntityID, EntityType: ev.EntityType}
	}
	next, err := r.States.Apply(ctx, current, ev)
	if err != nil {
		return err
	}
	return r.appendAudit(ctx, audit.KindStateChanged, ev.EntityID, ev.EntityType, ev.ActorID, "state changed", map[string]any{
		"from":       current.Value,
		"to":         next.Value,
		"event_id":   ev.ID,
		"event_type": ev.Type,
	})
}

// ProposeDecision stores and audits a decision.
func (r *Runtime) ProposeDecision(d decision.Decision) error {
	ctx := context.Background()
	if err := r.Decisions.Append(ctx, d); err != nil {
		return err
	}
	return r.appendAudit(ctx, audit.KindDecisionProposed, d.EntityID, d.EntityType, d.ActorID, "decision proposed", map[string]any{
		"decision_id":     d.ID,
		"decision_kind":   d.Kind,
		"proposed_action": d.ProposedAction,
		"confidence":      d.Confidence,
	})
}

// ValidateAction validates an action and appends the corresponding audit record.
func (r *Runtime) ValidateAction(actionCtx action.ActionContext, contract action.ActionContract) error {
	ctx := context.Background()
	result, err := r.Validator.Validate(ctx, actionCtx, contract, r.Permissions)
	if err != nil {
		kind := audit.KindActionFailed
		message := "action validation failed"
		if errors.Is(err, action.ErrApprovalRequired) {
			kind = audit.KindApprovalRequired
			message = "approval required"
		}
		auditErr := r.appendAudit(ctx, kind, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, message, map[string]any{
			"action":            contract.Name,
			"error":             err.Error(),
			"requires_approval": result.RequiresApproval,
		})
		if auditErr != nil {
			return auditErr
		}
		return err
	}
	return r.appendAudit(ctx, audit.KindActionValidated, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action validated", map[string]any{
		"action":            contract.Name,
		"requires_approval": result.RequiresApproval,
	})
}

// ExecuteAction validates, executes, and audits an action.
func (r *Runtime) ExecuteAction(actionCtx action.ActionContext, contract action.ActionContract) (action.ActionResult, error) {
	if err := r.ValidateAction(actionCtx, contract); err != nil {
		return action.ActionResult{}, err
	}

	ctx := context.Background()
	result := action.ActionResult{
		ActionName: contract.Name,
		EntityID:   actionCtx.EntityID,
		Executed:   true,
		Message:    "action executed",
		ExecutedAt: time.Now().UTC(),
	}
	var err error
	if contract.Executor != nil {
		result, err = contract.Executor(ctx, actionCtx)
	}
	if err != nil {
		auditErr := r.appendAudit(ctx, audit.KindActionFailed, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action execution failed", map[string]any{
			"action": contract.Name,
			"error":  err.Error(),
		})
		if auditErr != nil {
			return action.ActionResult{}, auditErr
		}
		return action.ActionResult{}, err
	}
	if result.ExecutedAt.IsZero() {
		result.ExecutedAt = time.Now().UTC()
	}
	if result.ActionName == "" {
		result.ActionName = contract.Name
	}
	if result.EntityID == "" {
		result.EntityID = actionCtx.EntityID
	}
	return result, r.appendAudit(ctx, audit.KindActionExecuted, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action executed", map[string]any{
		"action":  contract.Name,
		"message": result.Message,
	})
}

func (r *Runtime) appendAudit(ctx context.Context, kind audit.Kind, entityID, entityType, actorID, message string, data map[string]any) error {
	return r.Audit.Append(ctx, audit.Record{
		ID:         fmt.Sprintf("audit-%d", time.Now().UTC().UnixNano()),
		Kind:       kind,
		EntityID:   entityID,
		EntityType: entityType,
		ActorID:    actorID,
		Message:    message,
		Data:       data,
		CreatedAt:  time.Now().UTC(),
	})
}
