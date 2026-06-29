# KIFF

[![Go Reference](https://pkg.go.dev/badge/github.com/kiff/kiff.svg)](https://pkg.go.dev/github.com/kiff/kiff)
[![Go Report Card](https://goreportcard.com/badge/github.com/kiff/kiff)](https://goreportcard.com/report/github.com/kiff/kiff)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/kiff/kiff)](./go.mod)
[![Release](https://img.shields.io/github/v/release/kiff/kiff?include_prereleases&sort=semver)](https://github.com/kiff/kiff/releases)

**Let your agents act on consequential work, and stay in control of what they can do. Written in Go.**

KIFF is a Go framework for backends where agents take real actions. Govern the action, not the actor. It makes two guarantees load-bearing:
decisions use an entity's event-derived current state, and external callers cannot
compile a path that grants their own runtime approval.

## State-aware decisions prevent duplicate effects

An invoice is `PENDING`. A payment action succeeds, emits `PAYMENT_CAPTURED`, and
the event moves the invoice to `PAID`. Then a flaky connection retries the exact
same request.

KIFF derives the entity's current state by applying its events. The retry is
therefore decided against `PAID`, not the stale `PENDING` snapshot that motivated
the first call. Because the payment action is only valid from `PENDING`, KIFF
refuses the retry before its executor runs.

```text
PAYMENT_REQUESTED → PENDING → payment executes → PAYMENT_CAPTURED → PAID
                                                               ↳ retry refused
```

That is the first guarantee: state comes before action. Stored events can rebuild
the same current state later, so the decision and its evidence remain explainable.

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
KIFF does not claim to govern a side effect reached through a path that bypasses
the runtime entirely.

## Deciding beats watching

The common answer to agent risk is observability: traces, spans, dashboards.
But visibility into a completed action is not control over it. By the time a
trace shows the duplicate payment, the money is out. By the time a dashboard
flags the over-refund, the ceiling is breached.

KIFF sits on the one boundary that matters — between the proposal and the
consequential action — and refuses what the state forbids.

```text
event → state → decision → action → approval → audit
```

Agents propose. The runtime validates state, permissions, parameters, and
approval rules. Right proposals execute with a trail. Wrong ones are refused
with a reason. Six primitives. That is the whole protocol.

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

1. An order is placed and paid. Smooth flow.
2. An agent tries to issue a $999 refund without approval. **KIFF refuses it.**
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

Common fits: post-purchase operations, marketplace coordination, compliance
workflows, internal operational tools, financial operations, mission-style systems
where the next move is not fully known but still has to be recorded and bounded.

If your application is simple CRUD or direct LLM tool calls with no governed
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
Python, Ruby — drives the same governed runtime without importing Go. The
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

- [Why KIFF](./docs/why.md): the long-form argument for why agents need a governance layer, not better prompts.
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

KIFF is at v0.6. The core coordination loop is complete and tested. The trust
boundary is enforced at the framework level: approvals cannot be self-granted,
executors must be explicit, every validation and execution is audited.

Production deployments should implement the store interfaces against a real
backend. The file-backed JSONL stores are for demos and local development; the
Postgres-backed implementation in `pkg/kiff/store/postgres` is the production
reference.

## License

MIT. Use it. Fork it. Ship with it.
