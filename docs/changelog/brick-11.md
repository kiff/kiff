# Brick 11 - Approval Requests

Brick 11 makes pending approval requests explicit.

Before this brick, examples could manually create a pending approval record. That worked, but it made the approval lifecycle feel ad hoc. KIFF should provide a small runtime helper for the common case:

```text
This action requires approval. Create a pending approval request.
```

## Approval Request Semantics

An approval request:

- is only valid for an action contract that requires approval;
- records the affected entity and action;
- records the requesting actor;
- starts in `pending` status;
- includes a reason;
- is stored and audited.

Granting or denying approval remains separate.

## Runtime Flow

The flow is:

```text
ValidateAction -> approval required
RequestApproval -> pending approval recorded
RecordApproval -> granted or denied approval recorded
ExecuteAction -> granted approval allows execution
```

## Non-Goals

Brick 11 does not add:

- approval assignment;
- notifications;
- approval UI;
- escalation rules;
- SLAs;
- workflow orchestration;
- reviewer permission checks.

The goal is the backend protocol primitive for requesting approval.
