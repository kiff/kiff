# Audit is part of the protocol

> Every important event, state transition, decision, action validation, approval, execution result, and failure must be auditable. Audit is not optional logging.

Most teams treat audit as something they will add later. They build the system, ship the feature, and then realize too late that "what happened to this entity" is a question they cannot answer cleanly. KIFF makes audit non-optional. You cannot ingest an event, validate an action, or record an approval without producing an audit record. The runtime does it for you, and you cannot turn it off.

## The shape

Every runtime method that affects the world appends an audit record:

| Runtime call | Audit kind |
| --- | --- |
| `IngestEvent`, `IngestRaw` | `event_ingested`, `state_changed` |
| `ProposeDecision`, `RecordActionProposal` | `decision_recorded` |
| `ValidateAction` | `action_validated`, `action_validation_failed`, `approval_required` |
| `ExecuteAction` | `action_executed`, `action_execution_failed` |
| `RequestApproval` | `approval_requested` |
| `ReviewApproval` | `approval_granted`, `approval_denied` |
| `RebuildState` | `state_rebuilt` |

Audit records are typed. Each record carries:

- the kind (one of the strings above);
- the entity ID and type;
- the actor that triggered it;
- a human message;
- a domain-specific data payload;
- timestamps;
- trace correlation: `trace_id`, `correlation_id`, `causation_id`.

The trace IDs propagate. An event ingested with `trace_id: "req-abc-123"` produces audit records with the same trace ID. The action validation, the executor's follow-up event, and the audit records that follow all carry it forward. One filter call (`audit.Filter{TraceID: "req-abc-123"}`) returns the entire chain that started with that one external request.

## What this gets you

Three concrete capabilities you cannot get from after-the-fact logging:

**Reconstruction.** `runtime.Timeline(ctx, entityID)` returns every audit record for one entity, in chronological order. Six months from now, when a customer or auditor asks "why did this order get refunded," you have a deterministic answer.

**Trust boundary verification.** Every denial is audited. If your agent attempted a high-risk action without approval and got blocked, that is in the trail. If a human granted an approval after looking at the agent's reasoning, that is in the trail. The boundary is not just enforced at runtime; it is provable after the fact.

**Replay.** Events are stored. The audit records describe what happened. Together they let you replay an entity from scratch with `runtime.RebuildState(ctx, entityID)` and confirm that the current state matches what should have happened. Drift between events and state becomes visible instead of invisible.

## What it costs

Honestly, very little. Audit writes happen synchronously in the runtime, but the in-memory store is a slice append, and the file-backed JSONL store is one buffered write per record. For production, you implement the `audit.AuditStore` interface against your real backend (Postgres, ClickHouse, S3, whatever). The framework does not pretend to know what your retention or query patterns will be.

The pattern that does *not* work is "skip audit for the hot path and add it back later." The audit hooks are inside the runtime, not optional middleware. There is no flag to disable them. By the time you wanted to disable them you would also be removing the explanatory power that makes KIFF useful.

## What this looks like in code

```go
// One line. The runtime does the rest.
err := rt.IngestEvent(ctx, event.Event{
    ID:         "evt-123",
    Type:       "ORDER_PAID",
    EntityID:   "order-1",
    EntityType: "Order",
    Source:     "stripe-webhook",
    ActorID:    "stripe",
    OccurredAt: time.Now().UTC(),
    Metadata:   event.Metadata{TraceID: "req-abc-123"},
})

// Later, anyone can reconstruct what happened.
records, _ := rt.Audit.Query(ctx, audit.Filter{TraceID: "req-abc-123"})
// returns every audit record produced by the chain that started with that ingest

timeline, _ := rt.Timeline(ctx, "order-1")
// returns every audit record for one entity in chronological order
```

## What you write

Your domain code does not append audit records. The runtime does that for you, every time. Your domain code is responsible for:

- emitting follow-up events from executors so state stays event-driven;
- including useful messages in `ActionResult.Message` and `EffectsSummary` (these end up in the audit record);
- propagating trace IDs from inbound requests into the events you ingest.

That is the whole contract. Audit is the runtime's job, not yours.

## When to break it

You don't. There is no way to bypass the runtime's audit hooks short of bypassing the runtime entirely, which means you would not be using KIFF.

If you need lower-cost audit for very high-volume actions (think: a million events per minute), the right move is to implement an `AuditStore` that batches, partitions, or samples in your backend, while keeping the runtime contract intact. The framework cannot decide that for you, but the interface is small enough that you can.

The principle in one line: **trust comes from reconstruction**.
