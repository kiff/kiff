package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/adapter"
	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/audit"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/domain"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
	"github.com/kiffhq/kiff/pkg/kiff/proposal"
	"github.com/kiffhq/kiff/pkg/kiff/state"
	"github.com/kiffhq/kiff/pkg/kiff/store"
)

func mustNew(t *testing.T, config Config) *Runtime {
	t.Helper()
	rt, err := New(config)
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	return rt
}

func noopExecutor(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
	return action.ActionResult{
		ActionName: ctx.ActionName,
		EntityID:   ctx.EntityID,
		Executed:   true,
		Message:    "noop",
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func TestRuntimeAppendsAuditRecordsDuringEventIngestionAndActionExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{
		StateMachine:     state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
		PermissionPolicy: policy,
	})

	if err := rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "MISSION_SUBMITTED", EntityID: "attempt-1",
		EntityType: "MissionAttempt", Source: "test", ActorID: "agent", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}

	// Grant approval through the proper flow
	if err := rt.RecordApproval(context.Background(), approval.Approval{
		ID: "appr-1", EntityID: "attempt-1", EntityType: "MissionAttempt",
		ActionName: "EXECUTE_MOVE", RequestedBy: "agent", ReviewedBy: "human",
		Status: approval.StatusGranted, CreatedAt: time.Now().UTC(), ReviewedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record approval: %v", err)
	}

	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "SUBMITTED", Actor: actor.Actor{ID: "agent"}, ApprovalID: "appr-1",
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired, Executor: noopExecutor,
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}

	records, _ := rt.Audit.List(context.Background(), "attempt-1")
	if len(records) < 4 {
		t.Fatalf("expected at least 4 audit records, got %d", len(records))
	}
	var sawEvent, sawState, sawExecuted bool
	for _, r := range records {
		switch r.Kind {
		case audit.KindEventIngested:
			sawEvent = true
		case audit.KindStateChanged:
			sawState = true
		case audit.KindActionExecuted:
			sawExecuted = true
		}
	}
	if !sawEvent || !sawState || !sawExecuted {
		t.Fatal("expected event, state, and execution audit records")
	}
}

func TestRuntimeUsesInjectedStoreBundle(t *testing.T) {
	bundle := store.NewInMemoryBundle()
	rt := mustNew(t, Config{
		Stores:       &bundle,
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
	})
	if err := rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "MISSION_SUBMITTED", EntityID: "attempt-1",
		EntityType: "MissionAttempt", Source: "test", ActorID: "human", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	events, _ := bundle.Events.List(context.Background(), "attempt-1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event in injected bundle, got %d", len(events))
	}
	auditRecords, _ := bundle.Audit.List(context.Background(), "attempt-1")
	if len(auditRecords) != 2 {
		t.Fatalf("expected 2 audit records in injected bundle, got %d", len(auditRecords))
	}
}

