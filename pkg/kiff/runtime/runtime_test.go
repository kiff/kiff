package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
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

func TestRuntimeAppendsAuditRecordsDuringEventIngestionAndActionExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{
		StateMachine:     state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
		PermissionPolicy: policy,
	})

	if err := rt.IngestEvent(event.Event{
		ID:         "evt-1",
		Type:       "MISSION_SUBMITTED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "agent",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}

	_, err := rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "SUBMITTED",
		Actor:        actor.Actor{ID: "agent"},
		Approved:     true,
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}

	records, err := rt.Audit.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) < 4 {
		t.Fatalf("expected at least 4 audit records, got %d", len(records))
	}

	var sawEvent, sawState, sawExecuted bool
	for _, record := range records {
		switch record.Kind {
		case audit.KindEventIngested:
			sawEvent = true
		case audit.KindStateChanged:
			sawState = true
		case audit.KindActionExecuted:
			sawExecuted = true
		}
	}
	if !sawEvent || !sawState || !sawExecuted {
		t.Fatalf("expected event, state, and execution audit records, got %#v", records)
	}
}

func TestRuntimeUsesInjectedStoreBundle(t *testing.T) {
	bundle := store.NewInMemoryBundle()
	rt := New(Config{
		Stores:       &bundle,
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
	})

	err := rt.IngestEvent(event.Event{
		ID:         "evt-1",
		Type:       "MISSION_SUBMITTED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "human",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ingest event: %v", err)
	}

	events, err := bundle.Events.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list events from bundle: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event in injected bundle, got %d", len(events))
	}

	auditRecords, err := bundle.Audit.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list audit from bundle: %v", err)
	}
	if len(auditRecords) != 2 {
		t.Fatalf("expected event and state audit records in injected bundle, got %d", len(auditRecords))
	}
}

func TestRuntimeIngestRawUsesRegisteredAdapter(t *testing.T) {
	inputAdapter, err := adapter.NewPassthroughAdapter("mission")
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	rt := New(Config{
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
		Adapters:     []adapter.Adapter{inputAdapter},
	})

	ev, err := rt.IngestRaw(adapter.RawInput{
		ID:         "raw-1",
		Adapter:    "mission",
		Type:       "MISSION_SUBMITTED",
		Source:     "mission-cli",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		ActorID:    "human",
		ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ingest raw input: %v", err)
	}
	if ev.Type != "MISSION_SUBMITTED" {
		t.Fatalf("expected normalized event type, got %q", ev.Type)
	}

	events, err := rt.Events.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 ingested event, got %d", len(events))
	}
}

func TestRuntimeRecordsActionProposalAsDecision(t *testing.T) {
	rt := New(Config{})
	p := proposal.ActionProposal{
		ID:               "proposal-1",
		EntityID:         "attempt-1",
		EntityType:       "MissionAttempt",
		ActionName:       "PROPOSE_MOVE",
		ActorID:          "mission-agent",
		ReasoningSummary: "The active attempt can accept a proposed move.",
		Confidence:       0.82,
		CreatedAt:        time.Now().UTC(),
	}

	if err := rt.RecordActionProposal(p); err != nil {
		t.Fatalf("record action proposal: %v", err)
	}

	decisions, err := rt.Decisions.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Kind != decision.KindActionProposal {
		t.Fatalf("expected action proposal decision, got %q", decisions[0].Kind)
	}
}

func TestRuntimeValidatesActionProposalWithoutExecuting(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("mission-agent", "mission.propose_move")
	rt := New(Config{PermissionPolicy: policy})
	p := proposal.ActionProposal{
		ID:         "proposal-1",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		ActionName: "PROPOSE_MOVE",
		ActorID:    "mission-agent",
		Parameters: map[string]any{
			"move": "draft the first bounded move",
		},
		CreatedAt: time.Now().UTC(),
	}
	executed := false
	contract := action.ActionContract{
		Name:                "PROPOSE_MOVE",
		AllowedStates:       []string{"ACTIVE"},
		RequiredParameters:  []string{"move"},
		RequiredPermissions: []permission.Permission{"mission.propose_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			executed = true
			return action.ActionResult{Executed: true}, nil
		},
	}

	if err := rt.ValidateActionProposal(p, "ACTIVE", actor.Actor{ID: "mission-agent"}, contract); err != nil {
		t.Fatalf("validate action proposal: %v", err)
	}
	if executed {
		t.Fatal("expected proposal validation not to execute the action")
	}
}

