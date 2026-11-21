<p align="center">
  <img src="./docs/assets/kiff-logo.png" alt="KIFF" width="176">
</p>

<h1 align="center">KIFF</h1>

<p align="center"><strong>Decide what an AI agent may do before it does it.</strong></p>

<p align="center">
  <a href="https://github.com/kiff/kiff/actions/workflows/ci.yml"><img src="https://github.com/kiff/kiff/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/kiff/kiff"><img src="https://pkg.go.dev/badge/github.com/kiff/kiff.svg" alt="Go Reference"></a>
</p>

KIFF is an open-source Go engine that decides each consequential action an AI
agent proposes, before your system runs it. An agent, a person or a service
proposes a named action, such as refunding an order. KIFF checks it against the
current state of that order, the actor's permissions, the parameters, any
approval the action requires, and how much that actor may still do in total. It
returns `allowed`, `approval_required`, `blocked` or `invalid`, and records the
decision in one replayable history. Your executor runs only what was allowed.

The rules live in an **operational domain**. Events establish state. State
determines which named actions are valid. Permissions, approvals and limits
determine who may do what, and how much. Execution results return to the same
history, so every outcome can be explained and replayed.

KIFF does not replace your databases, your agent framework or your systems of
record. It sits in front of the consequential calls into them.

## Try It

```bash
go install github.com/kiff/kiff/cmd/kiff@latest
kiff new -scenario refund github.com/acme/refunds
cd refunds
go mod tidy
make demo
```

That creates a runnable refund domain with an `Order`, a `MARK_PAID` action, an
approval-gated `REFUND_ORDER` action, a headless HTTP API, and a demo script.
Use `kiff new <module>` without `-scenario` for a smaller starter project.

## A refund, step by step

```text
order-2 is PAID

  agent → REFUND_ORDER  $999, high-risk
  KIFF  ⏸ HELD — approval required            the executor did NOT run
  human → approves
  KIFF  ✓ ALLOWED — refund executes → REFUNDED

  agent → REFUND_ORDER  (same call, retried)
  KIFF  ✗ BLOCKED — order is already REFUNDED  refused before money moves again

  replay from events alone → REFUNDED          materialized == replayed
```

The useful action runs. The risky one waits for a human. The path rebuilds
from the event log. (Refusing the repeat is table stakes; it's shown because
the boundary reads state.)

## The Check No Per-Call Check Can Make

Your agent is probably already bounded. Whatever gave it the refund tool scoped
what it may call, from which state, with which parameters — and it is right
every time it runs. It runs once per call, so ten correct refunds is ten correct
decisions.

```text
a limit: refund-agent may refund €1,100 per calendar day

  agent → AUTO_REFUND  €42     ✓ ALLOWED   €1,058 left
  agent → AUTO_REFUND  €990    ✓ ALLOWED   €68 left
  agent → AUTO_REFUND  €88     ✗ REFUSED   limit_reached

the third is in the right state, with the right permission, valid
parameters, under the approval threshold, and smaller than the two that
just went through. Nothing is wrong with it. The day is spent.
```

