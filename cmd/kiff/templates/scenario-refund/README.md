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

## Connecting your own agent (custom-http)

The agent-facing tool is a single HTTP call. Anything that speaks HTTP — any
language, any framework — drives it:

```bash
curl -s -X POST http://localhost:8080/demo/agent/refund \
  -H 'content-type: application/json' \
  -d '{"order_id":"order-2","amount_cents":99900,"reason":"customer eligible"}'
```

The response is a typed decision envelope your agent can switch on:

```json
{ "outcome": "approval_required", "action": "REFUND_ORDER", "order_id": "order-2",
  "next_step": "request_approval", "approval_id": "approval-order-2-1" }
```

Possible `outcome` values: `allowed`, `approval_required`, `blocked`, `invalid`.
The agent never calls the business side effect directly — it asks KIFF, and only
a runtime-allowed action reaches the ledger.

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
