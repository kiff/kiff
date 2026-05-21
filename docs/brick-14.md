# Brick 14 - HTTP Demo Command

Brick 14 adds a runnable HTTP demo command for the mission example.

The command starts a local `net/http` server using:

- the mission example runtime;
- the optional `httpapi` handler;
- in-memory stores;
- the same state, action, permission, approval, execution, and audit rules used by the local demo.

It does not add a production service, persistence, authentication, tenancy, or a UI.

## Run

```bash
go run ./cmd/kiff-http-demo
```

The default address is:

```text
http://localhost:8080
```

Use another address with:

```bash
go run ./cmd/kiff-http-demo -addr :8090
```

## Full HTTP Loop

The examples below use Go struct field names in JSON because the current v1 scaffold keeps the public structs simple and does not add JSON tags yet.

Set a base URL:

```bash
BASE=http://localhost:8080
```

Ingest a mission submission:

```bash
curl -s "$BASE/events/raw" \
  -H 'Content-Type: application/json' \
  -d '{
    "ID": "evt-http-001",
    "Adapter": "mission",
    "Type": "MISSION_SUBMITTED",
    "Source": "curl",
    "EntityID": "mission-attempt-http-001",
    "EntityType": "MissionAttempt",
    "ActorID": "human-approver",
    "ReceivedAt": "2026-05-21T10:00:00Z",
    "Payload": {"mission": "cross the line"}
  }'
```

Execute the low-risk create attempt action:

```bash
curl -s "$BASE/entities/mission-attempt-http-001/actions/CREATE_ATTEMPT/execute" \
  -H 'Content-Type: application/json' \
  -d '{
    "actor": {
      "ID": "mission-agent",
      "Type": "agent",
      "DisplayName": "Mission Agent",
      "Roles": ["mission_agent"]
    }
  }'
```

Ingest the domain event that records the created attempt:

```bash
curl -s "$BASE/events/raw" \
  -H 'Content-Type: application/json' \
  -d '{
    "ID": "evt-http-002",
    "Adapter": "mission",
    "Type": "ATTEMPT_CREATED",
    "Source": "curl",
    "EntityID": "mission-attempt-http-001",
    "EntityType": "MissionAttempt",
    "ActorID": "mission-agent",
    "ReceivedAt": "2026-05-21T10:01:00Z"
  }'
```

Inspect allowed actions:

```bash
curl -s "$BASE/entities/mission-attempt-http-001/allowed-actions"
```

Validate a proposed move:

```bash
curl -s "$BASE/entities/mission-attempt-http-001/actions/PROPOSE_MOVE/validate" \
  -H 'Content-Type: application/json' \
  -d '{
    "actor": {
      "ID": "mission-agent",
      "Type": "agent",
      "DisplayName": "Mission Agent",
      "Roles": ["mission_agent"]
    },
    "parameters": {"move": "draft the first bounded move"}
  }'
```

Ingest the proposed move event:

```bash
curl -s "$BASE/events/raw" \
  -H 'Content-Type: application/json' \
  -d '{
    "ID": "evt-http-003",
    "Adapter": "mission",
    "Type": "MOVE_PROPOSED",
    "Source": "curl",
    "EntityID": "mission-attempt-http-001",
    "EntityType": "MissionAttempt",
    "ActorID": "mission-agent",
    "ReceivedAt": "2026-05-21T10:02:00Z",
    "Payload": {"move": "draft the first bounded move"}
  }'
```

Request approval for the high-risk execution:

```bash
curl -s "$BASE/entities/mission-attempt-http-001/actions/EXECUTE_MOVE/approvals" \
  -H 'Content-Type: application/json' \
  -d '{
    "actor": {
      "ID": "mission-agent",
      "Type": "agent",
      "DisplayName": "Mission Agent",
      "Roles": ["mission_agent"]
    },
    "approval_id": "approval-http-001",
    "reason": "high-risk move execution requires human authority",
    "parameters": {"move": "draft the first bounded move"}
  }'
```

Grant the approval:

```bash
curl -s "$BASE/approvals/approval-http-001/grant" \
  -H 'Content-Type: application/json' \
  -d '{
    "actor": {
      "ID": "human-approver",
      "Type": "human",
      "DisplayName": "Human Approver",
      "Roles": ["mission_approver"]
    },
    "reason": "approved after human review"
  }'
```

Ingest the human approval event:

```bash
curl -s "$BASE/events/raw" \
  -H 'Content-Type: application/json' \
  -d '{
    "ID": "evt-http-004",
    "Adapter": "mission",
    "Type": "HUMAN_APPROVAL_GRANTED",
    "Source": "curl",
    "EntityID": "mission-attempt-http-001",
    "EntityType": "MissionAttempt",
    "ActorID": "human-approver",
    "ReceivedAt": "2026-05-21T10:03:00Z",
    "Payload": {"approved_action": "EXECUTE_MOVE"}
  }'
```

Execute the approved action:

```bash
curl -s "$BASE/entities/mission-attempt-http-001/actions/EXECUTE_MOVE/execute" \
  -H 'Content-Type: application/json' \
  -d '{
    "actor": {
      "ID": "mission-agent",
      "Type": "agent",
      "DisplayName": "Mission Agent",
      "Roles": ["mission_agent"]
    },
    "approval_id": "approval-http-001",
    "parameters": {"move": "draft the first bounded move"}
  }'
```

Read the audit timeline:

```bash
curl -s "$BASE/entities/mission-attempt-http-001/timeline"
```

The timeline should show event ingestion, state changes, action validation, approval requirement, approval grant, action execution, follow-up event ingestion, and final state transition to `COMPLETED`.

## Boundary

This command is a local demo harness. It exists to make the HTTP boundary inspectable while the framework core is still small.

Production applications should provide their own:

- process supervision;
- authentication and authorization;
- persistence;
- request logging and telemetry;
- tenancy;
- deployment shape.
