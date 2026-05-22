# Brick 5 - Store Boundaries

Brick 5 makes the persistence boundary more explicit without adding persistence.

The framework already has package-level store interfaces:

- `event.Store`
- `decision.Store`
- `approval.Store`
- `audit.Store`

Those interfaces are the contracts future database, file, queue, or hosted adapters should implement. Brick 5 adds a small bundle so applications can wire the core stores into runtime as one unit.

## Store Bundle

A store bundle groups:

- event store;
- decision store;
- approval store;
- audit store.

The runtime may still accept individual stores for fine-grained configuration. The bundle is a convenience and boundary object, not a storage framework.

## In-Memory Default

KIFF keeps in-memory stores as the default local implementation.

This keeps examples easy to run:

```bash
go run ./cmd/kiff-demo
```

and tests easy to trust:

```bash
go test ./...
```

## Future Adapter Shape

Future adapters should implement the existing package-level interfaces rather than forcing the core to know about a specific database.

For example:

```text
PostgresEventStore implements event.Store
PostgresAuditStore implements audit.Store
```

Those adapters can then be placed into a store bundle and passed to the runtime.

## Non-Goals

Brick 5 does not add:

- SQL schemas;
- migrations;
- file persistence;
- hosted storage;
- transactions across stores;
- event sourcing replay;
- queues or streams.

The goal is only to make the storage boundary clearer before real adapters exist.
