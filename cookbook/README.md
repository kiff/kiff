# KIFF Cookbook

The cookbook contains runnable recipes for building governed agentic backends
with KIFF. Each recipe should demonstrate a workflow that teams would hesitate
to automate without an explicit action boundary.

The goal is not to show audit for its own sake. The goal is to show what KIFF
lets a product team safely launch.

## Recipe Standard

Each high-quality recipe should answer:

- What previously-too-risky action does this enable?
- What can the agent propose?
- What can only KIFF execute?
- Which states gate the action?
- Which actions require approval?
- What prevents duplicate or unsafe execution?
- What does the host app still need to own?

## Implemented Recipes

### Accounts Payable Payout Agent

[`accounts-payable-payout`](./accounts-payable-payout)

Shows a Claude Haiku AP agent that verifies invoices and proposes payment
release while KIFF keeps money movement behind state, typed parameters,
permissions, approval, lifecycle reconstruction, and audit.

### Insurance Claims Triage Agent

[`insurance-claims-triage`](./insurance-claims-triage)

Shows claim intake, evidence requests, coverage verification, fraud/risk
scoring, low-value payout preparation, service-only payout execution, high-risk
approval holds, and idempotent payout issuance.

### Healthcare Prior Authorization Coordinator

[`healthcare-prior-auth`](./healthcare-prior-auth)

Shows evidence collection, payer criteria checks, missing clinical documentation
requests, service-owned payer portal submission, clinician approval for
ambiguous or denial-risk cases, idempotency, and replayable state.

### Vendor Bank-Change Agent

[`vendor-bank-change`](./vendor-bank-change)

Shows payment-detail changes where an agent can collect vendor evidence and
prepare a change, but finance-controlled execution and approvals govern the
write to vendor banking data.

### Cloud Infrastructure Remediation Agent

[`cloud-infra-remediation`](./cloud-infra-remediation)

Shows incident triage and remediation in cloud infrastructure, including
approval-gated isolation actions and service-owned infrastructure execution.

### Security Incident Response Agent

[`security-incident-response`](./security-incident-response)

Shows identity-risk assessment, low-risk session reset, high-risk access
revocation, dynamic approval policy, reviewer authority, segregation of duties,
and idempotent identity-service execution.

### Procurement Purchase-Order Agent

[`procurement-purchase-order`](./procurement-purchase-order)

Shows quote collection, budget checks, purchase-risk assessment, purchase-order
preparation, ERP-service execution, dynamic manager approval, reviewer
authority, segregation of duties, and idempotent PO creation.

## Feature Map

Start here when evaluating a capability:

- **Money movement:** [`accounts-payable-payout`](./accounts-payable-payout),
  [`insurance-claims-triage`](./insurance-claims-triage)
- **Approval-gated operational writes:**
  [`vendor-bank-change`](./vendor-bank-change),
  [`procurement-purchase-order`](./procurement-purchase-order),
  [`cloud-infra-remediation`](./cloud-infra-remediation)
- **Dynamic approval policy and reviewer controls:**
  [`security-incident-response`](./security-incident-response),
  [`procurement-purchase-order`](./procurement-purchase-order),
  [`cloud-infra-remediation`](./cloud-infra-remediation)
- **Lifecycle view over governed history:**
  [`accounts-payable-payout`](./accounts-payable-payout),
  [`security-incident-response`](./security-incident-response),
  [`procurement-purchase-order`](./procurement-purchase-order)
- **Human/AI/software coordination around shared state:**
  all recipes

## Later Recipe Candidates

- Customer support refund agent
- Industrial shutdown / field dispatch agent
- Legal contract intake agent
- Loan application processing agent
- Data deletion/privacy request workflow