func TestRuntimeIngestRawUsesRegisteredAdapter(t *testing.T) {
	inputAdapter, _ := adapter.NewPassthroughAdapter("mission")
	rt := mustNew(t, Config{
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
		Adapters:     []adapter.Adapter{inputAdapter},
	})
	ev, err := rt.IngestRaw(context.Background(), adapter.RawInput{
		ID: "raw-1", Adapter: "mission", Type: "MISSION_SUBMITTED", Source: "mission-cli",
		EntityID: "attempt-1", EntityType: "MissionAttempt", ActorID: "human", ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ingest raw: %v", err)
	}
	if ev.Type != "MISSION_SUBMITTED" {
		t.Fatalf("expected normalized event type, got %q", ev.Type)
	}
}

func TestRuntimeRecordsActionProposalAsDecision(t *testing.T) {
	rt := mustNew(t, Config{})
	p := proposal.ActionProposal{
		ID: "proposal-1", EntityID: "attempt-1", EntityType: "MissionAttempt",
		ActionName: "PROPOSE_MOVE", ActorID: "mission-agent",
		ReasoningSummary: "active attempt", Confidence: 0.82, CreatedAt: time.Now().UTC(),
	}
	if err := rt.RecordActionProposal(context.Background(), p); err != nil {
		t.Fatalf("record action proposal: %v", err)
	}
	decisions, _ := rt.Decisions.List(context.Background(), "attempt-1")
	if len(decisions) != 1 || decisions[0].Kind != decision.KindActionProposal {
		t.Fatalf("expected 1 action proposal decision, got %v", decisions)
	}
}

func TestRuntimeValidatesActionProposalWithoutExecuting(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("mission-agent", "mission.propose_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	p := proposal.ActionProposal{
		ID: "proposal-1", EntityID: "attempt-1", EntityType: "MissionAttempt",
		ActionName: "PROPOSE_MOVE", ActorID: "mission-agent",
		Parameters: map[string]any{"move": "draft"}, CreatedAt: time.Now().UTC(),
	}
	executed := false
	contract := action.ActionContract{
		Name: "PROPOSE_MOVE", AllowedStates: []string{"ACTIVE"},
		RequiredParameters: []string{"move"}, RequiredPermissions: []permission.Permission{"mission.propose_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			executed = true
			return action.ActionResult{Executed: true}, nil
		},
	}
	if err := rt.ValidateActionProposal(context.Background(), p, "ACTIVE", actor.Actor{ID: "mission-agent"}, contract); err != nil {
		t.Fatalf("validate action proposal: %v", err)
	}
	if executed {
		t.Fatal("expected proposal validation not to execute the action")
	}
}

func TestRuntimeIngestRawReturnsAdapterNotFound(t *testing.T) {
	rt := mustNew(t, Config{})
	_, err := rt.IngestRaw(context.Background(), adapter.RawInput{
		ID: "raw-1", Adapter: "missing", Type: "X", Source: "cli", ReceivedAt: time.Now().UTC(),
	})
	if !errors.Is(err, adapter.ErrAdapterNotFound) {
		t.Fatalf("expected adapter.ErrAdapterNotFound, got %v", err)
	}
}

func TestRuntimeIndividualStoresOverrideBundleStores(t *testing.T) {
	bundle := store.NewInMemoryBundle()
	overrideEvents := event.NewInMemoryStore()
	rt := mustNew(t, Config{
		Stores: &bundle, EventStore: overrideEvents,
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
	})
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "MISSION_SUBMITTED", EntityID: "attempt-1",
		EntityType: "MissionAttempt", Source: "test", ActorID: "human", OccurredAt: time.Now().UTC(),
	})
	bundleEvents, _ := bundle.Events.List(context.Background(), "attempt-1")
	if len(bundleEvents) != 0 {
		t.Fatalf("expected bundle event store to be overridden, got %d events", len(bundleEvents))
	}
	overrideStoredEvents, _ := overrideEvents.List(context.Background(), "attempt-1")
	if len(overrideStoredEvents) != 1 {
		t.Fatalf("expected 1 event in override store, got %d", len(overrideStoredEvents))
	}
}

func TestRuntimeUsesGrantedApprovalRecordForActionValidation(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	_ = rt.RecordApproval(context.Background(), approval.Approval{
		ID: "approval-1", EntityID: "attempt-1", EntityType: "MissionAttempt",
		ActionName: "EXECUTE_MOVE", RequestedBy: "agent", ReviewedBy: "human",
		Status: approval.StatusGranted, CreatedAt: time.Now().UTC(), ReviewedAt: time.Now().UTC(),
	})
	executorSawApproved := false
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"}, ApprovalID: "approval-1",
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			executorSawApproved = ctx.IsApproved()
			return action.ActionResult{Executed: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("execute approved action: %v", err)
	}
	if !executorSawApproved {
		t.Fatal("expected executor to receive approved action context")
	}
}

func TestRuntimeRequestApprovalCreatesPendingApproval(t *testing.T) {
	rt := mustNew(t, Config{})
	actionCtx := action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		Actor: actor.Actor{ID: "agent"},
	}
	contract := action.ActionContract{Name: "EXECUTE_MOVE", ApprovalRequirement: action.ApprovalRequired}
	request, err := rt.RequestApproval(context.Background(), "approval-1", actionCtx, contract, "high-risk")
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if request.Status != approval.StatusPending {
		t.Fatalf("expected pending, got %q", request.Status)
	}
	stored, ok, _ := rt.Approvals.Get(context.Background(), "approval-1")
	if !ok || stored.Status != approval.StatusPending {
		t.Fatal("expected stored pending approval")
	}
}

