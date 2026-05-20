# Brick 4 - Audit Reconstruction

Brick 4 makes audit useful for operational reconstruction.

The goal is not only to append records. A KIFF system should be able to answer:

- what happened;
- when it happened;
- which actor triggered it;
- which state changes occurred;
- which decisions were proposed;
- which actions required approval;
- which approvals were granted or denied;
- which actions executed.

## Audit Querying

Audit stores should support filtering by:

- entity id;
- audit kind;
- actor id.

Results should be returned in chronological order. When records share the same timestamp, insertion order should remain stable.

## Runtime Timeline

The runtime should expose a compact reconstruction API:

```text
Timeline(entityID string) ([]audit.Record, error)
```

The timeline is intentionally plain in Brick 4. It returns audit records rather than inventing a richer reporting model too early.

## Non-Goals

Brick 4 does not add:

- persistent databases;
- search indexing;
- HTTP audit APIs;
- audit UI;
- export formats;
- observability integrations.

The next step is to make reconstruction reliable locally before exposing it through adapters or products.
