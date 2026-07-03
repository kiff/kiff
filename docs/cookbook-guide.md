# Cookbook Guide

The cookbook is the fastest way to understand what KIFF is for: launching
agentic workflows that touch real systems without handing the agent direct
authority over the side effect.

Use this guide when you are evaluating a recipe, adapting one to your own
domain, or deciding whether KIFF is the right amount of structure.

## Start With the Risk

Do not start with the agent. Start with the action you would not let an agent
perform directly.

Good KIFF cookbook candidates sound like:

- "Create or release a payment."
- "Change vendor banking details."
- "Revoke user access."
- "Create a purchase order."
- "Submit a prior authorization."
- "Issue a claims payout."
- "Isolate production infrastructure."

For each candidate, write down:

- the entity being governed;
- the state that entity can be in;
- the action that changes the outside world;
- the actor that may propose the action;
- the service or human that owns authority to execute or approve it;
- the facts that make the action low-risk, high-risk, invalid, or blocked.

If those answers are easy, KIFF is probably a good fit. If the action is simple
CRUD or has no meaningful state, KIFF may be too much structure.

## Choose the Closest Recipe

| If your workflow is about... | Start from... | What to study |
| --- | --- | --- |
| Money movement | `cookbook/accounts-payable-payout` | Payment boundary, finance approval, lifecycle view |
| Claims payout | `cookbook/insurance-claims-triage` | Evidence collection, payout preparation, service execution |
| Clinical or payer submission | `cookbook/healthcare-prior-auth` | Documentation state, clinician approval, external portal boundary |
| Vendor payment data | `cookbook/vendor-bank-change` | Sensitive data changes, finance-owned execution |
| Cloud remediation | `cookbook/cloud-infra-remediation` | Operational isolation and approval-gated remediation |
| Identity containment | `cookbook/security-incident-response` | Dynamic risk, reviewer authority, access revocation |
| Spend creation | `cookbook/procurement-purchase-order` | ERP writes, purchase risk, manager approval |

Read the recipe README first. Then read the domain file and tests before the
demo. The tests usually explain the boundary better than the demo does.

## Map Your Domain

Every recipe has the same KIFF shape:

```text
Raw input -> normalized event -> shared state -> decision/proposal
          -> validated action -> approval when needed -> execution -> audit
```

When adapting a recipe, keep domain semantics in your package and KIFF
mechanics in the framework.

### 1. Define the entity and state machine

Pick one governed entity and make its lifecycle explicit.

Examples:

- `Invoice`: `RECEIVED -> VERIFIED -> READY_FOR_PAYMENT -> PAYMENT_HELD -> PAID`
- `SecurityIncident`: `RECEIVED -> SIGNALS_ATTACHED -> REVIEW_REQUIRED -> CONTAINED`
- `PurchaseRequest`: `RECEIVED -> QUOTE_ATTACHED -> BUDGET_VERIFIED -> ORDERED`

The state machine should answer "what is true now?" before any action is
considered.

### 2. Separate proposal from execution

The agent proposes actions. It should not hold the credential that performs the
side effect.

The host app should pass the proposal to KIFF. KIFF validates state,
parameters, permissions, approval, and idempotency. Only then does the executor
run.

The useful topology is:

```text
agent proposal -> host app -> KIFF runtime -> executor with credential
```

The unsafe topology is:

```text
agent -> payment/ERP/cloud/identity SDK credential
```

If the unsafe path still exists, KIFF is not protecting that side effect.

### 3. Model actors and permissions

List actors by responsibility, not by implementation detail.

Common actors:

- agent actor: may propose or prepare work;
- service actor: may execute the side effect;
- human reviewer: may approve high-risk work;
- intake/source actor: may ingest external facts.

Use policy-owned role membership. Do not trust roles submitted by the caller as
proof of authority.

### 4. Make actions typed contracts

For every consequential action, define:

- allowed states;
- required parameters;
- typed parameter specs;
- required permissions;
- approval policy;
- executor behavior;
- follow-up event emitted on success;
- idempotency key when retries are possible.

Parameter validation should reject malformed or semantically invalid requests
before executor code can run.

### 5. Use dynamic approval when risk depends on facts

Static approval is enough for simple high-risk actions. Use dynamic approval
policy when approval depends on runtime facts.

Examples:

- amount exceeds a threshold;
- vendor is new or bank details changed;
- user is privileged or blast radius is broad;
- infrastructure action isolates production;
- clinical evidence is ambiguous.

This lets low-risk work proceed while high-risk work pauses for authority.

### 6. Record proposals and render lifecycle

Agents should leave a structured decision record, not just a chat transcript.

Record the proposal as a KIFF decision. Then use:

```go
life, err := rt.EntityLifecycle(ctx, entityID)
```

The lifecycle view gives a UI or API one read-only projection over:

- proposals and decisions;
- validation stages;
- approval holds, grants, and denials;
- execution and failures;
- idempotent replays;
- current state derived from audit records.

The lifecycle is a view over facts KIFF already stores. It is not a second
source of truth.

## Prove the Boundary With Tests

Every recipe should have tests that prove the executor does not run when it
should not.

At minimum, test:

- wrong-state action is blocked;
- missing or invalid parameter is rejected before execution;
- actor without permission cannot execute;
- high-risk action requires approval;
- reviewer cannot approve their own requested action;
- approval denial does not execute;
- approved action executes through the service actor;
- retry does not run the side effect twice;
- replay or lifecycle reconstructs the governed history.

For money, identity, cloud, ERP, or healthcare workflows, treat these tests as
the product claim. If a test does not prove the boundary, the demo is only a
story.

## What the Host App Still Owns

KIFF does not own everything. Your system still owns:

- the LLM or deterministic agent;
- raw input collection and normalization strategy;
- edge authentication;
- production persistence;
- credentials for payment, ERP, cloud, identity, payer, or ledger systems;
- UI and operator workflow;
- monitoring, deployment, and incident response;
- business-specific fraud, clinical, finance, or security policy.

KIFF owns the governed action boundary once a proposal reaches the runtime.

## When a Recipe Is Ready to Ship

A recipe is launch-grade when:

- the state machine covers all terminal and blocked states;
- all consequential actions are explicit contracts;
- executors are the only place credentials live;
- high-risk paths require the right human authority;
- retries are safe;
- audit and lifecycle explain the path;
- tests cover blocked, held, approved, denied, executed, and duplicate paths.

That is the point of the cookbook: not to show that KIFF can log what happened,
but to show how a team can safely launch work that previously felt too risky to
automate.
