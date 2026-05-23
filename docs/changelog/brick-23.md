# Brick 23 - Runtime Metrics Recorder

Brick 23 adds an optional `MetricsRecorder` interface to `pkg/kiff/runtime`. Adopters who want to count runtime operations — events ingested, decisions recorded, actions validated, actions executed, approvals requested, approvals reviewed — can pass an implementation through `runtime.Config.Metrics`. The default is `NoopMetrics`; existing wiring is unaffected.

## What Was Added

- `pkg/kiff/runtime/metrics.go`:
  - `MetricsRecorder` interface with one method: `Inc(name string, n uint64, attrs ...Attr)`.
  - `Attr` struct (`Key`, `Value`) and an `EntityType(value)` helper.
  - `NoopMetrics`, the package-level default. Discards every call with no allocation.
  - Six exported counter-name constants matching the runtime's instrumentation sites:
    - `CounterEventsIngested`     = `"kiff.events.ingested"`
    - `CounterDecisionsRecorded`  = `"kiff.decisions.recorded"`
    - `CounterActionsValidated`   = `"kiff.actions.validated"`
    - `CounterActionsExecuted`    = `"kiff.actions.executed"`
    - `CounterApprovalsRequested` = `"kiff.approvals.requested"`
    - `CounterApprovalsReviewed`  = `"kiff.approvals.reviewed"`

- `pkg/kiff/runtime/runtime.go`:
  - `Config` gains an optional `Metrics MetricsRecorder` field.
  - `Runtime` gains a private `metrics` field. `New` defaults it to `NoopMetrics` when `Config.Metrics` is nil.
  - Six instrumentation sites added on the successful operational path:
    - `IngestEvent`         → `kiff.events.ingested`
    - `ProposeDecision`     → `kiff.decisions.recorded`
    - `ValidateAction`      → `kiff.actions.validated` (only when validation passes)
    - `ExecuteAction`       → `kiff.actions.executed`  (only when status is `ExecutionSucceeded`)
    - `RequestApproval`     → `kiff.approvals.requested`
    - `ReviewApproval`      → `kiff.approvals.reviewed`
  - Each call passes one `Attr`: `entity_type`, taken from the operation's entity-type field. Adopters who want to segment counters by a different attribute compose it inside their `MetricsRecorder` implementation.

- `pkg/kiff/runtime/metrics_test.go`:
  - `TestNoopMetricsIsDefault` — runtime works with the default config; the noop default absorbs the increment.
  - `TestEntityTypeAttrShape` — guards the helper's contract.
  - `TestRuntimeIncrementsEventsIngested` / `TestRuntimeIncrementsDecisionsRecorded` — single-counter happy paths.
  - `TestRuntimeIncrementsActionsValidatedAndExecuted` — covers the interaction (`ExecuteAction` calls `ValidateAction` internally, so the validated counter increments twice for one execute).
  - `TestRuntimeIncrementsApprovalsRequestedAndReviewed` — both approval counters fire on their respective entrypoints.
  - `TestRuntimeDoesNotIncrementOnFailedValidation` — the validated counter must not increment when validation rejects.
  - `TestRuntimeMetricsConcurrentSafety` — 8 goroutines × 25 ingestions confirm the recorder receives every increment under concurrent runtime use.

## Why

Counters are the smallest useful operational signal that the runtime can produce. Adopters running KIFF in production already want to know "how many actions did the runtime execute today" or "did the approval queue grow this hour." Without a hook, they have to wrap stores or read the audit trail to derive these numbers — both work, both are heavier than what they need.

The interface is intentionally narrow:

- **Counters only**, not histograms or gauges. Histograms have many opinions (bucket layouts, quantile compute) and the runtime cannot make them on the adopter's behalf. If you need latency distributions, derive them from request-level timing in the layer that actually serves requests.
- **One `Attr` per call**, with `entity_type` as the only attribute the runtime emits. The runtime knows the entity type at every instrumentation site; richer labels (tenant id, region, environment) belong to the caller.
- **No registry, no exposition format**. Adopters who want a `/metrics` endpoint compose `MetricsRecorder` with their own registry. `pkg/kiff/observability` already provides one for adopters who want structured logs and counters in the same wrapper; a future brick may add an adapter that bridges `MetricsRecorder` calls into that registry.

## Coexistence with `pkg/kiff/observability`

`pkg/kiff/observability` already provides counters, but they are derived from audit records. Every audit append (success or failure, every kind) increments a counter named after the audit kind. That is the right shape for "what happened operationally, by audit kind."

`runtime.MetricsRecorder` is a separate signal:

- It fires **before** the audit append in some sites (events, decisions) and **after** in others (actions, approvals); always on the **successful path** only.
- Its names are **operational counters** (`kiff.events.ingested`, etc.), not audit kinds (`event_ingested`, `action_executed`).
- It is meant for adopters who want a stable, lightweight metering hook independent of the audit storage choice.

Both hooks compose. An adopter can wire `observability.WrapAuditStore` and supply a `MetricsRecorder` and they will not collide: different namespaces, different audiences.

## Compatibility

- No changes to existing public types except the addition of `Config.Metrics`. Zero-value `Config` continues to work.
- All existing runtime tests pass without modification.
- `runtime.New(runtime.Config{})` returns a runtime that internally uses `NoopMetrics`. The runtime's behavior is byte-for-byte identical to before this brick when `Config.Metrics` is unset.

## Limitations

- No outcome-by-error-class counters (e.g. distinguishing "approval denied" from "state not allowed" failures). Adopters who need that decompose the audit trail or wrap the runtime themselves. A future brick may add such counters once a clear adopter pattern emerges.
- No histograms or gauges. Out of scope; see the rationale above.
- No built-in adapter to the `observability` package's counter registry. Callable by hand in a few lines today; a future brick may ship the adapter once a second adopter asks for it.
