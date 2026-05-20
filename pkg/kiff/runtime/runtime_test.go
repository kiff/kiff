package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
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
