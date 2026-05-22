# Why KIFF

## Most AI backends start at the wrong layer

If you have built a serious AI feature in production, you have probably had this conversation. The product team wants the agent to do something real: issue a refund, send a contract, transfer a balance, escalate a case, change a permission. The engineering team builds it. It works in the demo. Then it ships, and one of three things happens.

The agent does the action it was supposed to do, but also five other things, and now you have to explain to a customer why their account got changed.

The agent gets confused, refuses to act, and the human in the loop has no way to take over without bypassing the system.

The agent does the right thing nine times out of ten, and the tenth time costs you a chargeback, a regulatory letter, or a customer.

This is not an alignment problem. It is an architecture problem.

Most AI applications start at the prompt. They wrap a model in a chat UI, expose a few tools, and trust the model's judgment to call them correctly. That works for demos. It collapses in production because the prompt is the wrong layer to enforce operational rules. The prompt does not know your state machine. The prompt cannot enforce permissions. The prompt cannot require a human signature. The prompt cannot reconstruct what happened later.

The result is software that *describes* governance in instructions and *enforces* nothing.

## The layer below the prompt

Underneath every operational system, AI or not, there is a coordination protocol:

```text
something happened → state changed → a decision was made →
an action was proposed → it was validated → it was executed → the trail explains it
```

Banks have it. Marketplaces have it. Hospitals have it. Air traffic control has it. The protocol is not new. What is new is that AI agents now want to act inside it, often without being invited.

The right place to govern an agent is not in the prompt. It is in this protocol. The agent's job ends at "I propose to refund $999." The system's job is to validate that proposal against the current state, the actor's permissions, the action's parameters, and the approval requirements before anything moves. If the agent is right, execution proceeds. If the agent is wrong, the system says no, in a way that is auditable, replayable, and explainable.

This is what KIFF gives you. Not better prompts. Not smarter tool calls. The layer underneath, where governance is enforced by code rather than described by words.

## What the protocol gets you

Once the protocol exists, four things become possible that were not possible before:

**You can let agents propose freely.** Because the runtime is the gate, an agent can be wrong without being dangerous. Wrong proposals get rejected with a reason. Right proposals get executed with a trail. The conversation about agent reliability stops being existential.

**You can mix actors safely.** Humans, agents, services, and integrations all submit proposals to the same loop. They are governed by the same rules. The question of "what happens if a human and an agent both try to refund this order" has an answer instead of a discussion.

**You can replay any incident.** Every event, decision, validation, approval, execution, and failure is in the audit trail. Six months from now, when someone asks why a refund was issued, you can rebuild the entity from events alone and show the chain. Trust is not a story you tell. It is a function you can run.

**You can ship faster, not slower.** This is the counterintuitive part. People assume governance slows you down. In practice, the time you save not debugging mysterious state, not unwinding bad agent actions, not rewriting "just enough" governance for the third time, dwarfs the time you spend declaring an action contract.

## What KIFF is not

KIFF is not a chatbot framework. The conversation layer is your problem. KIFF starts the moment a human, agent, or service wants to *do something* to your state.

KIFF is not an LLM wrapper. There is no model SDK, no prompt builder, no embeddings store. The framework is agent-ready, not agent-coupled. You can run KIFF with no AI at all and it still earns its keep.

KIFF is not a workflow engine. It does not manage long-running tasks, retries, or scheduled jobs. If you need Temporal, use Temporal. KIFF lives next to it, not instead of it.

KIFF is not a generic web framework. There is an optional `httpapi` package because most people want HTTP, but the runtime is a coordinator you can drive from anything: a queue consumer, a CLI, a cron job, a custom RPC.

KIFF is not a universal business ontology. Your events, states, actions, and permissions are yours. KIFF normalizes the *mechanics* of coordination, not the meaning. An order is not a mission. A mission is not a refund. KIFF does not pretend they are.

## The shape of the bet

The bet KIFF makes is simple. The next decade of software is going to put AI agents inside operational systems, not next to them. The systems that survive will be the ones with a real governance layer between the agent and the state. The systems that fail will be the ones that tried to govern with prompts.

KIFF is the smallest, most idiomatic Go framework for building that layer. It is not the only way to do this. You could roll it yourself. You could glue together a state machine library, an event log, an approval table, and an audit table, and you would have something close to KIFF after a quarter or two of work. Most teams will. Most teams should not have to.

The protocol is what we are giving away. The whole point of the open-source MIT framework is that the skeleton is not the moat. The moat is the time you save not building it from scratch, the operational confidence you get from a tested core, and the shared language KIFF gives your team to talk about governance without arguing about every primitive.

## How to read this repo

If you have read this far and want to feel it instead of just read about it, run the tour:

```bash
go run ./cmd/kiff-tour
```

You will watch the protocol stop a $999 refund, accept a human approval, and replay the full trail in about ninety seconds. That is the whole pitch in three minutes of terminal output.

If you want to build something on top, scaffold a project:

```bash
go install github.com/kiffhq/kiff/cmd/kiff@latest
kiff new github.com/acme/orders
```

Then read [`docs/conventions.md`](./conventions.md) and [`docs/build-a-domain.md`](./build-a-domain.md). The first explains the normal way. The second walks you through authoring your own domain end to end.

If you want the philosophical version, [`docs/philosophy.md`](./philosophy.md) lists every choice we have made and why.

## The sentence to remember

> Make AI useful in real operations without losing control.

That is the promise. The rest is plumbing.
