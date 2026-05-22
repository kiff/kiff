# Actions are contracts

> An action must declare when it is allowed, what it requires, who can perform it, and whether approval is needed.

In most AI applications, "actions" are tool calls. The model decides what to call, with what arguments, when. The tool does whatever the developer wrote, however the developer wrote it. The combination of model judgment and unconstrained tool implementation is where production goes wrong.

KIFF does not have tool calls. It has action contracts. A contract declares everything the runtime needs to decide whether the action is allowed before it starts running.

## The shape

A complete action contract looks like this:

```go
return action.ActionContract{
    Name:                "REFUND_ORDER",
    AllowedStates:       []string{"PAID"},
    RequiredParameters:  []string{"amount", "reason"},
    RequiredPermissions: []permission.Permission{"orders.refund"},
    Risk:                action.RiskHigh,
    ApprovalRequirement: action.ApprovalRequired,
    Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
        amount, _ := ctx.Parameters["amount"].(float64)
        return action.ActionResult{
            Status:         action.ExecutionSucceeded,
            Executed:       true,
            Message:        fmt.Sprintf("refund of $%.2f issued", amount),
            FollowUpEvents: []event.Event{ /* ORDER_REFUNDED */ },
            ExecutedAt:     time.Now().UTC(),
        }, nil
    },
}
```

Every field is doing real work:

- **`Name`** — the operational identifier. Shows up in audit records, HTTP routes, and proposal payloads. Stable.
- **`AllowedStates`** — the only states from which this action is meaningful. The runtime will refuse otherwise.
- **`RequiredParameters`** — the arguments that must be present and non-nil. Missing parameters fail validation; you do not need to defensively check inside the executor.
- **`RequiredPermissions`** — the permissions the actor must hold. The runtime queries your permission policy.
- **`Risk`** — operational metadata. Drives reporting, dashboards, and downstream tooling. Does not affect the runtime path.
- **`ApprovalRequirement`** — `ApprovalNever` or `ApprovalRequired`. The latter blocks execution until a granted approval matches the action context.
- **`Executor`** — what to actually do, given a validated context. The only place where domain logic runs.

## What this prevents

Free-form tool calls let the same logical action happen six different ways. Action contracts collapse that to one. Two consequences follow:

**The agent cannot call an action with the wrong arguments.** If the contract declares `RequiredParameters: []string{"amount", "reason"}`, the runtime returns `action.ErrMissingParameter` before the executor sees the call. Your executor never has to write `if amount == nil { return error }`.

**The audit trail is uniform.** Every execution of `REFUND_ORDER`, regardless of who proposed it, records the same fields. Six months from now, when you ask "show me every refund over $500 issued by an agent," the data is shaped to answer.

## What goes inside the executor

The executor is small. Conventionally:

1. Read parameters from `ctx.Parameters` (already validated to be present).
2. Do the side effect (call the payment processor, write to your DB, hit the external API).
3. Return an `ActionResult` describing what happened, including any follow-up events.

It does *not* check state; the runtime did. It does *not* check permissions; the runtime did. It does *not* check approval; the runtime did. By the time the executor runs, every gate has already been satisfied. The executor's only job is to do the thing and describe the outcome.

## How to write a new contract

The convention from [`docs/conventions.md`](../conventions.md):

```go
func RefundOrderContract() action.ActionContract {
    return action.ActionContract{
        Name:                ActionRefundOrder,
        AllowedStates:       []string{StatePaid},
        RequiredParameters:  []string{"amount", "reason"},
        RequiredPermissions: []permission.Permission{PermRefundOrder},
        Risk:                action.RiskHigh,
        ApprovalRequirement: action.ApprovalRequired,
        Executor:            refundOrderExecutor,
    }
}
```

One factory function per action. Constants for everything that names a thing. Field order is always the same. New developers can read down a contract and know the answer to every operational question in under thirty seconds.

The principle in one line: **declare it, do not describe it**.
