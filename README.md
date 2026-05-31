# KIFF

[![Go Reference](https://pkg.go.dev/badge/github.com/kiffhq/kiff.svg)](https://pkg.go.dev/github.com/kiffhq/kiff)
[![Go Report Card](https://goreportcard.com/badge/github.com/kiffhq/kiff)](https://goreportcard.com/report/github.com/kiffhq/kiff)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/kiffhq/kiff)](./go.mod)
[![Release](https://img.shields.io/github/v/release/kiffhq/kiff?include_prereleases&sort=semver)](https://github.com/kiffhq/kiff/releases)

**Trust infrastructure for AI-operated systems. Written in Go.**

Most AI applications start at the prompt. They wrap a model in a chat UI, expose a few tools, and trust the model's judgment to call them correctly. That works for demos. It collapses in production because the prompt cannot enforce a state machine, cannot check permissions, cannot require a human signature, and cannot reconstruct what happened later.

KIFF starts one layer earlier. It puts events, state, decisions, approvals, and audit *between* the agent and the system the agent is trying to act on. Agents propose. The runtime validates. Humans approve when it matters. Every fact is replayable.

This README walks through what that means, who it is for, and how to get a working domain in front of you in a few minutes. For the long-form argument, read [docs/why.md](./docs/why.md). For the framework's design choices, read [docs/philosophy.md](./docs/philosophy.md).

## The problem KIFF solves

If you have shipped an AI feature into a real operational system, you have probably had this conversation. The product team wants the agent to do something concrete: issue a refund, send a contract, change a permission, transfer a balance, escalate a ticket. The engineering team builds it. It demos cleanly. Then it ships, and one of three things happens.

The agent does the action it was told to do, and four other actions nobody asked for, and now you are explaining to a customer why their account was touched.

The agent gets confused, refuses to act, and the human in the loop has no way to take over without bypassing the system entirely.

The agent does the right thing nine times out of ten, and the tenth time costs you a chargeback, a regulatory letter, or a customer.

This is an architecture problem, not a model problem. The prompt is the wrong layer to enforce operational rules. The prompt does not know your state machine. The prompt cannot enforce permissions. The prompt cannot require a human signature. The prompt cannot reconstruct what happened.

The result is software that *describes* governance in instructions and *enforces* nothing.

## The layer below the prompt

Every operational system, AI or not, runs on the same coordination protocol:

```text
something happened → state changed → a decision was made
                  → an action was proposed → it was validated
                  → it was executed → the trail explains it
```

Banks have it. Marketplaces have it. Hospitals have it. The protocol is not new. What is new is that AI agents now want to act inside it, often without being invited.

The right place to govern an agent is not in the prompt. It is in this protocol. The agent's job ends at *"I propose to refund $999."* The system's job is to validate that proposal against the current state, the actor's permissions, the action's parameters, and the approval requirements before anything moves. If the agent is right, execution proceeds. If the agent is wrong, the system says no, in a way that is auditable, replayable, and explainable.

KIFF is an implementation of that protocol in Go. It works at the layer underneath, where governance is enforced by code rather than described by words.

## What you get when you adopt this

Once the protocol exists, four things become possible:

**Agents can propose freely.** Because the runtime is the gate, an agent can be wrong without being dangerous. Wrong proposals get rejected with a reason. Right proposals get executed with a trail. The conversation about agent reliability stops being existential.

**Humans, agents, services, and integrations share one loop.** All of them submit proposals to the same validator. All of them are governed by the same rules. The question of "what happens if a human and an agent both try to refund this order" has a deterministic answer instead of a discussion.

**Any incident is replayable.** Every event, decision, validation, approval, execution, and failure is in the audit trail. Six months from now, when someone asks why a refund was issued, the entity can be rebuilt from events alone and the chain reconstructed. Trust becomes a function you can run rather than a story you tell.

**Governance can speed you up.** This is the counterintuitive part. People assume governance slows you down. In practice, the time you save not debugging mysterious state, not unwinding bad agent actions, and not rewriting "just enough" governance for the third time, dwarfs the time you spend declaring an action contract.

## Who KIFF is for

You are building a backend where:

- multiple actors (humans, services, integrations, AI agents) touch the same state;
- entities have a lifecycle, and what is allowed depends on where they are in it;
- some actions are risky enough that a human should sign off;
- somebody, eventually, will ask "why did this happen?" and need a real answer;
- you would rather declare governance once than relitigate it in every PR.

Common fits: post-purchase operations, marketplace coordination, compliance workflows, internal operational tools, financial operations, mission-style systems where the next move is not fully known but still has to be recorded and bounded.

## Where KIFF stops

A few areas sit outside KIFF on purpose. The conversation layer is yours; KIFF starts the moment a human, agent, or service wants to *do something* to your state. There is no model SDK, prompt builder, or embeddings store, so you can run KIFF with no AI at all and it still earns its keep. The framework is agent-ready, not agent-coupled.

Long-running tasks, retries, and scheduled jobs belong to a workflow engine. If you need Temporal, use Temporal, and let KIFF live next to it. HTTP is optional as well: the `httpapi` package is there because most teams want it, but the runtime is a coordinator you can drive from a queue consumer, a CLI, a cron job, or a custom RPC.

If your application is simple CRUD, a router with handlers, or direct LLM tool calls with no governed state, KIFF is too much structure. Use something smaller and ship.

For an honest comparison to LangChain, Temporal, raw FSMs, and rolling your own, see [docs/comparisons.md](./docs/comparisons.md).

## See it run

The fastest way to understand KIFF is to watch it stop a real action and then let one through.

```bash
git clone https://github.com/kiffhq/kiff
cd kiff
go run ./cmd/kiff-tour
```

The tour narrates a tiny refund domain end to end:

1. An order is placed and paid. Smooth flow.
2. An agent confidently tries to issue a $999 refund. **KIFF blocks it.**
3. A human grants approval. The same action now executes.
4. Replay rebuilds the entity's state from events alone. The audit timeline reconstructs every fact.

About three minutes of terminal output. The whole framework story.

If you would rather see the original mission domain or the HTTP version:

```bash
go run ./cmd/kiff-demo                          # mission demo, log-style output
go run ./cmd/kiff-http-demo -data-dir ./data    # HTTP API with persistence
```

The HTTP demo is documented in [docs/changelog/brick-14.md](./docs/changelog/brick-14.md) with curl examples.

## Start your own project

```bash
go install github.com/kiffhq/kiff/cmd/kiff@latest
kiff new github.com/acme/orders
cd orders
go mod tidy
go run ./cmd/server
```

`kiff new` scaffolds a runnable HTTP server and a tiny `tasks` starter domain. Rename the entity, events, states, and actions to match yours and you are running. See [docs/conventions.md](./docs/conventions.md) for the normal way to lay things out and [docs/build-a-domain.md](./docs/build-a-domain.md) for the authoring walkthrough.

While the framework is unpublished, scaffold against a local checkout:

```bash
kiff new -replace-local /path/to/kiff github.com/acme/orders
```

### Try the agentic-ops template

When you want to evaluate KIFF *as governance for an AI agent*, scaffold the `agentic-ops` template instead. It includes a Go domain, an HTTP server, an Agno agent (offline + Bedrock providers), and a `make demo` target that runs the full governed-agent loop end to end:

```bash
kiff new -template=agentic-ops github.com/acme/ops
cd ops && go mod tidy && make demo
```

`make demo` spawns the server, runs the agent against deterministic tickets, prints the audit timeline (block, approve, execute, replay), and shuts down. Under five minutes from a clean directory.

The same shape ships as a worked example in three flavors:

- [`examples/refund-agno`](./examples/refund-agno/): depth. One tool, two runs (without KIFF and through KIFF), Agno agent, real LLM.
- [`examples/support-ops`](./examples/support-ops/): breadth. One agent, five tools, five distinct outcomes including consent-blocked validation.
- [`examples/ai-cafe-ops`](./examples/ai-cafe-ops/): operational authority. AI shift manager, four tools, both local-mode and cloud-mode (talks to a hosted KIFF Cloud tenant over HTTP).

## What a domain looks like

Your domain owns vocabulary. KIFF owns coordination. A complete domain definition is small. Here is the gist of [examples/refund](./examples/refund/):

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

That is the shape. Action contracts declare allowed states, required parameters, required permissions, risk level, approval requirement, and the executor function. The runtime handles the rest.

For a complete walkthrough, read [docs/build-a-domain.md](./docs/build-a-domain.md). The shortest worked example is [examples/refund/](./examples/refund/) (one entity, three states, two actions). For a more involved domain, see [examples/mission/](./examples/mission/).

## Documentation

Start here:

- [Why KIFF](./docs/why.md): the long-form argument for why agents need a governance layer, not better prompts.
- [Philosophy](./docs/philosophy.md): what KIFF chooses to be, and what it chooses not to be.
- [Comparisons](./docs/comparisons.md): honest positioning next to LangChain, Temporal, FSMs, and rolling your own.

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

For the LLM-bridge pattern, see [`examples/llm-bridge/`](./examples/llm-bridge/). For the layered concept of how Studio and Cloud sit on top of the framework, see [docs/vision.md §"Future layers"](./docs/vision.md).

## Status

KIFF is at v0.1. The core coordination loop is complete and tested. The trust boundary is enforced at the framework level: approvals cannot be self-granted, executors must be explicit, every validation and execution is audited.

Production deployments should implement the store interfaces against a real backend. The file-backed JSONL stores are for demos and local development; the Postgres-backed implementation in `pkg/kiff/store/postgres` is the production reference.

## License

MIT. Use it. Fork it. Ship with it.
