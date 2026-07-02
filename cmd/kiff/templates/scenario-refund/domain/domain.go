// Package domain is the refund scenario domain.
//
// It models an Order lifecycle (CREATED → PAID → REFUNDED) with two agent
// actions: MARK_PAID (low risk, no approval) and REFUND_ORDER (high risk,
// human approval required). This is the governed-action loop: a stateful
// entity, an agent that proposes mutations, and a runtime that validates and
// gates them before the side effect runs.
//
// Unlike a `kiff scaffold` skeleton, these executors are real (they emit the
// follow-up event that advances state), so the project runs and its tests pass
// out of the box. The mock business side effect (a refund ledger) lives in the
// app layer, in cmd/server, so it only runs after KIFF allows the action.
package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	kiffdomain "github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/state"
	"github.com/kiff/kiff/pkg/kiff/store"
)

const (
	AdapterRefund = "refund"

	EntityOrder = "Order"

	EventOrderPlaced   = "ORDER_PLACED"
	EventOrderPaid     = "ORDER_PAID"
	EventOrderRefunded = "ORDER_REFUNDED"

	StateCreated  = "CREATED"
	StatePaid     = "PAID"
	StateRefunded = "REFUNDED"

	ActionMarkPaid    = "MARK_PAID"
	ActionRefundOrder = "REFUND_ORDER"

	PermMarkPaid    permission.Permission = "refund.mark_paid"
	PermRefundOrder permission.Permission = "refund.refund_order"
	PermApprove     permission.Permission = "refund.approve"
)

// Demo actors. A real application sources these from its identity layer.
var (
	SystemActor   = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor    = actor.Actor{ID: "support-agent", Type: actor.TypeAgent, DisplayName: "Support Agent", Roles: []string{"support_agent"}}
	OperatorActor = actor.Actor{ID: "ops-operator", Type: actor.TypeHuman, DisplayName: "Ops Operator", Roles: []string{"ops_operator"}}
)

// NewStateMachine returns the order state machine.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
		state.Transition{EventType: EventOrderPlaced, From: "", To: StateCreated},
		state.Transition{EventType: EventOrderPaid, From: StateCreated, To: StatePaid},
		state.Transition{EventType: EventOrderRefunded, From: StatePaid, To: StateRefunded},
	)
	machine.SetAllowedActions(StateCreated, []string{ActionMarkPaid})
	machine.SetAllowedActions(StatePaid, []string{ActionRefundOrder})
	return machine
}

// NewPermissionPolicy returns the demo policy: agents propose the actions they
// need; only operators carry approve.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole("support_agent", PermMarkPaid)
	policy.GrantRole("support_agent", PermRefundOrder)
	policy.GrantRole("ops_operator", PermApprove)
	policy.GrantRole("system", PermMarkPaid)
	policy.AssignRole(AgentActor.ID, "support_agent")
	policy.AssignRole(OperatorActor.ID, "ops_operator")
	policy.AssignRole(SystemActor.ID, "system")
	return policy
}

// Contracts returns the domain's action contracts.
func Contracts() []action.ActionContract {
	return []action.ActionContract{markPaidContract(), refundOrderContract()}
}

func markPaidContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionMarkPaid,
		AllowedStates:       []string{StateCreated},
		RequiredParameters:  []string{"payment_id"},
		RequiredPermissions: []permission.Permission{PermMarkPaid},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			paymentID, _ := ctx.Parameters["payment_id"].(string)
			return action.ActionResult{
				ActionName:     ActionMarkPaid,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("payment %s captured", paymentID),
				EffectsSummary: "marked order paid",
				FollowUpEvents: []event.Event{
					orderEvent(ctx.EntityID, EventOrderPaid, ctx.Actor.ID, map[string]any{"payment_id": paymentID}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

func refundOrderContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionRefundOrder,
		AllowedStates:       []string{StatePaid},
		RequiredParameters:  []string{"amount_cents", "reason"},
		RequiredPermissions: []permission.Permission{PermRefundOrder},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			amount, err := ReadIntCents(ctx.Parameters, "amount_cents")
			if err != nil {
				return action.ActionResult{}, err
			}
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionRefundOrder,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("refund of %d cents issued: %s", amount, reason),
				EffectsSummary: "refund processed under approval",
				Output:         map[string]any{"amount_cents": amount, "reason": reason},
				FollowUpEvents: []event.Event{
					orderEvent(ctx.EntityID, EventOrderRefunded, ctx.Actor.ID, map[string]any{
						"amount_cents": amount,
						"reason":       reason,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// NewDefinition assembles the domain definition.
func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("refund").
		Entity(EntityOrder).
		Event(EventOrderPlaced).
		Event(EventOrderPaid).
		Event(EventOrderRefunded).
		Transition(EventOrderPlaced, "", StateCreated).
		Transition(EventOrderPaid, StateCreated, StatePaid).
		Transition(EventOrderRefunded, StatePaid, StateRefunded).
		Allow(StateCreated, ActionMarkPaid).
		Allow(StatePaid, ActionRefundOrder)
	for _, contract := range Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter creates the input adapter.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterRefund)
}

// NewRuntime returns a runtime wired for this domain using in-memory stores.
func NewRuntime() (*runtime.Runtime, error) {
	return NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores returns a runtime wired with the provided store bundle.
// A nil bundle falls back to in-memory stores.
func NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
	def, err := NewDefinition()
	if err != nil {
		return nil, err
	}
	in, err := NewInputAdapter()
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(def, runtime.Config{
		PermissionPolicy: NewPermissionPolicy(),
		Adapters:         []adapter.Adapter{in},
		Stores:           stores,
	})
}

func orderEvent(orderID, eventType, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", eventType, orderID, time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   orderID,
		EntityType: EntityOrder,
		Source:     "refund/executor",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}

// ReadIntCents coerces a JSON-decoded numeric parameter to int64 cents.
func ReadIntCents(params map[string]any, key string) (int64, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q", action.ErrMissingParameter, key)
	}
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%s must be a number, got %T", key, value)
	}
}
