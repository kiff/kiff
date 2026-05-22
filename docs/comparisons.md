# KIFF compared to neighbors

KIFF is a small framework with a sharp opinion. The cleanest way to explain where it sits is to compare it to the tools you might already be reaching for. None of the comparisons below are dismissive — every tool here is good at the thing it was built to do. The point is to make the boundaries legible so you know when KIFF helps and when something else is the right answer.

## The map

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│  the prompt / agent layer        LangChain, LangGraph, OpenAI tools, Agno   │
│  the workflow layer              Temporal, Inngest, Conductor, Cadence      │
│  the governance protocol         KIFF                                       │
│  the state primitive             stateless / FSM library                    │
│  the data layer                  Postgres, your DB, your event store        │
└─────────────────────────────────────────────────────────────────────────────┘
```

KIFF lives between the agent layer and the data layer. It is the protocol that says "before any actor changes shared state, here are the rules."

## KIFF vs LangChain / LangGraph / agent frameworks

Agent frameworks are about *how the agent thinks*. They give you prompt scaffolding, tool calling, memory, retrieval, multi-step reasoning, graph-shaped agent logic, and integrations with model providers.

KIFF is about *what the agent is allowed to do once it has decided*. It does not care how the proposal was generated. The same proposal, whether it came from GPT-5, Claude, a deterministic rule, or a human form submission, flows through the same gate.

When to use each:

- Use an agent framework to design the conversation, the reasoning, and the tool surface.
- Use KIFF to enforce that the tool calls produced by that framework cannot bypass your state, permissions, approvals, or audit.

They compose. The convention is: the agent framework calls into your domain through KIFF's `proposal` and `action` boundaries, not directly into your database.

## KIFF vs Temporal / Inngest / Cadence / Conductor

Workflow engines are about *durable orchestration*. They handle long-running processes, retries on failure, scheduled timers, distributed state, and saga patterns. They are excellent at "this multi-step thing must complete eventually, even if pieces fail."

KIFF is about *governed coordination*. It does not durably re-run failed steps. It does not own a scheduler. It does not coordinate across distributed workers. What it does is enforce that every step is allowed in the current state, was performed by an authorized actor, was approved if the action was risky, and was audited.

When to use each:

- Use a workflow engine when the hard problem is reliability across time and failure.
- Use KIFF when the hard problem is correctness across actors, especially actors with judgment (humans, agents).

They compose. A common pattern: Temporal owns the workflow shape, and at each step that mutates shared state, the workflow proposes a KIFF action and waits for the result. The workflow engine handles retries; KIFF handles whether the action was allowed at all.

## KIFF vs raw FSM libraries

A finite-state-machine library gives you a state machine. That is one of the six things KIFF gives you. The other five — events, decisions, actions with contracts, approvals, audit — you would still build yourself, and you would build them slightly differently in every project.

When to use each:

- Use a raw FSM library if state transitions are the only governance you need.
- Use KIFF when you also need explainable decisions, action validation, approval boundaries, and an audit trail you can replay.

The progression looks like this: most teams start with a raw FSM, then add an audit table, then add an approvals table, then add action validation, then realize they have rebuilt half of KIFF in their own codebase. KIFF exists to skip that arc.

## KIFF vs rolling your own

The most common alternative to KIFF is "we'll just build it." This is reasonable. The framework is small enough that any competent Go team could ship something equivalent in a quarter.

The honest reasons to roll your own:

- You need a unique store contract that does not fit `event.EventStore`, `decision.DecisionStore`, etc. (Rare. The interfaces are deliberately small.)
- You need a primitive KIFF does not provide. (Open an issue. We probably want it too.)
- Your team enjoys building infrastructure more than shipping product. (Be honest with yourself.)

The honest reasons not to:

- Every team that rolls its own ends up arguing about audit ID generation, approval state machines, and whether actions can mutate state directly. KIFF has already had those arguments and made the calls.
- You will not write tests for the trust boundary. KIFF already has them.
- You will not document the conventions. KIFF already does.
- You will write `eventStore`, `auditStore`, `approvalStore` in a slightly different way in every product, which means cross-team knowledge transfer is harder than it should be.

The framework is MIT-licensed. Reading the source is faster than reinventing it.

## KIFF vs a CRUD app

If your application is a thin layer over a database with no AI, no human approval boundaries, no risky actions, and no audit requirements, KIFF is too much structure. Use the smallest thing that works. Probably a router, a database, and a request handler.

KIFF starts paying for itself the moment any of these become true:

- A human or agent can do something that costs money or affects identity.
- More than one kind of actor (human, agent, service) can act on the same entity.
- Someone, eventually, will ask "why did this happen?" and you cannot point at a log line.
- A regulator, customer, or VP will eventually want a replay.

If you are pretty sure none of those will ever apply, you do not need KIFF. If you are pretty sure one of them already does, KIFF is going to feel like relief.

## When KIFF is the wrong choice

To be useful, this section has to be real:

- **You need ML inference orchestration.** KIFF is not a model server. Look at Ray Serve, BentoML, or your model provider's primitives.
- **You need a chatbot.** KIFF starts after the conversation. Build the chat layer with whatever you like and call into KIFF for actions.
- **You need a workflow engine.** Use Temporal, Inngest, or similar. KIFF can sit underneath them.
- **You need a feature flag system.** Different problem, different shape. KIFF does not gate features; it gates actions.
- **You need a generic permissions library.** KIFF has a small `permission` package because actions need permissions. If you need OPA, Casbin, or a full-blown policy engine, plug it in via the `permission.Policy` interface.

## Where KIFF wants to go

The framework is at v0.1. The core protocol is settled and tested. The next direction is making KIFF *adoptable*: a clearer onboarding path, better defaults for observability, an LLM bridge example, and richer test helpers. None of those will change the core comparison map above. KIFF will continue to be the governance protocol that lives between the agent layer and the data layer, regardless of what you choose at the layers around it.

If you are building in a space where the comparison map looks wrong, file an issue. Drawing the boundaries clearly is part of the design.
