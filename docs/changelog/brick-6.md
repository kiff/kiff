# Brick 6 - Input Adapters

Brick 6 adds the adapter boundary for raw inputs.

KIFF's coordination loop begins with:

```text
Raw inputs -> Normalized events
```

Previous bricks started at normalized events. Brick 6 adds a small package that makes the first step explicit.

## Adapter Role

An adapter receives a raw input shape and returns an `event.Event`.

Adapters should:

- validate the raw input fields they need;
- normalize source-specific payloads into KIFF event records;
- preserve actor, entity, source, timestamp, metadata, and payload;
- stay independent of transport concerns.

Adapters should not:

- start HTTP servers;
- own queue consumers;
- call external APIs;
- execute actions;
- bypass runtime event ingestion;
- decide domain state directly.

## Runtime Ingestion

The runtime can register adapters by name. A raw input declares which adapter should normalize it.

The flow is:

```text
RawInput -> Adapter.Normalize -> event.Event -> Runtime.IngestEvent
```

This keeps the rest of the KIFF loop unchanged.

## Non-Goals

Brick 6 does not add:

- webhook servers;
- queue workers;
- filesystem watchers;
- vendor SDKs;
- LLM integrations;
- long-running adapter processes.

The goal is the normalization contract, not integration infrastructure.
