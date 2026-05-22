# Building a Domain on KIFF

This guide walks through modeling a small domain on top of the KIFF framework. It uses a simplified `Order` example so the mechanics stay clear.

KIFF normalizes coordination mechanics. Your domain owns its vocabulary: events, states, actions, permissions, and approval policy. The framework provides the primitives that make those choices explicit, testable, and auditable.

## What You Define

Five things, in order:

1. Constants for entity types, event types, state values, action names, and permissions.
2. The state machine: which event types move an entity from which state to which state.
3. Allowed actions per state.
4. Action contracts: what an action requires and what its executor does.
5. The permission policy: which roles can perform which actions.

KIFF wires these together at runtime.

## A Minimal Domain

```go
package orders

import (
	"context"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/domain"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
)

const (
	EntityOrder = "Order"

	EventCreated = "ORDER_CREATED"
	EventPaid    = "ORDER_PAID"

	StateCreated = "CREATED"
	StatePaid    = "PAID"

	ActionMarkPaid = "MARK_PAID"

	PermMarkPaid permission.Permission = "orders.mark_paid"
)

func MarkPaidContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionMarkPaid,
		AllowedStates:       []string{StateCreated},
		RequiredParameters:  []string{"payment_id"},
		RequiredPermissions: []permission.Permission{PermMarkPaid},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			paymentID, _ := ctx.Parameters["payment_id"].(string)
			return action.ActionResult{
				ActionName:     ActionMarkPaid,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				EffectsSummary: "marked order paid",
				FollowUpEvents: []event.Event{{
					ID:         "evt-paid-" + ctx.EntityID,
					Type:       EventPaid,
					EntityID:   ctx.EntityID,
					EntityType: EntityOrder,
					Source:     "orders/executor",
					ActorID:    ctx.Actor.ID,
					OccurredAt: time.Now().UTC(),
					Payload:    map[string]any{"payment_id": paymentID},
				}},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

func Definition() (domain.Definition, error) {
	return domain.New("orders").
		Entity(EntityOrder).
		Event(EventCreated).
		Event(EventPaid).
		Transition(EventCreated, "", StateCreated).
		Transition(EventPaid, StateCreated, StatePaid).
		Allow(StateCreated, ActionMarkPaid).
		Action(MarkPaidContract()).
		Build()
}

func Policy() *permission.SimplePolicy {
	p := permission.NewSimplePolicy()
	p.GrantRole("orders_operator", PermMarkPaid)
	return p
}
```

That is the entire domain definition. About 70 lines.

## Wiring a Runtime

```go
import (
	"github.com/kiffhq/kiff/pkg/kiff/runtime"
)

def, err := orders.Definition()
if err != nil {
	return err
}
rt, err := runtime.NewForDomain(def, runtime.Config{
	PermissionPolicy: orders.Policy(),
})
```

## Driving the Loop

```go
ctx := context.Background()

// 1. Ingest the input event
err := rt.IngestEvent(ctx, event.Event{
	ID:         "evt-001",
	Type:       orders.EventCreated,
	EntityID:   "order-1",
	EntityType: orders.EntityOrder,
	Source:     "checkout",
	ActorID:    "user-42",
	OccurredAt: time.Now().UTC(),
})

// 2. Discover allowed actions for the current state
contracts, _ := rt.AllowedActions(ctx, "order-1")
// contracts == [MARK_PAID]

// 3. Execute an allowed action
contract, _ := rt.Actions.Get(orders.ActionMarkPaid)
result, err := rt.ExecuteAction(ctx, action.ActionContext{
	ActionName:   orders.ActionMarkPaid,
	EntityID:     "order-1",
	EntityType:   orders.EntityOrder,
	CurrentState: orders.StateCreated,
	Actor:        actor.Actor{ID: "operator-1", Roles: []string{"orders_operator"}},
	Parameters:   map[string]any{"payment_id": "pay-9"},
}, contract)

// 4. Reconstruct the audit trail
timeline, _ := rt.Timeline(ctx, "order-1")
```

`ExecuteAction` validates state, parameters, permissions, and approval before calling the executor. The executor's follow-up event is ingested through the normal path, so state transitions stay event-driven and audit records cover every step.

## High-Risk Actions

When an action affects production or is otherwise dangerous, set `ApprovalRequirement: action.ApprovalRequired`. The runtime then refuses to execute until a granted approval record matches the entity and action.

```go
contract := action.ActionContract{
	Name:                "REFUND_ORDER",
	ApprovalRequirement: action.ApprovalRequired,
	// ...
}

// Caller must request and resolve approval through the runtime.
_, err := rt.RequestApproval(ctx, "approval-1", actionCtx, contract, "refund needs human authority")
_, err = rt.ReviewApproval(ctx, "approval-1", "human-supervisor", approval.StatusGranted, "approved after review")

// Execution now succeeds because the runtime resolves the granted approval
// from the approval store. Callers cannot self-approve.
result, err := rt.ExecuteAction(ctx, actionCtxWithApprovalID, contract)
```

The runtime is the only component that can mark an action context as approved. There is no public field a caller can flip.

## Trace Correlation

Set `event.Metadata.TraceID` and `CorrelationID` on the input event. Every audit record produced from that event, every action executed against that entity, and every follow-up event emitted by an executor inherits the trace metadata. Filter audit records by `TraceID` to reconstruct a single external request end to end:

```go
records, _ := rt.Audit.Query(ctx, audit.Filter{TraceID: "req-abc-123"})
```

## What Stays Out of Your Domain Code

The following live in `pkg/kiff` and you should not duplicate them:

- Event normalization and storage.
- State application and transition validation.
- Action validation (state, parameters, permissions, approval).
- Executor invocation and follow-up event ingestion.
- Approval requests, reviews, and grant resolution.
- Audit record creation and trace propagation.

Your domain owns vocabulary and intent. KIFF owns coordination.

## See Also

- `examples/mission/mission.go` for a fuller worked example using the same builder.
- `docs/architecture.md` for the package boundaries.
- `docs/changelog/brick-17.md` for trace correlation behavior.
