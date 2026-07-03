# Vendor Bank-Detail Change Agent

This recipe shows how KIFF can let a vendor-risk agent move supplier bank-detail
changes forward without giving it direct authority to update the vendor master.

The consequential side effect is changing where vendor payments will go. The
agent can collect evidence, verify identity, assess risk, and prepare low-risk
known-account changes. The vendor-master update itself is service-owned, and new
or suspicious bank details require finance approval.

## What This Enables

An AP or vendor operations team can launch an agent that shortens vendor
maintenance cycle time while keeping the fraud-sensitive write behind state,
typed parameter validation, permission checks, approval, segregation of duties,
and idempotency.

```text
Bank-change request
  -> normalized event
  -> shared change state
  -> agent proposal
  -> typed KIFF action validation
  -> service or finance-owned execution
  -> audit trail
```

## Workflow

```text
BANK_CHANGE_REQUESTED -> RECEIVED
  ATTACH_EVIDENCE -> EVIDENCE_ATTACHED
  VERIFY_VENDOR -> VENDOR_VERIFIED
  ASSESS_BANK_CHANGE -> LOW_RISK_READY | REVIEW_REQUIRED
  PREPARE_KNOWN_ACCOUNT_CHANGE -> CHANGE_PREPARED
  APPLY_KNOWN_ACCOUNT_CHANGE -> UPDATED
  APPLY_APPROVED_BANK_CHANGE -> UPDATED
  REJECT_BANK_CHANGE -> REJECTED
```

## Action Boundary

The vendor-risk agent can:

- attach evidence from the vendor portal
- verify the vendor identity and callback
- assess whether the bank change is low risk
- prepare a known-account update
- hold or reject the change

The vendor-master service owns the write:

- `APPLY_KNOWN_ACCOUNT_CHANGE` runs only from `CHANGE_PREPARED`
- `APPLY_APPROVED_BANK_CHANGE` runs only from `REVIEW_REQUIRED`
- service authority comes from KIFF's permission policy, not caller-submitted
  actor roles
- idempotency prevents duplicate vendor-master writes

The finance controller owns high-risk authority:

- new accounts, high exposure, or fraud signals require approval
- `ReviewApprovalAs` enforces the reviewer permission and segregation of duties
- invalid country and malformed risk fields are rejected before executor code

## Run It

```bash
go run ./cookbook/vendor-bank-change/cmd/vendor-bank-demo
go test ./cookbook/vendor-bank-change/...
```

The demo follows a new-bank-account change. The agent attaches evidence,
verifies the vendor, and assesses risk. KIFF routes the change to
`REVIEW_REQUIRED`, blocks the vendor-master write until finance approval, and
then applies the change through an idempotent gateway.

## What To Notice

- `UPDATED` and `REJECTED` are terminal states.
- The agent cannot self-apply the bank change by adding the service role to its
  submitted actor.
- A requester cannot approve its own approval, and a reviewer without
  `vendors.review_bank_change` cannot approve it.
- The replay test rebuilds final state from the event log.
