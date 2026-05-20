package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/decision"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/domain"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/proposal"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store"
)

// Config wires the stores and policies used by a Runtime.
type Config struct {
	Domain           *domain.Definition
	Stores           *store.Bundle
	EventStore       event.Store
	DecisionStore    decision.Store
	AuditStore       audit.Store
	ApprovalStore    approval.Store
	StateMachine     state.StateMachine
	PermissionPolicy permission.Policy
	ActionValidator  action.Validator
	ActionCatalog    *action.Catalog
	Adapters         []adapter.Adapter
}

// Runtime coordinates event ingestion, decisions, action validation, execution, and audit.
type Runtime struct {
	Domain      *domain.Definition
	Events      event.Store
	Decisions   decision.Store
	Audit       audit.Store
	Approvals   approval.Store
	States      state.StateMachine
	Permissions permission.Policy
	Validator   action.Validator
	Actions     *action.Catalog
	Adapters    map[string]adapter.Adapter
}

// New creates a runtime with in-memory defaults for omitted stores.
func New(config Config) *Runtime {
	rt := &Runtime{
		Domain:      config.Domain,
		Events:      config.EventStore,
		Decisions:   config.DecisionStore,
		Audit:       config.AuditStore,
		Approvals:   config.ApprovalStore,
		States:      config.StateMachine,
		Permissions: config.PermissionPolicy,
		Validator:   config.ActionValidator,
		Actions:     config.ActionCatalog,
		Adapters:    map[string]adapter.Adapter{},
	}
	if config.Stores != nil {
		if rt.Events == nil {
			rt.Events = config.Stores.Events
		}
		if rt.Decisions == nil {
			rt.Decisions = config.Stores.Decisions
		}
		if rt.Audit == nil {
			rt.Audit = config.Stores.Audit
		}
		if rt.Approvals == nil {
			rt.Approvals = config.Stores.Approvals
		}
	}
	if config.Domain != nil {
		if rt.States == nil {
			rt.States = config.Domain.StateMachine
		}
		if rt.Actions == nil {
			rt.Actions = config.Domain.Actions
		}
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
	if rt.Approvals == nil {
		rt.Approvals = approval.NewInMemoryStore()
	}
	if rt.Validator == nil {
		rt.Validator = action.NewDefaultValidator()
	}
	if rt.Actions == nil {
		rt.Actions = action.NewCatalog()
	}
	for _, configuredAdapter := range config.Adapters {
		_ = rt.RegisterAdapter(configuredAdapter)
	}
	return rt
}

// NewForDomain validates a domain definition and creates a runtime wired to it.
func NewForDomain(definition domain.Definition, config Config) (*Runtime, error) {
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	config.Domain = &definition
	return New(config), nil
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

// RegisterAdapter registers an input adapter by name.
func (r *Runtime) RegisterAdapter(inputAdapter adapter.Adapter) error {
	if inputAdapter == nil {
		return errors.Join(adapter.ErrInvalidAdapter, errors.New("adapter is nil"))
	}
	if inputAdapter.Name() == "" {
		return errors.Join(adapter.ErrInvalidAdapter, errors.New("adapter name is required"))
	}
	if r.Adapters == nil {
		r.Adapters = map[string]adapter.Adapter{}
	}
	r.Adapters[inputAdapter.Name()] = inputAdapter
	return nil
}

// IngestRaw normalizes raw input with a registered adapter, then ingests the event.
func (r *Runtime) IngestRaw(input adapter.RawInput) (event.Event, error) {
	ctx := context.Background()
	if err := input.Validate(); err != nil {
		return event.Event{}, err
	}
	inputAdapter, ok := r.Adapters[input.Adapter]
	if !ok {
		return event.Event{}, fmt.Errorf("%w: %q", adapter.ErrAdapterNotFound, input.Adapter)
	}
	ev, err := inputAdapter.Normalize(ctx, input)
	if err != nil {
		return event.Event{}, err
	}
	if err := r.IngestEvent(ev); err != nil {
		return event.Event{}, err
	}
	return ev, nil
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

// RecordActionProposal records an action proposal as a decision.
func (r *Runtime) RecordActionProposal(p proposal.ActionProposal) error {
	d, err := p.Decision()
	if err != nil {
		return err
	}
	return r.ProposeDecision(d)
}

// ValidateActionProposal validates a proposal against an action contract.
func (r *Runtime) ValidateActionProposal(p proposal.ActionProposal, currentState string, proposalActor actor.Actor, contract action.ActionContract) error {
	actionCtx, err := p.ActionContext(currentState, proposalActor)
	if err != nil {
		return err
	}
	return r.ValidateAction(actionCtx, contract)
}

// AllowedActions returns the action contracts currently allowed for an entity.
func (r *Runtime) AllowedActions(entityID string) ([]action.ActionContract, error) {
	ctx := context.Background()
	if r.States == nil {
		return nil, fmt.Errorf("%w: state machine is not configured", store.ErrNotFound)
	}
	if r.Actions == nil {
		return nil, fmt.Errorf("%w: action catalog is not configured", store.ErrNotFound)
	}

	current, ok, err := r.States.Current(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: state for entity %q", store.ErrNotFound, entityID)
	}

	names, err := r.States.AllowedActions(ctx, current)
	if err != nil {
		return nil, err
	}

	contracts := make([]action.ActionContract, 0, len(names))
	for _, name := range names {
		contract, ok := r.Actions.Get(name)
		if !ok {
			return nil, fmt.Errorf("%w: action contract %q", store.ErrNotFound, name)
		}
		contracts = append(contracts, contract)
	}
	return contracts, nil
}

// Timeline returns the chronological audit trail for an entity.
func (r *Runtime) Timeline(entityID string) ([]audit.Record, error) {
	ctx := context.Background()
	return r.Audit.Query(ctx, audit.Filter{EntityID: entityID})
}

// RecordApproval stores and audits an approval record.
func (r *Runtime) RecordApproval(a approval.Approval) error {
	ctx := context.Background()
	if err := r.Approvals.Save(ctx, a); err != nil {
		return err
	}

	kind := audit.KindApprovalRecorded
	message := "approval recorded"
	switch a.Status {
	case approval.StatusGranted:
		kind = audit.KindApprovalGranted
		message = "approval granted"
	case approval.StatusDenied:
		kind = audit.KindApprovalDenied
		message = "approval denied"
	}

	actorID := a.ReviewedBy
	if actorID == "" {
		actorID = a.RequestedBy
	}

	return r.appendAudit(ctx, kind, a.EntityID, a.EntityType, actorID, message, map[string]any{
		"approval_id":  a.ID,
		"action":       a.ActionName,
		"requested_by": a.RequestedBy,
		"reviewed_by":  a.ReviewedBy,
		"status":       a.Status,
		"reason":       a.Reason,
	})
}

// ValidateAction validates an action and appends the corresponding audit record.
func (r *Runtime) ValidateAction(actionCtx action.ActionContext, contract action.ActionContract) error {
	ctx := context.Background()
	actionCtx = r.applyApproval(ctx, actionCtx, contract)
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
			"approval_id":       actionCtx.ApprovalID,
			"requires_approval": result.RequiresApproval,
		})
		if auditErr != nil {
			return auditErr
		}
		return err
	}
	return r.appendAudit(ctx, audit.KindActionValidated, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action validated", map[string]any{
		"action":            contract.Name,
		"approval_id":       actionCtx.ApprovalID,
		"requires_approval": result.RequiresApproval,
	})
}

