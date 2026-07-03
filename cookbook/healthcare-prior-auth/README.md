# Healthcare Prior Authorization Coordinator

This recipe shows how KIFF can let a prior authorization agent move payer
submissions forward without letting it submit ambiguous or denial-risk cases by
itself.

This is not clinical guidance. The recipe models operational coordination:
state, evidence, criteria checks, approval, and payer-portal side effects.

## What This Enables

A care operations team can launch an agent that collects clinical evidence and
checks payer criteria. Clean cases can be prepared and submitted by a service
actor. Ambiguous, incomplete, or high denial-risk cases are routed to clinician
review before any payer submission occurs.

```text
Request intake
  -> normalized event
  -> shared request state
  -> agent proposal
  -> KIFF action validation
  -> service or clinician-owned execution
  -> audit trail
```

## Workflow

```text
AUTH_REQUEST_RECEIVED -> RECEIVED
  REQUEST_CLINICAL_EVIDENCE -> WAITING_CLINICAL_EVIDENCE
  RECORD_CLINICAL_EVIDENCE -> READY_FOR_CRITERIA
  CHECK_POLICY_CRITERIA -> CRITERIA_MET | REVIEW_REQUIRED
  PREPARE_AUTHORIZATION_SUBMISSION -> SUBMISSION_PREPARED
  SUBMIT_AUTHORIZATION -> SUBMITTED
  SUBMIT_REVIEWED_AUTHORIZATION -> SUBMITTED
  WITHDRAW_REQUEST -> WITHDRAWN
```

## Action Boundary

The prior authorization agent can:

- request missing clinical evidence
- record an evidence packet
- check payer criteria
- prepare a submission package
- hold a request for clinician review

The payer portal service owns external submission:

- `SUBMIT_AUTHORIZATION` runs only from `SUBMISSION_PREPARED`
- `SUBMIT_REVIEWED_AUTHORIZATION` runs only from `REVIEW_REQUIRED`
- service authority comes from KIFF's permission policy, not caller-provided
  actor role metadata
- idempotency prevents duplicate submissions

The clinician owns review authority:

- ambiguous or high denial-risk cases require approval before submission
- approval review is wrapped by a recipe helper that checks the clinician review
  permission before calling the runtime approval store

## Run It

```bash
go run ./cookbook/healthcare-prior-auth/cmd/priorauth-demo
go test ./cookbook/healthcare-prior-auth/...
```

The demo follows an ambiguous prior authorization request. The agent records
evidence and checks payer criteria, KIFF routes the request to
`REVIEW_REQUIRED`, the payer submission is blocked until clinician approval, and
the payer portal service then submits through an idempotent gateway.

## What To Notice

- `SUBMITTED` and `WITHDRAWN` are terminal states with no allowed follow-up
  actions.
- The agent cannot submit to the payer portal by adding the portal role to its
  submitted actor.
- Approval is not treated as a note. It is part of the action validation path.
- The replay test rebuilds final state from the event log, proving state follows
  events rather than executor side effects.
