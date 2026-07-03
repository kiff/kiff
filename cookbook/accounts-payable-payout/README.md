# Accounts Payable Payout Agent

This cookbook recipe is a money-touching KIFF example. It is intentionally
larger than a one-step toy payment demo because the point is to show how KIFF
helps launch an agentic workflow that would otherwise be too risky to automate.

It models an AP workflow where a Claude Haiku agent can prepare and propose
work, but only KIFF-owned action executors can release money.

## Run It

Run the tests:

```bash
go test ./cookbook/accounts-payable-payout/...
```

Run the deterministic demo without AWS credentials:

```bash
go run ./cookbook/accounts-payable-payout/cmd/payables-demo
```

Run the local UI:

```bash
go run ./cookbook/accounts-payable-payout/cmd/payables-ui
```

The UI listens on `http://127.0.0.1:8790` and uses a real Bedrock Claude Haiku
agent by default with inference profile
`us.anthropic.claude-haiku-4-5-20251001-v1:0`.

## Workflow

```text
INVOICE_RECEIVED
  -> REQUEST_MISSING_INFO -> NEEDS_INFO -> RECORD_INFO_RECEIVED -> RECEIVED
  -> VERIFY_INVOICE -> VERIFIED
  -> MARK_READY_FOR_PAYMENT -> READY_FOR_PAYMENT
  -> RELEASE_LOW_RISK_PAYMENT -> PAID
  -> HOLD_FOR_APPROVAL -> PAYMENT_HELD -> RELEASE_APPROVED_PAYMENT -> PAID
  -> REJECT_INVOICE -> REJECTED
```

Terminal states are `PAID` and `REJECTED`.

## What KIFF Owns Here

- normalized invoice events
- invoice lifecycle state
- action contracts and allowed states
- permission checks
- approval requirement for high-risk release
- action execution records
- follow-up events and timeline reconstruction

## What The Host App Must Own

- the agent adapter and prompt
- invoice fact extraction and enrichment
- semantic payment validation
- credentials for the payment gateway
- idempotency keys
- UI and operator workflow

The agent is intentionally not the actor that releases funds. Payment release
runs as `payment-service`, and only that actor has payment release permission.

## Design Recommendation

For production, do not give the agent credentials to the payment processor,
banking API, ERP write API, or ledger. The agent should only return proposals.
The host should pass proposals into KIFF, and the KIFF executor should be the
only component with side-effect credentials.

Use stronger parameter validation for real deployments:

- amount bounds
- currency enum
- vendor/account matching
- bank fingerprint checks
- duplicate invoice detection
- invoice/PO/receipt matching
- idempotency keys
- segregation of duties for approvals

This example uses an in-memory ledger gateway to prove the boundary without
touching real money.

## What This Enables

Without KIFF, a team has two uncomfortable choices:

- keep the AP agent advisory-only, so humans still do all operational work; or
- give the agent direct payment/ERP credentials, which is too risky.

With KIFF, the agent can participate in the workflow while payment release stays
behind explicit state, permission, approval, and idempotency boundaries.
