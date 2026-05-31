# KIFF Vision

## Brick 1 summary

KIFF is a Go framework for building governed agentic backends.

It normalizes coordination mechanics, not business semantics. Domains define their own vocabulary; KIFF provides primitives for events, state, decisions, actions, permissions, approvals, evidence, audit, adapters, and runtime coordination.

The core position is simple:

- agents may propose decisions or actions;
- KIFF validates whether those actions are allowed;
- high-risk actions require human authority;
- every important event, decision, action, approval, execution result, and failure must be auditable.

Brick 1 focuses on the open-source framework core. It does not build KIFF Cloud, KIFF Studio, a UI, or an LLM integration.

## A Go framework for governed agentic backends

KIFF is a server-side application framework written in Go for building systems where humans, AI agents, and software components need to coordinate safely around shared operational state.

KIFF exists because most AI applications start too late in the architecture. They start with prompts, tools, chat interfaces, or automations. That works for demos, but it breaks when the system must operate in the real world: when actions have consequences, when multiple actors are involved, when approvals are required, when state must be trusted, and when decisions need to be explained later.

KIFF starts earlier.

Before asking what an agent should do, KIFF asks:

- What happened?
- Which entity changed?
- What state is that entity in?
- What decisions are now possible?
- What actions are allowed?
- Who or what is allowed to perform them?
- Does this action require human approval?
- What evidence supports the decision?
- How will the system explain what happened later?

KIFF is not a chatbot framework, a generic web framework, an LLM wrapper, a no-code automation tool, or a universal business ontology.

KIFF is a protocol-first backend framework for building governed, auditable, stateful, agent-ready systems.

---

## The core idea

Every serious operational system eventually needs the same coordination mechanics:

```text
Raw inputs → Normalized events → Shared state → Decisions → Validated actions → Execution → Audit
```

The domain can change completely. A financial case, an order, a mission, and a sensor anomaly are genuinely different things, and KIFF does not try to make them the same.

Instead, KIFF normalizes the mechanics of coordination.

Every domain can define its own vocabulary, entities, events, states, actions, risks, permissions, and business rules. KIFF provides the framework for making those mechanics explicit, testable, auditable, and safe for agentic execution.

The principle is simple:

> KIFF normalizes mechanics, not semantics.

This is the foundation of the framework.

---

## Why KIFF exists

Modern software is moving from passive systems to active systems.

Traditional applications mostly store data and wait for humans to act. Agentic systems observe, interpret, propose, decide, trigger workflows, prepare artifacts, call tools, and sometimes execute actions.

That creates a new backend problem.

If an AI agent can act, the system must know:

- what state the world is in;
- what actions are valid in that state;
- what actor or agent is allowed to perform those actions;
- what risk level each action carries;
- what approvals are required;
- what evidence was used;
- what was decided;
- what happened after execution;
- how the full chain can be reconstructed.

Without this structure, agentic systems become fragile. They may produce useful outputs, but they cannot be trusted as operational infrastructure. They skip context. They forget state. They bypass approvals. They hide reasoning inside prompts. They make decisions that cannot be audited.

KIFF exists to give agentic systems an operational skeleton.

---

## What KIFF provides

KIFF provides reusable backend primitives for modeling coordination:

### Events

An event is a normalized record of something that happened.

Events are immutable. They are timestamped. They identify the source, the actor, the affected entity, and the domain-specific payload.

Events are the primary way the outside world enters KIFF.

Examples:

- `CASE_CREATED`
- `CONSENT_GRANTED`
- `PROVIDER_MATCHED`
- `MISSION_SUBMITTED`
- `MOVE_PROPOSED`
- `PAYMENT_CAPTURED`
- `ORDER_DELIVERED`

KIFF does not decide which events a domain needs. The domain defines them. KIFF defines how events are structured, stored, validated, and applied.

### State

State is the current operational condition of an entity.

State is not a loose label. It is the shared reality that decisions depend on.

A domain defines its own state machine. KIFF provides the interfaces and runtime behavior for applying events to state, validating transitions, exposing allowed actions, and recording state changes.

Examples:

- A financial case may move from `INTAKE` to `READY` to `MATCHED` to `INTRODUCTION_PENDING`.
- A mission attempt may move from `SUBMITTED` to `ACTIVE` to `WAITING_APPROVAL` to `COMPLETED`.
- An order may move from `CREATED` to `PAID` to `FULFILLMENT_PENDING` to `DELIVERED`.

The state machine belongs to the domain. The coordination pattern belongs to KIFF.

### Decisions

A decision is a structured record of why a possible action, classification, recommendation, or next step was selected.

Decisions matter because agents should not only produce outputs. They should leave behind explainable operational traces.