func TestRuntimeIngestRawReturnsAdapterNotFound(t *testing.T) {
	rt := New(Config{})

	_, err := rt.IngestRaw(adapter.RawInput{
		ID:         "raw-1",
		Adapter:    "missing",
		Type:       "MISSION_SUBMITTED",
		Source:     "mission-cli",
		ReceivedAt: time.Now().UTC(),
	})
	if !errors.Is(err, adapter.ErrAdapterNotFound) {
		t.Fatalf("expected adapter.ErrAdapterNotFound, got %v", err)
	}
}

func TestRuntimeIndividualStoresOverrideBundleStores(t *testing.T) {
	bundle := store.NewInMemoryBundle()
	overrideEvents := event.NewInMemoryStore()
	rt := New(Config{
		Stores:       &bundle,
		EventStore:   overrideEvents,
		StateMachine: state.NewTransitionMachine(state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"}),
	})

	err := rt.IngestEvent(event.Event{
		ID:         "evt-1",
		Type:       "MISSION_SUBMITTED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "human",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ingest event: %v", err)
	}

	bundleEvents, err := bundle.Events.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list bundle events: %v", err)
	}
	if len(bundleEvents) != 0 {
		t.Fatalf("expected bundle event store to be overridden, got %d events", len(bundleEvents))
	}

	overrideStoredEvents, err := overrideEvents.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list override events: %v", err)
	}
	if len(overrideStoredEvents) != 1 {
		t.Fatalf("expected 1 event in override store, got %d", len(overrideStoredEvents))
	}
}

func TestRuntimeUsesGrantedApprovalRecordForActionValidation(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{PermissionPolicy: policy})

	if err := rt.RecordApproval(approval.Approval{
		ID:          "approval-1",
		EntityID:    "attempt-1",
		EntityType:  "MissionAttempt",
		ActionName:  "EXECUTE_MOVE",
		RequestedBy: "agent",
		ReviewedBy:  "human",
		Status:      approval.StatusGranted,
		CreatedAt:   time.Now().UTC(),
		ReviewedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record approval: %v", err)
	}

	executorSawApproved := false
	_, err := rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent"},
		ApprovalID:   "approval-1",
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			executorSawApproved = ctx.Approved
			return action.ActionResult{Executed: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("execute approved action: %v", err)
	}
	if !executorSawApproved {
		t.Fatal("expected executor to receive approved action context")
	}

	records, err := rt.Audit.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var sawApprovalGranted bool
	for _, record := range records {
		if record.Kind == audit.KindApprovalGranted {
			sawApprovalGranted = true
		}
	}
	if !sawApprovalGranted {
		t.Fatalf("expected approval granted audit record, got %#v", records)
	}
}

func TestRuntimeRequestApprovalCreatesPendingApproval(t *testing.T) {
	rt := New(Config{})
	actionCtx := action.ActionContext{
		ActionName: "EXECUTE_MOVE",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Actor:      actor.Actor{ID: "agent"},
	}
	contract := action.ActionContract{
		Name:                "EXECUTE_MOVE",
		ApprovalRequirement: action.ApprovalRequired,
	}

	request, err := rt.RequestApproval("approval-1", actionCtx, contract, "high-risk move execution")
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if request.Status != approval.StatusPending {
		t.Fatalf("expected pending approval, got %q", request.Status)
	}
	if request.RequestedBy != "agent" {
		t.Fatalf("expected requester agent, got %q", request.RequestedBy)
	}

	stored, ok, err := rt.Approvals.Get(context.Background(), "approval-1")
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if !ok {
		t.Fatal("expected stored approval")
	}
	if stored.Status != approval.StatusPending {
		t.Fatalf("expected stored pending approval, got %q", stored.Status)
	}
}

