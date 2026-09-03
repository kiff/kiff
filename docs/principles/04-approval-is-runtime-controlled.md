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

A caller can construct an `ActionContext` with any values they want, including an `ApprovalID` that points at any approval record in the world. Setting `approved: true` in a struct literal does not compile.

**That is not the enforcement, and an earlier version of this document said it was.** The lower-case field name is a compile-time rule; so is the `internal/` package that gates the `GrantApproval` capability. Reflection runs after the compiler has finished, and `reflect` exists precisely to reach values you cannot name:

```go
// From a separate module. No unsafe, four lines.
m := reflect.ValueOf(&ctx).MethodByName("GrantApproval")
m.Call([]reflect.Value{reflect.Zero(m.Type().In(0))})
```

`m.Type().In(0)` recovers the un-nameable capability type from the method's own signature, and `reflect.Zero` builds a value of it. An adversarial audit of this framework used exactly that, plus an `unsafe` variant, to run an approval-required action against an **empty approval store** — and the audit trail recorded both as `action_validated` and `action_executed`.

The real enforcement is that **the runtime does not believe the bit.**

When `runtime.ExecuteAction` runs, it calls `applyApproval`, which:

1. Looks up the approval record by ID in the configured approval store.
2. Verifies it is not nil, has matching entity and action, and has status `granted`.
3. Calls `actionCtx.GrantApproval()` (a method on `*ActionContext`) to flip the private bit.

Crucially, `applyApproval` **clears the bit before it consults the store**. Whatever a caller managed to put there — by reflection, by `unsafe`, by any means a compile-time rule cannot prevent — is discarded and re-derived from the approval record. Forging it accomplishes nothing, because the value is overwritten before anything reads it.

A second check sits above the pluggable `Validator`. Approval is the one decision an embedder must not be able to replace, so `runtime.Config.ActionValidator` can add requirements but cannot waive this one.

## What this prevents

The most common attempt to bypass governance, with or without intent:

```go
// This compiles, but it does NOT execute the action.
//
// The literal sets ApprovalID, which the runtime looks up in the approval
// store. If no granted approval matches, validation returns
// ErrApprovalRequired — and the same is true if the caller reaches the
// unexported bit by reflection, because the runtime clears it first.
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

The agent did not approve itself, and could not have. A human had to act, and their identity and reasoning are in the audit trail.

Since v0.8 the reviewer must also differ from the requester: `ReviewApproval` enforces segregation of duties by default. An approval the proposer can grant itself is a formality, and one that produces a record reading as a real two-party review is worse than no approval at all.

## What this looks like in tests

The boundary is testable at two levels, and both matter because the first is
not sufficient on its own.

**Compile-time**, in `pkg/kiff/action/approval_boundary_compile_test.go`: an
external module cannot name the field or import the capability package. Useful,
and never the property that mattered.

**Runtime**, in `pkg/kiff/action/approval_boundary_runtime_test.go`: three
fixtures in `testdata/self_approval/` build and *run* from a separate module
against an empty approval store, and must report refusal.

```
reflect_grant       reflect.Zero on the un-nameable capability type
unsafe_field        unsafe writes the unexported bit directly
hostile_validator   a permissive Validator via the public Config
```

Each fails without the fix and passes with it, so CI breaks if the boundary
regresses. Run them yourself:

```bash
go test ./pkg/kiff/action/ -run TestExternalCallerCannotGrantApprovalAtRuntime -v
```

A claim backed by a test that runs on every push is worth more than a stronger
claim backed by prose.

Your domain tests should include a denied-approval case (see [`examples/refund/refund_test.go`](../../examples/refund/refund_test.go)). It is the test that proves your governance boundary actually works.

## When to break it

There is no way to break this principle from outside the framework. That is the point. If you want a "skip approval" escape hatch, set the contract's `ApprovalRequirement: action.ApprovalNever` for the scenarios where it is acceptable. Do not try to engineer around the runtime check; the engineering will fail.

If you genuinely need a programmatic approver (a deterministic policy that auto-grants under certain conditions), implement it as a service that calls `rt.ReviewApproval` with `StatusGranted`. The auto-approver is just another reviewer, and its identity ends up in the audit trail.

The principle in one line: **approval is a fact, not a flag**.
