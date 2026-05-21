# Brick 17 - Trace Correlation in Audit and Follow-Up Events

Brick 17 makes the audit primitive answerable by external integrators. Audit records can now be joined to a specific external request, span, or workflow.

## What Changed

- `audit.Record` gains `TraceID`, `CorrelationID`, and `CausationID` fields with stable JSON tags.
- `audit.Filter` gains `TraceID` and `CorrelationID`. Callers can now query the full operational chain for one request.
- Runtime tracks the latest trace metadata and last event id per entity. Action, decision, validation, and approval audit records inherit the trace from the most recent ingested event for the entity.
- `Runtime.IngestEvent` propagates `event.Metadata.TraceID` and `CorrelationID` into the `event_ingested` and `state_changed` audit records. If the ingested event has no `CausationID`, the runtime sets it to the previous event id seen for the entity.
- `Runtime.ExecuteAction` injects trace metadata into follow-up events emitted by executors. Follow-ups inherit `TraceID` and `CorrelationID` from the parent entity context, and `CausationID` is set to the most recent event id on that entity. Executors can override any of these by setting them explicitly on the follow-up event.

## Why

The audit's central claim is that operational systems must be reconstructable. Without correlation, audit can answer "what happened to this entity" but not "which incoming request caused this." For governed agentic backends, those are the same question. Brick 17 closes the gap with one mechanical change.

## Demo

The mission demo now sets `TraceID = trace-mission-001` and `CorrelationID = corr-mission-001` on the raw input. The printed timeline shows `trace=trace-mission-001` on every record produced by that ingestion, including follow-up events emitted by executors.