func TestRuntimeRequestApprovalRejectsActionsWithoutApprovalRequirement(t *testing.T) {
	rt := New(Config{})
	_, err := rt.RequestApproval("approval-1", action.ActionContext{
		ActionName: "PROPOSE_MOVE",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Actor:      actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name:                "PROPOSE_MOVE",
		ApprovalRequirement: action.ApprovalNever,
	}, "not needed")
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected action.ErrApprovalRequired, got %v", err)
	}
}

func TestRuntimeReviewApprovalGrantsPendingApproval(t *testing.T) {
	rt := New(Config{})
	actionCtx := action.ActionContext{
		ActionName: "EXECUTE_MOVE",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Actor:      actor.Actor{ID: "agent"},
	}
	contract := action.ActionContract{
		Name:                "EXECUTE_MOVE",
		ApprovalRequirement: action.ApprovalRequired,
	}
	if _, err := rt.RequestApproval("approval-1", actionCtx, contract, "needs human authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}

	reviewed, err := rt.ReviewApproval("approval-1", "human", approval.StatusGranted, "approved after review")
	if err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if reviewed.Status != approval.StatusGranted {
		t.Fatalf("expected granted approval, got %q", reviewed.Status)
	}
	if reviewed.ReviewedBy != "human" {
		t.Fatalf("expected reviewer human, got %q", reviewed.ReviewedBy)
	}

	records, err := rt.Timeline("attempt-1")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	var sawGranted bool
	for _, record := range records {
		if record.Kind == audit.KindApprovalGranted {
			sawGranted = true
		}
	}
	if !sawGranted {
		t.Fatalf("expected approval granted audit record, got %#v", records)
	}
}

func TestRuntimeRequestedAndGrantedApprovalAllowsExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{PermissionPolicy: policy})
	actionCtx := action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent"},
		ApprovalID:   "approval-1",
	}
	contract := action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
	}

	if _, err := rt.RequestApproval(actionCtx.ApprovalID, actionCtx, contract, "needs human authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if err := rt.RecordApproval(approval.Approval{
		ID:          actionCtx.ApprovalID,
		EntityID:    actionCtx.EntityID,
		EntityType:  actionCtx.EntityType,
		ActionName:  contract.Name,
		RequestedBy: actionCtx.Actor.ID,
		ReviewedBy:  "human",
		Status:      approval.StatusGranted,
		CreatedAt:   time.Now().UTC(),
		ReviewedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("grant approval: %v", err)
	}

	if _, err := rt.ExecuteAction(actionCtx, contract); err != nil {
		t.Fatalf("execute with granted approval: %v", err)
	}
}

func TestRuntimeAllowedActionsUsesDomainStateAndCatalog(t *testing.T) {
	machine := state.NewTransitionMachine()
	machine.Set(state.State{EntityID: "attempt-1", EntityType: "MissionAttempt", Value: "ACTIVE"})
	machine.SetAllowedActions("ACTIVE", []string{"PROPOSE_MOVE"})

	catalog := action.NewCatalog()
	if err := catalog.Register(action.ActionContract{Name: "PROPOSE_MOVE", AllowedStates: []string{"ACTIVE"}}); err != nil {
		t.Fatalf("register contract: %v", err)
	}

	rt, err := NewForDomain(domain.Definition{
		Name:         "mission",
		EntityTypes:  []string{"MissionAttempt"},
		EventTypes:   []string{"MOVE_PROPOSED"},
		StateMachine: machine,
		Actions:      catalog,
	}, Config{})
	if err != nil {
		t.Fatalf("new runtime for domain: %v", err)
	}

	contracts, err := rt.AllowedActions("attempt-1")
	if err != nil {
		t.Fatalf("allowed actions: %v", err)
	}
	if len(contracts) != 1 {
		t.Fatalf("expected 1 allowed action, got %d", len(contracts))
	}
	if contracts[0].Name != "PROPOSE_MOVE" {
		t.Fatalf("expected PROPOSE_MOVE, got %q", contracts[0].Name)
	}
}

