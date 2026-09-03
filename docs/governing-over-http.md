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
> Identity is established by the transport, not the body. `httpapi.Handler`
> requires an `Authenticator`, and the principal it returns **overwrites** the
> actor the caller sent — see [Authentication](#authentication) below. And note
> the approval boundary is a
> compile-time property of KIFF's own Go runtime — an HTTP caller gets an
> API-level decision, not compile-time safety in their own code.


## Authentication

Every route requires an authenticated principal. The handler resolves it before
routing, and the principal **overwrites** whatever actor the request body
claimed — on `execute`, on `validate`, on approval review, and on `/events/raw`,
since ingested events drive state transitions and state is what actions are
judged against.

```go
auth := httpapi.NewStaticTokenAuthenticator(map[string]httpapi.Principal{
    "tok_agent":    {ActorID: "support-agent", Roles: []string{"support_agent"}},
    "tok_operator": {ActorID: "ops-human", Roles: []string{"ops_operator"}},
})
mux.Handle("/", httpapi.NewHandler(rt, auth))
```

```bash
curl -X POST $KIFF/entities/order-2/actions/REFUND_ORDER/execute \
  -H 'Authorization: Bearer tok_agent' \
  -H 'Content-Type: application/json' \
  -d '{"parameters":{"amount_cents":99900,"reason":"damaged"}}'
```

An `actor` in the body is ignored when the request is authenticated. That is the
point: it means a caller cannot act as, or approve as, someone else by editing
JSON. Roles from the principal are audit metadata — authority still resolves
through the `permission.Policy` by actor ID.

`StaticTokenAuthenticator` is a working default for a single trusted service.
For anything larger, implement the one-method interface against your identity
provider:

```go
type Authenticator interface {
    Authenticate(*http.Request) (Principal, error)
}
```

Implementations must not read the request body — the body is what the caller
controls, and the purpose of the interface is to establish identity from
something they do not.

### Running without authentication

A handler with neither an `Authenticator` nor an explicit opt-out **refuses to
serve**, because an undecided handler is a misconfiguration rather than an open
server. Two cases legitimately opt out — a local demo, and a deployment where an
upstream layer already authenticates and rewrites the actor before delegating,
which is what KIFF Cloud does:

```go
mux.Handle("/", httpapi.NewUnauthenticatedHandler(rt))
```

The name is blunt on purpose. Anyone who can reach that handler is any
principal.

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
separate, authenticated step, and the runtime enforces segregation of duties:
the actor that requested an approval cannot review it, so presenting the
reviewer's own credentials is not enough either.

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
