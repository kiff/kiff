# Approval is runtime-controlled

> Callers cannot self-approve high-risk actions. The runtime is the only component that can mark an action context as approved, and only after verifying a granted approval exists.

This is the most underappreciated guarantee in KIFF. If the trust boundary in principle three (agents propose, KIFF validates) is the headline, this is the line of code that makes the headline true.

## The shape

The `action.ActionContext` struct has an `approved` field. It is unexported:

```go
type ActionContext struct {
    ActionName   string
    EntityID     string
    EntityType   string
    CurrentState string
    Actor        actor.Actor
    Parameters   map[string]any
    ApprovalID   string
    approved     bool   // <-- private, lower-case
}
```

A caller can construct an `ActionContext` with any values they want, including an `ApprovalID` that points at any approval record in the world. What they cannot do is set `approved: true`. The lower-case field name is the entire enforcement.

When `runtime.ExecuteAction` runs, it calls `applyApproval`, which:

1. Looks up the approval record by ID in the configured approval store.
2. Verifies it is not nil, has matching entity and action, and has status `granted`.
3. Calls `actionCtx.GrantApproval()` (a method on `*ActionContext`) to flip the private bit.

Only after this private bit is set does the validator accept the action. There is no public way to fake the bit. A caller cannot pass `&action.ActionContext{approved: true}` because the field is unexported.

## What this prevents

The most common attempt to bypass governance, with or without intent:

```go
// This compiles, but it does NOT execute the action.
//
// approved is unexported. The literal sets ApprovalID, which the runtime
// will look up in the approval store. If no granted approval matches,
// validation returns ErrApprovalRequired.
ctx := action.ActionContext{
    ActionName: "REFUND_ORDER",
    ApprovalID: "i-made-this-up",
    // approved: true  ← cannot do this from outside the package
}
result, err := rt.ExecuteAction(context.Background(), ctx, contract)
// err == action.ErrApprovalRequired
```

The runtime does not trust the caller's claim that the action was approved. It checks, every time, against the approval store. The approval store is the source of truth. The `ApprovalID` is a pointer; the runtime resolves it.

## Why this matters for agents

Without this guarantee, an agent that constructs its own `ActionContext` could, in principle, set itself to approved. With this guarantee, the agent's only path to a granted approval is through the actual approval flow:

```go
// 1. The agent (or someone on its behalf) requests approval.
_, err := rt.RequestApproval(ctx, "approval-1", actionCtx, contract,
    "agent-initiated refund, customer reported damage")

// 2. A human reviewer grants or denies.
_, err = rt.ReviewApproval(ctx, "approval-1", "human-supervisor",
    approval.StatusGranted, "verified damage in photos")

// 3. The agent re-attempts execution. Now ExecuteAction succeeds because
//    applyApproval finds the granted record and flips the private bit.
result, err := rt.ExecuteAction(ctx, actionCtx, contract)
```

The agent did not approve itself. The agent could not approve itself. A human had to act, and the human's identity and reasoning are now in the audit trail forever.

## What this looks like in tests

The trust boundary is testable, and KIFF tests it:

```go
// pkg/kiff/action — the field is unexported.
ctx := action.ActionContext{
    // approved: true   ← does not compile
}

// pkg/kiff/runtime — only applyApproval can flip the bit.
// Direct callers of GrantApproval() exist for the runtime's internal use,
// but the validator only sees an approved context after the runtime
// resolves a granted approval from the store.
```

Your domain tests should include a denied-approval case (see [`examples/refund/refund_test.go`](../../examples/refund/refund_test.go)). It is the test that proves your governance boundary actually works.

## When to break it

There is no way to break this principle from outside the framework. That is the point. If you want a "skip approval" escape hatch, set the contract's `ApprovalRequirement: action.ApprovalNever` for the scenarios where it is acceptable. Do not try to engineer around the runtime check; the engineering will fail.

If you genuinely need a programmatic approver (a deterministic policy that auto-grants under certain conditions), implement it as a service that calls `rt.ReviewApproval` with `StatusGranted`. The auto-approver is just another reviewer, and its identity ends up in the audit trail.

The principle in one line: **approval is a fact, not a flag**.
