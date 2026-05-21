# Brick 19 - File-Backed JSONL Stores

Brick 19 is the first non-memory implementation of the KIFF store interfaces. It exists to prove that the store boundary defined in earlier bricks actually works for a different backend, and to let external testers run the HTTP demo without losing state on restart.

## What Was Added

- `pkg/kiff/store/file` package with append-only JSONL implementations of:
  - `event.Store`
  - `decision.Store`
  - `approval.Store` (latest snapshot wins per id)
  - `audit.Store` (with `Filter` support including `TraceID` / `CorrelationID`)
- `file.Bundle` and `file.Bundle.AsStoreBundle()` so an application can inject the file-backed bundle into `runtime.Config.Stores` without ceremony.
- `mission.NewRuntimeWithStores(*store.Bundle)` so the mission example can be wired against any backing store.
- `cmd/kiff-http-demo` gains a `-data-dir <path>` flag. When set, the demo persists every event, decision, approval, and audit record under `<path>/events.jsonl`, `decisions.jsonl`, `approvals.jsonl`, `audit.jsonl`. When empty, behavior is unchanged.

## Persistence Model

Every store appends one JSON record per line. Reads stream the file from the start, deserialize each line, and apply the per-store filter. There is no index, no compaction, and no transaction across files. This is intentional. The package is for demos, local testing, and small single-process deployments. Production use cases should implement the store interfaces against a real database or stream system.

For the approval store, `Save` appends a new snapshot; reads always return the latest snapshot per id. This preserves the operational rule that an approval moves through `pending → granted` or `pending → denied` and that intermediate states are auditable on disk.

## Restart Behavior

The integration test `TestFileBundleSurvivesProcessRestart` ingests events, closes the bundle, reopens against the same directory, and rebuilds state via `runtime.RebuildState`. The rebuilt state matches the pre-restart state, the audit log from the first run is still queryable, and `Filter.TraceID` still returns the full chain across the restart boundary.

## Why

External testers running the HTTP demo will restart the process. A restart that wipes everything kills credibility. Brick 19 is the smallest persistence proof that:

1. The package-level store interfaces are real injection points, not framework-internal types.
2. Trace correlation (Brick 17) and JSON tags (Brick 16) survive disk round-trips.
3. State rebuild from stored events (Brick 15) works against a non-memory backend.

## Limitations

- No file rotation, compaction, or retention policy.
- No transactional cross-file write. A crash between writes can leave the four files inconsistent.
- Reading is `O(n)` in the file size on every call. Acceptable for demos, not for large datasets.

These are explicit non-goals for v0.1. Any user who needs them should implement the store interfaces against a real database; the boundary already supports it.
