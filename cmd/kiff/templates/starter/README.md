# {{.ModulePath}}

A starter KIFF project. Built around a tiny `tasks` domain so you can see the full KIFF loop without learning a new vocabulary.

## Run it

```bash
go mod tidy
go run ./cmd/server
```

Then in another terminal:

```bash
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# 1. Create a task (raw event ingestion).
curl -s -X POST http://localhost:8080/events/raw \
  -H 'content-type: application/json' \
  -d "{
    \"id\": \"evt-1\",
    \"adapter\": \"tasks\",
    \"type\": \"TASK_CREATED\",
    \"source\": \"starter\",
    \"entity_id\": \"task-1\",
    \"entity_type\": \"Task\",
    \"actor_id\": \"system\",
    \"received_at\": \"${NOW}\"
  }"

# 2. See which actions are allowed in the current state.
curl -s http://localhost:8080/entities/task-1/allowed-actions

# 3. Start the task. Low-risk, no approval needed.
curl -s -X POST http://localhost:8080/entities/task-1/actions/START_TASK/execute \
  -H 'content-type: application/json' \
  -d '{
    "actor": {"id": "task-agent", "type": "agent", "roles": ["task_agent"]},
    "parameters": {"assignee": "alice"}
  }'

# 4. Try to complete the task. KIFF blocks it (approval required).
curl -s -X POST http://localhost:8080/entities/task-1/actions/COMPLETE_TASK/execute \
  -H 'content-type: application/json' \
  -d '{
    "actor": {"id": "task-agent", "type": "agent", "roles": ["task_agent"]},
    "parameters": {"summary": "ready to ship"},
    "approval_id": "approval-1"
  }'

# 5. Request approval, then have an operator grant it.
curl -s -X POST http://localhost:8080/entities/task-1/actions/COMPLETE_TASK/approvals \
  -H 'content-type: application/json' \
  -d '{
    "actor": {"id": "task-agent", "type": "agent", "roles": ["task_agent"]},
    "parameters": {"summary": "ready to ship"},
    "approval_id": "approval-1",
    "reason": "agent requests completion"
  }'
curl -s -X POST http://localhost:8080/approvals/approval-1/grant \
  -H 'content-type: application/json' \
  -d '{
    "actor": {"id": "task-operator", "type": "human", "roles": ["task_operator"]},
    "reason": "approved"
  }'

# 6. Now COMPLETE_TASK succeeds. Inspect the timeline.
curl -s -X POST http://localhost:8080/entities/task-1/actions/COMPLETE_TASK/execute \
  -H 'content-type: application/json' \
  -d '{
    "actor": {"id": "task-agent", "type": "agent", "roles": ["task_agent"]},
    "parameters": {"summary": "ready to ship"},
    "approval_id": "approval-1"
  }'
curl -s http://localhost:8080/entities/task-1/timeline
```

## Layout

This is the conventional KIFF project shape. Use it as a starting point:

```
{{.ModuleName}}/
├── cmd/server/main.go        # the entry point: parse flags, wire runtime, serve HTTP
├── domain/                   # your domain vocabulary lives here
│   └── domain.go             # state machine, action contracts, permission policy
├── go.mod
└── README.md
```

The line is simple: domain semantics live in `domain/`. Coordination mechanics live in `pkg/kiff` (the framework).

## Persistence

By default the server uses in-memory stores. State is lost on restart.

For local development with persistence, pass `-data-dir`:

```bash
go run ./cmd/server -data-dir ./data
```

This produces `events.jsonl`, `decisions.jsonl`, `approvals.jsonl`, and `audit.jsonl` in the directory. They are append-only and human-readable. Production deployments should implement the store interfaces (`event.EventStore`, `decision.DecisionStore`, `approval.ApprovalStore`, `audit.AuditStore`) against a real backend.

## What to change next

1. Open `domain/domain.go`. Rename the entity, events, states, and actions to match your domain.
2. Adjust transitions in `NewStateMachine`.
3. Adjust `Allow(state, action...)` in `NewDefinition`.
4. Write your own executor functions inside the action contracts.
5. Restart `go run ./cmd/server` and watch your domain run.

For a deeper guide see [`docs/build-a-domain.md`](https://github.com/kiff/kiff/blob/main/docs/build-a-domain.md) and [`docs/conventions.md`](https://github.com/kiff/kiff/blob/main/docs/conventions.md) in the framework repository.
