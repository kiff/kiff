# Governing actions over HTTP

KIFF's runtime is written in Go, but the application you are governing does
not have to be. The runtime exposes a small JSON/HTTP surface
(`pkg/kiff/httpapi`), so an agent, webhook, or backend in **any language** —
TypeScript, Python, Ruby, anything that can make an HTTP request — can ask
KIFF "may I do this?" before it touches a real system.

This is the same shape `kiff/kiff-guard` automates for named agent
frameworks. If you are on a custom agent or a plain backend, you do not need
an adapter: you make one HTTP call and branch on the answer.

> The domain (events, states, action contracts) is defined in Go and runs as
> a service. The application that calls it stays in its own stack. KIFF is the
> gate; your code is the runner.

## The gate: `validate`

The decision endpoint is:

```
POST /entities/{entityID}/actions/{actionName}/validate
```

Request body:

```json
{
  "entity_type": "Order",
  "actor": { "id": "agent-7" },
  "parameters": { "amount": 4900, "reason": "customer request" },
  "approval_id": ""
}
```

The runtime checks the action contract against current state, required
parameters, the actor's permissions, and the approval requirement — in that
order — and answers with an HTTP status you branch on:

| Outcome | Status | Meaning |
|---|---|---|
| allowed | `200` `{"valid": true}` | contract satisfied — safe to run your side effect |
| approval required | `409` | a human must grant approval first (see below) |
| permission denied | `403` | the actor does not hold a required permission |
| state not allowed | `400` | the entity is not in a state that permits this action |
| missing parameter | `400` | a contract-required parameter is absent |

> Authority note: KIFF does **not** take authority from the request body.
> `actor.roles` is descriptive metadata for audit/display only and carries no
> authorization power — the validator resolves the actor's roles from the
> `permission.Policy`, keyed by `Actor.ID` (assigned by the host from an
> authenticated identity). A caller cannot self-grant a permission by putting
> a role on the actor it submits (#19), just as it cannot set the approval bit
> (the self-approval boundary). Both refuse caller self-assertion.
>
> The caveat for an HTTP deployment is therefore about **identity, not roles**:
> authority keys on `Actor.ID`, and this handler reads `Actor.ID` from the
> request body, so a raw framework deployment must **authenticate the caller's
> identity** in front of `httpapi`. The hosted runtime resolves identity from
> the authenticated session/key for you. And note the approval boundary is a
> compile-time property of KIFF's own Go runtime — an HTTP caller gets an
> API-level decision, not compile-time safety in their own code.

## Observe vs enforce

Two integration postures, the same endpoint:

- **observe** — call `validate`, record/log the outcome, then run your tool
  regardless. Zero behavior change; you learn what your agents try to do.
- **enforce** — call `validate`, and only run your tool when the outcome is
  allowed. Fail safe: treat any non-200 (including a network/timeout error)
  as "do not run."

## TypeScript (no Go, no framework adapter)

```ts
type Decision = "allowed" | "approval_required" | "blocked";

async function decide(
  baseURL: string,
  entityID: string,
  action: string,
  body: unknown,
): Promise<Decision> {
  const res = await fetch(
    `${baseURL}/entities/${entityID}/actions/${action}/validate`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    },
  );
  if (res.status === 200) return "allowed";
  if (res.status === 409) return "approval_required";
  return "blocked"; // 400/403 — and, in enforce mode, any error too
}

// enforce: only run the side effect when KIFF says allowed
const outcome = await decide(KIFF_URL, "ord-1", "REFUND_ORDER", {
  entity_type: "Order",
  actor: { id: "agent-7" },
  parameters: { amount: 4900, reason: "customer request" },
});

if (outcome === "allowed") {
  await issueRefund(/* ... */); // your code, your stack
}
```

## Python

```python
import httpx

def decide(base_url, entity_id, action, body):
    r = httpx.post(
        f"{base_url}/entities/{entity_id}/actions/{action}/validate",
        json=body,
        timeout=5.0,
    )
    if r.status_code == 200:
        return "allowed"
    if r.status_code == 409:
        return "approval_required"
    return "blocked"

outcome = decide(KIFF_URL, "ord-1", "REFUND_ORDER", {
    "entity_type": "Order",
    "actor": {"id": "agent-7"},
    "parameters": {"amount": 4900, "reason": "customer request"},
})

if outcome == "allowed":
    issue_refund()  # your code, your stack
```

## When approval is required

A `409` means the action's contract requires human authority. The flow is
three calls, all plain HTTP:

1. **Request** an approval:
   ```
   POST /entities/{entityID}/actions/{actionName}/approvals
   { "actor": {...}, "approval_id": "appr-1", "reason": "over the cap" }
   ```
2. A human **grants** it (a different actor, server-side authority):
   ```
   POST /approvals/{approvalID}/grant
   { "actor": { "id": "supervisor-2" }, "reason": "verified" }
   ```
3. **Re-validate** the original action with the `approval_id` set. The runtime
   resolves the granted approval and the action now passes.

The caller can request approval but cannot grant its own — granting is a
separate, authenticated step.

## Inspecting what happened

Every ingest, decision, validation, approval, execution, and failure is
audited. Pull the chain for any entity:

```
GET /entities/{entityID}/timeline
```

That is the whole loop — event → state → decision → validated action →
approval → audit — reachable from any language over HTTP, with no Go in your
application.

See also: [architecture](./architecture.md), [conventions](./conventions.md),
and the [`agentic-ops` template](../cmd/kiff/templates/agentic-ops/) for a Go
domain that serves exactly these routes.
