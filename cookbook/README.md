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

Status: planned in [`insurance-claims-triage`](./insurance-claims-triage).

Should show claim intake, evidence requests, fraud/risk scoring, low-value
payout preparation, and high-value or suspicious claim holds.

### Healthcare Prior Authorization Coordinator

Status: planned.

Should show evidence collection, policy criteria checks, missing clinical
documentation requests, and human review for ambiguous or denial-risk cases.

## Later Recipe Candidates

- Customer support refund agent
- Vendor onboarding and bank-detail change agent
- Cloud infrastructure remediation agent
- Legal contract intake agent
- Security incident response agent
- Loan application processing agent
- Procurement purchase-order approval
- Data deletion/privacy request workflow
