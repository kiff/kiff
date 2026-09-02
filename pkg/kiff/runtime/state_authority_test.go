package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/kifftest"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/state"
)

// orderMachine models PLACED -> PAID -> REFUNDED.
func orderMachine() state.StateMachine {
	return state.NewTransitionMachine(
		state.Transition{EventType: "ORDER_PLACED", From: "", To: "PLACED"},
		state.Transition{EventType: "ORDER_PAID", From: "PLACED", To: "PAID"},
		state.Transition{EventType: "ORDER_REFUNDED", From: "PAID", To: "REFUNDED"},
	)
}

func stateRefundContract(ran *bool) action.ActionContract {
	return action.ActionContract{
		Name:          "REFUND_ORDER",
		AllowedStates: []string{"PAID"},
		Risk:          action.RiskCritical,
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			*ran = true
			return action.ActionResult{Status: action.ExecutionSucceeded, Executed: true}, nil
		},
	}
}

func ingest(t *testing.T, rt *Runtime, entityID, eventType string) {
	t.Helper()
	ev := kifftest.NewEvent(eventType).WithEntity(entityID, "Order").WithSource("test").Build()
	if err := rt.IngestEvent(context.Background(), ev); err != nil {
		t.Fatalf("ingest %s: %v", eventType, err)
	}
}

// TestCallerCannotAssertAFavourableState is the state half of the trust
// boundary. The approval bit cannot be forged (see the action package's
// conformance tests); neither may the state an action is judged against.
//
// The entity below has already been refunded. An agent that could name its own
// CurrentState could re-authorize the refund simply by claiming the order is
// still PAID — the runtime must derive the state instead of believing it.
func TestCallerCannotAssertAFavourableState(t *testing.T) {
	executed := false
	rt := mustNew(t, Config{StateMachine: orderMachine()})
	contract := stateRefundContract(&executed)

	ingest(t, rt, "order-1", "ORDER_PLACED")
	ingest(t, rt, "order-1", "ORDER_PAID")
	ingest(t, rt, "order-1", "ORDER_REFUNDED")

	_, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   "REFUND_ORDER",
		EntityID:     "order-1",
		EntityType:   "Order",
		CurrentState: "PAID", // a lie; the store says REFUNDED
		Actor:        actor.Actor{ID: "agent-1"},
	}, contract)

	if executed {
		t.Fatal("STATE FORGERY: the executor ran against a caller-asserted state")
	}
	if !errors.Is(err, action.ErrStateMismatch) {
		t.Fatalf("want ErrStateMismatch, got %v", err)
	}
	if got, _ := outcome.Classify(err); got != outcome.Blocked {
		t.Errorf("a state mismatch should classify as blocked, got %v", got)
	}
}

// The stored state is authoritative in the permissive direction too: a caller
// that supplies nothing gets the real state rather than an empty one.
func TestRuntimeFillsCurrentStateFromTheStore(t *testing.T) {
	executed := false
	rt := mustNew(t, Config{StateMachine: orderMachine()})
	contract := stateRefundContract(&executed)

	ingest(t, rt, "order-2", "ORDER_PLACED")
	ingest(t, rt, "order-2", "ORDER_PAID")

	if _, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName: "REFUND_ORDER",
		EntityID:   "order-2",
		EntityType: "Order",
		Actor:      actor.Actor{ID: "agent-1"},
	}, contract); err != nil {
		t.Fatalf("execute with derived state: %v", err)
	}
	if !executed {
		t.Fatal("executor should have run: the stored state is PAID")
	}
}

// Bootstrapping must keep working. An entity with no ingested events has no
// stored state, so the caller's value stands — otherwise a state machine could
// never be started, and every seed path would break.
func TestCallerStateStandsWhenTheStoreHasNone(t *testing.T) {
	executed := false
	rt := mustNew(t, Config{StateMachine: orderMachine()})
	contract := stateRefundContract(&executed)

	if _, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   "REFUND_ORDER",
		EntityID:     "never-seen",
		EntityType:   "Order",
		CurrentState: "PAID",
		Actor:        actor.Actor{ID: "seeder"},
	}, contract); err != nil {
		t.Fatalf("bootstrap execute: %v", err)
	}
	if !executed {
		t.Fatal("executor should have run on the bootstrap path")
	}
}

// With no state machine wired the runtime has nothing to derive from, so
// behavior is unchanged for embedders that manage state themselves.
func TestCallerStateStandsWithoutAStateMachine(t *testing.T) {
	executed := false
	rt := mustNew(t, Config{})
	contract := stateRefundContract(&executed)

	if _, err := rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   "REFUND_ORDER",
		EntityID:     "order-3",
		EntityType:   "Order",
		CurrentState: "PAID",
		Actor:        actor.Actor{ID: "agent-1"},
	}, contract); err != nil {
		t.Fatalf("execute without a state machine: %v", err)
	}
	if !executed {
		t.Fatal("executor should have run without a state machine")
	}
}