A KIFF decision can include:

- the affected entity;
- the decision kind;
- the proposed action;
- the evidence considered;
- the reasoning summary;
- the confidence level;
- the actor or agent that produced it;
- the time it was created.

Agents may propose decisions. Humans may make decisions. Software may derive decisions deterministically. KIFF records them in a common operational structure.

### Actions

An action is an operation that may change the world.

Actions are not free-form tool calls. They are contracts.

An action contract defines:

- the action name;
- the states in which it is allowed;
- the required parameters;
- the required permissions;
- the risk level;
- whether human approval is required;
- how validation works;
- how execution works.

This is one of KIFF’s most important ideas:

> Agents may propose actions, but KIFF validates actions before execution.

The framework must make it hard for agents, humans, or integrations to bypass the operational rules.

### Permissions

Permissions define who or what can perform an action.

An actor may be:

- a human user;
- an AI agent;
- an external system;
- an internal service;
- an organization;
- a role.

Permissions may be simple at first, but they must be explicit. A framework for agentic systems cannot treat authority as an afterthought.

KIFF should support permission policies that can answer:

- Can this actor perform this action?
- Can this actor perform it in this state?
- Is the action above the actor’s authority level?
- Does this require human approval?
- Is the approval already present?

### Evidence

Evidence is any supporting input used to justify a decision or action.

Evidence may come from documents, user submissions, system data, external APIs, agent analysis, human review, logs, or previous events.

KIFF should not assume that every decision is purely deterministic. In agentic systems, judgment matters. But judgment must leave a trace.

Evidence references help the system explain why a decision was proposed or accepted.

### Audit

Audit is not an optional logging feature. Audit is part of the protocol.

Every important event, state transition, decision, action validation, approval, execution result, and failure should be recordable.

The audit trail is how KIFF systems explain themselves.

A KIFF system should be able to answer:

- What happened?
- When did it happen?
- Which actor triggered it?
- What state was the entity in?
- What decision was made?
- What action was proposed?
- Was it valid?
- Was approval required?
- Was it approved?
- Was it executed?
- What was the result?

Trust comes from reconstruction.

---

## The role of AI agents

KIFF is agent-ready, but it is not agent-first.

The framework should not assume that an LLM is always present. A KIFF system should work with deterministic rules, human decisions, traditional software, or AI agents.

Agents become useful when they operate inside the KIFF structure.

An agent may:

- classify an event;
- propose a decision;
- suggest the next action;
- prepare an artifact;
- summarize evidence;
- detect missing information;
- recommend escalation;
- generate implementation plans;
- assist a human approver.

But the agent should not silently bypass state, permissions, approvals, or audit.

KIFF’s position is:

> AI agents can propose. KIFF validates. Humans retain authority over high-risk actions.

This makes KIFF different from frameworks that focus only on tool use or autonomous execution.

---

## What KIFF is not

KIFF does not try to replace your Go web framework; it can sit beside one. It does not try to replace workflow engines either, so systems that need Temporal, queues, schedulers, or orchestration can keep them. Agent frameworks are no different: KIFF can integrate with Agno, LangGraph, OpenAI tools, or other agent runtimes later. It has no ambition to create a universal business language or to own a client's domain logic.

KIFF provides the coordination structure that lets domain logic become explicit and governable.

---

## Target users

KIFF is for developers and teams building systems where operations cannot be reduced to simple CRUD.

It is especially useful when a system has:

- multiple actors;
- stateful entities;
- approvals;
- exceptions;
- audit requirements;
- human and AI collaboration;
- risky actions;
- external integrations;
- business rules that must be explicit;
- decisions that need to be reconstructed later.

Early users may include:

- AI product builders;
- fintech teams;
- marketplace operators;
- workflow automation teams;
- compliance-heavy startups;
- operations platforms;
- post-purchase systems;
- service orchestration products;
- internal tools teams;
- founders building agentic applications.

---

## Reference implementations

KIFF should be validated through real domain implementations under `examples/`. The framework ships several worked examples that exercise different shapes of the coordination loop:

- [`examples/refund`](../examples/refund/): the shortest worked domain. One entity, three states, two actions (one low-risk, one approval-required). Run this first.
- [`examples/mission`](../examples/mission/): a fuller mission/challenge domain with attempts, moves, approvals, and replay. Used by the `kiff-tour` and `kiff-demo` commands.
- [`examples/refund-agno`](../examples/refund-agno/): depth. One tool, two runs (without KIFF and through KIFF), real Agno agent, real LLM. Demonstrates KIFF as governance for an AI agent.
- [`examples/support-ops`](../examples/support-ops/): breadth. One agent, five tools, five distinct outcomes including consent-blocked validation.
- [`examples/ai-cafe-ops`](../examples/ai-cafe-ops/): operational authority. AI shift manager, four tools, both local-mode and cloud-mode (talks to a hosted KIFF Cloud tenant over HTTP).
- [`examples/llm-bridge`](../examples/llm-bridge/): the canonical pattern for bridging an LLM tool-call surface into governed KIFF actions.

