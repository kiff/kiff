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

## Wave 1 Recipes

### Accounts Payable Payout Agent

Status: implemented in [`accounts-payable-payout`](./accounts-payable-payout).

Shows a Claude Haiku AP agent that verifies invoices and proposes payment
release while KIFF keeps money movement behind state, permissions, approval, and
idempotency.

### Industrial Shutdown / Field Dispatch Agent

Status: prototype exists in the local sandbox.

Shows physical-world risk: an agent can triage a machine incident and propose a
shutdown, while KIFF pauses critical action for human authority and prevents
unsafe follow-up actions after the line is already shut off.

### Insurance Claims Triage Agent

Status: implemented in [`insurance-claims-triage`](./insurance-claims-triage).

Shows claim intake, evidence requests, coverage verification, fraud/risk
scoring, low-value payout preparation, service-only payout execution, high-risk
approval holds, and idempotent payout issuance.

### Healthcare Prior Authorization Coordinator

Status: implemented in [`healthcare-prior-auth`](./healthcare-prior-auth).

Shows evidence collection, payer criteria checks, missing clinical documentation
requests, service-owned payer portal submission, clinician approval for
ambiguous or denial-risk cases, idempotency, and replayable state.

## Later Recipe Candidates

- Customer support refund agent
- Vendor onboarding and bank-detail change agent
- Cloud infrastructure remediation agent: implemented in
  [`cloud-infra-remediation`](./cloud-infra-remediation).
- Legal contract intake agent
- Security incident response agent
- Loan application processing agent
- Procurement purchase-order approval
- Data deletion/privacy request workflow