func TestRuntimeAllowedActionsReturnsNotFoundForUnknownEntity(t *testing.T) {
	rt := New(Config{StateMachine: state.NewTransitionMachine()})

	_, err := rt.AllowedActions("missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected store.ErrNotFound, got %v", err)
	}
}

func TestRuntimeAuditsSuccessfulExecutionResultDetails(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{PermissionPolicy: policy})

	result, err := rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "READY",
		Actor:        actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"READY"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				Message:        "move executed",
				EffectsSummary: "created move artifact",
				Output:         map[string]any{"artifact_id": "artifact-1"},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}
	if result.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded status, got %q", result.Status)
	}

	records, err := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "attempt-1", Kind: audit.KindActionExecuted})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 execution audit record, got %d", len(records))
	}
	if records[0].Data["status"] != action.ExecutionSucceeded {
		t.Fatalf("expected audit status succeeded, got %#v", records[0].Data["status"])
	}
	if records[0].Data["effects_summary"] != "created move artifact" {
		t.Fatalf("expected effects summary in audit, got %#v", records[0].Data["effects_summary"])
	}
}

func TestRuntimeAuditsFailedExecutionResultDetails(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{PermissionPolicy: policy})

	result, err := rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "READY",
		Actor:        actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"READY"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{}, fmt.Errorf("executor failed")
		},
	})
	if err == nil {
		t.Fatal("expected execution error")
	}
	if result.Status != action.ExecutionFailed {
		t.Fatalf("expected failed status, got %q", result.Status)
	}
	if result.Error != "executor failed" {
		t.Fatalf("expected failed result error, got %q", result.Error)
	}

	records, err := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "attempt-1", Kind: audit.KindActionFailed})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 failure audit record, got %d", len(records))
	}
	if records[0].Data["status"] != action.ExecutionFailed {
		t.Fatalf("expected audit status failed, got %#v", records[0].Data["status"])
	}
	if records[0].Data["error"] != "executor failed" {
		t.Fatalf("expected audit error, got %#v", records[0].Data["error"])
	}
}

func TestRuntimeIngestsFollowUpEventsAfterSuccessfulExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "MOVE_EXECUTED", From: "WAITING_APPROVAL", To: "COMPLETED"},
		),
		PermissionPolicy: policy,
	})
	rt.States.(*state.TransitionMachine).Set(state.State{
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Value:      "WAITING_APPROVAL",
	})

	result, err := rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				Message:        "move executed",
				EffectsSummary: "emitted move executed event",
				FollowUpEvents: []event.Event{
					{
						ID:         "evt-follow-up-1",
						Type:       "MOVE_EXECUTED",
						EntityID:   "attempt-1",
						EntityType: "MissionAttempt",
						Source:     "executor",
						ActorID:    "agent",
						OccurredAt: time.Now().UTC(),
					},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}
	if result.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded status, got %q", result.Status)
	}

	events, err := rt.Events.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 follow-up event, got %d", len(events))
	}
	current, ok, err := rt.States.Current(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("current state: %v", err)
	}
	if !ok || current.Value != "COMPLETED" {
		t.Fatalf("expected COMPLETED state, got %#v", current)
	}

	timeline, err := rt.Timeline("attempt-1")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	expectedKinds := []audit.Kind{audit.KindActionValidated, audit.KindActionExecuted, audit.KindEventIngested, audit.KindStateChanged}
	if len(timeline) != len(expectedKinds) {
		t.Fatalf("expected %d timeline records, got %d: %#v", len(expectedKinds), len(timeline), timeline)
	}
	for i, kind := range expectedKinds {
		if timeline[i].Kind != kind {
			t.Fatalf("expected timeline kind %q at index %d, got %q", kind, i, timeline[i].Kind)
		}
	}
}