func TestRuntimeRequestApprovalRejectsActionsWithoutApprovalRequirement(t *testing.T) {
	rt := mustNew(t, Config{})
	_, err := rt.RequestApproval(context.Background(), "approval-1", action.ActionContext{
		ActionName: "PROPOSE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{Name: "PROPOSE_MOVE", ApprovalRequirement: action.ApprovalNever}, "not needed")
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}

func TestRuntimeReviewApprovalGrantsPendingApproval(t *testing.T) {
	rt := mustNew(t, Config{})
	actionCtx := action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		Actor: actor.Actor{ID: "agent"},
	}
	contract := action.ActionContract{Name: "EXECUTE_MOVE", ApprovalRequirement: action.ApprovalRequired}
	_, _ = rt.RequestApproval(context.Background(), "approval-1", actionCtx, contract, "needs human authority")
	reviewed, err := rt.ReviewApproval(context.Background(), "approval-1", "human", approval.StatusGranted, "approved")
	if err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if reviewed.Status != approval.StatusGranted || reviewed.ReviewedBy != "human" {
		t.Fatalf("unexpected review result: %+v", reviewed)
	}
	records, _ := rt.Timeline(context.Background(), "attempt-1")
	var sawGranted bool
	for _, r := range records {
		if r.Kind == audit.KindApprovalGranted {
			sawGranted = true
		}
	}
	if !sawGranted {
		t.Fatal("expected approval granted audit record")
	}
}

func TestRuntimeRequestedAndGrantedApprovalAllowsExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	actionCtx := action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"}, ApprovalID: "approval-1",
	}
	contract := action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired, Executor: noopExecutor,
	}
	_, _ = rt.RequestApproval(context.Background(), actionCtx.ApprovalID, actionCtx, contract, "needs human")
	_ = rt.RecordApproval(context.Background(), approval.Approval{
		ID: actionCtx.ApprovalID, EntityID: actionCtx.EntityID, EntityType: actionCtx.EntityType,
		ActionName: contract.Name, RequestedBy: actionCtx.Actor.ID, ReviewedBy: "human",
		Status: approval.StatusGranted, CreatedAt: time.Now().UTC(), ReviewedAt: time.Now().UTC(),
	})
	if _, err := rt.ExecuteAction(context.Background(), actionCtx, contract); err != nil {
		t.Fatalf("execute with granted approval: %v", err)
	}
}

func TestRuntimeAllowedActionsUsesDomainStateAndCatalog(t *testing.T) {
	machine := state.NewTransitionMachine()
	machine.Set(state.State{EntityID: "attempt-1", EntityType: "MissionAttempt", Value: "ACTIVE"})
	machine.SetAllowedActions("ACTIVE", []string{"PROPOSE_MOVE"})
	catalog := action.NewCatalog()
	_ = catalog.Register(action.ActionContract{Name: "PROPOSE_MOVE", AllowedStates: []string{"ACTIVE"}})
	rt, err := NewForDomain(domain.Definition{
		Name: "mission", EntityTypes: []string{"MissionAttempt"}, EventTypes: []string{"MOVE_PROPOSED"},
		StateMachine: machine, Actions: catalog,
	}, Config{})
	if err != nil {
		t.Fatalf("new runtime for domain: %v", err)
	}
	contracts, _ := rt.AllowedActions(context.Background(), "attempt-1")
	if len(contracts) != 1 || contracts[0].Name != "PROPOSE_MOVE" {
		t.Fatalf("expected [PROPOSE_MOVE], got %v", contracts)
	}
}

func TestRuntimeAllowedActionsReturnsNotFoundForUnknownEntity(t *testing.T) {
	rt := mustNew(t, Config{StateMachine: state.NewTransitionMachine()})
	_, err := rt.AllowedActions(context.Background(), "missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected store.ErrNotFound, got %v", err)
	}
}

