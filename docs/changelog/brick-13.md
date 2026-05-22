# Brick 13 - HTTP Approval Routes

Brick 13 exposes the approval lifecycle through the optional HTTP API.

Approval remains a KIFF protocol primitive, not a UI workflow. The HTTP layer only gives applications a narrow transport surface for:

- requesting approval for an action that requires human authority;
- listing approvals for an entity;
- granting a pending approval;
- denying a pending approval.

## Routes

Brick 13 adds:

```text
POST /entities/{entityID}/actions/{actionName}/approvals
GET /entities/{entityID}/approvals
POST /approvals/{approvalID}/grant
POST /approvals/{approvalID}/deny
```

## Requesting Approval

Requesting approval still resolves:

- the current state from the state machine;
- the action contract from the runtime catalog;
- permissions through the configured permission policy;
- required parameters through action validation.

The request body provides:

- actor;
- approval id;
- parameters;
- reason.

The handler validates the action first. Only the approval requirement is allowed to remain unresolved. If state, permissions, or parameters are invalid, the approval request fails.

## Reviewing Approval

Granting or denying approval requires:

- an existing pending approval;
- a reviewer actor id;
- a final status of granted or denied.

The runtime preserves the approval identity, records the reviewer and review time, and appends the normal approval audit record.
If the reviewer provides a reason, it becomes the approval record reason.

## Boundary

HTTP does not decide who is authenticated or which human is allowed to review. Production applications should wrap these handlers with their own authentication, tenancy, and edge authorization.

KIFF records the authority decision once it reaches the runtime.

## Non-Goals

Brick 13 does not add:

- approval assignment queues;
- notification delivery;
- auth middleware;
- role-based reviewer enforcement at the HTTP edge;
- approval UI;
- external workflow engines.

The goal is a small local HTTP bridge for the approval protocol already present in the runtime.
