# State before action

> The system must know the current state before deciding what can happen next.

This is the principle that decides whether your code is KIFF-shaped or not. Most production bugs in agentic systems trace back to here: the agent acted on a stale or imagined state, and nothing checked.

## The shape

In KIFF, every action contract declares which states allow it. The runtime resolves the entity's current state from the event log before validating anything else. If the action is not allowed in the current state, the runtime refuses, and execution never starts.

```go
return action.ActionContract{
    Name:          "REFUND_ORDER",
    AllowedStates: []string{"PAID"},   // <-- the gate
    // ...
}
```

That single line is the principle. It says: this action is meaningless unless the order is in the `PAID` state. An agent that proposes a refund on a `CREATED` order gets stopped at validation, with a typed error (`action.ErrStateNotAllowed`).

## What it prevents

Without this principle, an agent could:

- refund an order that was never paid, because it pattern-matched on customer language;
- mark a task complete twice, because it forgot it had already done so;
- submit a contract for a deal that was already canceled, because the chat history was stale.

With this principle, none of those reach an executor. The runtime returns the error, the audit trail records the attempt, and the entity does not move.

## The state itself is event-driven

KIFF refuses to let actions mutate state directly. Executors do not write to a state table. They emit follow-up events, and those events drive transitions through the state machine. This sounds like extra ceremony; it is the thing that makes replay possible.

```go
Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
    return action.ActionResult{
        Status:   action.ExecutionSucceeded,
        Executed: true,
        FollowUpEvents: []event.Event{
            // The state change is the event, not a side effect.
            orderEvent(ctx.EntityID, "ORDER_REFUNDED", ctx.Actor.ID, nil),
        },
        ExecutedAt: time.Now().UTC(),
    }, nil
},
```

When the runtime sees that follow-up event, it ingests it through the normal path: applies the transition, runs the audit hook, and updates the state. Six months from now, you can rebuild the entity from events alone with `runtime.RebuildState` and get the same final state.

## What this looks like at runtime

```go
// The runtime knows the state; the caller does not have to assert it correctly.
allowed, _ := rt.AllowedActions(ctx, "order-1")
// → returns only the contracts whose AllowedStates contains the current state.

// The validator checks state first, before parameters or permissions.
err := rt.ValidateAction(ctx, actionCtx, contract)
// → returns action.ErrStateNotAllowed if you tried to refund a CREATED order.
```

`AllowedActions` is the public-facing version of this principle. Your UI, your agent, your CLI can ask the runtime "what can I do right now?" and get back exactly the set of contracts that the current state permits. The agent stops guessing; the system knows.

## When to break it

There is no good reason to. If you find yourself wanting to skip the state check, you are usually trying to model a side effect that does not actually depend on entity state (sending a notification, logging analytics). Those are not KIFF actions; they are infrastructure. Keep them out of action contracts.

The principle in one line: **state is the question; the action is the answer**.
