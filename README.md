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
- `proposal`: action proposals from agents, humans, or software
- `action`: action contracts, risk, approval, and validation
- `permission`: simple actor permission policies
- `approval`: approval records and approval stores for high-risk actions
- `audit`: append-only audit records
- `actor`: human, agent, service, and system actor identity
- `evidence`: references used to support decisions or actions
- `domain`: domain definitions that bundle state machines and action catalogs
- `adapter`: raw input normalization before events enter KIFF
- `httpapi`: optional standard-library HTTP handlers around runtime methods
- `runtime`: a small coordinator that wires stores, policies, validation, and audit
- `store`: common store-level helpers

## Quickstart

Run the mission demo:

```bash
go run ./cmd/kiff-demo
```

Run the HTTP mission demo:

```bash
go run ./cmd/kiff-http-demo
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

## Brick 2: Approvals And Action Catalogs

Brick 2 adds two small governance ergonomics:

- approval records for actions that require human authority
- action catalogs for registering and looking up domain action contracts

An approval record is not just a boolean. It captures which action was reviewed, which entity it affects, who requested it, who reviewed it, whether it was granted or denied, and when that happened.

An action catalog belongs to a domain. It keeps action contracts discoverable without putting domain vocabulary into the KIFF core.

## Brick 3: Domain Definitions

Brick 3 adds a small `domain` package and runtime allowed-action lookup.

A domain definition names a domain and bundles:

- known entity types
- known event types
- the domain state machine
- the domain action catalog

The runtime can use a domain definition to answer which action contracts are currently allowed for an entity based on its state.

## Brick 4: Audit Reconstruction

Brick 4 makes the audit trail queryable enough to reconstruct what happened.

Audit records can be queried by entity, kind, and actor. The runtime exposes a timeline method so a domain can explain an entity's operational path in chronological order.

## Brick 5: Store Boundaries

Brick 5 groups the core stores into a small store bundle.

The bundle gives applications one clear place to inject event, decision, approval, and audit stores. The default demo still uses in-memory stores, but future persistence adapters can implement the same package-level store interfaces and plug into the runtime without changing domain code.

## Brick 6: Input Adapters

Brick 6 adds the first adapter boundary.

Adapters normalize raw inputs into KIFF events. They do not own transport. A webhook handler, queue consumer, CLI command, or agent runtime can receive input however it wants, then hand a raw input to an adapter before KIFF ingests the normalized event.

## Brick 7: HTTP API

Brick 7 adds an optional `net/http` surface over the runtime.

The HTTP API is intentionally thin. It exposes raw input ingestion, allowed action lookup, and audit timelines without introducing a web framework, authentication layer, database, or UI.

Initial routes:

```text
POST /events/raw
GET  /entities/{entityID}/allowed-actions
GET  /entities/{entityID}/timeline
```

## Brick 8: Proposal Boundary

Brick 8 adds a proposal boundary for agentic systems without integrating an LLM SDK.

An actor may propose an action with parameters, evidence, reasoning, and confidence. KIFF records that proposal as a decision and can convert it into an action context for validation. Recording a proposal never executes the action.

## Brick 9: Execution Results

Brick 9 makes action execution outcomes explicit.

Execution results include status, message, error, effects summary, output, and timestamp. Runtime audit records include those result details so successful and failed executions can be reconstructed later.

## Brick 10: Follow-Up Events

Brick 10 lets successful execution results include follow-up events.

Actions still do not mutate state directly. When an execution result includes follow-up events, the runtime ingests those events through the normal event path so state transitions and audit records remain explicit.

## Brick 11: Approval Requests

Brick 11 makes pending approval requests explicit.

Runtime can create a pending approval record for an action contract that requires approval. Granting or denying that approval remains a separate authority step.

## Brick 12: HTTP Action Routes

Brick 12 adds guarded HTTP routes for action validation and execution.

The HTTP API resolves action contracts from the runtime action catalog and current state from the state machine. Clients provide actor, parameters, and approval id, but not the contract itself.

## Brick 13: HTTP Approval Routes

Brick 13 exposes approval requests, approval listing, and grant/deny review through the optional HTTP API.

Approval routes still use runtime validation and audit. They make approval available over transport without adding authentication, assignment workflows, notifications, or a UI.

## Brick 14: HTTP Demo Command

Brick 14 adds `cmd/kiff-http-demo`, a runnable local HTTP server for the mission example.

The command hosts the optional `httpapi` handler with the mission runtime and documents curl examples for the full governed loop.

## Brick 15: Event Replay And State Rebuild

Brick 15 realigns with the original roadmap's Event Replay / State Rebuild item.

Runtime can rebuild an entity's current state by replaying stored events through the configured domain state machine. The rebuild returns replay steps and records a `state_rebuilt` audit fact.

## v0.1 Trust Boundary

KIFF enforces governance at the framework level, not by convention:

- Agents and callers can propose actions. KIFF validates state, permissions, parameters, and approvals before execution.
- Callers cannot self-approve high-risk actions. The `approved` state is private to the runtime and can only be set through the approval flow (`RequestApproval` → `ReviewApproval` → granted `ApprovalID`).
- Actions require explicit executors to be considered executed. A contract without an `Executor` function cannot produce a successful execution result.
- Every validation, denial, approval, and execution is audited with collision-resistant IDs.
- The demo proves both paths: granted approval completes the loop, denied approval blocks execution.

## Brick 16: Context Threading and JSON Tags

All public `Runtime` methods accept `context.Context` as the first argument. Callers can cancel ingestion, validation, execution, approval review, audit queries, and state rebuilds with a deadline or context.

Core serialized types now carry stable `snake_case` JSON tags. The HTTP API field names are decoupled from Go field renames.

## Brick 17: Trace Correlation

Audit records carry `trace_id`, `correlation_id`, and `causation_id`. The runtime propagates trace metadata from ingested events into every downstream audit record and into follow-up events emitted by executors. `audit.Filter.TraceID` returns the full operational chain for one external request.

## Brick 18: Domain Builder and Authoring Guide

`pkg/kiff/domain.Builder` provides a chainable helper for declaring a domain's events, transitions, allowed actions, and contracts. `docs/build-a-domain.md` walks a developer through modeling a small domain end to end.

## Brick 19: File-Backed JSONL Stores

`pkg/kiff/store/file` provides append-only JSONL implementations of the event, decision, approval, and audit store interfaces. The HTTP demo accepts `-data-dir <path>` to persist state across restarts:

```bash
go run ./cmd/kiff-http-demo -data-dir ./data
```

This is for demos and local testing, not a production database. Production deployments should implement the store interfaces against a real backend.