func TestRuntimeRebuildStateFromStoredEvents(t *testing.T) {
	eventStore := event.NewInMemoryStore()
	now := time.Now().UTC()
	_ = eventStore.Append(context.Background(), event.Event{
		ID: "evt-1", Type: "MISSION_SUBMITTED", EntityID: "attempt-1",
		EntityType: "MissionAttempt", Source: "test", ActorID: "human", OccurredAt: now,
	})
	_ = eventStore.Append(context.Background(), event.Event{
		ID: "evt-2", Type: "ATTEMPT_CREATED", EntityID: "attempt-1",
		EntityType: "MissionAttempt", Source: "test", ActorID: "agent", OccurredAt: now.Add(time.Second),
	})
	rt := mustNew(t, Config{
		EventStore: eventStore,
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"},
			state.Transition{EventType: "ATTEMPT_CREATED", From: "SUBMITTED", To: "ACTIVE"},
		),
	})
	result, err := rt.RebuildState(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("rebuild state: %v", err)
	}
	if result.State.Value != "ACTIVE" || len(result.Steps) != 2 {
		t.Fatalf("unexpected rebuild result: %+v", result)
	}
	records, _ := rt.Timeline(context.Background(), "attempt-1")
	var sawRebuilt bool
	for _, r := range records {
		if r.Kind == audit.KindStateRebuilt {
			sawRebuilt = true
		}
	}
	if !sawRebuilt {
		t.Fatal("expected state rebuild audit record")
	}
}

func TestRuntimeRebuildStateReturnsNotFoundWithoutEvents(t *testing.T) {
	rt := mustNew(t, Config{StateMachine: state.NewTransitionMachine()})
	_, err := rt.RebuildState(context.Background(), "missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected store.ErrNotFound, got %v", err)
	}
}

func TestRuntimeAuditsSuccessfulExecutionResultDetails(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	result, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "READY", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"READY"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				Message: "move executed", EffectsSummary: "created move artifact",
				Output: map[string]any{"artifact_id": "artifact-1"},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}
	if result.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %q", result.Status)
	}
	records, _ := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "attempt-1", Kind: audit.KindActionExecuted})
	if len(records) != 1 || records[0].Data["effects_summary"] != "created move artifact" {
		t.Fatalf("unexpected audit: %+v", records)
	}
}

func TestRuntimeAuditsFailedExecutionResultDetails(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	result, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "READY", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"READY"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{}, fmt.Errorf("executor failed")
		},
	})
	if err == nil {
		t.Fatal("expected execution error")
	}
	if result.Status != action.ExecutionFailed || result.Error != "executor failed" {
		t.Fatalf("unexpected result: %+v", result)
	}
	records, _ := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "attempt-1", Kind: audit.KindActionFailed})
	if len(records) != 1 || records[0].Data["error"] != "executor failed" {
		t.Fatalf("unexpected audit: %+v", records)
	}
}

func TestRuntimeIngestsFollowUpEventsAfterSuccessfulExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "MOVE_EXECUTED", From: "WAITING_APPROVAL", To: "COMPLETED"},
		),
		PermissionPolicy: policy,
	})
	rt.States.(*state.TransitionMachine).Set(state.State{
		EntityID: "attempt-1", EntityType: "MissionAttempt", Value: "WAITING_APPROVAL",
	})
	result, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				Message: "move executed", EffectsSummary: "emitted event",
				FollowUpEvents: []event.Event{{
					ID: "evt-f1", Type: "MOVE_EXECUTED", EntityID: "attempt-1",
					EntityType: "MissionAttempt", Source: "executor", ActorID: "agent", OccurredAt: time.Now().UTC(),
				}},
			}, nil
		},
	})
	if err != nil || result.Status != action.ExecutionSucceeded {
		t.Fatalf("execute: err=%v status=%v", err, result.Status)
	}
	current, ok, _ := rt.States.Current(context.Background(), "attempt-1")
	if !ok || current.Value != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %v", current)
	}
}

func TestRuntimeDoesNotIngestFollowUpEventsAfterFailedExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{
		StateMachine:     state.NewTransitionMachine(state.Transition{EventType: "MOVE_EXECUTED", From: "WAITING_APPROVAL", To: "COMPLETED"}),
		PermissionPolicy: policy,
	})
	rt.States.(*state.TransitionMachine).Set(state.State{EntityID: "attempt-1", EntityType: "MissionAttempt", Value: "WAITING_APPROVAL"})
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{FollowUpEvents: []event.Event{{
				ID: "evt-f1", Type: "MOVE_EXECUTED", EntityID: "attempt-1",
				EntityType: "MissionAttempt", Source: "executor", ActorID: "agent", OccurredAt: time.Now().UTC(),
			}}}, fmt.Errorf("executor failed")
		},
	})
	if err == nil {
		t.Fatal("expected executor failure")
	}
	events, _ := rt.Events.List(context.Background(), "attempt-1")
	if len(events) != 0 {
		t.Fatalf("expected no follow-up events after failure, got %d", len(events))
	}
}

