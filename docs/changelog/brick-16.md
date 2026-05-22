# Brick 16 - Context Threading and JSON Tags

Brick 16 makes the runtime safe to embed in real services and stable to expose over transport.

## Context Threading

Every public `Runtime` method now accepts `context.Context` as its first argument:

- `IngestEvent(ctx, event)`
- `IngestRaw(ctx, rawInput)`
- `ProposeDecision(ctx, decision)`
- `RecordActionProposal(ctx, proposal)`
- `ValidateActionProposal(ctx, proposal, currentState, actor, contract)`
- `ValidateAction(ctx, actionCtx, contract)`
- `ExecuteAction(ctx, actionCtx, contract)`
- `RequestApproval(ctx, approvalID, actionCtx, contract, reason)`
- `ReviewApproval(ctx, approvalID, reviewedBy, status, reason)`
- `RecordApproval(ctx, approval)`
- `AllowedActions(ctx, entityID)`
- `Timeline(ctx, entityID)`
- `RebuildState(ctx, entityID)`

Context is threaded through to event, state, decision, approval, and audit stores, the action validator, action executors, and adapter `Normalize` calls. Callers can cancel a request, set a deadline, and propagate request-scoped values.

The HTTP handler uses `r.Context()` for every runtime call, so cancellation propagates from the HTTP server through the runtime to the stores.

## JSON Struct Tags

Core types serialized through the optional HTTP API carry stable `snake_case` JSON tags:

- `event.Event`, `event.Metadata`
- `state.State`, `state.ReplayResult`, `state.ReplayStep`
- `actor.Actor`
- `evidence.Ref`
- `decision.Decision`
- `approval.Approval`
- `audit.Record`
- `action.ActionResult`
- `adapter.RawInput`

External clients can rely on field names. Renaming a Go field will not break HTTP consumers as long as the JSON tag is preserved.

## Why

KIFF is meant to embed inside real Go services. Without `context.Context`, callers cannot enforce deadlines or cancellation. Without JSON tags, the HTTP API is fragile to internal renames. Both gaps were called out in the v0.1 audit.
