# {{.ModulePath}}

A KIFF **governed-action** project, scaffolded with:

```bash
kiff new {{.ModulePath}} -scenario refund -agent custom-http
```

It puts an agent on a real, consequential action — issuing refunds — and makes
that safe to ship. The agent proposes the refund; KIFF checks the order's
current state, the parameters, the permission, and the approval requirement
before the money moves. An eligible refund executes; a repeat or an
unapproved one is refused with a typed reason.

## Run the demo

```bash
make demo
```

No agent framework or API key needed — it is pure `curl`. You will watch:

1. **Unguarded** — a refund endpoint with no governance double-refunds an order. The money goes out twice.
2. **Guarded (through KIFF)** — the same refund is *held* for approval, executes once an operator grants it, and is then *refused* on repeat because the order already moved to `REFUNDED`.
3. **Replay** — the order's final state is rebuilt from its events alone.

That contrast is the whole point: the boundary is what lets you put the agent on the refund path in the first place.

## The action

```text
ORDER_PLACED -> CREATED -> MARK_PAID -> PAID -> REFUND_ORDER (approval) -> REFUNDED
```

- `MARK_PAID` — low risk, no approval.
- `REFUND_ORDER` — high risk, **human approval required**. Sends real money, so it only runs once, from `PAID`, under an approval.

The domain lives in [`domain/domain.go`](./domain/domain.go); its executors are real (the project runs and its tests pass out of the box). The mock business side effect — a refund ledger — lives in [`cmd/server`](./cmd/server), reached **only** after KIFF allows the action.

## Layout

```
{{.ModuleName}}/
├── cmd/server/        # HTTP server: KIFF governance API + demo routes + the mock ledger
├── domain/            # the governed-action domain (state machine, contracts, policy)
├── scripts/demo.sh    # the curl walkthrough
├── Makefile           # make demo | test | verify | build
└── go.mod
```

## The app API (what an agent calls)

The generated project exposes a **headless app API** under `/api` — the surface
an agent (or any HTTP client) calls to operate the app. It is distinct from the
KIFF governance API (mounted at `/`): every `/api/tools/{tool}` call is
validated and executed by the KIFF runtime, and the business side effect runs
only after the runtime allows the action. The agent never touches the side
effect directly.

```
POST /api/tools/{tool}              invoke a governed action (mark_paid, refund_order)
GET  /api/actions                   list the governed actions
GET  /api/tools/manifest.json       compact tool manifest (for agent tool runners)
GET  /api/openapi.json              OpenAPI 3 doc (for generic HTTP clients)
GET  /api/entities/{id}             current state
GET  /api/entities/{id}/timeline    audit timeline
POST /api/approvals/{id}/grant|deny review an approval
```

The routes, manifest, and OpenAPI document are all generated from the runtime's
action catalog, so they never drift from the domain.

```bash
curl -s -X POST http://localhost:8080/api/tools/refund_order \
  -H 'content-type: application/json' \
  -d '{"entity_id":"order-2","parameters":{"amount_cents":99900,"reason":"customer eligible"}}'
```

The response is a typed decision envelope your agent can switch on:

```json
{ "outcome": "approval_required", "action": "REFUND_ORDER", "tool": "refund_order",
  "entity_id": "order-2", "next_step": "request_approval", "approval_id": "appr-order-2-1" }
```

Possible `outcome` values: `allowed`, `approval_required`, `blocked`, `invalid`.
By convention this app API maps `approval_required` and `blocked` to `409`,
`invalid` to `400`, and `allowed` to `200`; the `outcome` field is the source
of truth.

## Verify it

```bash
make verify   # kiff verify ./domain
```

Because this project ships real executors (not TODO stubs), `kiff verify`
passes. A domain generated with `kiff scaffold` fails verify until you fill in
its executors — that is the difference between a skeleton and a ready domain.

## Next steps

- Rename the entity, states, and action to your own consequential operation.
- Replace the mock ledger in `cmd/server` with your real side effect.
- Keep the rule: the side effect runs only after `ExecuteAction` succeeds.

> Security note: the demo HTTP API is unauthenticated. Add authentication and
> transport security before exposing it beyond localhost.
