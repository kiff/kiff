# Brick 12 - HTTP Action Routes

Brick 12 exposes action validation and execution through the optional HTTP API.

The routes are guarded by the same runtime rules as local Go callers:

- current state is loaded from the state machine;
- action contracts are resolved from the runtime catalog;
- permissions are checked;
- approval requirements are enforced;
- execution results and follow-up events are audited.

## Routes

Brick 12 adds:

```text
POST /entities/{entityID}/actions/{actionName}/validate
POST /entities/{entityID}/actions/{actionName}/execute
```

The request body provides:

- entity type;
- actor;
- parameters;
- approval id;
- approved flag.

The request body does not provide the action contract.

## Boundary

HTTP is only transport. It does not bypass KIFF governance.

Execution through HTTP still calls:

```text
Runtime.ExecuteAction
```

which means execution is validated, audited, and may emit follow-up events through the normal event path.

## Non-Goals

Brick 12 does not add:

- authentication;
- edge authorization middleware;
- approval grant/deny routes;
- request signing;
- OpenAPI generation;
- web UI;
- arbitrary contract execution.

Production applications should wrap these handlers with their own auth, tenancy, deployment, and observability choices.
