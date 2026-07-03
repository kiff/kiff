# The side-effect boundary: agents propose, executors own credentials

KIFF's value shows up only when the deployment is wired a specific way: the
agent proposes, and the credentials that actually move money (or touch the ERP,
the cloud API, the payment rail) live behind KIFF, in executors the agent cannot
call directly.

Get this topology right and you can put an agent on work you previously
wouldn't automate. Get it wrong — hand the agent the payment SDK "with a
prompt telling it to be careful" — and KIFF is not in the path at all.

This page is the deployment companion to
[the governed action boundary](./governed-action-boundary.md), which covers how
a single decision is validated, approved, and replayed. Here we cover *where the
credentials live* and *how a proposal reaches a side effect*.

## What KIFF protects

For any consequential call that routes through the runtime:

- **State.** The action is checked against the entity's event-derived current
  state — not a state the caller asserts.
- **Parameters and permissions.** Required parameters must be present; the actor
  must hold the permission under policy-owned role membership (not roles the
  caller puts on the actor it submits).
- **Approval.** High-risk actions do not execute until a real, granted approval
  exists. Approval cannot be self-granted (enforced at compile time — see below).
- **Evidence.** Every decision, approval, execution, and state change is recorded
  and replayable.

## What KIFF does not protect

- **A side effect reached on a path that bypasses the runtime.** If the agent
  holds the payment credential directly, KIFF never sees the call. The boundary
  is only as good as the topology.
- **The conversation layer.** Prompt content, model choice, and tool wiring are
  yours. KIFF starts the moment a tool call is about to become a side effect.
- **Edge authentication.** KIFF resolves *authorization* (does this actor hold
  this permission?) from policy; authenticating *who the caller is* remains the
  host's job at the edge.

## Bad topology: the agent holds the credential

```text
  agent ──(Stripe/ERP/cloud SDK + secret key)──► money moves
     ▲
     └── KIFF is off to the side, auditing after the fact (or not at all)
```

Here the agent can move money directly. A prompt injection, a hallucinated
tool call, or a retry storm goes straight to the side effect. Audit tells you
what happened; it does not stop it. This is the failure mode KIFF exists to
remove — do not ship it.

## Good topology: the proposal is the only path

```text
  agent ──(proposal: action + params, NO credentials)──► your host
                                                            │
                                                            ▼
                                                    KIFF runtime
                                          (state · params · permission · approval)
                                                            │
                                                   allowed? │ only then
                                                            ▼
                                          executor / adapter  ──(holds the secret)──► money moves
                                                            │
                                                            ▼
                                                   audit · replay · timeline
```

- The agent receives context and returns a **proposal** — an action name plus
  parameters. It never receives the payment/cloud/ERP credential.
- The host passes the proposal into KIFF (in-process, or over the HTTP API — a
  proposal is a single POST, so the agent can be in any language).
- KIFF validates state, parameters, permissions, and approval. Only an allowed
  action reaches the executor.
- The **executor (or adapter) is the only component that holds the side-effect
  credential**, and it runs only after the runtime says allowed.

The rule in one line: **the secret lives with the executor, never with the
agent.** The agent's most powerful move is to *ask*.

## How to prove the agent cannot bypass KIFF

This isn't a promise on a slide — it's enforced and tested in-repo:

- **Self-approval is a compile error, from outside the module.**
  `pkg/kiff/action/approval_boundary_compile_test.go` runs `go build` against
  external-module fixtures and asserts they fail to compile:
  `action.ActionContext{approved: true}` fails (`approved` is unexported), and
  `ctx.GrantApproval(trust.Grant{})` fails (the capability type lives in an
  `internal` package a caller can't import). An agent's SDK literally cannot
  express a self-approval.
- **The executor never runs on a non-allowed path.**
  `pkg/kiff/runtime/boundary_test.go` proves a spy executor stays uncalled on
  wrong-state, missing-parameter, permission-denied, approval-not-granted, and
  denied-approval paths — and runs only on the allowed path.
- **Permissions come from policy, not the caller.** The permission check
  resolves roles from policy-owned membership keyed by actor ID, not from
  `Actor.Roles` on the submitted context, so an agent cannot self-grant a
  permission by decorating the actor it sends.

For your own domain, add the same style of test: submit a proposal as the agent
actor with no approval and assert the executor's side effect never fires, and
assert a repeated/terminal action is refused before the executor runs.

## Why this is the product story

The strongest reason to adopt KIFF is not after-the-fact audit — it is
**enablement**: teams can launch agentic workflows they previously wouldn't,
because the risky action is bounded, approved when it must be, and replayable.
Audit and replay are the trust layer underneath, not the headline.
