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

KIFF is not a chatbot framework, a generic web framework, or an LLM wrapper.

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
- Keep LLM and agent-framework integrations out of `pkg/kiff`. They belong in
  `examples/`, `cookbook/`, or the guard SDK — never in a core package.
- When adding examples, make the KIFF loop visible and easy to run.
- When uncertain, choose the smallest idiomatic Go design that preserves explicit governance.

## Where Things Live

KIFF is several repositories. This one is the MIT framework; the rules above
apply to it. Work that belongs elsewhere should go elsewhere rather than
leaking into `pkg/kiff`.

| Repository | Visibility | What it is |
|---|---|---|
| `kiff/kiff` (this repo) | public, MIT | The framework: the governed action boundary, stores, HTTP API, CLI |
| [`kiff/kiff-guard`](https://github.com/kiff/kiff-guard) | public, MIT | Guard SDKs (`kiff-guard` on PyPI, `@kiff/kiff-guard` on npm) and per-framework adapters |
| [`kiff/kiff-scan`](https://github.com/kiff/kiff-scan) | public | Static diagnostic for reachable consequential actions |
| `kiff/kiff-cloud` | private | The hosted control plane at kiff.dev: tenancy, auth, billing, dashboard |

## Current Goal

The framework core is at v0.7 and the action boundary is complete: approvals
cannot be self-granted, executors must be explicit, and every validation and
execution is recorded.

Rules that still hold for this repository:

- Cloud concerns — tenancy, authentication, billing, dashboards — stay in
  `kiff-cloud`. They must not appear in the framework's public surface.
- LLM and agent-framework code stays out of `pkg/kiff`.
- When a cloud RFC needs a framework change, the framework PR lands here
  first, then the cloud implementation follows.

Current work is finishing what a self-hoster needs to run this framework
honestly on its own: durable idempotency, a Postgres-backed state store, and
an explicit trust boundary on the `httpapi` surface.