func TestRuntimeDoesNotIngestFollowUpEventsAfterFailedExecution(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "MOVE_EXECUTED", From: "WAITING_APPROVAL", To: "COMPLETED"},
		),
		PermissionPolicy: policy,
	})
	rt.States.(*state.TransitionMachine).Set(state.State{
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Value:      "WAITING_APPROVAL",
	})

	_, err := rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "WAITING_APPROVAL",
		Actor:        actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"WAITING_APPROVAL"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				FollowUpEvents: []event.Event{
					{
						ID:         "evt-follow-up-1",
						Type:       "MOVE_EXECUTED",
						EntityID:   "attempt-1",
						EntityType: "MissionAttempt",
						Source:     "executor",
						ActorID:    "agent",
						OccurredAt: time.Now().UTC(),
					},
				},
			}, fmt.Errorf("executor failed")
		},
	})
	if err == nil {
		t.Fatal("expected executor failure")
	}

	events, err := rt.Events.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no follow-up events after failure, got %d", len(events))
	}
	current, ok, err := rt.States.Current(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("current state: %v", err)
	}
	if !ok || current.Value != "WAITING_APPROVAL" {
		t.Fatalf("expected WAITING_APPROVAL state, got %#v", current)
	}
}

func TestRuntimeTimelineReconstructsOperationalPath(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "mission.execute_move")
	rt := New(Config{
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "MISSION_SUBMITTED", From: "", To: "SUBMITTED"},
		),
		PermissionPolicy: policy,
	})

	if err := rt.IngestEvent(event.Event{
		ID:         "evt-1",
		Type:       "MISSION_SUBMITTED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "human",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	if err := rt.ProposeDecision(decisionForTest("dec-1", "attempt-1")); err != nil {
		t.Fatalf("propose decision: %v", err)
	}
	err := rt.ValidateAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "SUBMITTED",
		Actor:        actor.Actor{ID: "agent"},
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
	})
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval requirement, got %v", err)
	}
	if err := rt.RecordApproval(approval.Approval{
		ID:          "approval-1",
		EntityID:    "attempt-1",
		EntityType:  "MissionAttempt",
		ActionName:  "EXECUTE_MOVE",
		RequestedBy: "agent",
		ReviewedBy:  "human",
		Status:      approval.StatusGranted,
		CreatedAt:   time.Now().UTC(),
		ReviewedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record approval: %v", err)
	}
	_, err = rt.ExecuteAction(action.ActionContext{
		ActionName:   "EXECUTE_MOVE",
		EntityID:     "attempt-1",
		EntityType:   "MissionAttempt",
		CurrentState: "SUBMITTED",
		Actor:        actor.Actor{ID: "agent"},
		ApprovalID:   "approval-1",
	}, action.ActionContract{
		Name:                "EXECUTE_MOVE",
		AllowedStates:       []string{"SUBMITTED"},
		RequiredPermissions: []permission.Permission{"mission.execute_move"},
		ApprovalRequirement: action.ApprovalRequired,
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}

	timeline, err := rt.Timeline("attempt-1")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	expectedKinds := []audit.Kind{
		audit.KindEventIngested,
		audit.KindStateChanged,
		audit.KindDecisionProposed,
		audit.KindApprovalRequired,
		audit.KindApprovalGranted,
		audit.KindActionValidated,
		audit.KindActionExecuted,
	}
	if len(timeline) != len(expectedKinds) {
		t.Fatalf("expected %d timeline records, got %d: %#v", len(expectedKinds), len(timeline), timeline)
	}
	for i, kind := range expectedKinds {
		if timeline[i].Kind != kind {
			t.Fatalf("expected timeline kind %q at index %d, got %q", kind, i, timeline[i].Kind)
		}
	}
}

func decisionForTest(id, entityID string) decision.Decision {
	return decision.Decision{
		ID:             id,
		EntityID:       entityID,
		EntityType:     "MissionAttempt",
		Kind:           decision.KindActionProposal,
		ProposedAction: "EXECUTE_MOVE",
		ActorID:        "agent",
		CreatedAt:      time.Now().UTC(),
	}
}
