# AGENTS.md - KIFF Framework

## Product Vision

KIFF is a Go framework for building governed agentic backends.

KIFF helps developers model:

- events
- state
- decisions
- actions
- permissions
- approvals
- evidence
- audit trails
- adapters

before connecting AI agents or automation.

KIFF is not a chatbot framework.
KIFF is not a generic web framework.
KIFF is not an LLM wrapper.

KIFF exists for systems where AI agents, humans, and software need to coordinate safely around shared operational state.

## Core Coordination Loop

```text
Raw inputs -> Normalized events -> Shared state -> Decisions -> Validated actions -> Execution -> Audit
```

The first framework milestone should demonstrate:

```text
Event ingested -> State changed -> Decision recorded -> Action validated -> Execution audited
```

## Design Principles

1. Normalize mechanics, not semantics.
2. Domains define their own business vocabulary.
3. KIFF provides reusable coordination primitives.
4. State comes before action.
5. Actions are explicit contracts, not free-form tool calls.
6. Agents may propose actions, but KIFF validates them before execution.
7. High-risk actions require human authority.
8. Every important event, state transition, decision, action validation, approval, execution result, and failure must be auditable.
9. Prefer boring, idiomatic Go over clever abstractions.
10. Keep the framework small and composable.
11. Do not add external dependencies without a strong reason.
12. Always include focused tests for new behavior.

## Architecture Rules

Core framework code belongs under:

```text
pkg/kiff/
```

Example domains belong under:

```text
examples/
```

Runnable demos belong under:

```text
cmd/
```

Documentation belongs under:

```text
docs/
```

Do not put domain-specific logic into the core packages.

The core packages should provide primitives and interfaces for coordination mechanics, including:

- event
- state
- action
- decision
- proposal
- permission
- approval
- audit
- actor
- evidence
- domain
- adapter
- httpapi
- runtime
- store

## Coding Rules

- Use `gofmt`.
- Keep public APIs clear and small.
- Add tests for every package with behavior.
- Avoid global mutable state.
- Prefer interfaces only where they create real substitution points.
- Keep errors explicit and useful.
- Favor typed contracts over stringly typed flows where practical.
- Keep domain examples readable enough to teach the framework.
- Run `go test ./...` before finishing code changes.

## Agent Alignment Rules

- Read `docs/vision.md` before making architectural changes.
- Treat audit as part of the protocol, not as optional logging.
- Do not let agent behavior bypass state, permissions, approvals, or validation.
- Keep AI integrations outside the core until the framework primitives are stable.
- When adding examples, make the KIFF loop visible and easy to run.
- When uncertain, choose the smallest idiomatic Go design that preserves explicit governance.

## Current Goal

Build the open-source MIT framework core first.

Do not build KIFF Cloud.
Do not build KIFF Studio.
Do not integrate LLMs yet.

Those are future layers. The immediate job is a clean local Go framework scaffold with tests and a runnable demo.
