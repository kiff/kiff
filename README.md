# KIFF

**Trust infrastructure for AI-operated systems. Written in Go.**

Most AI backends start at the prompt. They work in demos and collapse in production, because nothing controls what the agent actually does to your state.

KIFF starts earlier. It puts events, state, decisions, approvals, and audit *between* the agent and your system. Agents propose. KIFF validates. Humans approve when it matters. Everything is replayable.

> Make AI useful in real operations without losing control.

For the long version of why this layer matters, read [docs/why.md](./docs/why.md).

## The 60-second pitch

Without KIFF, an AI feature in production usually looks like this:

```text
agent → tool call → mutates your database → you hope
```

With KIFF:

```text
agent → proposal → KIFF validates state, permissions, parameters
                 → high-risk? requires human approval
                 → executor runs only if everything checks out
                 → every step audited and replayable
```

You keep your domain. KIFF carries the governance.

## What you get

- **A coordination protocol that works.** The loop `event → state → decision → action → approval → audit` is the framework. Six packages. Fits in your head.
- **Governance that cannot be bypassed.** Approval state is private to the runtime. Callers cannot self-approve. Executors are explicit. Every fact is audited with a trace ID.
- **An optional HTTP API.** Built on `net/http`. No web framework lock-in. Includes a read-only `/admin` page over the runtime.
- **File-backed stores.** Persistence for demos and local development with one flag. Production swaps in real backends behind the same interfaces.
- **Default-on observability.** Wrap the audit store with `observability.WrapAuditStore` and every fact becomes a structured log line and a counter. No dependencies.
- **Test helpers in the box.** `kifftest` gives you event builders, a fixed clock, and policy seeds so domain tests stay short.
- **A runnable demo that proves both paths.** Granted approval completes the loop. Denied approval blocks execution. You can see KIFF stop an action as clearly as it executes one.

## Try it in 60 seconds

```bash
git clone https://github.com/kiffhq/kiff
cd kiff-framework
go run ./cmd/kiff-tour
```

You will see a narrated walk-through of the KIFF loop on a tiny refund domain:

1. An order is placed and paid. Smooth flow.
2. An agent confidently tries to issue a $999 refund. **KIFF blocks it.**
3. A human grants approval. The same action now executes.
4. Replay rebuilds the entity's state from events alone, and the audit timeline reconstructs every fact.

Three minutes. The whole framework story.

If you would rather see the original mission domain or the HTTP version:

```bash
go run ./cmd/kiff-demo                          # mission demo, log-style output
go run ./cmd/kiff-http-demo -data-dir ./data    # HTTP API with persistence
```

Then try the curl examples in [docs/changelog/brick-14.md](./docs/changelog/brick-14.md).

## Start your own project

```bash
go install github.com/kiffhq/kiff/cmd/kiff@latest
kiff new github.com/acme/orders
cd orders
go mod tidy
go run ./cmd/server
```

`kiff new` scaffolds a runnable HTTP server and a tiny `tasks` starter domain. Rename the entity, events, states, and actions to match yours and you are running. See [docs/conventions.md](./docs/conventions.md) for the normal way to lay things out.

While the framework is unpublished, scaffold against a local checkout:

```bash
kiff new -replace-local /path/to/kiff-framework github.com/acme/orders
```

### Try the agentic-ops template

When you want to evaluate KIFF *as governance for an AI agent*, scaffold the `agentic-ops` template instead. It includes a Go domain, an HTTP server, an Agno agent (offline + Bedrock providers), and a `make demo` target that runs the full governed-agent loop end to end:

```bash
kiff new -template=agentic-ops github.com/acme/ops
cd ops && go mod tidy && make demo
```

`make demo` spawns the server, runs the agent against deterministic tickets, prints the audit timeline (block, approve, execute, replay), and shuts down. Under five minutes from a clean directory.

The same shape is also available as a worked example in [`examples/refund-agno`](./examples/refund-agno/) (depth: one tool, two runs) and [`examples/support-ops`](./examples/support-ops/) (breadth: one agent, five distinct outcomes).

## Who KIFF is for

You are building a backend where:

- multiple actors — humans, services, integrations, AI agents — touch the same state;
- entities have a lifecycle, and what is allowed depends on where they are in it;
- some actions are risky enough that a human should sign off;
- somebody, eventually, will ask "why did this happen?" and need a real answer;
- you would rather declare governance once than relitigate it in every PR.

