# Conventions

KIFF is opinionated. There is a normal way to lay out a KIFF project, name things, and split responsibilities. Following the conventions makes your code readable to anyone else who has read the same five pages.

The default starter (`kiff new`) is the reference shape. This page makes the choices explicit so you can deviate intentionally rather than by accident.

## The line that explains everything

```text
Domain semantics live in your code.
Coordination mechanics live in pkg/kiff.
```

If you find yourself writing event/state/audit logic in your domain package, stop. The framework probably already handles it. If you find yourself adding domain vocabulary inside `pkg/kiff`, stop. That belongs in your code.

## Project layout

A KIFF application has the following shape:

```
your-app/
├── cmd/server/main.go        # entry point: parse flags, build runtime, serve HTTP
├── domain/                   # your domain vocabulary and contracts
│   └── domain.go             # state machine, action contracts, permission policy
├── go.mod
└── README.md
```

This is what `kiff new` produces. It is the starting layout, not a constraint. As your domain grows you can split `domain/` into multiple files (`events.go`, `actions.go`, `policy.go`) or even multiple subpackages. Keep `cmd/server` thin and keep domain vocabulary out of `pkg/kiff` and you will stay on the path.

For larger applications, two extensions are common:

```
your-app/
├── cmd/
│   ├── server/main.go        # HTTP entry point
│   └── worker/main.go        # background consumer (queue, cron, etc.)
├── domain/
│   ├── events.go             # event types and metadata helpers
│   ├── states.go             # state machine and transitions
│   ├── actions.go            # action contracts and executors
│   ├── policy.go             # permission policy
│   └── domain.go             # NewDefinition wires it all together
├── adapters/                 # one file per inbound integration
│   ├── webhook.go
│   └── queue.go
├── go.mod
└── README.md
```

Resist creating a `domain/internal/` or `domain/services/` until you actually need it. The framework was written so domains stay small.

## Naming

KIFF leans on UPPER_SNAKE_CASE for runtime identifiers and dotted lowercase for permissions, because the two read very differently in code review:

| What | Convention | Example |
| --- | --- | --- |
| Event types | UPPER_SNAKE_CASE | `ORDER_PLACED`, `REFUND_REQUESTED` |
| State values | UPPER_SNAKE_CASE | `CREATED`, `PAID`, `REFUNDED` |
| Action names | UPPER_SNAKE_CASE | `MARK_PAID`, `REFUND_ORDER` |
| Entity types | PascalCase | `Order`, `MissionAttempt` |
| Permissions | dotted.lowercase | `orders.refund`, `tasks.approve` |
| Adapter names | lowercase, single word | `tasks`, `stripe`, `inbound-webhooks` |
| Go constants | mirror the value | `const EventOrderPlaced = "ORDER_PLACED"` |

Names are part of the audit trail. Choose them as if a human will read them in an incident review six months from now, because they will.

## Constants block

Put every domain identifier in one constants block at the top of your domain file. This is the single most useful affordance for code review, refactor, and onboarding.

```go
const (
    AdapterTasks = "tasks"

    EntityTask = "Task"

    EventTaskCreated   = "TASK_CREATED"
    EventTaskStarted   = "TASK_STARTED"
    EventTaskCompleted = "TASK_COMPLETED"

    StateOpen       = "OPEN"
    StateInProgress = "IN_PROGRESS"
    StateDone       = "DONE"

    ActionStartTask    = "START_TASK"
    ActionCompleteTask = "COMPLETE_TASK"

    PermStartTask    permission.Permission = "tasks.start"
    PermCompleteTask permission.Permission = "tasks.complete"
)
```

Reading the block tells a new developer the whole shape of the domain in twenty lines. Do not scatter these across files.

## Action contracts

Action contracts are the most important objects in your domain. Treat each one as a small contract that future you will want to read at 3am during an incident.

The convention is:

- One factory function per action contract: `func MarkPaidContract() action.ActionContract`.
- Order fields the same way every time: `Name`, `AllowedStates`, `RequiredParameters`, `RequiredPermissions`, `Risk`, `ApprovalRequirement`, `Executor`.
- Set `ApprovalRequirement: action.ApprovalRequired` for any action that affects money, identity, security, infrastructure, or external systems with side effects. When in doubt, require approval. Removing the requirement later is cheap; explaining a missing one is expensive.
- Keep executors short. If an executor needs more than ~50 lines, extract the work behind an interface and inject it via the runtime config or your domain package, not via globals.
- Always emit follow-up events from successful executions. State changes are event-driven; do not mutate state from an executor.

