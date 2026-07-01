# KIFF

[![Go Reference](https://pkg.go.dev/badge/github.com/kiff/kiff.svg)](https://pkg.go.dev/github.com/kiff/kiff)
[![Go Report Card](https://goreportcard.com/badge/github.com/kiff/kiff)](https://goreportcard.com/report/github.com/kiff/kiff)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/kiff/kiff)](./go.mod)
[![Release](https://img.shields.io/github/v/release/kiff/kiff?include_prereleases&sort=semver)](https://github.com/kiff/kiff/releases)

**A Go framework for making risky agent actions shippable.**

KIFF is for backends where agents need to do real work: issue refunds, mark
invoices paid, trigger payouts, change records. The agent proposes the action;
KIFF checks the entity's current state and your domain contract before your
executor runs.

If the action is allowed, it executes. If the entity is in the wrong state, the
actor lacks permission, or a human approval is missing, KIFF returns a typed
reason and leaves production untouched. The point is not to watch agents after
the fact. It is to make the action safe enough to ship.

## The boundary that lets the action run

Start with the useful path. An order is `CREATED`. An ops agent proposes
`MARK_PAID` with a payment id. KIFF validates the action against the current
state, required parameters, and permissions, then runs the executor. The executor
emits `ORDER_PAID`, and the order becomes `PAID`.

```text
ORDER_PLACED -> CREATED -> MARK_PAID executes -> ORDER_PAID -> PAID
```

That is the line KIFF is trying to make shippable: the agent did the work. The
same boundary handles the dangerous cases. If a flaky connection retries a
payment after the entity is already `PAID`, or an agent asks for a high-risk
refund without approval, KIFF decides against the new current state and returns a
reason before the executor runs.

Two guarantees are load-bearing: decisions use the entity's event-derived
current state, and external callers cannot compile a path that grants their own
runtime approval.

## Compile-time self-approval boundary

External Go code using KIFF's public API cannot grant itself runtime approval,
and cannot compile a path that does. `ActionContext.approved` is unexported, while
`GrantApproval` requires a capability from an `internal` package that external
modules cannot import.

The conformance suite proves exactly those two boundaries by running `go build`
against external-module fixtures. It asserts that both
`action.ActionContext{approved: true}` and
`ctx.GrantApproval(trust.Grant{})` fail to compile for the expected
access-control reason.

This guarantee applies when consequential calls route through the KIFF runtime.
KIFF does not claim to control a side effect reached through a path that bypasses
the runtime entirely.

## Below the model, before the side effect

KIFF is not a chatbot framework, prompt builder, model SDK, or workflow engine.
The conversation layer stays yours. The model can use OpenAI tool calls,
Anthropic tool use, LangChain, Agno, a cron job, or a plain HTTP client.

KIFF starts at the moment a tool call is about to become a side effect:

```text
event -> state -> decision -> action -> result
```

Agents propose. The runtime validates state, parameters, permissions, and
approval rules. Allowed proposals execute through explicit executor functions.
Everything else returns a stable reason: `approval_required`,
`permission_denied`, `state_not_allowed`, `missing_parameter`, or `blocked`.

## See it decide a real action

No installation needed: watch the committed 24-second terminal recording.

![KIFF terminal tour showing a blocked action, approval, execution, and replay](./docs/demo/kiff-tour.svg)

The recording follows the runnable tour. A reproducible terminal-recording recipe
is committed at [`docs/demo/kiff-tour.tape`](./docs/demo/kiff-tour.tape).

To run the same tour yourself:

```bash
git clone https://github.com/kiff/kiff
cd kiff
go run ./cmd/kiff-tour
```

Three minutes of terminal output:

1. An order is placed and the agent marks it paid. The action executes.
2. The same agent tries a $999 refund without approval. KIFF holds it.
3. A human grants approval. The same call now executes.
4. Replay rebuilds the entity from events alone. Every fact reconstructs.

```bash
go run ./cmd/kiff-demo                          # mission demo, log-style output
go run ./cmd/kiff-http-demo -data-dir ./data    # HTTP API with persistence
```

The HTTP demo is documented in [docs/changelog/brick-14.md](./docs/changelog/brick-14.md) with curl examples.

## Who it is for

You are building a backend where:

- multiple actors — humans, services, integrations, AI agents — touch the same state;
- what is allowed depends on where an entity is in its lifecycle;
- some actions are risky enough that a human should sign off;
- someone, eventually, asks "why did this happen?" and needs a real answer.

Common fits: post-purchase operations, marketplace coordination, internal
operational tools, financial operations, payouts, refunds, recovery, fulfillment,
and mission-style systems where the next move is not fully known but still has
to stay inside exact limits.

If your application is simple CRUD or direct LLM tool calls with no consequential
state, KIFF is too much structure. Use something smaller and ship.

## Where it stops

KIFF starts the moment someone wants to *do something* to your state. The
conversation layer is yours — no model SDK, no prompt builder — so KIFF runs
with no AI at all and still earns its keep. Long-running tasks, retries, and
scheduled jobs belong to a workflow engine beside KIFF. HTTP is optional: the
`httpapi` package is there because most teams want it, but the runtime drives
equally from a queue consumer, a CLI, or a cron job.

For an honest comparison with adjacent architectural categories, see
[docs/comparisons.md](./docs/comparisons.md).

## Start your own project

```bash
go install github.com/kiff/kiff/cmd/kiff@latest
kiff new github.com/acme/orders
cd orders
go mod tidy
go run ./cmd/server
```

`kiff new` scaffolds a runnable HTTP server and a tiny `tasks` starter domain.
Rename the entity, events, states, and actions to match yours and you are running.
See [docs/conventions.md](./docs/conventions.md) for the normal way to lay things
out and [docs/build-a-domain.md](./docs/build-a-domain.md) for the authoring
walkthrough.

`kiff scaffold` goes the other way: give it a JSON domain descriptor and it
generates a framework-faithful `domain/` package — state machine, action
contracts with TODO executor stubs, and passing tests — optionally inside a
full project shell. See [docs/scaffold-a-domain.md](./docs/scaffold-a-domain.md).

`kiff verify` is the design-time check that a domain is done: it flags any
action still bound to a scaffold stub, an inconsistent state machine, or an
incomplete contract, and exits non-zero (with `-json` for CI). A scaffolded
domain fails `kiff verify` until you implement the executors.

While the framework is unpublished, scaffold against a local checkout:

```bash
kiff new -replace-local /path/to/kiff github.com/acme/orders
```

An external caller does not need to import Go. A proposal is a single
HTTP POST, so an agent, webhook, or backend in any language — TypeScript,
Python, Ruby — drives the same KIFF runtime without importing Go. The
domain is defined in Go and runs as a service; the application that calls it
stays in its own stack. See [docs/governing-over-http.md](./docs/governing-over-http.md)
for copy-paste TypeScript and Python.

## What a domain looks like

Your domain owns vocabulary. KIFF owns coordination. A complete domain definition
is small. Here is the gist of [examples/refund](./examples/refund/):

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

Action contracts declare allowed states, required parameters, required permissions,
risk level, approval requirement, and the executor function. The runtime handles
the rest.

For a complete walkthrough, read [docs/build-a-domain.md](./docs/build-a-domain.md).
The shortest worked example is [examples/refund/](./examples/refund/) (one entity,
three states, two actions). For a more involved domain, see
[examples/mission/](./examples/mission/).

## Documentation

Start here:

- [Why KIFF](./docs/why.md): the long-form argument for why risky agent actions need a boundary outside the prompt.
- [Philosophy](./docs/philosophy.md): what KIFF chooses to be, and what it chooses not to be.
- [Comparisons](./docs/comparisons.md): honest positioning beside adjacent architectural categories.

Build with it:

- [Conventions](./docs/conventions.md): the normal way to lay out a KIFF project.
- [Build a domain](./docs/build-a-domain.md): the authoring guide, end to end.
- [Principles in practice](./docs/principles/): five short pages, one principle each, with code.

Reference:

- [Architecture](./docs/architecture.md): package boundaries and responsibilities.
- [Vision](./docs/vision.md): long-form rationale.
- [Changelog](./docs/changelog/): how the framework evolved, brick by brick.

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

For the tool-call bridge pattern, see [`examples/llm-bridge/`](./examples/llm-bridge/).

## Status

KIFF is at v0.6. The core action boundary is complete and tested. The trust
boundary is enforced at the framework level: approvals cannot be self-granted,
executors must be explicit, and every validation and execution is recorded.

Production deployments should implement the store interfaces against a real
backend. The file-backed JSONL stores are for demos and local development; the
Postgres-backed implementation in `pkg/kiff/store/postgres` is the production
reference.

## License

MIT. Use it. Fork it. Ship with it.
