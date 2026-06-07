# KIFF

## A coordination protocol for governed action

**Working notes, May 2026**
**Gabriel Sarmiento**
Released under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
Code referenced is part of the open-source MIT framework at
[github.com/kiffhq/kiff](https://github.com/kiffhq/kiff).

---

## Abstract

Operational software increasingly involves multiple actors — humans,
services, integrations, AI agents — writing to the same state. The default
shape of this software is brittle: each actor mutates state through its own
path, governance is enforced by convention, and the audit trail is whatever
log the engineer remembered to add.

KIFF is a small Go framework that puts a runtime between actors and shared
state. Actors propose actions. The runtime validates state, parameters,
permissions, and approvals before any executor runs. Every step — proposal,
validation, approval, execution, failure — is appended to an immutable
audit trail with trace correlation. State can be replayed from events.

This document describes the protocol, the trust boundary it enforces,
and the artifacts that prove the design is real: a working v0.2 framework,
two end-to-end demos with real LLM proposals, a Postgres backend, a shared
conformance test suite, and a CLI that inspects any KIFF server.

The argument is small. Operational governance belongs in the runtime, not
in the prompt or in convention. The runtime is small enough to read in a
weekend.

---

## Contents

1. [The problem](#1-the-problem)
2. [The coordination loop](#2-the-coordination-loop)
3. [The trust boundary](#3-the-trust-boundary)
4. [Mechanics, not semantics](#4-mechanics-not-semantics)
5. [Evidence: what we built](#5-evidence-what-we-built)
6. [Honest limits](#6-honest-limits)
7. [Where this goes](#7-where-this-goes)
8. [Appendix A: the action contract](#appendix-a-the-action-contract)
9. [Appendix B: what KIFF is not](#appendix-b-what-kiff-is-not)

---

## 1. The problem

Coordination failures around shared state look the same across very
different industries. The vocabulary changes; the operational shape does
not. Five examples, in five sectors, with the same underlying story:

**E-commerce.** A support agent (human or AI) issues a $999 refund on an
order whose payment never cleared. Nothing in the path between the agent
and the payment processor checked the order's state. The refund executes.
The customer is delighted, then confused, then files a second dispute.

**Insurance.** A claims-handling service auto-approves a $50,000 payout
because the claim text matched a low-friction template. The state of the
claim — under fraud review by a different team — was visible in another
system but not consulted. Funds move; the fraud team finds out the next
morning.

**Healthcare.** A clinical workflow tool ingests a lab result and writes
an order that conflicts with an active prescription. There is no shared
state both modules read from before writing. Two clinicians have to
reconcile what happened by reading two audit logs that don't share a
trace ID.

**Fintech.** An automated treasury-management service moves funds between
two corporate accounts to optimize float. The transfer exceeds the
threshold that should have triggered dual control, but dual control was
implemented in a spreadsheet by the operations team, not in the service.
The transfer settles; the CFO gets a flagged email three hours later.

**Internal DevOps.** An on-call engineer's AI assistant restarts a
production service to clear what looked like a memory leak. The deploy
freeze that was active for the next two hours was a calendar event, not a
runtime check. The restart triggers a cascade. Twelve minutes of
downtime.

These are not AI failures. The AI assistant in the last example is
incidental; the human engineer makes the same mistake. The common pattern
is that *the actor and the executor were the same component*, with no
runtime between them that could refuse the action.

Every team eventually builds something to address this: a state-machine
table, a webhook that emits to an audit log, a Slack approval bot, a
review queue, a "do not run during freeze" check. The components are
real and they work. But they are built once per company, in different
languages, with different ergonomics, and the cost of wiring them
together is large enough that most teams build the minimum viable version
and stop.

The result is what the old vendor-built systems looked like: governance
that lives in convention, not in code; audit trails that explain part of
what happened; replay capability that exists in theory because the events
were stored, but no software can actually reconstruct the entity.

KIFF is the same components, written once, in idiomatic Go, with the
contracts visible to every actor.

One scoping note belongs up front, because it shapes everything below.
KIFF is an **advisory contract gate, not a whole-system enforcer.** It
evaluates a proposed action against state, parameters, permissions, and
approvals, and returns a verdict; honoring that verdict is the caller's
responsibility. Enforcement is real where the caller routes through the
gate — for AI agents, the guard SDK sits in the agent's pre-tool hook and
withholds the tool call on any non-`allowed` verdict, so the gate governs
the agent's tool-call surface. KIFF does not, and cannot, prevent a
side effect reached by a code path that never asks. It governs what your
agents do through the gate; it does not make a bad action physically
impossible through a path you didn't route. That boundary is a design
choice, not a gap, and the rest of this document stays inside it.

---

## 2. The coordination loop

The framework has six primitives. They are not invented; they are what
operational systems already have, named consistently and connected by a
small runtime.

```
event → state → decision → action → approval → audit
```

**Events** are normalized records of what happened. They are append-only,
timestamped, attributed to a source. They carry domain-specific payloads
inside a stable structural envelope.

**State** is the maintained, shared, auditable condition of an entity. It
is updated by events through deterministic transitions. State is the
single source of truth all actors read from before deciding anything.

**Decisions** are auditable records of intent. An agent that wants to
issue a refund records a decision with its reasoning, its evidence, its
confidence. The decision exists whether or not the action ever runs. This
is what gives the system the answer to "why did you try this?" — not just
"what did you do?".

**Actions** are explicit contracts, not free-form tool calls. A contract
declares the action's name, the states in which it is allowed, the
parameters it requires, the permissions it requires, its risk level, and
whether it requires human approval. The full type is reproduced in
[Appendix A](#appendix-a-the-action-contract).

**Approvals** are first-class records, not booleans. An approval has an
identity, an entity, an action, a requester, a reviewer, a status, a
reason, and timestamps. The runtime, and only the runtime, can mark an
action context as approved — and only after looking up a granted
approval record from the store.

**Audit** is part of the protocol, not a logging concern. Every event
ingested, every state transition, every decision recorded, every action
validated, every approval requested or reviewed, every execution
result, and every failure produces an audit record. Records carry
`trace_id`, `correlation_id`, and `causation_id`. One filter call returns
the full chain that started from any inbound request.

The loop runs left to right. An event arrives, state advances, a
decision is recorded (by an agent or a human), an action is validated,
approval is requested if needed, the executor runs, the result is
audited. Any step can fail; the failure is also audited. Six months
later, the entity can be replayed from events alone and the materialized
state can be checked against the replayed state.

This is not a framework that asks you to learn a new programming model.
It is the model you would have built yourself, written once.

---

## 3. The trust boundary

The single technical claim KIFF stands on:

> Callers cannot self-approve.

A naive system would let any caller pass `approved: true` along with the
action they want to run. KIFF refuses to expose that field.

Inside the runtime, an `ActionContext` has an unexported `approved`
boolean. The Go compiler enforces that fields with lower-case names are
not visible outside the package, so no other package can construct an
`ActionContext` with `approved: true` directly. The bit is also the only
thing `DefaultValidator` accepts as proof of approval — a caller cannot
substitute anything else.

```go
// pkg/kiff/action/action.go (excerpt)
type ActionContext struct {
    ActionName   string
    EntityID     string
    EntityType   string
    CurrentState string
    Actor        actor.Actor
    Parameters   map[string]any
    ApprovalID   string
    approved     bool   // unexported. Only the runtime can set this.
}
```

The unexported field closes the front door (a struct literal). The side
door — a setter — is closed by a capability: `GrantApproval` requires a
value of a type that lives in an `internal/` package, so only code inside
the framework's own module can mint it. A caller that merely imports the
`action` package cannot name the type, let alone construct it. The
runtime mints the capability in a single method, `applyApproval`, which
runs only after looking up an approval record by ID, verifying the record
matches the entity and action, and confirming its status is `granted`. If
any check fails, the bit stays false. If the approval store returns an
error, the runtime propagates it; it does not silently treat a missing
approval as not-required.

This is testable. The conformance suite confirms that neither a struct
literal (`ActionContext{approved: true}`) nor a setter call from outside
the framework compiles, and that calling `ExecuteAction` with an invented
approval ID returns `action.ErrApprovalRequired` rather than running the
executor. The test exists because the boundary is the framework's most
important property.

### Host responsibilities

The self-approval boundary is enforced by the framework. **Authority is
not.** `DefaultValidator` checks an action's `RequiredPermissions`
against the roles on `ActionContext.Actor`, and that context is built by
the caller. The framework has no way to know whether those roles were
resolved from an authenticated identity or copied from untrusted request
input.

So the integrating host carries one load-bearing requirement:
`Actor.Roles` MUST come from a trusted, server-resolved source — an
authenticated session or identity lookup — and never from a request body
or any input the actor controls. A host that threads caller-supplied
roles into the actor lets a caller self-grant the permission that
authorizes its own action. This is an integration contract, not a
footnote: the framework guarantees deterministic gate ordering (state →
parameters → permissions → approval), an unforgeable `approved` bit, and
audit on every step; the host guarantees that the identity feeding those
gates is real.

The same shape applies to other guarantees: actions require explicit
executor functions (a missing executor returns `ErrExecutorMissing`
rather than a silent no-op), audit IDs combine an atomic counter with
random bytes (collision-resistant under concurrent writes), and the
runtime validates a domain definition when one is supplied (an invalid
state machine fails fast, not after the first event).

These are small properties. Each one is one decision. The framework's
value is that the decisions are made consistently across packages and
backed by tests, not that any one of them is novel.

---

## 4. Mechanics, not semantics

The most common protocol-design mistake is normalizing too much. The
instinct is reasonable: if KIFF defines a coordination layer, why not
also define what an order, a claim, or a clinical encounter is?

The answer is that business semantics do not portably abstract.

A refund and a fraud hold and a prescription order share none of their
business meaning. Forcing them into a common ontology produces a
specification that has to change every time a new domain is added — and
that fails the moment a domain has a concept the ontology did not
anticipate.

KIFF normalizes the *operational structure*, not the meaning. Every
domain that uses the framework defines its own:

- entity types (`Order`, `Claim`, `Encounter`, `Account`, `Service`)
- event types (`ORDER_PAID`, `CLAIM_FILED`, `LAB_RESULT_RECEIVED`)
- states (`PAID`, `UNDER_REVIEW`, `RESOLVED`)
- action contracts (`REFUND_ORDER`, `APPROVE_CLAIM`, `RESTART_SERVICE`)
- permissions (dotted lowercase identifiers like `orders.refund`)

KIFF only defines how those are *structured*: events have a stable
envelope (id, type, entity, source, actor, timestamp, metadata,
payload), states are values updated by deterministic transitions,
actions are contracts with the seven fields above, approvals are upsert
records, audit is append-only with trace correlation.

The analogy is TCP/IP. The protocol does not know whether the bytes are
a transaction confirmation or a video frame. It defines the structure
inside which those things travel. KIFF is the same kind of layer for
operational coordination.

This boundary is what makes the framework's coordination story work
across the five sectors above without becoming a 200-page specification.
The mechanics generalize. The vocabulary does not.

---

## 5. Evidence: what we built

The framework is at v0.2. The artifacts in the repository are the
evidence the design is real, not aspirational.

### 5.1 The framework

About 6,000 lines of Go under `pkg/kiff/`. Seventeen packages, each with
one job, each with tests. The entire core protocol — events, state,
decisions, actions, approvals, audit, runtime, store interfaces, HTTP
API, observability wrapper, test helpers — runs against `go 1.23` with
one external dependency (`pgx/v5` for the Postgres backend, optional).

Anything implementing the four `Store` interfaces (`event.Store`,
`decision.Store`, `approval.Store`, `audit.Store`) is a valid backend.
Three implementations exist: in-memory (default), file-backed JSONL
(local persistence), Postgres (production). All three pass the same
shared conformance suite (`pkg/kiff/store/storetest`), which has 21
cases covering ordering, filtering, payload round-trips, upsert
semantics, validation rejection, and context cancellation.

### 5.2 The refund demo

Located at `examples/refund-agno/`. Two runs of the same Agno-shaped
agent against the same prompts, same model, same fixture.

**Run A — without KIFF.** The agent's `refund_order` tool mutates a mock
database directly. A $999 refund on an unpaid order succeeds because
nothing checks the order's state.

**Run B — through KIFF.** The agent's tool POSTs to a small HTTP server
that wraps `pkg/kiff/runtime`. Small refunds (≤ $100) hit `AUTO_REFUND`
and execute immediately. Refunds above the ceiling hit `REFUND_ORDER`,
which has `ApprovalRequirement: ApprovalRequired`. The runtime returns
`approval_required` to the agent. A human grants the approval. The
same call from the agent now executes. The audit timeline shows the
proposal, the validation gate, the approval cycle, the execution, and
the rebuild check — `materialized = REFUNDED`, `replayed = REFUNDED`,
`events = 3 ✓`.

The fixture is deterministic (`agent.OfflineProvider`) so the demo runs
without an LLM API key. The same code runs against AWS Bedrock when
credentials are set. The point is that the *governance behavior* is
identical regardless of where the proposal came from.

The demo is a single `make demo` command. Output reproduced in Appendix
C of the demo's own README; the canonical 90-second screencast on the
landing page is a recording of this command.

### 5.3 The breadth demo

Located at `examples/support-ops/`. One agent with five tools running
on a five-ticket batch produces five distinct outcomes:

| Ticket | Tool | Outcome | Reason |
|---|---|---|---|
| 1 | `issue_refund` | executed | small amount, no approval needed |
| 2 | `issue_refund` | approval_required → granted → executed | over the cap, granted on review |
| 3 | `send_outreach` | blocked_consent_missing | structural rejection before any approval is opened |
| 4 | `escalate_to_human` | executed | escalation never needs approval |
| 5 | `close_ticket` | executed | only legal in `RESOLVED` |

Ticket 3 is the most interesting case. The `SEND_OUTREACH` action has a
custom validator that rejects the action when `consent_verified` is
missing or false — *before* an approval is ever opened. Approvals are
for authority decisions; eligibility checks happen earlier. The
breadth demo demonstrates that a heterogeneous tool surface can flow
through the same runtime cleanly, including domain-specific validators.

### 5.4 The conformance suite

`pkg/kiff/store/storetest/` defines the contract every persistence
backend must satisfy. Adding a new backend is a known-cost exercise:
implement the four `Store` interfaces, write a test factory that creates
a clean instance of each, register the factories with the suite. The
suite confirms the new backend behaves identically to the in-memory and
file-backed reference implementations.

This matters because backends accumulate. A small contract that holds
across implementations is the difference between a real boundary and a
"plug your own here" hand-wave.

### 5.5 The Postgres backend

`pkg/kiff/store/postgres/`. About 600 lines plus `schema.sql`. Four
tables (`kiff_events`, `kiff_decisions`, `kiff_approvals`, `kiff_audit`)
with `JSONB` payloads, indexes only on the columns the suite filters
on. Connection pooling via `pgx/v5/pgxpool`. Conformance tests gated by
`KIFF_POSTGRES_TEST_URL` so the default `go test ./...` does not need a
running database.

Verified against `postgres:16-alpine`. Every conformance subtest passes.
Schema is idempotent (`CREATE TABLE IF NOT EXISTS`). Production
migrations are the operator's tool of choice (`golang-migrate`, `goose`,
Atlas); KIFF does not bundle one.

### 5.6 The operator surface

Two pieces, intentionally minimal:

**`/admin` and `/admin/entities/{id}`**, served by `pkg/kiff/httpapi`.
Read-only HTML rendered with `html/template`. Lists pending approvals,
shows entity timelines, color-codes denials and failures. Production
deployments put auth in front of this; KIFF does not pretend to be a
multi-tenant UI.

**`kiff timeline -base <url> -entity <id>`**, the inspection CLI. Hits
the `/entities/{id}/timeline` and `/demo/rebuild` endpoints on any
running KIFF server. Renders a compact terminal table with the audit
trail and the rebuild check. Twenty seconds from typing the command to
having the answer to "why is this entity in this state?".

### 5.7 What this evidence does and does not prove

It proves the protocol can be implemented small (six primitives, seventeen
packages, one external dependency). It proves the trust boundary is
testable (the unexported field plus the conformance suite). It proves
the persistence interface is real (three backends pass the same suite).
It proves the agent integration story works in two demos with real LLM
proposals.

It does not yet prove the protocol generalizes to all five sectors in
§1. Four of those sectors — insurance, healthcare, fintech, and internal
DevOps — are hypothetical for KIFF today. The evidence in §5 is concrete
for e-commerce shapes; the rest is reasoned from the structural argument
in §4. The cross-sector examples in §1 are structural analogies, not
proof of sector readiness or compliance with sector-specific
regulations. The next twelve months will close this gap or surface where
the protocol breaks down. We expect both.

---

## 6. Honest limits

The framework v0.2 does not handle the following. These are design
boundaries, not missing features — each one has a composition story
with a tool that already solves it.

**Durable execution.** KIFF is not a workflow engine. If your domain
needs steps that survive crashes, replay on retry, or coordinate
across multiple processes over hours or days, KIFF should sit
*underneath* a workflow engine (Temporal, Inngest, Restate), not
replace it. The workflow handles durability; KIFF handles whether each
step is allowed.

**Multi-tenant identity.** KIFF has actors and permissions but no
notion of organizations, projects, or scopes. Production deployments
add their own auth layer in front of the HTTP API. The framework does
not impose a tenant model because the model varies too much across
adopters.

**Distributed state.** State today is centralized. The runtime assumes
a single source of truth per entity. Distributed state (replicated,
eventually consistent, or sharded across regions) is not part of v0.2.
Most adopters do not need it; the ones who do should compose KIFF with
a state backend that handles the replication.

**Event ordering across producers.** KIFF preserves insertion order
within a single store. Cross-producer ordering (clock skew across
sources, exactly-once delivery, idempotency keys) is the producer's
responsibility. The framework refuses to pretend it has solved
distributed-systems problems it has not solved.

**A complete observability story.** `pkg/kiff/observability` wraps the
audit store with `slog` and a counter registry. There are no traces, no
spans, no metrics histograms. Production deployments instrument their
own observability layer; KIFF gives them the audit records to
instrument from.

**A managed runtime.** KIFF Cloud is mentioned in the project's vision
as a future commercial layer. It does not exist yet. Adopting KIFF
today means embedding it as a library and running it yourself.

These are not bugs. They are explicit non-goals for v0.2. Each has a
real design reason; each has a composition story with the tools that
already solve it. The protocol's value depends on staying small.

---

## 7. Where this goes

The next six months, ordered by likelihood:

**Get to twenty-five real adopters.** The framework is in good shape.
The bottleneck now is not code; it is people writing domains on top of
it. The launch sequence — public push, hosted demo, screencast, Show HN
— is the next major effort.

**A second persistence backend.** SQLite is the most likely candidate,
because it covers single-binary deployments and edge use cases that
Postgres does not. DynamoDB for AWS-native deployments lands behind
SQLite if the adoption signal supports it.

**One more sector demo.** A second worked example outside e-commerce.
The two strongest candidates are a simple fintech-ops domain (a small
treasury rule that gates large transfers behind dual control) and a
clinical-workflow domain (a tiny EHR ingest path with active-state
checks). The choice depends on which adopter pulls hardest in the
launch period.

**A managed runtime.** "KIFF Cloud" — hosted runtime, multi-tenant
admin UI, audit retention, compliance exports — is the eventual
commercial product. It is not a v0.2 deliverable. It exists in this
document only as a footnote so the reader knows it is planned.

**Specification work.** The current type signatures are the spec. A
proper protocol document, language-independent, that other
implementations can read against, is on the roadmap once the v0.2 API
is stable. The Markdown for that document already exists in fragmented
form across `docs/architecture.md`, `docs/conventions.md`, and this
whitepaper.

What is *not* on the roadmap: an LLM SDK in the core, a workflow
engine, a managed agent platform, a model gateway, an app builder. KIFF
is one thing. The next six months are about doing that thing publicly.

---

## Appendix A: the action contract

Reproduced verbatim from `pkg/kiff/action/action.go`:

```go
// ActionContract describes when and how an action is allowed to run.
type ActionContract struct {
    Name                string
    AllowedStates       []string
    RequiredParameters  []string
    RequiredPermissions []permission.Permission
    Risk                RiskLevel
    ApprovalRequirement ApprovalRequirement
    Executor            func(context.Context, ActionContext) (ActionResult, error)
}
```

Field by field:

- **`Name`** — the operational identifier, stable across versions.
  Shows up in audit records, HTTP routes, and proposal payloads.

- **`AllowedStates`** — the only states from which this action is
  meaningful. The runtime returns `action.ErrStateNotAllowed` if the
  current state is not in this list. State is checked before
  parameters, permissions, or approvals; an action that does not
  belong in the current state never reaches the rest of the gate.

- **`RequiredParameters`** — the parameters that must be present and
  non-nil. Missing parameters fail validation with
  `action.ErrMissingParameter`. The executor never has to write
  defensive nil-checks.

- **`RequiredPermissions`** — permissions the actor must hold. The
  runtime queries the configured `permission.Policy` interface. A
  missing permission returns `action.ErrPermissionDenied`.

- **`Risk`** — operational metadata. Drives reporting, dashboards, and
  downstream tooling. Does not affect the runtime path.

- **`ApprovalRequirement`** — `ApprovalNever` or `ApprovalRequired`.
  When set to `ApprovalRequired`, execution returns
  `action.ErrApprovalRequired` until a granted approval record is
  resolved by the runtime.

- **`Executor`** — the side-effect function. Takes a validated
  `ActionContext`, returns an `ActionResult`. The executor never
  re-validates state, parameters, permissions, or approval; the
  runtime did. Its only job is to do the thing and describe the
  outcome, including any follow-up events that drive the next state
  transition.

The contract is the operational document for the action. A new engineer
joining the team can read down a contract and answer every governance
question for that action in under thirty seconds.

---

## Appendix B: what KIFF is not

Five categories KIFF deliberately does not occupy:

**Not an agent framework.** No prompt builder, no model SDK, no tool
registry, no harness. Agents are clients of KIFF; they are not what
KIFF is. Use LangGraph, Agno, OpenAI Agents SDK, or your own harness.

**Not a workflow engine.** No durable execution, no retries, no
timers, no saga support. KIFF composes underneath workflow engines
that own those concerns.

**Not a managed agent platform.** No deploy story for the agent's code,
no session durability, no harness orchestration. Use Modal, Lambda,
Dari, your own infrastructure.

**Not a model gateway.** No routing between providers, no request
shaping, no caching. Talk to your model provider directly.

**Not an app builder.** No UI scaffolding, no admin SDK beyond the
read-only `/admin` page. KIFF's HTTP API is JSON; build whatever
frontend you want.

The reason this list matters: every operational team eventually
encounters pressure to add one of these capabilities to the governance
layer. The discipline is to refuse, because the moment KIFF tries to
replace any of these tools it stops being small enough to read in a
weekend, and the small-enough-to-read property is what makes it
trustworthy.

KIFF composes. It is not a platform.

---

*This document is part of the KIFF Framework, MIT-licensed, at
[github.com/kiffhq/kiff](https://github.com/kiffhq/kiff).
The whitepaper itself is released under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).*

*Comments, corrections, and counterexamples welcome at
hello@kiff.dev or as issues on the repository.*