func TestRuntimeTimelineReconstructsOperationalPath(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{
		StateMachine:     state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
		PermissionPolicy: policy,
	})
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "MISSION_SUBMITTED", EntityID: "attempt-1",
		EntityType: "MissionAttempt", Source: "test", ActorID: "human", OccurredAt: time.Now().UTC(),
	})
	_ = rt.ProposeDecision(context.Background(), decisionForTest("dec-1", "attempt-1"))
	// Validate action requiring approval (will fail with ErrApprovalRequired)
	_ = rt.ValidateAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "SUBMITTED", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
	})
	_ = rt.RecordApproval(context.Background(), approval.Approval{
		ID: "approval-1", EntityID: "attempt-1", EntityType: "MissionAttempt",
		ActionName: "EXECUTE_MOVE", RequestedBy: "agent", ReviewedBy: "human",
		Status: approval.StatusGranted, CreatedAt: time.Now().UTC(), ReviewedAt: time.Now().UTC(),
	})
	_, _ = rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "SUBMITTED", Actor: actor.Actor{ID: "agent"}, ApprovalID: "approval-1",
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired, Executor: noopExecutor,
	})
	timeline, _ := rt.Timeline(context.Background(), "attempt-1")
	expectedKinds := []audit.Kind{
		audit.KindEventIngested, audit.KindStateChanged, audit.KindDecisionProposed,
		audit.KindApprovalRequired, audit.KindApprovalGranted,
		audit.KindActionValidated, audit.KindActionExecuted,
	}
	if len(timeline) != len(expectedKinds) {
		t.Fatalf("expected %d timeline records, got %d: %v", len(expectedKinds), len(timeline), timeline)
	}
	for i, kind := range expectedKinds {
		if timeline[i].Kind != kind {
			t.Fatalf("expected %q at %d, got %q", kind, i, timeline[i].Kind)
		}
	}
}

// === v0.1 Trust Boundary Hardening Tests ===

func TestCallerCannotBypassApprovalBySettingFields(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	// No approval exists — caller cannot self-approve
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"},
		ApprovalID: "fake-approval",
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired, Executor: noopExecutor,
	})
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}

func TestExecutionBlockedWhenApprovalDenied(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	actionCtx := action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"}, ApprovalID: "approval-1",
	}
	contract := action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired, Executor: noopExecutor,
	}
	_, _ = rt.RequestApproval(context.Background(), "approval-1", actionCtx, contract, "needs human")
	_, _ = rt.ReviewApproval(context.Background(), "approval-1", "human", approval.StatusDenied, "too risky")
	_, err := rt.ExecuteAction(context.Background(), actionCtx, contract)
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}
}

func TestMissingExecutorDoesNotProduceSuccessfulExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.create_attempt")
	rt := mustNew(t, Config{PermissionPolicy: policy})
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "CREATE_ATTEMPT", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "SUBMITTED", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "CREATE_ATTEMPT", AllowedStates: []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.create_attempt"},
		// No Executor
	})
	if !errors.Is(err, action.ErrExecutorMissing) {
		t.Fatalf("expected ErrExecutorMissing, got %v", err)
	}
	records, _ := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "attempt-1", Kind: audit.KindActionFailed})
	if len(records) != 1 {
		t.Fatalf("expected 1 action_failed audit record, got %d", len(records))
	}
}

// failingApprovalStore always returns an error from IsGranted.
type failingApprovalStore struct {
	approval.Store
}

func (f *failingApprovalStore) IsGranted(context.Context, string, string, string) (bool, error) {
	return false, fmt.Errorf("approval store unavailable")
}
func (f *failingApprovalStore) Save(ctx context.Context, a approval.Approval) error {
	return nil
}
func (f *failingApprovalStore) Get(ctx context.Context, id string) (approval.Approval, bool, error) {
	return approval.Approval{}, false, nil
}
func (f *failingApprovalStore) List(ctx context.Context, entityID string) ([]approval.Approval, error) {
	return nil, nil
}

