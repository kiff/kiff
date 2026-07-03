package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/idempotency"
	"github.com/kiff/kiff/pkg/kiff/internal/trust"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/proposal"
	"github.com/kiff/kiff/pkg/kiff/state"
	"github.com/kiff/kiff/pkg/kiff/store"
)

// auditSeq is a process-wide counter used to guarantee unique audit IDs.
var auditSeq atomic.Uint64

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
	IdempotencyStore idempotency.Store
	ActionValidator  action.Validator
	ActionCatalog    *action.Catalog
	Adapters         []adapter.Adapter

	// Metrics, if non-nil, receives counter increments on the
	// successful operational path. The runtime defaults to
	// NoopMetrics when this is nil; existing wiring is unaffected.
	Metrics MetricsRecorder
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
	Idempotency idempotency.Store
	Validator   action.Validator
	Actions     *action.Catalog
	Adapters    map[string]adapter.Adapter

	// metrics receives counter increments on the successful path.
	// Set from Config.Metrics; defaults to NoopMetrics so existing
	// wiring continues to work unchanged.
	metrics MetricsRecorder

	// trace tracks the last known trace metadata and event id per entity.
	// It lets action and approval audit records inherit the trace context
	// from the most recent ingested event for the same entity.
	traceMu sync.RWMutex
	trace   map[string]traceContext
}

// traceContext is the per-entity trace metadata snapshot.
type traceContext struct {
	TraceID       string
	CorrelationID string
	LastEventID   string
}

// auditMeta carries optional correlation fields for an audit record.
type auditMeta struct {
	TraceID       string
	CorrelationID string
	CausationID   string
}

