# KIFF

[![Go Reference](https://pkg.go.dev/badge/github.com/kiff/kiff.svg)](https://pkg.go.dev/github.com/kiff/kiff)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/kiff/kiff)](./go.mod)
[![Release](https://img.shields.io/github/v/release/kiff/kiff?include_prereleases&sort=semver)](https://github.com/kiff/kiff/releases)

**KIFF is a Go framework for putting agents on consequential actions — safely.**

Refunds, payouts, invoices, record changes: the work you've kept agents away
from, because one wrong move is expensive. KIFF sits between what an agent
*proposes* and what actually happens to your data. The agent proposes; KIFF
decides against real state and your rules; only allowed actions run.

KIFF helps you model, before you connect an agent:

- events, state, and the lifecycle an entity moves through
- actions as explicit contracts — not free-form tool calls
- permissions, and human approvals for the risky moves
- decisions, evidence, and an audit trail you can replay

KIFF is not a chatbot framework, a web framework, or an LLM wrapper. The
conversation layer stays yours — OpenAI, Anthropic, LangGraph, Agno, a cron job,
or a plain HTTP client. KIFF starts the moment a tool call becomes a side effect.

```text
agent proposes → KIFF reads state → action runs or waits for a human → result is audited & replayable
```

## See it

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
refused. And the whole path rebuilds from the event log. Watch the live
version any time with `go run ./cmd/kiff-tour`.

## Try it

```bash
go install github.com/kiff/kiff/cmd/kiff@latest
kiff new -scenario refund github.com/acme/refunds
cd refunds
go mod tidy
make demo
```

That scaffolds a complete, runnable project: an `Order` with `MARK_PAID` and an
approval-gated `REFUND_ORDER`, a headless HTTP API an agent can call, and a
`make demo` walkthrough of the flow above. `kiff new <module>` (without a
scenario) gives a minimal `tasks` starter instead.

Then `kiff verify` tells you the domain is ready to ship (no stub executors,
consistent state machine, complete contracts), and `kiff scaffold` generates a
`domain/` package from a JSON descriptor. Building against a local checkout? Add
`-replace-local /path/to/kiff`.

## What you get

- a typed action contract per risky action — allowed states, parameters, permissions, risk, approval
- a headless HTTP API for agent tools, plus the KIFF governance API
- persistence for the action and evidence trail: `file`, `postgres`, or `memory`
- deterministic duplicate handling — the repeat is refused, not double-executed
- replayable decision evidence — rebuild any entity from its events
- an authority boundary callers can't bypass — approvals can't be self-granted (enforced at compile time)

A caller doesn't need Go: a proposal is a single HTTP POST, so an agent or
backend in any language drives the same runtime.

## Where to go next

This README is the doorway. The depth lives in the docs.

**Understand**
- [Why KIFF](./docs/why.md) — why risky agent actions need a boundary outside the prompt
- [The governed action boundary](./docs/governed-action-boundary.md) — how decisions, approvals, and replay work
- [Philosophy](./docs/philosophy.md) · [Comparisons](./docs/comparisons.md) — what KIFF is, and where it stops

**Build**
- [Build a domain](./docs/build-a-domain.md) — the authoring guide, end to end
- [Conventions](./docs/conventions.md) — the normal way to lay out a project
- [Scaffold from a descriptor](./docs/scaffold-a-domain.md) — generate a domain from JSON
- [Govern over HTTP](./docs/governing-over-http.md) — drive KIFF from TypeScript, Python, or any stack
- [Connect an existing agent](https://github.com/kiff/kiff-guard) — `kiff-guard` adapters for Agno, LangGraph, OpenAI Agents, and more

**Reference**
- [Architecture & packages](./docs/architecture.md) — the package map and responsibilities
- [Principles in practice](./docs/principles/) — five short pages, one principle each
- [Vision](./docs/vision.md) · [Changelog](./docs/changelog/)

**Examples**
- [examples/refund](./examples/refund/) — one entity, three states, two actions (start here)
- [examples/mission](./examples/mission/) — a larger domain · [examples/llm-bridge](./examples/llm-bridge/) — the tool-call bridge pattern

## Who it is not for

If your app is simple CRUD, or direct LLM tool calls with no consequential
state, KIFF is too much structure — ship something smaller. KIFF earns its keep
when multiple actors touch the same state, what's allowed depends on lifecycle,
some actions need a human sign-off, and someone eventually asks "why did this
happen?"

## Status

KIFF is at v0.6. The core action boundary is complete and tested: approvals
can't be self-granted, executors must be explicit, and every validation and
execution is recorded. For production, implement the store interfaces against a
real backend — the [Postgres store](./pkg/kiff/store/postgres) is the reference;
the file-backed JSONL stores are for demos and local development.

## License

MIT. Use it. Fork it. Ship with it.