That refusal cannot come from checking one call, because no single call can see
the other two. [`pkg/kiff/limit`](./pkg/kiff/limit) holds the ledger that can.
It is the same check behind a [KIFF Card](#kiff-cards).

The rules, the wiring and five properties to know before you rely on it:
[docs/limits.md](./docs/limits.md). Run the live tour: `go run ./cmd/kiff-tour`.

## KIFF Cards

On [kiff.dev](https://kiff.dev), this engine checks an agent's **Card**: the
authority a business gives one agent. A Card says which actions the agent may
take, how much per action and in total over a window, and what happens to a
call outside it (it waits for the owner, or it is refused). The owner changes
or revokes the Card without changing the agent.

## One CLI, End to End

The `kiff` CLI stays with the project from the first domain to a running
system:

| Workflow | What it gives you | Commands |
| --- | --- | --- |
| Start | A runnable KIFF project, with an optional working scenario | `kiff new` |
| Generate | A typed Go domain from a JSON descriptor | `kiff scaffold` |
| Validate | Structural domain checks and static analysis for unguarded Go tools | `kiff verify`, `kiff scan` |
| Investigate | A readable action, approval, and outcome history for one entity | `kiff timeline` |
| Publish | Authentication and domain contract deployment to KIFF Cloud | `kiff auth`, `kiff apply` |
| Operate | Views of domains, runtimes, governed usage, and active API keys | `kiff domains`, `kiff runtimes`, `kiff usage`, `kiff keys` |

Run `kiff help` for the full command list or `kiff <command> -h` for flags.

## Connect an Agent

If you already have an agent, you can put its tool calls in front of KIFF
without restructuring it. The guard SDK connects KIFF to
the pre-execution seam your framework already exposes:

```bash
pip install kiff-guard          # or: npm install @kiff/kiff-guard
```

Mode `observe` records; `enforce` refuses before the call runs.

Adapters for 11 Python and TypeScript agent frameworks, each with a quickstart,
or plain HTTP with no adapter: [kiff-guard](https://github.com/kiff/kiff-guard#readme) (MIT).

## Run It Yourself or Hosted

The framework and guard SDK are MIT. Self-host your operational domain with the
`postgres` store and your own HTTP stack. Or use [KIFF Cloud](https://kiff.dev),
which runs the state, approvals, receipts and retention for you: `kiff auth
login`, then `kiff apply`. Nothing in this repository requires it.

| | This repository | Hosted |
| --- | --- | --- |
| Decision boundary, approvals, replay | yes | same code |
| Aggregate limits | yes (in-process ledger) | issued as KIFF Cards: shared ledger, statements, Cards shared by several agents, owner approval of held calls, and a management credential separating who may act from who sets how much |
| Audit records | append-only, no hash, no signature | each record's data hashed into a causal chain, the chain signed |
| Tenancy, billing, retention | not attempted | the product |
| Lease between a decision and the executor | open | **also open** |

## Documentation

- [Check the agent boundary](./docs/scan.md) — `kiff scan` for Go agent repositories, and kiff-scan for Python
- [Why KIFF](./docs/why.md) — why agents, people, and services need one domain to act against
- [The governed action boundary](./docs/governed-action-boundary.md) — how decisions, approvals, and replay work
- [The side-effect boundary](./docs/side-effect-boundary.md) — deployment topology: agents propose, executors own credentials
- [Limits](./docs/limits.md) — what an actor may do in total, and why no per-call check can answer it
- [Cookbook guide](./docs/cookbook-guide.md) — choose, evaluate, and adapt a governed agent recipe
- [Build a domain](./docs/build-a-domain.md) — the authoring guide, end to end
- [Scaffold from a descriptor](./docs/scaffold-a-domain.md) — generate a domain from JSON
- [Govern over HTTP](./docs/governing-over-http.md) — drive KIFF from TypeScript, Python, or any stack
- [Coding assistant integrations](./docs/assistant-integrations.md) — install the scanner and audit skills in a coding assistant
- [Architecture & packages](./docs/architecture.md) — the package map and responsibilities
- [Philosophy](./docs/philosophy.md) and [Comparisons](./docs/comparisons.md) — what KIFF is, and where it stops

More: [examples/](./examples/) and the [cookbook](./cookbook/).

## Who It Is Not For

If your app is simple CRUD, or direct LLM tool calls with no consequential
state, KIFF is too much structure — ship something smaller. KIFF earns its keep
when agents take consequential actions, what they may do depends on current
state and on how much they have already done, some actions need a human
sign-off, and someone eventually asks "why did this happen?"

## Status

KIFF is at v0.9. The core action boundary is complete and adversarially
tested; the attacks ship as conformance tests that fail the build if it
regresses ([docs/changelog/](./docs/changelog/README.md)).

Known gaps, stated plainly: audit records are durable and append-only but not
tamper-evident in the OSS stores; there is no lease or version check between a
decision and the executor, so a decision can in principle be acted on against
state that has since moved; and the bundled limit ledger is in-process, so two
replicas each enforce the full ceiling and the real total is the sum.
The [Postgres store](./pkg/kiff/store/postgres) is the production reference;
the file-backed JSONL stores are for demos and local development.

## License

MIT.
