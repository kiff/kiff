# KIFF compared to adjacent categories

KIFF is a small framework with a sharp opinion: consequential actions should be
decided against event-derived entity state, validated as typed contracts, and
recorded with their authority and evidence. This page compares that role with
neighboring architectural categories without prescribing a particular vendor.

## The map

```text
agent or application layer       proposes decisions and actions
workflow layer                   coordinates durable work over time
KIFF governance protocol         owns event → state → decision → action checks
state or policy primitive        evaluates a transition or supplied snapshot
data layer                       persists operational records
```

KIFF lives between proposal and consequence. It says: before an actor changes
shared operational state, reconstruct what is true, validate what is allowed,
resolve authority, and leave an audit trail.

## Agent frameworks

An agent framework is concerned with how an agent reasons: prompts, tool calls,
memory, retrieval, multi-step logic, and model integration.

KIFF is concerned with what may happen after a proposal exists. It does not care
whether the proposal came from an agent, a deterministic rule, a human form, or
another service. Each proposal crosses the same state, permission, approval, and
audit boundary.

The two categories compose. The application routes consequential tool calls
through KIFF's proposal and action boundaries instead of directly invoking the
side effect.

## Workflow engines

A workflow engine provides durable orchestration: retries, timers, long-running
processes, distributed workers, and recovery after failure.

KIFF provides governed coordination. It answers whether a step is valid in the
entity's current state, whether the actor has authority, whether approval is
present, and how the result is audited. It is not a scheduler and does not promise
eventual completion.

The two categories compose. A workflow can coordinate when to attempt a step;
KIFF decides whether that step is allowed now.

## Stateless policy interceptors

A stateless policy interceptor evaluates a snapshot supplied by its host. This is
useful when the host already owns trustworthy lifecycle state and only needs a
central allow-or-deny decision.

KIFF owns more of the lifecycle. Events enter the runtime, drive state transitions,
support replay, constrain available actions, and connect decisions to execution and
audit. A host does not merely submit an arbitrary `PENDING` snapshot and ask for a
verdict; the normal runtime path resolves current state produced by the entity's
recorded events.

Choose a stateless interceptor when snapshot provenance and lifecycle ownership are
already solved elsewhere. Choose KIFF when event-to-state lifecycle ownership is
part of the governance problem.

## Finite-state-machine libraries

A finite-state-machine library gives you transitions and allowed states. KIFF uses
that shape but also provides normalized events, explainable decisions, typed action
contracts, permissions, approvals, execution results, replay, and audit.

Use a standalone state machine when transitions are the whole problem. Use KIFF
when the system must also govern who may act, under what evidence and approval,
with a reconstructable operational history.

## Building the protocol yourself

Building an equivalent protocol can be reasonable when a system needs contracts
that do not fit KIFF's small interfaces or when governance is inseparable from a
specialized runtime.

KIFF is useful when teams would otherwise repeatedly design event records, approval
state machines, action validation, replay, and audit conventions. Its value is not
that these parts are impossible to build; it is that they arrive as one tested,
composable protocol.

## CRUD applications

If an application is a thin database layer with no risky actions, shared entity
lifecycle, approvals, or reconstruction requirements, KIFF is probably too much
structure.

KIFF starts paying for itself when multiple kinds of actor touch the same entity,
an action can have consequences, or someone must later answer what happened, why
it was allowed, and which state justified the decision.

## When KIFF is the wrong choice

- You only need model inference or conversation management.
- You only need durable scheduling and retries.
- You only need feature flags.
- You only need a general-purpose authorization library.
- Your operations have no meaningful state, authority, approval, or audit boundary.

KIFF can sit beside tools in those categories. It does not try to replace them.
