# KIFF Framework

KIFF is a server-side Go framework for building governed agentic backends.

It helps developers model the operational protocol that should exist before AI agents, humans, automations, or integrations start changing shared state:

```text
Raw inputs -> Normalized events -> Shared state -> Decisions -> Validated actions -> Execution -> Audit
```

KIFF normalizes coordination mechanics, not business semantics. Your domain defines its own events, states, actions, permissions, risks, approvals, and evidence. KIFF provides small Go primitives for making those mechanics explicit, testable, and auditable.

## When To Use KIFF

Use KIFF when your backend has:

- multiple actors, including humans, services, integrations, or AI agents
- stateful entities whose allowed actions depend on current state
- approvals or human authority boundaries
- decisions that must be explained later
- risky actions that need validation before execution
- audit requirements across events, decisions, actions, approvals, and failures

KIFF is useful for domain implementations such as financial-provider coordination, mission or challenge systems, marketplace operations, post-purchase workflows, compliance workflows, and internal operational tools.

## When Not To Use KIFF

KIFF is not a chatbot framework.
KIFF is not a generic web framework.
KIFF is not an LLM wrapper.
KIFF is not a workflow engine replacement.
KIFF is not a universal business ontology.

If your application only needs simple CRUD, a web router, or direct LLM tool calls with no governed state, KIFF is probably too much structure.

## Core Packages

The initial framework scaffold lives under `pkg/kiff/`:

- `event`: normalized event records and event stores
- `state`: domain-owned state machines and transitions
- `decision`: explainable decision records
- `action`: action contracts, risk, approval, and validation
- `permission`: simple actor permission policies
- `audit`: append-only audit records
- `actor`: human, agent, service, and system actor identity
- `evidence`: references used to support decisions or actions
- `runtime`: a small coordinator that wires stores, policies, validation, and audit
- `store`: common store-level helpers

## Quickstart

Run the mission demo:

```bash
go run ./cmd/kiff-demo
```

Run the tests:

```bash
go test ./...
```

The demo shows the first KIFF loop locally:

```text
Event ingested -> State changed -> Decision recorded -> Action validated -> Execution audited
```

## Extending KIFF With A Domain

A domain implementation should define its own vocabulary outside the core packages:

- entity types
- event names
- state names
- transition rules
- action contracts
- required permissions
- evidence references
- approval rules

The core packages should stay domain-neutral. For example, the `examples/mission` package models a simplified challenge attempt system, but none of its mission-specific concepts are hardcoded into `pkg/kiff`.

The important boundary is simple:

```text
Domain semantics live in examples, apps, or product code.
Coordination mechanics live in pkg/kiff.
```
