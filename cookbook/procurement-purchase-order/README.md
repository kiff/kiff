# Procurement Purchase-Order Agent

This recipe shows how KIFF can let a procurement agent move purchasing requests
forward without giving it direct authority to create spend in the ERP.

The consequential side effect is creating a purchase order. The agent can
collect quote facts, check budget status, classify purchase risk, and prepare a
standard PO. The ERP write itself is service-owned, and high-value, new-vendor,
sole-source, or security-sensitive purchases require procurement-manager
approval.

## What This Enables

Procurement teams can launch an agent that shortens quote-to-PO cycle time while
keeping spend creation behind state, typed parameters, permissions, dynamic
approval policy, segregation of duties, runtime idempotency, and audit.

```text
Purchase request
  -> normalized event
  -> shared request state
  -> agent proposal
  -> typed KIFF action validation
  -> dynamic approval decision
  -> ERP service execution
  -> lifecycle view, audit trail, and replay
```

## Workflow

```text
PURCHASE_REQUEST_RECEIVED -> RECEIVED
  ATTACH_QUOTE -> QUOTE_ATTACHED
  CHECK_BUDGET -> BUDGET_VERIFIED
  ASSESS_PURCHASE_RISK -> LOW_RISK_READY | REVIEW_REQUIRED
  PREPARE_STANDARD_PO -> PO_PREPARED
  CREATE_STANDARD_PO -> ORDERED
  CREATE_APPROVED_PO -> ORDERED
  REJECT_PURCHASE_REQUEST -> REJECTED
```

## Action Boundary

The procurement agent can:

- attach supplier quote and sourcing facts
- check budget and security-review requirements
- assess whether a purchase is standard or review-required
- prepare a standard purchase order
- hold or reject the request

The ERP purchasing service owns PO creation:

- `CREATE_STANDARD_PO` runs only from `PO_PREPARED`
- `CREATE_APPROVED_PO` runs only from `REVIEW_REQUIRED`
- service authority comes from KIFF's permission policy, not caller-submitted
  actor roles
- `ActionContext.IdempotencyKey` prevents retries from creating duplicate POs

The procurement manager owns high-risk authority:

- high-value spend, new vendors, sole-source purchases, unavailable budget, or
  security review requirements dynamically require approval
- `ReviewApprovalAs` enforces reviewer permission and segregation of duties
- invalid currencies and malformed amounts are rejected before executor code

## Run It

```bash
go run ./cookbook/procurement-purchase-order/cmd/procurement-demo
go test ./cookbook/procurement-purchase-order/...
```

The demo follows a high-value new-vendor SaaS purchase. The agent gathers quote
and budget facts, KIFF routes the request to `REVIEW_REQUIRED`, blocks ERP PO
creation until a procurement manager approves, executes through the ERP service,
and then proves a retry is deduplicated by the runtime.

The demo also prints `Runtime.EntityLifecycle`, which assembles the
decision/proposal, approval record, execution stages, and deduplicated retry into
one read-only view for a UI or API response.
