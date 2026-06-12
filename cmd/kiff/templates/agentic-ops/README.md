# {{.ModulePath}}

A KIFF starter project, scaffolded with `kiff new -template=agentic-ops`. It is a complete agentic-ops backend in one repo:

- A KIFF runtime hosting one risky action (`REFUND_ORDER`) under approval.
- A tiny HTTP server that exposes the kiff httpapi plus a `/demo/agent/refund` route the agent calls.
- An Agno-shaped Python agent with two providers (offline fixture + AWS Bedrock).
- Two runners: one mutates a mock DB, one talks to KIFF. The diff is the pitch.

## Quickstart (60 seconds, no AWS)

```bash
go mod tidy
make demo
```

You will see the same agent run twice: once unguarded (a $999 refund mutates the mock DB), once through KIFF (the same refund is held for approval, granted, then executed). The audit timeline and a rebuild check confirm materialized state matches the events.

## Bedrock (live LLM, ~5 minutes)

```bash
cp agent/.env.example agent/.env
# edit agent/.env: AGNO_MODEL_PROVIDER=bedrock plus your AWS_* values
make demo-bedrock
```

The Makefile also loads `../.env` if present, so you can keep credentials at the parent directory level.

## Layout

```
{{.ModuleName}}/
├── cmd/server/             Go HTTP server
├── internal/domain/        KIFF domain (entity, states, action contracts)
├── agent/                  Python agent (offline + bedrock providers)
│   ├── agent.py
│   ├── run_no_kiff.py
│   ├── run_with_kiff.py
│   ├── requirements.txt
│   └── .env.example
├── scripts/demo.sh         Side-by-side runner used by `make demo`
├── Makefile                demo / demo-offline / demo-bedrock / test / build / clean
├── go.mod
└── README.md
```

## What to change next

1. Open `internal/domain/refund.go`. Rename the entity, events, states, and actions to your vocabulary. Add or remove contracts; tighten the permission policy.
2. If you add a new contract, mirror its tool surface in `cmd/server/main.go` (one route per agent-facing tool) and register the corresponding tool in `agent/agent.py`.
3. Re-run `make test` and `make demo`.

## Persistence

Pass `-data-dir ./data` to `cmd/server` (or set it in the Makefile's build step) to switch from in-memory stores to file-backed JSONL. Production should swap the store interfaces (`event.EventStore`, `decision.DecisionStore`, `approval.ApprovalStore`, `audit.AuditStore`) for a real backend.

## Inspecting the audit trail

While the server is running:

```bash
go install github.com/kiff/kiff/cmd/kiff
kiff timeline -base http://localhost:<port> -entity order-2
```

The CLI renders a compact table and, if the demo server is up, the rebuild equality line at the bottom.

## Why this template

If your application has at least one stateful entity that an AI agent will mutate, you need:

- a contract that says what the agent is allowed to propose,
- a runtime that validates state, parameters, permissions, and approval,
- an audit log that lets you reconstruct what happened.

This template ships a working version of all three. Replace the domain. Keep the loop.

For the long argument see [`docs/why.md`](https://github.com/kiff/kiff/blob/main/docs/why.md).
