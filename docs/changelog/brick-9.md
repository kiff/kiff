# Brick 9 - Execution Results And Effects

Brick 9 makes execution outcomes explicit.

KIFF already separates proposal, validation, approval, and execution. Brick 9 strengthens the execution side of that boundary:

```text
Validation is not execution.
Proposal is not execution.
Execution produces a result.
```

## Execution Result

An action execution result records:

- action name;
- entity id;
- execution status;
- whether execution actually happened;
- message;
- error message when execution failed;
- effects summary;
- output payload;
- execution timestamp.

The status can be:

- `succeeded`
- `failed`
- `skipped`

## Runtime Audit

Runtime audit should record execution result details for both success and failure.

This lets KIFF answer:

- was the action validated;
- was the action executed;
- did execution succeed or fail;
- what effects did execution claim;
- what error occurred.

## Non-Goals

Brick 9 does not add:

- automatic follow-up event emission;
- state transitions from action results;
- retries;
- compensating actions;
- distributed transactions;
- workflow orchestration.

Follow-up event emission is important, but it should be a later brick because it changes how execution relates to state transitions.
