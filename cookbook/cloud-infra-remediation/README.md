# Cloud Infrastructure Remediation Agent

This recipe shows how KIFF can let an operations agent remediate infrastructure
incidents without giving it unrestricted cloud control.

The recipe is intentionally provider-neutral. The gateway is an in-memory cloud
control adapter, but the boundary is the same place a production app would call
AWS, GCP, Kubernetes, or an internal platform API.

## What This Enables

An operations team can launch an agent that triages alerts, attaches telemetry,
and prepares remediation. Low-risk restarts can be executed by a service actor.
High-risk isolation requires SRE approval before the cloud control operation is
allowed to run.

```text
Alert
  -> ALERT_RECEIVED
  -> shared incident state
  -> agent proposal
  -> typed KIFF action validation
  -> service or SRE-owned execution
  -> audit trail
```

## Workflow

```text
ALERT_RECEIVED -> RECEIVED
  ATTACH_TELEMETRY -> TRIAGED
  ASSESS_REMEDIATION -> LOW_RISK_READY | REVIEW_REQUIRED
  PREPARE_PROCESS_RESTART -> RESTART_PREPARED
  EXECUTE_PROCESS_RESTART -> REMEDIATED
  ISOLATE_INSTANCE -> ISOLATED
  ESCALATE_INCIDENT -> ESCALATED
```

## Action Boundary

The ops agent can:

- attach telemetry to an incident
- assess whether remediation is low risk
- prepare a process restart runbook
- hold an incident for SRE review
- escalate the incident to manual response

The cloud automation service owns infrastructure side effects:

- `EXECUTE_PROCESS_RESTART` runs only from `RESTART_PREPARED`
- `ISOLATE_INSTANCE` runs only from `REVIEW_REQUIRED`
- service authority comes from KIFF's policy-owned role assignments
- idempotency prevents duplicate cloud operations

The SRE lead owns high-risk authority:

- `ISOLATE_INSTANCE` requires a granted approval
- approval review is wrapped by a recipe helper that checks
  `cloud.review_isolation`
- malformed isolation parameters are rejected by typed action schemas before
  any approval or executor path

## Run It

```bash
go run ./cookbook/cloud-infra-remediation/cmd/cloud-remediation-demo
go test ./cookbook/cloud-infra-remediation/...
```

The demo follows a high-risk payments incident. The agent attaches telemetry and
assesses the remediation path, KIFF routes the incident to `REVIEW_REQUIRED`,
cloud isolation is blocked until SRE approval, and the cloud automation service
then isolates the instance through an idempotent gateway.

## What To Notice

- `REMEDIATED`, `ISOLATED`, and `ESCALATED` are terminal states.
- The agent cannot isolate an instance by adding the cloud-service role to its
  submitted actor.
- Typed parameter schemas reject invalid risk scores, customer impact values,
  and isolation scopes before executor code runs.
- The replay test rebuilds final state from events, proving state follows the
  audit-friendly event trail.