## Permission policy

One factory function per domain: `func NewPermissionPolicy() *permission.SimplePolicy`. Grant permissions by role, not by actor ID, unless you have a strong reason. Roles are auditable; identity sprawl is not.

Add the system role for anything an automated process needs to do. Add a human role for anything that requires authority. Do not give one role both `Action` and `Approve` permissions for the same action — that defeats the approval boundary.

## Runtime wiring

Always wire the runtime in one place, and make `NewRuntime()` (in-memory) and `NewRuntimeWithStores(stores *store.Bundle)` (injectable) the only constructors your application exposes:

```go
func NewRuntime() (*runtime.Runtime, error) {
    return NewRuntimeWithStores(nil)
}

func NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
    def, err := NewDefinition()
    if err != nil { return nil, err }
    in, err := NewInputAdapter()
    if err != nil { return nil, err }
    return runtime.NewForDomain(def, runtime.Config{
        PermissionPolicy: NewPermissionPolicy(),
        Adapters:         []adapter.Adapter{in},
        Stores:           stores,
    })
}
```

Every demo, every test, and every production binary uses one of these two. No surprises about which stores are wired or which adapters are registered.

## Tests

KIFF is deliberately small enough that domain tests stay short. The convention is one test file per domain package with three kinds of cases:

1. **The happy path.** End-to-end through the loop with a granted approval. State should land where you expect.
2. **The blocked path.** Verify a high-risk action is refused without approval and the entity state did not change.
3. **The replay path.** `runtime.RebuildState` should reconstruct the same final state from the event log alone.

These three cases exercise the trust boundary, the audit trail, and the state machine together. If they pass, your domain is structurally sound.

See [`examples/refund/refund_test.go`](../examples/refund/refund_test.go) for a worked reference.

## What to put in `cmd/server/main.go`

`main.go` is the wiring layer. Convention is:

- Parse flags.
- Build the runtime via the domain's `NewRuntime` / `NewRuntimeWithStores`.
- Wrap it in `httpapi.NewHandler` (or whatever transport you use).
- Serve.

If you find yourself writing business logic in `main.go`, move it into the domain package. `main.go` should not import `pkg/kiff/action` or `pkg/kiff/event` directly — your domain re-exports what callers need.

## Persistence

Default to in-memory stores during development. Use `pkg/kiff/store/file` for local persistence between runs. Implement the store interfaces against a real backend for production. The interfaces never change shape; only the implementation does.

Do not couple the runtime to a specific backend. `NewRuntimeWithStores(stores *store.Bundle)` is the seam.

## Adapters

One adapter per inbound source. A webhook is an adapter. A queue consumer is an adapter. A CLI command can be an adapter. Adapters do not own transport. They normalize raw input into KIFF events and hand control to the runtime.

If your application has more than two adapters, give them their own package (`adapters/`) and keep the domain package free of transport concerns.

## What KIFF will not do for you

KIFF does not provide:

- Authentication or authorization at the edge. Add your own middleware around the HTTP handler.
- A web UI. The HTTP API is JSON over `net/http`. Build whatever frontend you want.
- Database migrations or ORM. You implement the store interfaces.
- A queue, scheduler, or workflow engine. Adapters compose with anything you already use.
- Magic. There is no reflection, no code generation, no hidden state. Read the package you are using and that is what runs.

This is intentional. The framework stays small so your domain stays clear.

## Where examples live: `examples/` vs `cookbook/`

The repo keeps two deliberate categories of worked code:

- **`examples/`** — focused, single-concept demos of one framework capability
  (e.g. `examples/refund/` for the core loop, `examples/llm-bridge/` for the
  tool-call bridge). Read these to learn a primitive.
- **`cookbook/`** — end-to-end, production-shaped recipes for a real
  consequential workflow (e.g. `cookbook/accounts-payable-payout/`). A recipe
  wires a domain, an app controller, an agent adapter, tests, and a runnable
  demo together, and follows the recipe standard in
  [`cookbook/README.md`](../cookbook/README.md). Read these to see the whole
  shape of a governed agentic workflow.

Both stay runnable and hermetic (offline by default; any real LLM/provider is
opt-in, with no credentials in CI).

## When to break the conventions

Break them when you have a real reason. Then write a short comment explaining what you broke and why. The conventions exist so that, when you do deviate, the deviation is visible and intentional.

The default-driven path is the fast path. Use it until you have a reason not to.