// ExecuteAction validates, executes, and audits an action.
func (r *Runtime) ExecuteAction(actionCtx action.ActionContext, contract action.ActionContract) (action.ActionResult, error) {
	ctx := context.Background()
	actionCtx = r.applyApproval(ctx, actionCtx, contract)
	if err := r.ValidateAction(actionCtx, contract); err != nil {
		return action.ActionResult{}, err
	}

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
		result = action.FailedResult(contract.Name, actionCtx.EntityID, err)
		auditErr := r.appendAudit(ctx, audit.KindActionFailed, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action execution failed", map[string]any{
			"action":          contract.Name,
			"status":          result.Status,
			"error":           result.Error,
			"message":         result.Message,
			"effects_summary": result.EffectsSummary,
			"output":          result.Output,
			"executed_at":     result.ExecutedAt,
		})
		if auditErr != nil {
			return action.ActionResult{}, auditErr
		}
		return result, err
	}
	if result.ActionName == "" {
		result.ActionName = contract.Name
	}
	if result.EntityID == "" {
		result.EntityID = actionCtx.EntityID
	}
	if result.Status == "" {
		result.Executed = true
	}
	result = result.Normalize()
	return result, r.appendAudit(ctx, audit.KindActionExecuted, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action executed", map[string]any{
		"action":          contract.Name,
		"status":          result.Status,
		"executed":        result.Executed,
		"message":         result.Message,
		"error":           result.Error,
		"effects_summary": result.EffectsSummary,
		"output":          result.Output,
		"executed_at":     result.ExecutedAt,
	})
}

func (r *Runtime) applyApproval(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract) action.ActionContext {
	if actionCtx.Approved || actionCtx.ApprovalID == "" || r.Approvals == nil {
		return actionCtx
	}
	if contract.ApprovalRequirement != action.ApprovalRequired {
		return actionCtx
	}
	granted, err := r.Approvals.IsGranted(ctx, actionCtx.ApprovalID, actionCtx.EntityID, contract.Name)
	if err == nil && granted {
		actionCtx.Approved = true
	}
	return actionCtx
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
