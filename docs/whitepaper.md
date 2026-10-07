# KIFF

## A coordination protocol for governed action

**Working notes, May 2026**
**Gabriel Sarmiento**
Released under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
Code referenced is part of the open-source MIT framework at
[github.com/kiff/kiff](https://github.com/kiff/kiff).

---

> **Scope: framework v0.9.** This document describes that version's
> behavior and limitations. Section 3 includes the approval and state
> checks revised after an adversarial audit.

## Abstract

Humans, services, integrations and AI agents can act on the same order,
claim or account. Each action needs checks against the entity's current
state, the actor's permissions and any required approval. When those
checks differ between callers, an action may run without the review or
state check the business requires.

KIFF is a Go framework that validates proposed actions before calling
their executors. It provides events, state transitions, decisions,
action contracts, approvals and audit records. Events can be replayed
to reconstruct an entity's state.

This paper describes the validation sequence, its trust boundary and
the evidence supplied by the framework, demos and tests. It also
identifies the checks an application must perform outside the framework.

---

## Contents

1. [The problem](#1-the-problem)
2. [The coordination loop](#2-the-coordination-loop)
3. [The trust boundary](#3-the-trust-boundary)
4. [Domain definitions](#4-mechanics-not-semantics)
5. [Implementation and tests](#5-evidence-what-we-built)
6. [Limits](#6-honest-limits)
7. [Future work](#7-where-this-goes)
8. [Appendix A: the action contract](#appendix-a-the-action-contract)
9. [Appendix B: integration boundaries](#appendix-b-what-kiff-is-not)

---

## 1. The problem

An actor's access to a tool does not establish whether a particular
request is allowed. A refund tool may accept an amount even when the
order is unpaid; a restart command may work during a deployment freeze.
The application needs to check the request before the side effect occurs.

The following hypothetical examples illustrate checks that operational
systems may need:

| Context | Proposed action | Check needed before execution |
|---|---|---|
| E-commerce | Refund an unpaid order | Confirm the order's payment state |
| Insurance | Pay a claim under fraud review | Confirm the claim is eligible for payment |
| Healthcare | Create an order that conflicts with an active prescription | Check the relevant clinical state |
| Fintech | Transfer an amount above a dual-control threshold | Obtain the required review |
| Internal DevOps | Restart a service during a deployment freeze | Check whether the restart is permitted |

These checks apply to requests from humans and software as well as AI
agents. Teams implement them with state machines, validators, review
queues and audit stores. KIFF connects those components through a shared
action contract and runtime.

### Scope of enforcement

KIFF evaluates requests submitted to its gate. In an embedded application,
the runtime validates an action before invoking its registered executor.
When an application uses a decision API or the guard SDK, the calling
code must honor the verdict; the guard's pre-tool hook withholds a call
on a non-`allowed` verdict.

A code path that invokes a tool without consulting KIFF remains outside
this boundary. Integration must route the relevant actions through the
gate and control any credentials or alternative execution paths that
could bypass it.

---

## 2. The coordination loop

The framework connects six types of record:

```
event → state → decision → action → approval → audit
```

**Events** record what happened. Each append-only record includes a
timestamp, source and domain-specific payload inside a common envelope.

**State** describes an entity's current condition. Deterministic
transitions update it from events, giving the runtime a stored value
against which to validate an action.

**Decisions** record an actor's intent, including any submitted reasoning,
evidence and confidence. A decision remains available even if its proposed
action never runs. The record preserves the actor's explanation; it does
not verify that the explanation is correct.

**Actions** declare a name, allowed states, required parameters,
permissions, risk level, approval requirement and executor. The runtime
checks the contract before execution. See [Appendix A](#appendix-a-the-action-contract).

**Approvals** record the requester, reviewer, entity, action, status,
reason and timestamps. The runtime resolves a granted approval from the
store before marking the action context approved.

**Audit records** cover ingestion, state transitions, decisions,
validation, approval requests and reviews, execution results and
failures. Their `trace_id`, `correlation_id` and `causation_id` fields
link the steps of a request.

An event updates state, an actor records a decision and the runtime
validates the proposed action. If the action needs approval, execution
waits for a granted record. The executor then runs and its outcome is
audited. Replaying the entity's events allows comparison with its
materialized state.

---

## 3. The trust boundary

An action that requires approval must resolve a granted record from the
approval store. A caller-supplied flag cannot substitute for that record.

`ActionContext` carries an unexported `approved` field:

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

Go prevents a package outside `action` from setting that field in a
struct literal. `GrantApproval` also requires a capability type from an
`internal/` package, which external callers cannot name. These compiler
restrictions alone do not establish a runtime security boundary.

### The approval bypass and its fix

An adversarial audit used reflection to call the setter from a separate
module:

```go
// From a separate module. No unsafe, four lines.
m := reflect.ValueOf(&ctx).MethodByName("GrantApproval")
m.Call([]reflect.Value{reflect.Zero(m.Type().In(0))})
```

This attack and an `unsafe` variant executed an approval-required action
against an empty approval store. Both attempts produced
`action_validated` and `action_executed` audit records. The earlier
implementation therefore allowed a bypass while recording it as valid.

The revised runtime checks approval independently of the supplied bit:

- `applyApproval` clears any inbound approved value and looks up the
  approval by ID. It checks that the record matches the entity and
  action and has status `granted` before deriving the approved value.
- The capability check rejects a zero `trust.Grant`.
- After the pluggable `Validator` accepts an action, the runtime checks
  the approval requirement again, so a permissive validator cannot waive it.

If a check fails, the context stays unapproved. Store errors propagate
to the caller rather than allowing execution.

By default, the reviewer must differ from the requester. A requester
attempting to review their own approval receives `approval.ErrSelfReview`.
This segregation of duties prevents the requester from providing their
own sign-off.

### Stored state

The runtime also verifies the state used for validation. Earlier code
accepted a caller's `CurrentState` string without checking it, allowing
the caller to name a state in which its action was permitted.
`ValidateAction` now reads stored state and returns `ErrStateMismatch`
when the caller's assertion disagrees with it.

### Tests

Compile-time tests check that an external caller cannot set
`ActionContext{approved: true}` or directly invoke the protected setter.
Runtime fixtures execute reflection, `unsafe` and permissive-validator
attacks from a separate module against an empty approval store. Each
must receive a refusal. These fixtures fail against the earlier
implementation and pass after the fix.

A separate test checks that an invented approval ID causes
`ExecuteAction` to return `action.ErrApprovalRequired` without running
the executor. Both compiler and runtime tests are needed: the compiler
tests passed even when the reflection bypass existed.

### Permissions

The framework resolves roles from `permission.Policy`, keyed by
`Actor.ID`. Role membership belongs to the policy and is assigned through
`AssignRole`. The permission check does not trust `Actor.Roles` supplied
in the action context.

The unit test in `pkg/kiff/permission/permission_test.go` checks that
submitting `Roles: ["admin"]` gives
an actor no admin permissions unless the policy assigned that role.
`Actor.Roles` remains descriptive metadata for audit and display.

### Host responsibilities

The host authenticates the actor through a session, API-key record or
identity-provider claim. It supplies that identity and its assigned
roles to the policy. KIFF then validates state, parameters, permissions
and approval in that order, then any aggregate limits. The aggregate
check runs last, after the action contract accepts the request. If the
host accepts a forged identity or
assigns excessive permissions, the framework cannot correct that mistake.

The runtime also requires explicit executors (`ErrExecutorMissing` on
a missing executor), uses atomic counters and random bytes for audit IDs,
and validates a supplied domain definition before processing events.

---

<a id="4-mechanics-not-semantics"></a>
## 4. Domain definitions

Each application defines the business vocabulary its contracts use:

- entity types, such as `Order`, `Claim` or `Service`;
- event types, such as `ORDER_PAID` or `CLAIM_FILED`;
- states, such as `PAID`, `UNDER_REVIEW` or `RESOLVED`;
- action contracts, such as `REFUND_ORDER` or `RESTART_SERVICE`;
- permission identifiers, such as `orders.refund`.

KIFF provides the record formats and validation sequence. Events carry
an ID, type, entity, source, actor, timestamp, metadata and payload.
Transitions update state; contracts define action requirements;
approval records track review; audit records link the resulting steps.

A domain must supply its own eligibility rules. For example, a refund
contract needs payment-state checks, while an outreach contract may
require verified consent. Sharing the runtime does not establish those
rules or make one domain's checks sufficient for another.

---

<a id="5-evidence-what-we-built"></a>
## 5. Implementation and tests

The repository provides the following implementations, examples and
tests for the version covered by this paper.

### 5.1 The framework

`pkg/kiff/` contains the core records, runtime, store interfaces, HTTP
API, observability wrapper and test helpers. The Postgres backend uses
`pgx/v5`; applications can use the in-memory and file-backed stores
without that backend.

The `event.Store`, `decision.Store`, `approval.Store` and `audit.Store`
interfaces have in-memory, JSONL and Postgres implementations. The shared
conformance suite checks ordering, filtering, payload round-trips,
upserts, validation rejection and context cancellation.

### 5.2 The refund demo

`examples/refund-agno/` runs an agent against a mock order database with
the same prompts, model and fixture in two configurations.

**Direct tool access.** The agent's `refund_order` tool mutates the mock
database. A $999 refund on an unpaid order succeeds because the tool
does not check payment state.

**Through KIFF.** The tool sends its request to an HTTP server wrapping
`pkg/kiff/runtime`. Small refunds (≤ $100) use `AUTO_REFUND` and execute
immediately when their checks pass. Larger refunds use `REFUND_ORDER`,
which requires approval. The runtime returns `approval_required`;
after a human grants approval, the agent can repeat the request and
execute it.

The timeline records the proposal, validation, approval and execution.
Its rebuild check reports `materialized = REFUNDED`,
`replayed = REFUNDED`, `events = 3 ✓`.

`agent.OfflineProvider` makes the fixture deterministic and runnable
without an LLM API key. With credentials configured, the same example
can use AWS Bedrock. Run it with `make demo`; the example's README
includes sample output.

### 5.3 The breadth demo

`examples/support-ops/` processes five support tickets with five tools:

| Ticket | Tool | Outcome | Reason |
|---|---|---|---|
| 1 | `issue_refund` | executed | small amount, no approval needed |
| 2 | `issue_refund` | approval_required → granted → executed | over the cap, granted on review |
| 3 | `send_outreach` | blocked_consent_missing | structural rejection before any approval is opened |
| 4 | `escalate_to_human` | executed | escalation never needs approval |
| 5 | `close_ticket` | executed | only legal in `RESOLVED` |

The `SEND_OUTREACH` validator rejects missing or false
`consent_verified` before opening an approval request. This shows the
order of eligibility and approval checks: review cannot make an
ineligible request valid.

### 5.4 The conformance suite

`pkg/kiff/store/storetest/` defines the persistence tests shared by the
backends. To check a new backend, implement the four store interfaces,
provide factories for clean instances and run the suite. Passing it
establishes agreement on the behaviors covered by its test cases.

### 5.5 The Postgres backend

`pkg/kiff/store/postgres/` uses four tables: `kiff_events`,
`kiff_decisions`, `kiff_approvals` and `kiff_audit`. Payloads use `JSONB`,
and connections use `pgx/v5/pgxpool`.

The conformance tests use `KIFF_POSTGRES_TEST_URL` and have been run
against `postgres:16-alpine`. Default `go test ./...` does not require
a running database. The schema uses `CREATE TABLE IF NOT EXISTS`;
operators manage production migrations separately.

### 5.6 The operator surface

`pkg/kiff/httpapi` serves read-only HTML at `/admin` and
`/admin/entities/{id}`. These pages list pending approvals and entity
timelines, including denials and failures. Production deployments must
protect access to them.

The inspection command `kiff timeline -base <url> -entity <id>` reads
`/entities/{id}/timeline` and `/demo/rebuild` from a KIFF server. It
displays audit records and the state rebuild result in a terminal table.

### 5.7 What the evidence establishes

The examples exercise the proposal, validation, approval and execution
sequence. The conformance suite compares three persistence backends,
and the adversarial fixtures test specific attempts to bypass approval.
The approval bypass demonstrates why compile-time restrictions need
runtime checks and tests.

The demos cover e-commerce and support workflows. `cookbook/` also
contains seven runnable recipes with domain tests, including
`insurance-claims-triage`, `healthcare-prior-auth` and
`cloud-infra-remediation`. Those tested recipes and the hypothetical
examples in section 1 do not establish sector readiness or regulatory
compliance. Passing the documented tests also does not establish safety
for every integration or attack. An adopter must test its own contracts
and execution paths.

---

<a id="6-honest-limits"></a>
## 6. Limits

The framework version covered here leaves these responsibilities to
the application or supporting infrastructure.

**Durable execution.** A workflow engine must handle steps that survive
crashes or coordinate multiple processes over long periods. KIFF can
check whether each step is allowed; it does not provide that engine's
scheduling and recovery mechanisms.

**Multi-tenant identity.** The framework supplies actors and permissions
without an organization, project or tenant model. Its HTTP API requires
an `Authenticator`, whose principal overrides the actor in the request
body. The host must implement tenancy and identity management.

**Distributed state.** The runtime assumes a single authoritative state
per entity. Replication, regional sharding and eventual consistency
require an appropriate state backend and application design.

**Event ordering across producers.** A store preserves insertion order.
Producers must handle cross-source ordering, clock skew and duplicate
delivery. This does not provide exactly-once delivery across systems.

**Observability.** `pkg/kiff/observability` wraps the audit store with
`slog` and counters. Applications must add tracing, spans and metric
histograms when they need them.

**Audit integrity.** Framework stores are append-only through their
interfaces. They do not provide hash chains or signatures, so an
operator with direct store access can rewrite records. Tamper evidence
requires an additional signing or verification mechanism.

**Time-of-check to time-of-use.** There is no lease, version check or
compare-and-swap between a decision and execution. Idempotency covers
an identical retried request; it does not serialize different proposals
against the same entity. Applications must account for state changes
during that interval.

---

<a id="7-where-this-goes"></a>
## 7. Future work

Further work includes evaluating additional persistence backends and
documenting the protocol independently of its Go types. SQLite could
support single-binary deployments, while
DynamoDB could serve AWS-based applications. These are possible
extensions, not capabilities established by the examples in this paper.

The framework's interfaces and the documents in `docs/architecture.md`
and `docs/conventions.md` currently describe its contract. A separate
protocol specification would make that contract easier to implement
in another language. Adoption and additional domain tests should inform
that work.

---

## Appendix A: the action contract

Core fields from `pkg/kiff/action/action.go`:

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

- **`Name`** identifies the action in audit records, HTTP routes and
  proposal payloads.
- **`AllowedStates`** lists the states from which it may run. The
  runtime returns `action.ErrStateNotAllowed` for any other state,
  before checking parameters, permissions or approval.
- **`RequiredParameters`** lists values that must be present and
  non-nil. Missing values produce `action.ErrMissingParameter`.
  Domain validators must check any additional requirements on those values.
- **`RequiredPermissions`** lists permissions resolved through
  `permission.Policy`. A missing permission produces
  `action.ErrPermissionDenied`.
- **`Risk`** supplies metadata for reporting and downstream tooling;
  it does not change the runtime's validation sequence.
- **`ApprovalRequirement`** is `ApprovalNever` or `ApprovalRequired`.
  An approval-required action returns `action.ErrApprovalRequired`
  until the runtime resolves a granted record.
- **`Executor`** performs the side effect after validation and returns
  an `ActionResult`, including any follow-up events. The executor must
  handle failures and any safeguards required by the external system,
  including changes since validation.

---

<a id="appendix-b-what-kiff-is-not"></a>
## Appendix B: integration boundaries

**Agent frameworks** provide model access, prompts, tools and agent
execution. An agent built with LangGraph, Agno, OpenAI Agents SDK or
another harness can submit actions to KIFF for validation.

**Workflow engines** provide durable execution, retries, timers and
recovery across steps. They can use KIFF to validate individual steps.

**Agent hosting** runs the agent's code and manages its sessions and
infrastructure. The framework does not deploy or orchestrate agents.

**Model gateways** route requests between model providers and may shape
or cache them. KIFF's action validation does not require it to route
model requests.

**Application frontends** provide the user interface. Beyond the
read-only operator pages, applications build their own frontend against
the JSON HTTP API.

---

*This document is part of the KIFF Framework, MIT-licensed, at
[github.com/kiff/kiff](https://github.com/kiff/kiff).
The whitepaper itself is released under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).*

*Comments, corrections, and counterexamples welcome at
hello@kiff.dev or as issues on the repository.*
