# Brick 10 - Follow-Up Events From Execution Results

Brick 10 connects execution results back into the KIFF event loop.

The rule is:

```text
Actions do not mutate state directly.
Actions may emit follow-up events.
Follow-up events are ingested through the normal event path.
```

This completes the local loop:

```text
Validated action -> Execution result -> Follow-up event -> State transition -> Audit
```

## Follow-Up Events

An action execution result may include zero or more follow-up events.

The runtime only ingests follow-up events when execution succeeds. Failed execution results are audited, but their follow-up events are ignored.

## Runtime Order

Runtime execution order is:

1. validate action;
2. execute action;
3. audit execution result;
4. ingest follow-up events through `IngestEvent`.

This means follow-up events produce the same event and state audit records as any other event.

## Non-Goals

Brick 10 does not add:

- queues;
- async execution;
- retries;
- workflow orchestration;
- compensating actions;
- event sourcing replay;
- transactions across execution and event ingestion.

The goal is a simple synchronous bridge from execution results back into the existing KIFF loop.
