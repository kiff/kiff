# KIFF

[![Go Reference](https://pkg.go.dev/badge/github.com/kiff/kiff.svg)](https://pkg.go.dev/github.com/kiff/kiff)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/kiff/kiff)](./go.mod)
[![Release](https://img.shields.io/github/v/release/kiff/kiff?include_prereleases&sort=semver)](https://github.com/kiff/kiff/releases)

**KIFF is a Go framework for building governed agentic backends.**

Use KIFF when AI agents, humans, and software need to coordinate safely around
shared operational state. It helps developers model events, state, decisions,
action contracts, permissions, approvals, evidence, and audit trails before an
agent or automation changes data.

Domains define their own business vocabulary: events, states, actions,
permissions, approvals, evidence, and rules. KIFF provides the reusable
coordination mechanics around them: validation, execution records, replay, and
audit.

KIFF is not a chatbot framework, a generic web framework, or an LLM wrapper. Use
any agent, workflow engine, HTTP stack, queue, cron job, or deterministic
service. KIFF starts when something proposes an action against shared state.

```text
Event ingested -> State changed -> Decision recorded -> Action validated -> Execution audited
```

Agents may propose actions. KIFF validates them against current state,
permissions, parameters, and approval requirements before your executor runs.

## See It

One agent, one refund, read at your own pace:

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

Against a running KIFF cloud (endpoint via `-endpoint` or `KIFF_CLOUD_URL`),
use `kiff apply` to push a `kiff.yaml` domain contract, and the read-only
operator commands — `kiff domains list`/`show`, `kiff runtimes`, `kiff usage`,
`kiff keys list` — to inspect what a tenant is running.

## Documentation

- [Why KIFF](./docs/why.md) — why risky agent actions need a boundary outside the prompt
- [The governed action boundary](./docs/governed-action-boundary.md) — how decisions, approvals, and replay work
- [The side-effect boundary](./docs/side-effect-boundary.md) — deployment topology: agents propose, executors own credentials
- [Cookbook guide](./docs/cookbook-guide.md) — choose, evaluate, and adapt a governed agent recipe
- [Build a domain](./docs/build-a-domain.md) — the authoring guide, end to end
- [Scaffold from a descriptor](./docs/scaffold-a-domain.md) — generate a domain from JSON
- [Govern over HTTP](./docs/governing-over-http.md) — drive KIFF from TypeScript, Python, or any stack
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

KIFF is at v0.7. The core action boundary is complete and tested: approvals
cannot be self-granted, executors must be explicit, and every validation and
execution is recorded. The cookbook now includes launch-grade recipes across
finance, insurance, healthcare, infrastructure, security, and procurement. The
[Postgres store](./pkg/kiff/store/postgres) is the production reference; the
file-backed JSONL stores are for demos and local development.

## License

MIT. Use it. Fork it. Ship with it.
