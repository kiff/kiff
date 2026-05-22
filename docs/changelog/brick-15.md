# Brick 15 - Event Replay And State Rebuild

This brick realigns with the original roadmap item:

```text
Brick 13: Event Replay / State Rebuild
```

The repository already used Brick 13 and Brick 14 for HTTP approval and HTTP demo work, so this capability lands as Brick 15 in commit history.

## Purpose

KIFF state should not only be whatever is currently in memory.

Because events are the normalized record of what happened, a KIFF runtime should be able to rebuild an entity state from stored events. This strengthens auditability and trust:

- state can be reconstructed from operational facts;
- replay exposes the event-to-state path;
- domains can test that their event history is enough to recover current state;
- future persistence adapters can restore state after process restart.

## Mechanics

Brick 15 adds:

- `state.Rebuild`;
- `state.ReplayResult`;
- `state.ReplayStep`;
- `state.ErrInvalidReplay`;
- `runtime.RebuildState`;
- `audit.KindStateRebuilt`.

Replay applies events in the order returned by the event store.

For each event, KIFF records:

- event id;
- event type;
- previous state;
- next state;
- resulting version.

The runtime method stores the rebuilt final state through the configured state machine and appends a `state_rebuilt` audit record.

## Boundary

Replay is still domain-owned at the transition level.

KIFF does not infer business state. The domain state machine defines which event transitions are valid.

## Non-Goals

Brick 15 does not add:

- snapshots;
- branching replay;
- migration of old event schemas;
- database persistence;
- time-travel queries;
- partial rollback;
- a workflow engine.

Those may become useful later, but the first step is the smallest trustable primitive: rebuild current state from stored events.
