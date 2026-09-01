# KIFF

[![CI](https://github.com/kiff/kiff/actions/workflows/ci.yml/badge.svg)](https://github.com/kiff/kiff/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/kiff/kiff.svg)](https://pkg.go.dev/github.com/kiff/kiff)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/kiff/kiff)](./go.mod)
[![Release](https://img.shields.io/github/v/release/kiff/kiff?include_prereleases&sort=semver)](https://github.com/kiff/kiff/releases)

**KIFF gives agents, humans, and services one operational truth.**

KIFF is a Go framework for building operational systems where many actors work
on the same business process. Model events, state, actions, authority,
execution, and audit once; then let agents, people, and services use the same
domain instead of each rebuilding it.

## The Pattern Without KIFF

The first agent is usually a feature. The next few need a system.

In a conventional implementation, each new agent, application, or automation
builds its own view of state, rules, and integrations. That works in isolation,
then creates coordination work: decisions drift, integrations multiply, and it
becomes harder to explain what happened.

![Without a shared operational domain, each actor builds and maintains its own state, rules, and integrations.](./docs/diagrams/traditional-operational-pattern.png)

## The KIFF Pattern

KIFF makes the business process explicit once. Events establish what is true;
named actions describe what may happen; authority and approvals apply the same
way to every actor; execution results return to the shared history.

This does not replace your agent framework, HTTP stack, queue, cron job, or
systems of record. It gives them a shared operational foundation and evaluates
proposed actions against current state before your executor runs.

![KIFF provides one domain for events, state, actions, authority, and execution.](./docs/diagrams/kiff-shared-domain.png)

| Without a shared domain | With KIFF | Practical effect |
| --- | --- | --- |
| Each actor carries local state and rules. | Events and derived state are shared. | Less state and policy drift. |
| Every new actor builds another integration path. | Actors propose the same named actions. | Add capabilities without rebuilding coordination. |
| Decisions live in application code, prompts, and tool handlers. | One authority boundary evaluates every proposal. | Consistent approvals and refusals. |
| Logs are scattered across services. | Events, decisions, and results form one history. | Replay and explain an operational outcome. |

## A Concrete Example

One domain, one refund:

```text
order-2 is PAID

  agent → REFUND_ORDER  $999, high-risk
  KIFF  ⏸ HELD — approval required            the executor did NOT run
  human → approves
  KIFF  ✓ ALLOWED — refund executes → REFUNDED

  agent → REFUND_ORDER  (same call, retried)
  KIFF  ✗ BLOCKED — order is already REFUNDED  refused before money moves again

  replay from events alone → REFUNDED          materialized == replayed
```

The useful action runs. The risky one waits for a human. The duplicate is
refused. The path rebuilds from the event log.

Run the live tour:

```bash
go run ./cmd/kiff-tour
```

## Try It

```bash
go install github.com/kiff/kiff/cmd/kiff@latest
kiff new -scenario refund github.com/acme/refunds
cd refunds
go mod tidy
make demo
```

That creates a runnable refund domain with an `Order`, a `MARK_PAID` action, an
approval-gated `REFUND_ORDER` action, a headless HTTP API, and a demo script.
Use `kiff new <module>` without `-scenario` for a smaller starter project.

## Connect An Agent

If you already have an agent and want KIFF deciding in front of its tool
calls, you do not need to restructure it. The guard SDK sits on the
pre-execution seam your framework already exposes:

```bash
pip install kiff-guard          # or: npm install @kiff/kiff-guard
```

```python
from kiff_guard import Guard
from kiff_guard.adapters.agno import agno_hook

guard = Guard(mode="observe")   # observe records; enforce refuses before the call runs
agent = Agent(model=..., tools=[refund_order], tool_hooks=[agno_hook(guard)])
```

Adapters ship for Agno, LangGraph/LangChain, OpenAI Agents SDK, Google ADK,
Pydantic AI, Strands, Microsoft Agent Framework, Hermes, Haystack, and
LlamaIndex. Custom stacks use the core over plain HTTP with no adapter. Source
and per-framework docs: [kiff/kiff-guard](https://github.com/kiff/kiff-guard)
(MIT).

## Run It Yourself Or Hosted

The framework is MIT. Self-host the shared domain with the `postgres` store and
your own HTTP stack for as long as you like.

If you would rather not operate the state, approvals, receipts, and retention
yourself, [KIFF Cloud](https://kiff.dev) runs them as a hosted control plane
with a free tier. The CLI talks to it directly — `kiff auth login`, then
`kiff apply` to push a domain contract, and `kiff domains`, `kiff runtimes`,
`kiff usage`, `kiff keys` to inspect a tenant. Nothing in this repository
requires it.

## What You Get

- domain definitions for events, states, transitions, and action contracts
- validation against state, typed parameters, permissions, risk, and approvals
- dynamic approval policies for actions whose risk depends on runtime facts
- reviewer authority and segregation-of-duties checks for human approvals
- idempotency protection for consequential executor retries
- lifecycle views that assemble proposals, approvals, execution, and outcomes
- approval records and audit records as protocol data, not optional logs
- state replay from stored events
- `memory`, `file`, and `postgres` stores
- an optional `net/http` API for external agents, services, and tools
- CLI commands to scaffold and verify domains, and to apply and inspect them against a running KIFF cloud

Use `kiff verify` to check a domain before shipping. Use `kiff scaffold` to
generate a `domain/` package from a JSON descriptor. Building against a local
checkout? Add `-replace-local /path/to/kiff`.

Use `kiff scan .` inside a Go agent repository to find explicit agent tools
that can reach consequential operations without a recognized decision earlier
in the function. Mark a handler with `//kiff:tool`, pass it to a common tool
registration call, or identify it with `-tool FunctionName`. The initial Go
scanner emits terminal, JSON, and SARIF reports and can enforce a CI threshold:

```bash
kiff scan .
kiff scan -format sarif -output kiff-scan.sarif -fail-on high .
```

This is static analysis: a clean result means no supported path was found, not
that the application is safe or that every framework-specific registration
shape is understood.

For Python agent repositories, use the separate
[kiff-scan](https://github.com/kiff/kiff-scan) tool instead:

```bash
uvx kiff-scan scan .
```

It also ships a GitHub Action: `uses: kiff/kiff-scan@v0.1.1`.

### Agent Assistants

The `kiff-governance` skill lets Codex, Claude Code, and Kiro run the scanner,
explain findings, and help fix them. See
[Coding assistant integrations](./docs/assistant-integrations.md) for setup and
example prompts.

Against a running KIFF cloud (endpoint via `-endpoint` or `KIFF_CLOUD_URL`),
sign in with `kiff auth login`, then use `kiff apply` to push a `kiff.yaml`
domain contract, and the read-only operator commands — `kiff domains list`/`show`,
`kiff runtimes`, `kiff usage`, `kiff keys list` — to inspect what a tenant is
running. `kiff auth status` shows the current session; `kiff auth logout` revokes it.

## Documentation

- [Why KIFF](./docs/why.md) — why the second agent needs a shared operational system
- [The governed action boundary](./docs/governed-action-boundary.md) — how decisions, approvals, and replay work
- [The side-effect boundary](./docs/side-effect-boundary.md) — deployment topology: agents propose, executors own credentials
- [Cookbook guide](./docs/cookbook-guide.md) — choose, evaluate, and adapt a governed agent recipe
- [Build a domain](./docs/build-a-domain.md) — the authoring guide, end to end
- [Scaffold from a descriptor](./docs/scaffold-a-domain.md) — generate a domain from JSON
- [Govern over HTTP](./docs/governing-over-http.md) — drive KIFF from TypeScript, Python, or any stack
- [Coding assistant integrations](./docs/assistant-integrations.md) — install and use the Codex, Claude Code, and Kiro skill
- [Architecture & packages](./docs/architecture.md) — the package map and responsibilities
- [Philosophy](./docs/philosophy.md) and [Comparisons](./docs/comparisons.md) — what KIFF is, and where it stops

## Examples

- [examples/refund](./examples/refund/) — one entity, three states, two actions
- [examples/mission](./examples/mission/) — a larger stateful coordination domain
- [examples/llm-bridge](./examples/llm-bridge/) — the tool-call bridge pattern

## Cookbook

Use the cookbook when you want to see what KIFF lets a team launch, not just
what it can audit after the fact. These recipes model agents proposing useful
work while KIFF owns the consequential action boundary.

- [accounts-payable-payout](./cookbook/accounts-payable-payout/) — a
  Claude Haiku AP agent with a money-moving payout boundary, finance approval,
  and lifecycle view
- [security-incident-response](./cookbook/security-incident-response/) —
  containment decisions, session reset, and access revocation through an
  identity-service boundary
- [procurement-purchase-order](./cookbook/procurement-purchase-order/) —
  purchase-order creation through an ERP service with manager approval
- [insurance-claims-triage](./cookbook/insurance-claims-triage/) — claim
  evidence, coverage/risk scoring, and payout execution
- [healthcare-prior-auth](./cookbook/healthcare-prior-auth/) — clinical
  documentation, payer criteria, and portal submission
- [cloud-infra-remediation](./cookbook/cloud-infra-remediation/) —
  infrastructure remediation with approval-gated isolation
- [vendor-bank-change](./cookbook/vendor-bank-change/) — vendor payment-detail
  changes with finance-controlled execution
- [cookbook index](./cookbook/) — recipe standards, feature map, and later candidates

## Who It Is Not For

If your app is simple CRUD, or direct LLM tool calls with no consequential
state, KIFF is too much structure — ship something smaller. KIFF earns its keep
when multiple actors touch the same state, what's allowed depends on lifecycle,
some actions need a human sign-off, and someone eventually asks "why did this
happen?"

## Status

KIFF is at v0.8. The release adds the cloud-facing CLI loop, native Go source
scanning, and a portable governance skill for coding assistants. The core
action boundary remains complete and tested: approvals cannot be self-granted,
executors must be explicit, and every validation and execution is recorded.
The [Postgres store](./pkg/kiff/store/postgres) is the production reference;
the file-backed JSONL stores are for demos and local development.

## License

MIT. Use it. Fork it. Ship with it.