These examples are not the framework itself. They are the same coordination mechanics applied to different domains, kept readable enough to teach the framework end to end.

---

## Framework philosophy

KIFF should be small, idiomatic, and composable.

It should feel like a Go framework, not like a research project.

The core must be boring enough to trust and opinionated enough to matter.

### Principles

1. **Normalize mechanics, not semantics.**  
   Domains define their own business meaning. KIFF defines the coordination structure.

2. **State before action.**  
   The system must know the current state before deciding what can happen next.

3. **Actions are contracts.**  
   An action must declare when it is allowed, what it requires, who can perform it, and whether approval is needed.

4. **Agents propose; KIFF validates.**  
   AI output is not execution. Agent proposals must pass through validation.

5. **Human authority for high-risk actions.**  
   The framework must make approval requirements explicit.

6. **Audit is mandatory.**  
   Operational systems must explain themselves after the fact.

7. **Domain logic stays out of the core.**  
   The framework should provide primitives and interfaces, not hardcoded business workflows.

8. **No unnecessary dependencies.**  
   The first version should stay lightweight.

9. **Tests define trust.**  
   Every primitive should have clear tests.

10. **Examples should teach the framework.**  
   A developer should understand KIFF by running a simple example.

---

## Initial framework structure

The first version of KIFF Framework should include these packages:

```text
pkg/kiff/event
pkg/kiff/state
pkg/kiff/action
pkg/kiff/decision
pkg/kiff/permission
pkg/kiff/audit
pkg/kiff/actor
pkg/kiff/evidence
pkg/kiff/runtime
pkg/kiff/store
```

Example domains should live under:

```text
examples/
```

Runnable demos should live under:

```text
cmd/
```

Documentation should live under:

```text
docs/
```

The first demo should show a minimal mission or challenge attempt system because it demonstrates the agentic nature of KIFF without requiring a complex external integration.

---

## First milestone

The first milestone is not KIFF Cloud.

The first milestone is not KIFF Studio.

The first milestone is a clean open-source Go framework scaffold that can demonstrate the KIFF loop locally:

```text
Event ingested → State changed → Decision recorded → Action validated → Execution audited
```

A developer should be able to run:

```bash
go run ./cmd/kiff-demo
```

and see the coordination loop in action.

They should be able to run:

```bash
go test ./...
```

and see that the framework primitives are tested.

---

## Future layers

The open-source KIFF Framework is only the foundation.

Future commercial layers may include:

### KIFF Studio

A product that takes a URL, workflow, or problem description and generates a first protocol blueprint:

- primitive identification;
- event taxonomy;
- state machine draft;
- action catalog;
- permission model;
- agent opportunities;
- audit requirements;
- implementation scaffold.

### KIFF Cloud

A hosted runtime for teams that want managed coordination infrastructure:

- hosted event store;
- audit explorer;
- state dashboard;
- action approval UI;
- agent proposal review;
- observability;
- integrations;
- deployment support.

### KIFF Templates

Premium domain accelerators for common operational patterns:

- fintech case coordination;
- post-purchase operations;
- marketplace dispute handling;
- partner workflows;
- compliance workflows;
- mission and move systems;
- service orchestration.

### KIFF Build

A service layer that uses the framework, Studio, templates, and AI coding tools to build governed agentic systems quickly.

The open-source framework builds trust and adoption. The commercial layers provide speed, hosting, domain expertise, and production support.

---

## Licensing direction

The KIFF Framework core should be released under the MIT License.

The reason is adoption.

A new framework needs low friction. Developers should be able to inspect it, use it, test it, fork it, and build with it. The goal is not to protect the skeleton by hiding it. The goal is to make KIFF the reference architecture for governed agentic backends.

The business should not depend on secrecy of the basic primitives.

The business should be built around:

- the brand;
- the best implementation;
- the best documentation;
- the hosted runtime;
- the domain templates;
- the Studio experience;
- the implementation service;
- the accumulated operational knowledge;
- the speed of applying KIFF to real domains.

Open-source the skeleton. Monetize the muscle, brain, and operating room.

---

## The sentence to remember

KIFF is a Go framework for building governed agentic backends that know what happened, what state an entity is in, what actions are allowed, who can approve them, and why every decision was made.

That is the vision.
