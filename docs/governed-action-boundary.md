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

## Compile-time self-approval boundary

The authority boundary is not only a runtime check — the "no self-approval"
guarantee is enforced by the Go type system. External code using KIFF's public
API cannot grant itself runtime approval, and cannot compile a path that does:

- `ActionContext.approved` is unexported, so external modules cannot set it via
  a struct literal (`action.ActionContext{approved: true}` fails to compile).
- `GrantApproval` requires a capability value from an `internal` package that
  external modules cannot import (`ctx.GrantApproval(trust.Grant{})` fails to
  compile). The approval bit is minted only inside the framework's trust
  boundary, after the runtime verifies a granted approval exists in the store.

The conformance suite proves exactly these two boundaries by running `go build`
against external-module fixtures and asserting both fail to compile for the
expected access-control reason.
