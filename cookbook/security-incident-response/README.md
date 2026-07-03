# Security Incident Response Agent

This recipe shows how KIFF can let a security response agent move a suspected
account-compromise incident forward without giving it direct authority to revoke
identity access.

The consequential side effect is containment in the identity plane: resetting
sessions or revoking access. The agent can collect signals, assess risk, prepare
low-risk session resets, and route privileged or broad-impact cases. The
identity-control write itself is service-owned.

## What This Enables

A security operations team can launch an agent that compresses alert triage and
containment preparation while keeping access revocation behind state, typed
parameters, permissions, dynamic approval policy, segregation of duties,
runtime idempotency, and audit.

```text
Security alert
  -> normalized event
  -> shared incident state
  -> agent proposal
  -> typed KIFF action validation
  -> dynamic approval decision
  -> identity-service execution
  -> audit trail and replay
```

## Workflow

```text
SECURITY_ALERT_RECEIVED -> RECEIVED
  ATTACH_SIGNALS -> SIGNALS_ATTACHED
  ASSESS_IDENTITY_RISK -> LOW_RISK_READY | REVIEW_REQUIRED
  PREPARE_SESSION_RESET -> RESET_PREPARED
  EXECUTE_SESSION_RESET -> CONTAINED
  REVOKE_USER_ACCESS -> CONTAINED
  ESCALATE_INCIDENT -> ESCALATED
```

## Action Boundary

The security response agent can:

- attach identity, endpoint, and access-graph signals
- assess whether an incident is low-risk or review-required
- prepare a session reset for low-risk containment
- hold or escalate the incident

The identity-control service owns side effects:

- `EXECUTE_SESSION_RESET` runs only from `RESET_PREPARED`
- `REVOKE_USER_ACCESS` runs only from `REVIEW_REQUIRED`
- service authority comes from KIFF's permission policy, not caller-submitted
  actor roles
- `ActionContext.IdempotencyKey` prevents retries from executing the identity
  operation twice

The security lead owns high-risk authority:

- all-access revocation, privileged-role revocation, broad blast radius,
  privileged users, or data-exfiltration signals dynamically require approval
- `ReviewApprovalAs` enforces reviewer permission and segregation of duties
- invalid scopes and malformed scores are rejected before executor code

## Run It

```bash
go run ./cookbook/security-incident-response/cmd/security-demo
go test ./cookbook/security-incident-response/...
```

The demo follows a privileged-user compromise. The agent attaches signals and
assesses risk. KIFF routes the incident to `REVIEW_REQUIRED`, blocks all-access
revocation until a security lead approves, executes through the identity
service, and then proves a retry is deduplicated by the runtime.

## What To Notice

- `CONTAINED` and `ESCALATED` are terminal states.
- Dynamic approval policy decides based on concrete action parameters.
- The agent cannot self-revoke access by adding the service role to its
  submitted actor.
- Runtime idempotency returns the prior successful result before state
  validation, so a retry after containment does not emit another event.
