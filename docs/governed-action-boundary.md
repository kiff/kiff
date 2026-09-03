# The governed action boundary

The governed action boundary is the core of KIFF: the one line an agent's
proposal must cross before it becomes a side effect. KIFF makes that boundary
**enforceable, explainable, and replayable**.

```text
proposal → validation decision → optional approval → execution → result → audit/replay
```

An agent (or a human, service, or integration) proposes a consequential action.
KIFF validates it against the entity's event-derived **current state**, the
action's **required parameters**, the actor's **permissions**, and its
**approval requirement**. Only an allowed action reaches its executor. Every
decision, approval, execution, and state change is recorded, so the same
current state — and the reason behind every decision — can be rebuilt from the
event log alone.

## The decision envelope

Every layer that reports what KIFF decided speaks one typed vocabulary,
`pkg/kiff/outcome`. A `Decision` is a small, JSON-serializable envelope:

```go
type Decision struct {
    Outcome      Outcome `json:"outcome"`       // allowed | approval_required | blocked | invalid
    Reason       Reason  `json:"reason,omitempty"`
    Action       string  `json:"action,omitempty"`
    EntityID     string  `json:"entity_id,omitempty"`
    CurrentState string  `json:"current_state,omitempty"`
    NextStep     string  `json:"next_step,omitempty"`
    Message      string  `json:"message,omitempty"`
}
```

### Outcomes

| Outcome | Meaning |
|---|---|
| `allowed` | Passed every check; if execution was requested, the executor ran. |
| `approval_required` | Valid in the current state, but a human must approve before it executes. |
| `blocked` | Policy or state forbids it now: wrong state, or the actor lacks permission. |
| `invalid` | The proposal or contract is malformed: missing parameter, unknown action, or no executor. |

### Reason codes

`state_not_allowed`, `permission_denied` (→ `blocked`); `missing_parameter`,
`executor_missing`, `invalid_contract`, `unknown_action` (→ `invalid`);
`approval_required` (→ `approval_required`). An unclassified failure fails safe
to `blocked` with reason `error` — a caller never reads an unknown failure as
permission to run.

`outcome.Classify` maps the framework's existing `action.Err*` sentinels onto
this vocabulary, so there is a single source of truth rather than strings
re-invented per caller.

## Evaluating vs executing

Two entry points on the runtime:

- `Runtime.EvaluateAction(ctx, actionCtx, contract) outcome.Decision` — read-only.
  It answers "what would happen if I ran this?" It never runs the executor and
  never writes an audit record. Use it to hand an agent or an app API a typed
  outcome before deciding whether to execute.
- `Runtime.ExecuteAction(ctx, actionCtx, contract) (action.ActionResult, error)` —
  validates, and on success runs the executor and audits the result. On any
  non-allowed path it returns before the executor is reached.

## Guarantees

- **Execution boundary.** The executor runs only after validation passes.
  Blocked, invalid, approval-held, denied-approval, wrong-state, and
  missing-parameter proposals never reach it. (See
  `pkg/kiff/runtime/boundary_test.go`.)
- **Authority boundary.** A caller cannot self-assert the facts that grant
  authority: approvals cannot be self-granted (the approval bit is minted only
  inside the framework's trust boundary), permissions are resolved from the
  policy by actor ID rather than from caller-supplied `Actor.Roles`, and the
  current state is derived from stored events, not from the caller.
- **Replay as proof.** Rebuilding the entity from its events yields the same
  current state a decision was made against, so any decision can be explained
  after the fact.

This boundary is a local framework guarantee. It applies to consequential calls
that route through the KIFF runtime; KIFF does not claim to control a side
effect reached through a path that bypasses the runtime entirely.

## The self-approval boundary

The "no self-approval" guarantee has two layers, and the second is the one that
carries it.

**Compile-time** raises the cost. `ActionContext.approved` is unexported, so a
struct literal fails to compile, and `GrantApproval` takes a capability type
from an `internal` package an external module cannot import. The conformance
suite asserts both by running `go build` against external-module fixtures.

**These are compiler rules, and reflection runs after the compiler.** A caller
can recover the un-nameable capability type from the method's own signature
(`m.Type().In(0)`) and synthesise a value with `reflect.Zero`; `unsafe` can
write the field directly. Neither is exotic, and an adversarial audit used both
to execute an approval-required action against an empty approval store.

**Runtime is the real boundary.** The runtime never trusts an inbound approved
bit:

- `applyApproval` **clears the bit unconditionally** and re-derives it from the
  approval store, so a forged value is overwritten before anything reads it.
- The capability is checked — a zero `trust.Grant` is not a grant.
- A **non-overridable approval check** runs above the pluggable `Validator`,
  after it returns. A custom validator may add requirements; it cannot remove
  this one.

The three attacks ship as runtime fixtures in
`pkg/kiff/action/testdata/self_approval/`. They build and run from a separate
module against an empty approval store and assert refusal — failing without the
fix, passing with it, so CI breaks if the boundary regresses.

## The state boundary

An approval bit is not the only thing a caller could assert. `CurrentState` is
a plain string on `ActionContext`, and a proposer that could name its own state
could authorize a rollback, refund, or failover by claiming a favourable one.

Since v0.8 the state machine is authoritative. When a runtime has one wired,
`ValidateAction` reads the stored state and refuses a mismatch with
`ErrStateMismatch` rather than silently correcting it — the disagreement is the
signal. A caller's value stands only where the store has no state for that
entity yet, which is the bootstrap case.
