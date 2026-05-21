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

### `pkg/kiff/proposal`

The proposal package defines action proposals from humans, agents, services, or deterministic software.

A proposal captures the proposed action, parameters, evidence, reasoning, confidence, and actor. Runtime can record a proposal as a decision and convert it into an action context for validation. Proposal recording is intentionally separate from action execution.

### `pkg/kiff/action`

The action package defines action contracts and validation.

An action contract declares its name, allowed states, required parameters, required permissions, risk level, approval requirement, and optional executor function. The default validator checks state, parameters, permissions, and approval before execution.

Action execution returns an explicit result with status, message, error, effects summary, output, and timestamp. Runtime audit stores those result fields so execution can be reconstructed separately from validation.

Successful execution results may include follow-up events. Runtime ingests those events through the normal event path after auditing execution. This keeps state changes event-driven rather than action-driven.

Action catalogs let domains register contracts by name. The catalog is a convenience layer, not a global registry. Domains still own the action vocabulary.

### `pkg/kiff/approval`

The approval package records human authority over actions that require review.

An approval identifies the affected entity, action name, requester, reviewer, status, reason, and timestamps. Brick 2 includes an in-memory approval store. Runtime validation can resolve an approval id from an action context and treat a granted approval as the approval signal for that action.

Runtime can also request approval for a contract that requires it. Requesting approval creates a pending approval record and audits that request. Granting or denying approval remains a separate human authority step.

### `pkg/kiff/permission`

The permission package answers whether an actor is allowed to perform an action.

Brick 1 includes a simple in-memory policy that can grant permissions directly to actors or to actor roles.

### `pkg/kiff/audit`

The audit package records important operational facts: event ingestion, state changes, decisions, action validation, approval requirements, execution results, and failures.

Audit is part of the KIFF protocol. It is not optional logging added after the system is built.

Audit stores support filtered queries by entity, audit kind, and actor. Query results are returned in chronological order so a KIFF system can reconstruct the path of an entity after the fact.

### `pkg/kiff/actor`

The actor package defines the identity of a human, AI agent, service, system, or external integration.

Actors are used by events, decisions, permissions, and actions.

### `pkg/kiff/evidence`

The evidence package defines references to material used to support a decision or action.

Evidence can point to documents, events, system data, external APIs, agent analysis, human review, or logs.

### `pkg/kiff/domain`

The domain package defines a small boundary object for domain-owned coordination vocabulary.

A domain definition names the domain, declares known entity and event types, and bundles the state machine with the action catalog. This keeps application setup readable without moving mission, financial, marketplace, or post-purchase semantics into the KIFF core.

### `pkg/kiff/adapter`

The adapter package defines how raw inputs become normalized KIFF events.

Adapters do not own HTTP, queues, files, or SDK integrations. They sit after transport and before event ingestion. Their job is to validate raw input, map it into an `event.Event`, and let runtime ingestion apply the normal state and audit behavior.

### `pkg/kiff/httpapi`

The httpapi package exposes a small optional `net/http` handler around runtime methods.

It is a transport wrapper, not a web framework. It can ingest raw inputs, list allowed actions for an entity, and return audit timelines. Applications remain responsible for authentication, authorization at the edge, deployment, routing composition, and production middleware.

### `pkg/kiff/runtime`

The runtime package wires the primitive stores and policies together.

It ingests normalized events or adapter-normalized raw inputs, applies state transitions, records decisions, validates actions, executes actions, resolves currently allowed actions, reconstructs audit timelines, and appends audit records. It is a coordinator, not an application server.

### `pkg/kiff/store`

The store package contains common store-level helpers and errors when they are useful across packages.

Brick 5 adds a store bundle that groups the core event, decision, approval, and audit stores. The bundle is an injection boundary for future persistence adapters. It does not introduce a database or change the package-specific store contracts.

## Domain Boundary

KIFF does not define business meaning. Domains do.

The framework should never hardcode domain-specific workflows such as Fidel, The Line, OP3, or the mission example into `pkg/kiff`. Domain implementations should live under `examples/`, applications, or product-specific packages.

## Mission Example

`examples/mission` demonstrates a simplified challenge attempt domain.

It defines:

- events: `MISSION_SUBMITTED`, `ATTEMPT_CREATED`, `MOVE_PROPOSED`, `HUMAN_APPROVAL_GRANTED`, `MOVE_EXECUTED`
- states: `SUBMITTED`, `ACTIVE`, `WAITING_APPROVAL`, `COMPLETED`
- actions: `CREATE_ATTEMPT`, `PROPOSE_MOVE`, `REQUEST_HUMAN_APPROVAL`, `EXECUTE_MOVE`

The example exposes a mission domain definition, uses an action catalog and an approval record, and shows how risky execution is proposed, reviewed, validated, executed, and audited. It is not part of the framework core.
