# Why KIFF

The first agent is usually a feature. The next few need a system.

An agent that drafts an answer or finds information can often live inside its
own application. The architecture changes when agents, people, and services all
need to work on the same order, case, invoice, account, or incident. That work
already has a lifecycle, valid next steps, and people with different authority.

Without a shared model, each new actor rebuilds those facts in its own prompts,
tool handlers, and integrations. Over time, the rules drift, state fragments,
and every new capability becomes another backend project.

## One Operational Truth

KIFF gives all of those actors one domain to work against.

```text
events -> shared state -> actions -> execution -> audit
```

The domain is yours. You define what an order, claim, mission, or account means.
KIFF supplies the mechanics that usually get rebuilt around it:

- events that record what happened
- state derived from those events
- typed action contracts with parameters, permissions, risk, and approvals
- executors that perform valid side effects
- decisions, execution results, and audit records that explain the outcome

This is not an integration hub that replaces your systems of record. Payment
providers, warehouses, internal services, and agent frameworks keep doing their
jobs. KIFF gives every participant the same answer to two operational questions:
what is true now, and what may happen next?

## The Same Contract for Every Actor

An agent proposes an action. A human application proposes an action. A service
or workflow proposes an action. They all meet the same action contract.

That matters when the result depends on live state rather than intent alone. A
refund may be allowed while an order is paid, require approval above a threshold,
and be refused once it has already been refunded. The rule belongs to the
domain, not to one agent's prompt or a single integration's handler.

Governance is therefore part of the operational model, not a bolt-on control.
KIFF evaluates the proposed action against current state, authority, parameters,
and approvals before the executor runs. The result is recorded so the next
actor starts from the same reality.

## Why a Framework

Teams can assemble this themselves: a state machine, event store, permissions,
approval tables, action validation, execution records, and a way to replay an
entity later. They often do, then rebuild much of it when a second agent or a
new integration arrives.

KIFF provides those primitives as an idiomatic Go framework. It standardizes
the mechanics of coordination while leaving business semantics to the domain.
You can use the agent framework, transport, queue, workflow engine, and
database that fit your system.

## What KIFF Does Not Replace

KIFF is not a chatbot framework, model SDK, workflow engine, or generic web
framework. It does not own your prompts, systems of record, or business
vocabulary.

It starts when an actor wants to change shared operational state. A workflow
engine can schedule the work; KIFF determines whether the named action is valid
now. An executor performs the side effect; KIFF records the decision and result.

## Try It

Run the tour to see one domain shared by an agent and a human:

```bash
go run ./cmd/kiff-tour
```

Or scaffold a runnable example:

```bash
go install github.com/kiff/kiff/cmd/kiff@latest
kiff new -scenario refund github.com/acme/orders
cd orders
go mod tidy
make demo
```

The refund is only an example. The reusable result is the domain: its events,
state, actions, authority, execution boundary, and history are ready for every
actor that follows.