func TestApprovalStoreErrorsArePropagated(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := mustNew(t, Config{
		PermissionPolicy: policy,
		ApprovalStore:    &failingApprovalStore{},
	})
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "EXECUTE_MOVE", EntityID: "attempt-1", EntityType: "MissionAttempt",
		CurrentState: "WAITING_APPROVAL", Actor: actor.Actor{ID: "agent"}, ApprovalID: "approval-1",
	}, action.ActionContract{
		Name: "EXECUTE_MOVE", AllowedStates: []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired, Executor: noopExecutor,
	})
	if err == nil {
		t.Fatal("expected error from failing approval store")
	}
	if errors.Is(err, action.ErrApprovalRequired) {
		t.Fatal("should not downgrade store error to ErrApprovalRequired")
	}
}

func TestAuditIDsAreUniqueAcrossManyRecords(t *testing.T) {
	rt := mustNew(t, Config{
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "X", From: "", To: "A"}),
	})
	for i := 0; i < 100; i++ {
		_ = rt.IngestEvent(context.Background(), event.Event{
			ID: fmt.Sprintf("evt-%d", i), Type: "X", EntityID: "e1",
			EntityType: "T", Source: "test", ActorID: "a", OccurredAt: time.Now().UTC(),
		})
	}
	records, _ := rt.Audit.List(context.Background(), "e1")
	ids := map[string]bool{}
	for _, r := range records {
		if ids[r.ID] {
			t.Fatalf("duplicate audit ID: %s", r.ID)
		}
		ids[r.ID] = true
	}
}

func TestRuntimeNewValidatesConfigDomain(t *testing.T) {
	_, err := New(Config{
		Domain: &domain.Definition{Name: ""}, // invalid: no name
	})
	if err == nil {
		t.Fatal("expected validation error for invalid domain")
	}
}

func decisionForTest(id, entityID string) decision.Decision {
	return decision.Decision{
		ID: id, EntityID: entityID, EntityType: "MissionAttempt",
		Kind: decision.KindActionProposal, ProposedAction: "EXECUTE_MOVE",
		ActorID: "agent", CreatedAt: time.Now().UTC(),
	}
}

// === Brick 17: Trace Correlation Tests ===

func TestRuntimeIngestPropagatesTraceMetadataToAudit(t *testing.T) {
	rt := mustNew(t, Config{
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "X", From: "", To: "A"}),
	})
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "X", EntityID: "e1", EntityType: "T",
		Source: "test", ActorID: "a", OccurredAt: time.Now().UTC(),
		Metadata: event.Metadata{TraceID: "trace-abc", CorrelationID: "corr-xyz"},
	})
	records, _ := rt.Audit.List(context.Background(), "e1")
	if len(records) != 2 {
		t.Fatalf("expected 2 audit records, got %d", len(records))
	}
	for _, r := range records {
		if r.TraceID != "trace-abc" {
			t.Fatalf("expected trace_id trace-abc on %s, got %q", r.Kind, r.TraceID)
		}
		if r.CorrelationID != "corr-xyz" {
			t.Fatalf("expected correlation_id corr-xyz on %s, got %q", r.Kind, r.CorrelationID)
		}
	}
}

func TestRuntimeFollowUpEventsCarryParentCausation(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "p")
	rt := mustNew(t, Config{
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "PARENT", From: "", To: "ACTIVE"},
			state.Transition{EventType: "CHILD", From: "ACTIVE", To: "DONE"},
		),
		PermissionPolicy: policy,
	})
	// Ingest a parent event with trace metadata.
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "parent-1", Type: "PARENT", EntityID: "e1", EntityType: "T",
		Source: "test", ActorID: "a", OccurredAt: time.Now().UTC(),
		Metadata: event.Metadata{TraceID: "trace-1", CorrelationID: "corr-1"},
	})
	// Execute an action whose executor emits a follow-up event.
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "DO", EntityID: "e1", EntityType: "T",
		CurrentState: "ACTIVE", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "DO", AllowedStates: []string{"ACTIVE"},
		RequiredPermissions: []permission.Permission{"p"},
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				FollowUpEvents: []event.Event{{
					ID: "child-1", Type: "CHILD", EntityID: "e1", EntityType: "T",
					Source: "executor", ActorID: "agent", OccurredAt: time.Now().UTC(),
					// Note: executor does NOT set metadata; runtime should inherit.
				}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	events, _ := rt.Events.List(context.Background(), "e1")
	if len(events) != 2 {
		t.Fatalf("expected parent + child = 2 events, got %d", len(events))
	}
	var child event.Event
	for _, ev := range events {
		if ev.ID == "child-1" {
			child = ev
		}
	}
	if child.ID == "" {
		t.Fatal("expected child-1 event in store")
	}
	if child.Metadata.TraceID != "trace-1" {
		t.Fatalf("expected child to inherit trace_id, got %q", child.Metadata.TraceID)
	}
	if child.Metadata.CorrelationID != "corr-1" {
		t.Fatalf("expected child to inherit correlation_id, got %q", child.Metadata.CorrelationID)
	}
	if child.Metadata.CausationID != "parent-1" {
		t.Fatalf("expected causation_id=parent-1, got %q", child.Metadata.CausationID)
	}
}

