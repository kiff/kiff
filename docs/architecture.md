# KIFF Architecture

KIFF is a protocol-first backend framework. It gives Go developers the reusable mechanics needed to coordinate humans, AI agents, services, and integrations around shared operational state.

The framework is intentionally small. There is no HTTP server, database adapter, LLM integration, UI, or workflow engine. The first version proves the local coordination loop:

```text
Event ingested -> State changed -> Decision recorded -> Action validated -> Execution audited
```

## Package Map

### `pkg/kiff/event`

The event package defines normalized records for things that happened.

An event identifies the affected entity, event type, source, actor, timestamp, metadata, and domain payload. Events enter KIFF through an `EventStore`. Brick 1 includes an in-memory store for tests, examples, and demos.

### `pkg/kiff/state`

The state package defines the current operational condition of an entity and the transition rules that can change it.

Domains own their state vocabulary. KIFF only provides the state shape, transition structure, state machine interface, and invalid transition error.

### `pkg/kiff/decision`

The decision package records why an action, classification, recommendation, or next step was proposed.

Decisions may come from humans, deterministic software, or AI agents. KIFF stores them as auditable operational records rather than hidden prompt output.

### `pkg/kiff/action`

The action package defines action contracts and validation.

An action contract declares its name, allowed states, required parameters, required permissions, risk level, approval requirement, and optional executor function. The default validator checks state, parameters, permissions, and approval before execution.

Action catalogs let domains register contracts by name. The catalog is a convenience layer, not a global registry. Domains still own the action vocabulary.

### `pkg/kiff/approval`

The approval package records human authority over actions that require review.

An approval identifies the affected entity, action name, requester, reviewer, status, reason, and timestamps. Brick 2 includes an in-memory approval store. Runtime validation can resolve an approval id from an action context and treat a granted approval as the approval signal for that action.

### `pkg/kiff/permission`

The permission package answers whether an actor is allowed to perform an action.

Brick 1 includes a simple in-memory policy that can grant permissions directly to actors or to actor roles.

### `pkg/kiff/audit`

The audit package records important operational facts: event ingestion, state changes, decisions, action validation, approval requirements, execution results, and failures.

Audit is part of the KIFF protocol. It is not optional logging added after the system is built.

### `pkg/kiff/actor`

The actor package defines the identity of a human, AI agent, service, system, or external integration.

Actors are used by events, decisions, permissions, and actions.

### `pkg/kiff/evidence`

The evidence package defines references to material used to support a decision or action.

Evidence can point to documents, events, system data, external APIs, agent analysis, human review, or logs.

### `pkg/kiff/runtime`

The runtime package wires the primitive stores and policies together.

It ingests events, applies state transitions, records decisions, validates actions, executes actions, and appends audit records. It is a coordinator, not an application server.

### `pkg/kiff/store`

The store package contains common store-level helpers and errors when they are useful across packages.

## Domain Boundary

KIFF does not define business meaning. Domains do.

The framework should never hardcode domain-specific workflows such as Fidel, The Line, OP3, or the mission example into `pkg/kiff`. Domain implementations should live under `examples/`, applications, or product-specific packages.

## Mission Example

`examples/mission` demonstrates a simplified challenge attempt domain.

It defines:

- events: `MISSION_SUBMITTED`, `ATTEMPT_CREATED`, `MOVE_PROPOSED`, `HUMAN_APPROVAL_GRANTED`, `MOVE_EXECUTED`
- states: `SUBMITTED`, `ACTIVE`, `WAITING_APPROVAL`, `COMPLETED`
- actions: `CREATE_ATTEMPT`, `PROPOSE_MOVE`, `REQUEST_HUMAN_APPROVAL`, `EXECUTE_MOVE`

The example uses an action catalog and an approval record to show how risky execution is proposed, reviewed, validated, executed, and audited. It is not part of the framework core.