Common fits: financial-provider coordination, marketplace operations, post-purchase workflows, compliance workflows, internal operational tools, mission or challenge systems.

## Who KIFF is not for

KIFF is not a chatbot framework. Not a generic web framework. Not an LLM wrapper. Not a workflow engine. Not a universal business ontology.

If your application is simple CRUD, a router with handlers, or direct LLM tool calls with no governed state, KIFF is too much structure. Use something smaller and ship.

For an honest comparison to LangChain, Temporal, raw FSMs, and rolling your own, see [docs/comparisons.md](./docs/comparisons.md).

## How a domain looks

Your domain owns vocabulary. KIFF owns coordination. A complete domain definition is small — here's the gist of [examples/refund](./examples/refund/):

```go
def, _ := domain.New("refund").
    Entity("Order").
    Event("ORDER_PLACED").
    Event("ORDER_PAID").
    Event("ORDER_REFUNDED").
    Transition("ORDER_PLACED", "", "CREATED").
    Transition("ORDER_PAID", "CREATED", "PAID").
    Transition("ORDER_REFUNDED", "PAID", "REFUNDED").
    Allow("CREATED", "MARK_PAID").
    Allow("PAID", "REFUND_ORDER").
    Action(MarkPaidContract()).      // low-risk, no approval
    Action(RefundOrderContract()).   // high-risk, approval required
    Build()

rt, _ := runtime.NewForDomain(def, runtime.Config{
    PermissionPolicy: refund.NewPermissionPolicy(),
})
```

That is the entire shape. The action contracts declare allowed states, required parameters, required permissions, risk, approval requirement, and the executor function. The runtime handles the rest.

Walk through a complete domain in [docs/build-a-domain.md](./docs/build-a-domain.md). The shortest worked example is [examples/refund/](./examples/refund/) (one entity, three states, two actions). For a more involved domain see [examples/mission/](./examples/mission/).

## Documentation

Start here:

- [Why KIFF](./docs/why.md) — the argument: why agents need a governance layer, not better prompts
- [Philosophy](./docs/philosophy.md) — what KIFF is choosing to be, and what it is choosing not to be
- [Comparisons](./docs/comparisons.md) — honest positioning next to LangChain, Temporal, FSMs, and rolling your own

Build with it:

- [Conventions](./docs/conventions.md) — the normal way to lay out a KIFF project
- [Build a domain](./docs/build-a-domain.md) — the authoring guide, end to end
- [Principles in practice](./docs/principles/) — five short pages, one principle each, with code

Reference:

- [Architecture](./docs/architecture.md) — package boundaries and responsibilities
- [Vision](./docs/vision.md) — the long-form rationale
- [Changelog](./docs/changelog/) — how the framework evolved, brick by brick

## Core packages

`pkg/kiff/` is intentionally small. Each package has one job.

| Package | Job |
| --- | --- |
| `event` | Normalized event records and stores |
| `state` | Domain-owned state machines, transitions, replay |
| `decision` | Explainable decision records |
| `proposal` | Action proposals from agents, humans, or services |
| `action` | Action contracts, validation, execution |
| `permission` | Actor permission policies |
| `approval` | Approval records for high-risk actions |
| `audit` | Append-only audit trail with trace correlation |
| `actor` | Human, agent, service, system actor identity |
| `evidence` | References supporting decisions or actions |
| `domain` | Domain definitions bundling state and actions |
| `adapter` | Raw input normalization into events |
| `httpapi` | Optional `net/http` surface around the runtime, including a read-only `/admin` view |
| `runtime` | The coordinator wiring everything together |
| `store` | Common store helpers and file-backed implementations |
| `store/postgres` | Production-grade Postgres backend (also covers Supabase, Neon, RDS) |
| `store/storetest` | Shared conformance test suite every store implementation must pass |
| `observability` | Default-on structured logging and counters via an audit-store wrapper |
| `kifftest` | Test helpers: event builders, fixed clock, predefined actors, policy seeds |

For an example of bridging an LLM tool-call surface into governed KIFF actions, see [`examples/llm-bridge/`](./examples/llm-bridge/).

## Status

KIFF is at v0.1. The core coordination loop is complete and tested. The trust boundary is enforced at the framework level: approvals cannot be self-granted, executors must be explicit, every validation and execution is audited.

Production deployments should implement the store interfaces against a real backend. The file-backed JSONL stores are for demos and local development.

## License

MIT. Use it. Fork it. Ship with it.
