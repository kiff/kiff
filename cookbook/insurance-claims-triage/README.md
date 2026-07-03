# Insurance Claims Triage Agent

This recipe shows how KIFF can let a claims agent move a real insurance claim
forward without letting it issue suspicious or high-value payouts by itself.

## What This Enables

An insurer can launch an agent that handles evidence gathering, coverage checks,
risk triage, and payout preparation. The actual money movement remains behind
KIFF state, permission, approval, and idempotency checks.

```text
Raw claim input
  -> CLAIM_RECEIVED
  -> shared claim state
  -> agent proposes next action
  -> KIFF validates action contract
  -> service or human-owned action executes
  -> audit records every step
```

## Workflow

```text
CLAIM_RECEIVED -> RECEIVED
  REQUEST_EVIDENCE -> WAITING_EVIDENCE
  RECORD_EVIDENCE -> RECEIVED
  VERIFY_COVERAGE -> COVERAGE_VERIFIED
  ASSESS_RISK -> LOW_RISK_READY | REVIEW_REQUIRED
  PREPARE_LOW_VALUE_PAYOUT -> PAYOUT_PREPARED
  ISSUE_LOW_VALUE_PAYOUT -> PAID
  ISSUE_APPROVED_PAYOUT -> PAID
  DENY_CLAIM -> DENIED
```

## Action Boundary

The claims agent can propose and execute operational preparation actions:

- request missing evidence
- record received evidence
- verify coverage facts
- assess claim risk
- prepare a low-value payout instruction
- hold a claim for adjuster review

The claims service owns payout execution:

- `ISSUE_LOW_VALUE_PAYOUT` can only run from `PAYOUT_PREPARED`
- `ISSUE_APPROVED_PAYOUT` can only run from `REVIEW_REQUIRED`
- the service actor must hold the payout permission in KIFF's policy-owned role
  assignments
- the gateway uses an idempotency key to prevent duplicate payout issuance

The adjuster owns human authority:

- high-risk or high-value claims require a granted approval before payout
- approval review is wrapped by the recipe helper so only an actor with
  `claims.review_payout_approval` can grant it

## Run It

```bash
go run ./cookbook/insurance-claims-triage/cmd/claims-demo
go test ./cookbook/insurance-claims-triage/...
```

The demo follows a high-risk claim: the agent verifies coverage and assesses
risk, KIFF routes the claim to `REVIEW_REQUIRED`, the payout attempt is blocked
until adjuster approval, and the claims service then issues the payout.

## What To Notice

- The state machine is complete enough to avoid terminal-state confusion:
  `PAID` and `DENIED` expose no follow-up actions.
- The agent cannot self-issue a payout by adding a service role to its submitted
  actor. KIFF resolves authority from the permission policy, not caller-provided
  role metadata.
- The low-value payout executor still validates amount thresholds, even after
  the action is allowed by state and permission.
- The high-risk payout path leaves audit records for the failed approval gate,
  approval request, approval grant, action execution, and state transition.
