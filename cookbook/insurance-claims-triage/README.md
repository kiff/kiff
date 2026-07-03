# Insurance Claims Triage Agent

This cookbook recipe is planned as the next flagship example after Accounts
Payable Payout.

## What It Should Enable

An insurer can let a real agent help move claims forward without letting it
approve suspicious or high-value payouts by itself.

## Proposed Workflow

```text
CLAIM_RECEIVED
  -> REQUEST_EVIDENCE -> WAITING_EVIDENCE -> EVIDENCE_RECEIVED -> CLAIM_RECEIVED
  -> VERIFY_COVERAGE -> COVERAGE_VERIFIED
  -> ASSESS_RISK -> LOW_RISK_READY | REVIEW_REQUIRED
  -> PREPARE_LOW_VALUE_PAYOUT -> PAYOUT_PREPARED
  -> HOLD_FOR_ADJUSTER -> REVIEW_REQUIRED
  -> APPROVE_PAYOUT -> PAYOUT_APPROVED
  -> ISSUE_PAYOUT -> PAID
  -> DENY_CLAIM -> DENIED
```

## Action Boundary

The agent should be able to propose:

- requesting evidence
- verifying coverage
- assessing claim risk
- preparing a low-value payout
- holding for adjuster review
- denying or approving only within configured authority

Only KIFF-owned executors should be allowed to:

- issue payout
- mark claim denied
- record adjuster approval
- write to the claims system of record

## Robustness Goals

- Evidence completeness checks
- Policy/coverage matching
- Low-value payout threshold
- Fraud/suspicion signals
- Human adjuster approval
- Duplicate payout prevention
- Clear terminal states
