// Package refundagno is the demo domain for examples/refund-agno.
//
// It models a tiny support-ops surface a CTO recognizes immediately:
// support tickets land, an agent decides what to do, KIFF gates the actions
// that should not be left to the agent alone.
//
// The domain is intentionally neutral. There is no The Line vocabulary here.
// Entity is "Order"; states are CREATED, PAID, REFUNDED. The agent is given
// one tool surface ("refund_order") that the server routes to one of two
// KIFF action contracts based on amount:
//
//   - AUTO_REFUND  (low risk, no approval, allowed when amount <= 100 USD)
//   - REFUND_ORDER (high risk, approval required, any amount, the moment
//     KIFF's value becomes obvious)
//   - WAIVE_FEE    (always requires approval; the "different action, same
//     governance shape" backup story for a CTO who wants to see how a
//     second sensitive action plugs in)
//   - MARK_PAID    (no approval; lets the seed flow be one ingest call)
//
// The split is the standard production pattern: the contract declares risk
// and authority; runtime policy decides which contract a particular request
// reaches. KIFF v0.1's ApprovalRequirement is binary, and we keep it that
// way. The amount threshold lives in routing, not in the framework.
package refundagno

import (
	"context"
	"fmt"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/adapter"
	"github.com/kiffhq/kiff/pkg/kiff/domain"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
	"github.com/kiffhq/kiff/pkg/kiff/runtime"
	"github.com/kiffhq/kiff/pkg/kiff/state"
	"github.com/kiffhq/kiff/pkg/kiff/store"
)

// AutoRefundCeilingCents is the routing threshold: refunds at or below this
// amount are routed to AUTO_REFUND (no approval). Anything above goes to
// REFUND_ORDER (approval required). The value is 100 USD expressed in cents
// so the wire protocol stays integer.
const AutoRefundCeilingCents = 10000

// Adapter, entity, event, state, action, and permission identifiers.
const (
	AdapterRefund = "refund-agno"

	EntityOrder = "Order"

	EventOrderPlaced   = "ORDER_PLACED"
	EventOrderPaid     = "ORDER_PAID"
	EventOrderRefunded = "ORDER_REFUNDED"
	EventFeeWaived     = "FEE_WAIVED"

	StateCreated  = "CREATED"
	StatePaid     = "PAID"
	StateRefunded = "REFUNDED"

	ActionMarkPaid    = "MARK_PAID"
	ActionAutoRefund  = "AUTO_REFUND"
	ActionRefundOrder = "REFUND_ORDER"
	ActionWaiveFee    = "WAIVE_FEE"

	PermMarkPaid    permission.Permission = "refundagno.mark_paid"
	PermAutoRefund  permission.Permission = "refundagno.auto_refund"
	PermRefundOrder permission.Permission = "refundagno.refund_order"
	PermWaiveFee    permission.Permission = "refundagno.waive_fee"
	PermApprove     permission.Permission = "refundagno.approve"
)

// Demo actors. A real application would source these from its identity layer.
var (
	SystemActor   = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor    = actor.Actor{ID: "support-agent", Type: actor.TypeAgent, DisplayName: "Support Agent", Roles: []string{"support_agent"}}
	OperatorActor = actor.Actor{ID: "ops-human", Type: actor.TypeHuman, DisplayName: "Ops Operator", Roles: []string{"ops_operator"}}
)

// NewPermissionPolicy returns the demo permission policy.
//
// Agents may propose every action and may execute the low-risk ones.
// Only operators carry the explicit approve permission. The runtime, not
// this policy, enforces that approval requirement on REFUND_ORDER and
// WAIVE_FEE; this policy just expresses who is allowed to do what.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole("support_agent", PermMarkPaid)
	policy.GrantRole("support_agent", PermAutoRefund)
	policy.GrantRole("support_agent", PermRefundOrder)
	policy.GrantRole("support_agent", PermWaiveFee)
	policy.GrantRole("ops_operator", PermApprove)
	policy.GrantRole("ops_operator", PermAutoRefund)
	policy.GrantRole("ops_operator", PermRefundOrder)
	policy.GrantRole("ops_operator", PermWaiveFee)
	policy.GrantRole("system", PermMarkPaid)
	return policy
}