func TestRuntimeActionAuditCarriesEntityTraceMetadata(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "p")
	rt := mustNew(t, Config{
		StateMachine:     state.NewTransitionMachine(state.Transition{EventType: "X", From: "", To: "A"}),
		PermissionPolicy: policy,
	})
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "X", EntityID: "e1", EntityType: "T",
		Source: "test", ActorID: "human", OccurredAt: time.Now().UTC(),
		Metadata: event.Metadata{TraceID: "trace-9", CorrelationID: "corr-9"},
	})
	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "DO", EntityID: "e1", EntityType: "T",
		CurrentState: "A", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "DO", AllowedStates: []string{"A"},
		RequiredPermissions: []permission.Permission{"p"},
		Executor:            noopExecutor,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	records, _ := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "e1", Kind: audit.KindActionExecuted})
	if len(records) != 1 {
		t.Fatalf("expected 1 action_executed record, got %d", len(records))
	}
	if records[0].TraceID != "trace-9" {
		t.Fatalf("expected action audit to carry trace-9, got %q", records[0].TraceID)
	}
}

func TestAuditFilterByTraceIDReturnsFullChain(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "p")
	rt := mustNew(t, Config{
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "X", From: "", To: "A"},
			state.Transition{EventType: "Y", From: "A", To: "B"},
		),
		PermissionPolicy: policy,
	})
	// Two entities, each with its own trace
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-1", Type: "X", EntityID: "e1", EntityType: "T",
		Source: "t", ActorID: "a", OccurredAt: time.Now().UTC(),
		Metadata: event.Metadata{TraceID: "trace-A"},
	})
	_ = rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-2", Type: "X", EntityID: "e2", EntityType: "T",
		Source: "t", ActorID: "a", OccurredAt: time.Now().UTC(),
		Metadata: event.Metadata{TraceID: "trace-B"},
	})
	_, _ = rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "DO", EntityID: "e1", EntityType: "T",
		CurrentState: "A", Actor: actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name: "DO", AllowedStates: []string{"A"},
		RequiredPermissions: []permission.Permission{"p"},
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				FollowUpEvents: []event.Event{{
					ID: "follow-1", Type: "Y", EntityID: "e1", EntityType: "T",
					Source: "x", ActorID: "agent", OccurredAt: time.Now().UTC(),
				}},
			}, nil
		},
	})
	records, _ := rt.Audit.Query(context.Background(), audit.Filter{TraceID: "trace-A"})
	if len(records) == 0 {
		t.Fatal("expected records for trace-A")
	}
	for _, r := range records {
		if r.EntityID != "e1" {
			t.Fatalf("trace-A filter returned record from %s", r.EntityID)
		}
		if r.TraceID != "trace-A" {
			t.Fatalf("expected trace-A on every record, got %q", r.TraceID)
		}
	}
	// Verify the chain includes ingest, state, execute, follow-up ingest, follow-up state
	expectedKinds := map[audit.Kind]bool{
		audit.KindEventIngested: false, audit.KindStateChanged: false,
		audit.KindActionValidated: false, audit.KindActionExecuted: false,
	}
	for _, r := range records {
		if _, ok := expectedKinds[r.Kind]; ok {
			expectedKinds[r.Kind] = true
		}
	}
	for kind, seen := range expectedKinds {
		if !seen {
			t.Fatalf("expected kind %q in trace-A chain", kind)
		}
	}
}
