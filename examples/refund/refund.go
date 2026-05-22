// Package refund is an example KIFF domain modeling a tiny order/refund flow.
//
// It is intentionally legible: anyone who has worked with money understands
// "an order is paid" and "a refund needs human approval." The domain exists
// to make the KIFF loop visible to a reader in under a minute.
//
// Domain semantics live here. Coordination mechanics live in pkg/kiff.
package refund

import (
	"context"
	"fmt"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/domain"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store"
)

// Adapter, entity, event, state, action, and permission identifiers.
const (
	AdapterRefund = "refund"

	EntityOrder = "Order"

	EventOrderPlaced   = "ORDER_PLACED"
	EventOrderPaid     = "ORDER_PAID"
	EventOrderRefunded = "ORDER_REFUNDED"

	StateCreated  = "CREATED"
	StatePaid     = "PAID"
	StateRefunded = "REFUNDED"

	ActionMarkPaid     = "MARK_PAID"
	ActionRefundOrder  = "REFUND_ORDER"

	PermMarkPaid    permission.Permission = "refund.mark_paid"
	PermRefundOrder permission.Permission = "refund.refund_order"
	PermApprove     permission.Permission = "refund.approve"
)

// Demo actors. A real application would source these from its identity layer.
var (
	SystemActor   = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor    = actor.Actor{ID: "ops-agent", Type: actor.TypeAgent, DisplayName: "Ops Agent", Roles: []string{"ops_agent"}}
	OperatorActor = actor.Actor{ID: "ops-human", Type: actor.TypeHuman, DisplayName: "Ops Operator", Roles: []string{"ops_operator"}}
)

// NewStateMachine builds the order state machine.
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

// NewPermissionPolicy returns the demo permission policy. Agents can propose
// and execute. Only operators can approve refunds.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole("ops_agent", PermMarkPaid)
	policy.GrantRole("ops_agent", PermRefundOrder)
	policy.GrantRole("ops_operator", PermApprove)
	policy.GrantRole("ops_operator", PermRefundOrder)
	policy.GrantRole("system", PermMarkPaid)
	return policy
}

// Contracts returns the refund action contracts.
func Contracts() []action.ActionContract {
	return []action.ActionContract{
		markPaidContract(),
		refundOrderContract(),
	}
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
		RequiredParameters:  []string{"amount", "reason"},
		RequiredPermissions: []permission.Permission{PermRefundOrder},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			amount, _ := ctx.Parameters["amount"].(float64)
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionRefundOrder,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("refund of $%.2f issued: %s", amount, reason),
				EffectsSummary: "refund processed",
				Output:         map[string]any{"amount": amount, "reason": reason},
				FollowUpEvents: []event.Event{
					orderEvent(ctx.EntityID, EventOrderRefunded, ctx.Actor.ID, map[string]any{
						"amount": amount,
						"reason": reason,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// NewDomainDefinition assembles the refund domain using the domain.Builder.
func NewDomainDefinition() (domain.Definition, error) {
	b := domain.New("refund").
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

// NewInputAdapter creates the refund domain input adapter.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterRefund)
}

// NewRuntime returns a runtime wired for the refund example using in-memory stores.
func NewRuntime() (*runtime.Runtime, error) {
	return NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores returns a runtime wired for the refund example using the
// provided store bundle. A nil bundle falls back to in-memory stores.
func NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
	def, err := NewDomainDefinition()
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
		Source:     "examples/refund",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}