// Contracts returns the action contracts for the demo.
func Contracts() []action.ActionContract {
	return []action.ActionContract{
		markPaidContract(),
		autoRefundContract(),
		refundOrderContract(),
		waiveFeeContract(),
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

func autoRefundContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionAutoRefund,
		AllowedStates:       []string{StatePaid},
		RequiredParameters:  []string{"amount_cents", "reason"},
		RequiredPermissions: []permission.Permission{PermAutoRefund},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			amount, err := readAmountCents(ctx.Parameters)
			if err != nil {
				return action.ActionResult{}, err
			}
			if amount <= 0 {
				return action.ActionResult{}, fmt.Errorf("auto refund amount must be positive: %d", amount)
			}
			if amount > AutoRefundCeilingCents {
				return action.ActionResult{}, fmt.Errorf(
					"auto refund amount %d cents exceeds ceiling %d; must be routed to %s",
					amount, AutoRefundCeilingCents, ActionRefundOrder,
				)
			}
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionAutoRefund,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("auto-refunded %d cents: %s", amount, reason),
				EffectsSummary: "auto refund issued (under ceiling)",
				Output: map[string]any{
					"amount_cents": amount,
					"reason":       reason,
					"path":         "auto",
				},
				FollowUpEvents: []event.Event{
					orderEvent(ctx.EntityID, EventOrderRefunded, ctx.Actor.ID, map[string]any{
						"amount_cents": amount,
						"reason":       reason,
						"path":         "auto",
					}),
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
			amount, err := readAmountCents(ctx.Parameters)
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
				Output: map[string]any{
					"amount_cents": amount,
					"reason":       reason,
					"path":         "approved",
				},
				FollowUpEvents: []event.Event{
					orderEvent(ctx.EntityID, EventOrderRefunded, ctx.Actor.ID, map[string]any{
						"amount_cents": amount,
						"reason":       reason,
						"path":         "approved",
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

func waiveFeeContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionWaiveFee,
		AllowedStates:       []string{StatePaid},
		RequiredParameters:  []string{"fee_cents", "reason"},
		RequiredPermissions: []permission.Permission{PermWaiveFee},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			fee, err := readIntCents(ctx.Parameters, "fee_cents")
			if err != nil {
				return action.ActionResult{}, err
			}
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionWaiveFee,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("waived %d cents in fees: %s", fee, reason),
				EffectsSummary: "fee waived under approval",
				Output: map[string]any{
					"fee_cents": fee,
					"reason":    reason,
				},
				FollowUpEvents: []event.Event{
					orderEvent(ctx.EntityID, EventFeeWaived, ctx.Actor.ID, map[string]any{
						"fee_cents": fee,
						"reason":    reason,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// NewStateMachine constructs the order state machine. Both refund paths
// (AUTO_REFUND, REFUND_ORDER) emit the same ORDER_REFUNDED event so the
// state machine and replay stay simple.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
		state.Transition{EventType: EventOrderPlaced, From: "", To: StateCreated},
		state.Transition{EventType: EventOrderPaid, From: StateCreated, To: StatePaid},
		state.Transition{EventType: EventOrderRefunded, From: StatePaid, To: StateRefunded},
		state.Transition{EventType: EventFeeWaived, From: StatePaid, To: StatePaid},
	)
	machine.SetAllowedActions(StateCreated, []string{ActionMarkPaid})
	machine.SetAllowedActions(StatePaid, []string{ActionAutoRefund, ActionRefundOrder, ActionWaiveFee})
	return machine
}

// NewDomainDefinition assembles the demo domain.
func NewDomainDefinition() (domain.Definition, error) {
	b := domain.New("refund-agno").
		Entity(EntityOrder).
		Event(EventOrderPlaced).
		Event(EventOrderPaid).
		Event(EventOrderRefunded).
		Event(EventFeeWaived).
		Transition(EventOrderPlaced, "", StateCreated).
		Transition(EventOrderPaid, StateCreated, StatePaid).
		Transition(EventOrderRefunded, StatePaid, StateRefunded).
		Transition(EventFeeWaived, StatePaid, StatePaid).
		Allow(StateCreated, ActionMarkPaid).
		Allow(StatePaid, ActionAutoRefund).
		Allow(StatePaid, ActionRefundOrder).
		Allow(StatePaid, ActionWaiveFee)
	for _, contract := range Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter creates the input adapter for raw events.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterRefund)
}

// NewRuntime returns a runtime wired for the demo using in-memory stores.
func NewRuntime() (*runtime.Runtime, error) {
	return NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores returns a runtime wired for the demo using the
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

// RouteRefund maps an agent's "refund_order" intent to a concrete KIFF
// action name based on amount. The threshold lives here, not in any
// contract: contracts express authority shape; routing expresses policy.
func RouteRefund(amountCents int64) string {
	if amountCents > 0 && amountCents <= AutoRefundCeilingCents {
		return ActionAutoRefund
	}
	return ActionRefundOrder
}

func orderEvent(orderID, eventType, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", eventType, orderID, time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   orderID,
		EntityType: EntityOrder,
		Source:     "examples/refund-agno",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}

func readAmountCents(params map[string]any) (int64, error) {
	return readIntCents(params, "amount_cents")
}

// readIntCents tolerates int, int64, and float64 (which JSON decodes by
// default) so the executor accepts payloads from both Go callers and
// HTTP/JSON callers without contention.
func readIntCents(params map[string]any, key string) (int64, error) {
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
