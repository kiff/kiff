package runtime

import (
	"context"
	"errors"
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