// New creates a runtime with in-memory defaults for omitted stores.
// If Config.Domain is provided, it must be valid.
func New(config Config) (*Runtime, error) {
	if config.Domain != nil {
		if err := config.Domain.Validate(); err != nil {
			return nil, err
		}
	}
	rt := &Runtime{
		Domain:      config.Domain,
		Events:      config.EventStore,
		Decisions:   config.DecisionStore,
		Audit:       config.AuditStore,
		Approvals:   config.ApprovalStore,
		States:      config.StateMachine,
		Permissions: config.PermissionPolicy,
		Idempotency: config.IdempotencyStore,
		Validator:   config.ActionValidator,
		Actions:     config.ActionCatalog,
		Adapters:    map[string]adapter.Adapter{},
		metrics:     config.Metrics,
		trace:       map[string]traceContext{},
	}
	if rt.metrics == nil {
		rt.metrics = NoopMetrics
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
	if rt.Idempotency == nil {
		rt.Idempotency = idempotency.NewInMemoryStore()
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
	return rt, nil
}

// NewForDomain validates a domain definition and creates a runtime wired to it.
func NewForDomain(definition domain.Definition, config Config) (*Runtime, error) {
	config.Domain = &definition
	return New(config)
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

// IngestEvent stores an event, applies state when a state machine is present, and audits both facts.
func (r *Runtime) IngestEvent(ctx context.Context, ev event.Event) error {
	if err := r.Events.Append(ctx, ev); err != nil {
		return err
	}

	// Snapshot the prior last-event-id before remembering this event, so the
	// causation chain links to the previous fact rather than to itself.
	priorEventID := r.lastEventID(ev.EntityID)
	r.rememberTrace(ev)

	meta := auditMeta{
		TraceID:       ev.Metadata.TraceID,
		CorrelationID: ev.Metadata.CorrelationID,
		CausationID:   ev.Metadata.CausationID,
	}
	if meta.CausationID == "" {
		meta.CausationID = priorEventID
	}

	if err := r.appendAuditWithMeta(ctx, audit.KindEventIngested, ev.EntityID, ev.EntityType, ev.ActorID, "event ingested", map[string]any{
		"event_id":   ev.ID,
		"event_type": ev.Type,
	}, meta); err != nil {
		return err
	}

	r.metrics.Inc(CounterEventsIngested, 1, EntityType(ev.EntityType))

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
	return r.appendAuditWithMeta(ctx, audit.KindStateChanged, ev.EntityID, ev.EntityType, ev.ActorID, "state changed", map[string]any{
		"from":       current.Value,
		"to":         next.Value,
		"event_id":   ev.ID,
		"event_type": ev.Type,
	}, meta)
}

// IngestRaw normalizes raw input with a registered adapter, then ingests the event.
func (r *Runtime) IngestRaw(ctx context.Context, input adapter.RawInput) (event.Event, error) {
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
	if err := r.IngestEvent(ctx, ev); err != nil {
		return event.Event{}, err
	}
	return ev, nil
}

// ProposeDecision stores and audits a decision.
func (r *Runtime) ProposeDecision(ctx context.Context, d decision.Decision) error {
	if err := r.Decisions.Append(ctx, d); err != nil {
		return err
	}
	if err := r.appendAudit(ctx, audit.KindDecisionProposed, d.EntityID, d.EntityType, d.ActorID, "decision proposed", map[string]any{
		"decision_id":     d.ID,
		"decision_kind":   d.Kind,
		"proposed_action": d.ProposedAction,
		"confidence":      d.Confidence,
	}); err != nil {
		return err
	}
	r.metrics.Inc(CounterDecisionsRecorded, 1, EntityType(d.EntityType))
	return nil
}

// RecordActionProposal records an action proposal as a decision.
func (r *Runtime) RecordActionProposal(ctx context.Context, p proposal.ActionProposal) error {
	d, err := p.Decision()
	if err != nil {
		return err
	}
	return r.ProposeDecision(ctx, d)
}

// ValidateActionProposal validates a proposal against an action contract.
func (r *Runtime) ValidateActionProposal(ctx context.Context, p proposal.ActionProposal, currentState string, proposalActor actor.Actor, contract action.ActionContract) error {
	actionCtx, err := p.ActionContext(currentState, proposalActor)
	if err != nil {
		return err
	}
	return r.ValidateAction(ctx, actionCtx, contract)
}

// AllowedActions returns the action contracts currently allowed for an entity.
func (r *Runtime) AllowedActions(ctx context.Context, entityID string) ([]action.ActionContract, error) {
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
func (r *Runtime) Timeline(ctx context.Context, entityID string) ([]audit.Record, error) {
	return r.Audit.Query(ctx, audit.Filter{EntityID: entityID})
}

// RebuildState reconstructs an entity state by replaying its stored events.
func (r *Runtime) RebuildState(ctx context.Context, entityID string) (state.ReplayResult, error) {
	if entityID == "" {
		return state.ReplayResult{}, fmt.Errorf("%w: entity id is required", state.ErrInvalidReplay)
	}
	if r.Events == nil {
		return state.ReplayResult{}, fmt.Errorf("%w: event store is not configured", store.ErrNotFound)
	}
	if r.States == nil {
		return state.ReplayResult{}, fmt.Errorf("%w: state machine is not configured", store.ErrNotFound)
	}

	events, err := r.Events.List(ctx, entityID)
	if err != nil {
		return state.ReplayResult{}, err
	}
	if len(events) == 0 {
		return state.ReplayResult{}, fmt.Errorf("%w: events for entity %q", store.ErrNotFound, entityID)
	}

	result, err := state.Rebuild(ctx, r.States, events)
	if err != nil {
		return state.ReplayResult{}, err
	}
	if r.Audit != nil {
		if err := r.appendAudit(ctx, audit.KindStateRebuilt, result.EntityID, result.EntityType, "", "state rebuilt", map[string]any{
			"events_replayed": len(events),
			"final_state":     result.State.Value,
			"final_version":   result.State.Version,
		}); err != nil {
			return state.ReplayResult{}, err
		}
	}
	return result, nil
}

// RequestApproval creates a pending approval for an action that requires approval.
func (r *Runtime) RequestApproval(ctx context.Context, approvalID string, actionCtx action.ActionContext, contract action.ActionContract, reason string) (approval.Approval, error) {
	required, _, err := contract.RequiresApproval(ctx, actionCtx)
	if err != nil {
		return approval.Approval{}, err
	}
	if !required {
		return approval.Approval{}, fmt.Errorf("%w: action %q does not require approval", action.ErrApprovalRequired, contract.Name)
	}
	if approvalID == "" {
		return approval.Approval{}, fmt.Errorf("%w: approval id is required", approval.ErrInvalidApproval)
	}
	actionName := contract.Name
	if actionName == "" {
		actionName = actionCtx.ActionName
	}
	request := approval.Approval{
		ID:          approvalID,
		EntityID:    actionCtx.EntityID,
		EntityType:  actionCtx.EntityType,
		ActionName:  actionName,
		RequestedBy: actionCtx.Actor.ID,
		Status:      approval.StatusPending,
		Reason:      reason,
		CreatedAt:   time.Now().UTC(),
	}
	if err := r.RecordApproval(ctx, request); err != nil {
		return approval.Approval{}, err
	}
	r.metrics.Inc(CounterApprovalsRequested, 1, EntityType(actionCtx.EntityType))
	return request, nil
}

// ReviewApproval grants or denies an existing pending approval by reviewer id.
//
// This is the simple path: it performs no reviewer-authority or
// segregation-of-duties check, so it stays usable for low-complexity demos.
// For high-risk workflows that must verify the reviewer holds authority and
// is not the requester, use ReviewApprovalAs.
func (r *Runtime) ReviewApproval(ctx context.Context, approvalID string, reviewedBy string, status approval.Status, reason string) (approval.Approval, error) {
	return r.reviewApproval(ctx, approvalID, actor.Actor{ID: reviewedBy}, ReviewRequirement{}, status, reason)
}

// RecordApproval stores and audits an approval record.
func (r *Runtime) RecordApproval(ctx context.Context, a approval.Approval) error {
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

// EvaluateAction validates an action against the current state, required
// parameters, permissions, and approval, and returns a normalized decision
// envelope. It is read-only: it never runs the executor and never writes an
// audit record. Use it to answer "what would happen if I ran this?" — for
// example, to hand an agent or an app API a typed outcome before deciding
// whether to execute.
func (r *Runtime) EvaluateAction(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract) outcome.Decision {
	name := contract.Name
	if name == "" {
		name = actionCtx.ActionName
	}
	resolved, err := r.applyApproval(ctx, actionCtx, contract)
	if err != nil {
		return outcome.FromError(err, name, actionCtx.EntityID, actionCtx.CurrentState)
	}
	if _, err := r.Validator.Validate(ctx, resolved, contract, r.Permissions); err != nil {
		return outcome.FromError(err, name, resolved.EntityID, resolved.CurrentState)
	}
	return outcome.Succeeded(name, resolved.EntityID, resolved.CurrentState)
}

// ValidateAction validates an action and appends the corresponding audit record.
func (r *Runtime) ValidateAction(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract) error {
	var err error
	actionCtx, err = r.applyApproval(ctx, actionCtx, contract)
	if err != nil {
		return err
	}
	result, validationErr := r.Validator.Validate(ctx, actionCtx, contract, r.Permissions)
	if validationErr != nil {
		kind := audit.KindActionFailed
		message := "action validation failed"
		if errors.Is(validationErr, action.ErrApprovalRequired) {
			kind = audit.KindApprovalRequired
			message = "approval required"
		}
		auditErr := r.appendAudit(ctx, kind, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, message, map[string]any{
			"action":            contract.Name,
			"error":             validationErr.Error(),
			"approval_id":       actionCtx.ApprovalID,
			"requires_approval": result.RequiresApproval,
		})
		if auditErr != nil {
			return auditErr
		}
		return validationErr
	}
	if err := r.appendAudit(ctx, audit.KindActionValidated, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action validated", map[string]any{
		"action":            contract.Name,
		"approval_id":       actionCtx.ApprovalID,
		"requires_approval": result.RequiresApproval,
	}); err != nil {
		return err
	}
	r.metrics.Inc(CounterActionsValidated, 1, EntityType(actionCtx.EntityType))
	return nil
}

// idempotencyKeyFor builds the dedup key for an action context. It reports
// false when idempotency does not apply (no store, or no key on the context),
// so execution behaves exactly as before.
func (r *Runtime) idempotencyKeyFor(actionCtx action.ActionContext, contract action.ActionContract) (idempotency.Key, bool) {
	if r.Idempotency == nil || actionCtx.IdempotencyKey == "" {
		return idempotency.Key{}, false
	}
	name := contract.Name
	if name == "" {
		name = actionCtx.ActionName
	}
	return idempotency.Key{Value: actionCtx.IdempotencyKey, EntityID: actionCtx.EntityID, ActionName: name}, true
}

// auditDeduplicated records an idempotent replay distinctly from a first
// execution, so the trail shows the retry returned a prior result.
func (r *Runtime) auditDeduplicated(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract, prior action.ActionResult) error {
	return r.appendAudit(ctx, audit.KindActionDeduplicated, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action deduplicated (idempotent replay)", map[string]any{
		"action":          contract.Name,
		"idempotency_key": actionCtx.IdempotencyKey,
		"prior_status":    prior.Status,
		"duplicate":       true,
	})
}

// ExecuteAction validates, executes, and audits an action.
func (r *Runtime) ExecuteAction(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract) (action.ActionResult, error) {
	idemKey, useIdem := r.idempotencyKeyFor(actionCtx, contract)
	if useIdem {
		// A retry of an already-succeeded request returns the stored result
		// before validation, so it is not refused by a state that has since
		// advanced (e.g. REFUNDED), and without re-emitting follow-up events.
		if prior, ok, err := r.Idempotency.Lookup(ctx, idemKey); err != nil {
			return action.ActionResult{}, err
		} else if ok {
			if err := r.auditDeduplicated(ctx, actionCtx, contract, prior); err != nil {
				return action.ActionResult{}, err
			}
			r.metrics.Inc(CounterActionsDeduplicated, 1, EntityType(actionCtx.EntityType))
			return prior, nil
		}
	}

	var err error
	actionCtx, err = r.applyApproval(ctx, actionCtx, contract)
	if err != nil {
		return action.ActionResult{}, err
	}
	if err := r.ValidateAction(ctx, actionCtx, contract); err != nil {
		return action.ActionResult{}, err
	}

	if contract.Executor == nil {
		failResult := action.FailedResult(contract.Name, actionCtx.EntityID, action.ErrExecutorMissing)
		auditErr := r.appendAudit(ctx, audit.KindActionFailed, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action execution failed", map[string]any{
			"action":  contract.Name,
			"status":  failResult.Status,
			"error":   failResult.Error,
			"message": failResult.Message,
		})
		if auditErr != nil {
			return action.ActionResult{}, auditErr
		}
		return failResult, action.ErrExecutorMissing
	}

	if useIdem {
		// Atomic reserve-or-return: only one caller may execute a given key.
		begin, beginErr := r.Idempotency.Begin(ctx, idemKey)
		if beginErr != nil {
			return action.ActionResult{}, beginErr
		}
		switch begin.Status {
		case idempotency.Completed:
			// Completed by a concurrent caller between Lookup and Begin.
			if err := r.auditDeduplicated(ctx, actionCtx, contract, begin.Result); err != nil {
				return action.ActionResult{}, err
			}
			r.metrics.Inc(CounterActionsDeduplicated, 1, EntityType(actionCtx.EntityType))
			return begin.Result, nil
		case idempotency.InProgress:
			return action.ActionResult{}, fmt.Errorf("%w: action %q for entity %q", idempotency.ErrInProgress, contract.Name, actionCtx.EntityID)
		}
	}

	result, err := contract.Executor(ctx, actionCtx)
	if err != nil {
		// The executor failed: drop the reservation so the request can be
		// retried rather than being frozen as a cached failure.
		if useIdem {
			_ = r.Idempotency.Release(ctx, idemKey)
		}
		result = action.FailedResult(contract.Name, actionCtx.EntityID, err)
		auditErr := r.appendAudit(ctx, audit.KindActionFailed, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action execution failed", map[string]any{
			"action":           contract.Name,
			"status":           result.Status,
			"error":            result.Error,
			"message":          result.Message,
			"effects_summary":  result.EffectsSummary,
			"output":           result.Output,
			"follow_up_events": len(result.FollowUpEvents),
			"executed_at":      result.ExecutedAt,
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
	if err := r.appendAudit(ctx, audit.KindActionExecuted, actionCtx.EntityID, actionCtx.EntityType, actionCtx.Actor.ID, "action executed", map[string]any{
		"action":           contract.Name,
		"status":           result.Status,
		"executed":         result.Executed,
		"message":          result.Message,
		"error":            result.Error,
		"effects_summary":  result.EffectsSummary,
		"output":           result.Output,
		"follow_up_events": len(result.FollowUpEvents),
		"executed_at":      result.ExecutedAt,
	}); err != nil {
		return action.ActionResult{}, err
	}
	if result.Status == action.ExecutionSucceeded {
		r.metrics.Inc(CounterActionsExecuted, 1, EntityType(actionCtx.EntityType))
		parentTrace := r.entityTrace(actionCtx.EntityID)
		parentEventID := r.lastEventID(actionCtx.EntityID)
		for _, followUpEvent := range result.FollowUpEvents {
			// Inherit trace metadata if executor did not set it explicitly.
			if followUpEvent.Metadata.TraceID == "" {
				followUpEvent.Metadata.TraceID = parentTrace.TraceID
			}
			if followUpEvent.Metadata.CorrelationID == "" {
				followUpEvent.Metadata.CorrelationID = parentTrace.CorrelationID
			}
			if followUpEvent.Metadata.CausationID == "" {
				followUpEvent.Metadata.CausationID = parentEventID
			}
			if err := r.IngestEvent(ctx, followUpEvent); err != nil {
				if useIdem {
					_ = r.Idempotency.Release(ctx, idemKey)
				}
				return result, err
			}
		}
	}
	if useIdem {
		// Cache only a fully-applied terminal success. A non-success result
		// (e.g. skipped) releases the reservation so it can be retried.
		if result.Status == action.ExecutionSucceeded {
			if err := r.Idempotency.Complete(ctx, idemKey, result); err != nil {
				return action.ActionResult{}, err
			}
		} else {
			_ = r.Idempotency.Release(ctx, idemKey)
		}
	}
	return result, nil
}

func (r *Runtime) applyApproval(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract) (action.ActionContext, error) {
	if actionCtx.IsApproved() || actionCtx.ApprovalID == "" || r.Approvals == nil {
		return actionCtx, nil
	}
	required, _, err := contract.RequiresApproval(ctx, actionCtx)
	if err != nil {
		return actionCtx, err
	}
	if !required {
		return actionCtx, nil
	}
	granted, err := r.Approvals.IsGranted(ctx, actionCtx.ApprovalID, actionCtx.EntityID, contract.Name)
	if err != nil {
		return actionCtx, fmt.Errorf("approval store error: %w", err)
	}
	if granted {
		actionCtx.GrantApproval(trust.NewGrant())
	}
	return actionCtx, nil
}

func (r *Runtime) appendAudit(ctx context.Context, kind audit.Kind, entityID, entityType, actorID, message string, data map[string]any) error {
	return r.appendAuditWithMeta(ctx, kind, entityID, entityType, actorID, message, data, r.entityTrace(entityID))
}

func (r *Runtime) appendAuditWithMeta(ctx context.Context, kind audit.Kind, entityID, entityType, actorID, message string, data map[string]any, meta auditMeta) error {
	return r.Audit.Append(ctx, audit.Record{
		ID:            generateAuditID(),
		Kind:          kind,
		EntityID:      entityID,
		EntityType:    entityType,
		ActorID:       actorID,
		Message:       message,
		Data:          data,
		TraceID:       meta.TraceID,
		CorrelationID: meta.CorrelationID,
		CausationID:   meta.CausationID,
		CreatedAt:     time.Now().UTC(),
	})
}

// entityTrace returns the latest trace metadata snapshot for an entity.
func (r *Runtime) entityTrace(entityID string) auditMeta {
	if r.trace == nil || entityID == "" {
		return auditMeta{}
	}
	r.traceMu.RLock()
	defer r.traceMu.RUnlock()
	tc, ok := r.trace[entityID]
	if !ok {
		return auditMeta{}
	}
	return auditMeta{TraceID: tc.TraceID, CorrelationID: tc.CorrelationID}
}

// rememberTrace updates the latest trace metadata for an entity from an event.
func (r *Runtime) rememberTrace(ev event.Event) {
	if r.trace == nil || ev.EntityID == "" {
		return
	}
	if ev.Metadata.TraceID == "" && ev.Metadata.CorrelationID == "" {
		// Even with no metadata, remember the last event id for causation.
		r.traceMu.Lock()
		existing := r.trace[ev.EntityID]
		existing.LastEventID = ev.ID
		r.trace[ev.EntityID] = existing
		r.traceMu.Unlock()
		return
	}
	r.traceMu.Lock()
	r.trace[ev.EntityID] = traceContext{
		TraceID:       ev.Metadata.TraceID,
		CorrelationID: ev.Metadata.CorrelationID,
		LastEventID:   ev.ID,
	}
	r.traceMu.Unlock()
}

// lastEventID returns the most recent event id observed for an entity.
func (r *Runtime) lastEventID(entityID string) string {
	if r.trace == nil || entityID == "" {
		return ""
	}
	r.traceMu.RLock()
	defer r.traceMu.RUnlock()
	return r.trace[entityID].LastEventID
}

// generateAuditID produces a unique audit record ID using an atomic counter and random bytes.
func generateAuditID() string {
	seq := auditSeq.Add(1)
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("audit-%d-%s", seq, hex.EncodeToString(buf[:]))
}
