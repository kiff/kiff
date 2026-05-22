# Brick 7 - HTTP API Boundary

Brick 7 adds the first HTTP transport surface.

The goal is to expose existing KIFF runtime behavior through standard-library HTTP handlers without changing the core protocol.

## HTTP Role

HTTP sits outside the KIFF coordination kernel.

Requests should call runtime methods:

- raw input ingestion;
- allowed action lookup;
- audit timeline reconstruction.

HTTP should not:

- own domain semantics;
- bypass adapters;
- bypass action validation;
- replace runtime audit;
- add persistence;
- add authentication or authorization policy;
- introduce a web framework dependency.

## Initial Routes

Brick 7 provides:

```text
POST /events/raw
GET  /entities/{entityID}/allowed-actions
GET  /entities/{entityID}/timeline
```

`POST /events/raw` accepts an adapter raw input document, normalizes it, and ingests the resulting event through the runtime.

The entity routes expose current allowed actions and the audit timeline for the entity.

## Non-Goals

Brick 7 does not add:

- auth middleware;
- TLS configuration;
- server lifecycle management;
- OpenAPI generation;
- frontend UI;
- database-backed persistence;
- action execution routes.

The HTTP package is a small transport adapter. Production applications can wrap it with their own routing, auth, deployment, and observability choices.
