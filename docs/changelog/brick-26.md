# Brick 26 - Governed Launch Cookbook and Action-Safety Primitives

Brick 26 turns KIFF from "core primitives plus small demos" into a framework
with launch-grade examples for the kinds of agent actions teams hesitate to
ship: payouts, payment-detail changes, infrastructure remediation, access
revocation, purchase-order creation, claims payouts, and prior-authorization
submission.

## What Was Added

- Typed action parameters:
  - `action.ParameterSpec` contracts for strings, booleans, integers, enums,
    min/max bounds, required fields, and custom semantic validators.
  - Runtime validation rejects malformed action input before executor code can
    run.

- Dynamic approval policies:
  - Action contracts can decide approval requirements from the current
    `ActionContext`, not only a fixed static setting.
  - Recipes use this for amount thresholds, new vendors, privileged users,
    blast radius, security review requirements, and similar runtime facts.

- Approval reviewer authority:
  - `runtime.ReviewApprovalAs` validates reviewer permission.
  - Segregation of duties prevents the requester from approving their own
    consequential action.

- Runtime idempotency:
  - `idempotency.Store` protects executor retries with reserve, complete,
    release, and replay behavior.
  - Successful retries return the prior result and emit an
    `action_deduplicated` audit record rather than running the side effect
    twice.

- Governed lifecycle view:
  - `runtime.EntityLifecycle` assembles audit records, decisions, and
    approvals into a read-only view for UIs and APIs.
  - The view follows a proposal through validation, approval, execution,
    failure, denial, or deduplication without making each app stitch the records
    together by hand.

- Cookbook recipes:
  - `cookbook/accounts-payable-payout`
  - `cookbook/insurance-claims-triage`
  - `cookbook/healthcare-prior-auth`
  - `cookbook/vendor-bank-change`
  - `cookbook/cloud-infra-remediation`
  - `cookbook/security-incident-response`
  - `cookbook/procurement-purchase-order`

## Why

The framework's value is not that it can audit an agent after the fact. Its
value is that it lets a product team safely launch an agent that would otherwise
be too risky: an agent can propose the useful work, while KIFF validates state,
parameters, permissions, approvals, idempotency, execution, and audit before the
side effect happens.

The cookbook now gives adopters concrete patterns for that boundary across
several domains. Each recipe keeps domain semantics in the recipe and
coordination mechanics in the framework.

## What Still Belongs to the Host App

KIFF still does not own your LLM, queue, HTTP server, identity provider, ERP,
ledger, cloud credentials, payer portal, or production database topology. Those
are host-system concerns. KIFF provides the governed action boundary and the
records needed to explain what happened.

Use `docs/side-effect-boundary.md` for deployment topology, and start with the
top-level `README.md` plus `cookbook/README.md` when evaluating which recipe
maps to your use case.
