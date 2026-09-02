# Governance Audit — github.com/kiff/kiff

> Reference audit. This is the calibration example for the `kiff-audit` skill:
> a real engagement in which three of four attacks against the project's central
> enforcement claim succeeded. Read it for depth, tone, and evidence density.
>
> It also demonstrates a process failure worth learning from — see *Engagement
> note* below. Pin the revision first.

## Engagement

| | |
| --- | --- |
| Target | `github.com/kiff/kiff` (the Go governance runtime) |
| Revision | `v0.7.0-6-g95a7292` |
| Scope | `kiff/kiff`; `kiff-cloud` and `kiff-guard` as context. No production systems. |
| Authorization | Repository owner requested the audit. |
| Toolchain | go1.26.4 darwin/amd64 |

**Engagement note.** This audit ran against a checkout that `git describe`
resolved to `v0.7.0-6` — two commits behind the `v0.8.0` tag. Two conclusions
were wrong as a result: the report stated no Go scanner existed (`cmd/kiff/scan.go`
shipped in v0.8.0) and that no governance skill existed (`skills/kiff-governance/`
shipped in the same commit). Both were corrected after the fact. **Run
`git describe --tags HEAD` and compare against `origin/HEAD` before extracting
claims.** An audit that reports a missing feature which shipped two commits later
loses the reader on everything else.

## 1. Executive verdict

Product value 6 · **Enforcement soundness 3** · Technical quality 7 · UX/DX 7 ·
Differentiation 5 · Launch readiness 4.

The engineering is better than the project's profile suggests: clean build,
silent `go vet`, full suite passing under `-race`, and a scaffolded demo that
reaches a first refused action in under ten seconds. But KIFF is judged as a
boundary, not a framework, and the boundary does not hold. The `approved` flag
the README calls unreachable is reachable three ways from an external module,
and the shipped HTTP API has no authentication, so an agent can request an
approval and then grant its own — emitting an audit trail that reads
`approval_granted → action_validated → action_executed` for a payment no human
saw. **A governance runtime that certifies an ungoverned action is worse than no
runtime.** Ship after P0 fixes; the in-process half is 41 lines and is verified
below.

## 2. Guarantees tested

| Claim (verbatim) | Source | Verdict | Evidence |
| --- | --- | --- | --- |
| "A caller that merely imports the action package cannot construct a Grant — the parameter type is un-nameable outside the module — so it cannot self-approve." | `pkg/kiff/action/action.go:128-134` | **broken** | §4.2, §4.3 |
| "approvals cannot be self-granted" | `README.md` (Status) | **broken** | §4.2–§4.5 |
| "Conformance tests validate that bypassing fails to build" | `README.md` | held, but tests the wrong property | §4.1 |
| "reviewer authority and segregation-of-duties checks for human approvals" | `README.md` (What You Get) | **broken** — opt-in, off on every HTTP path | §4.5 |
| "Record is an append-only operational trace" | `pkg/kiff/audit/audit.go:41` | **not assessable as integrity** — no mechanism exists | §5 |
| "every governed operation records a signed, tamper-evident receipt" | kiff.dev docs | true of Cloud, **false of this repo** | §5 |
| "validation against state, typed parameters, permissions, risk, and approvals" | `README.md` | held for parameters/permissions; **state is caller-asserted** | §4.6 |
| "state replay from stored events" | `README.md` | **held** | §6 |

## 3. Critical gaps

| Priority | Problem | Why it matters | Evidence | Fix | Cx |
| --- | --- | --- | --- | --- | --- |
| P0 | `approved` bit forgeable via `unsafe`, reflection, and a permissive `Validator` | The project's one load-bearing claim | §4.2–4.4 | Clear inbound bit; re-derive from store; non-overridable backstop | S |
| P0 | HTTP API has no authentication; `Actor.ID` read from the body | Any caller is any principal | `httpapi.go:231,260` | Authenticator on `NewHandler` | M |
| P0 | SoD opt-in and off on all HTTP paths | Self-approval, proven | `approval_review.go:86`, `httpapi.go:236` | Default `SeparateFromRequester: true` | S |
| P0 | pgx SQL injection reachable from `ApprovalStore.IsGranted` | Injection sink is the approval oracle | `govulncheck` | `pgx@v5.9.2` | S |
| P1 | Runtime never reads its own state store when deciding | The core thesis | `runtime.go:495-697` | Read `States.Current` in `ValidateAction` | M |
| P1 | No TOCTOU protection: no lease, version, or CAS | Two concurrent proposals both validate stale | `runtime.go:557-697` | Entity version checked in-transaction | M |
| P1 | Audit records have no integrity mechanism | "Append-only" is a comment | §5 | Hash chain + `kiff verify --audit` | M |

## 4. Attacks executed

All in-process attacks ran from a **separate Go module** (`example.com/attacker`)
importing `github.com/kiff/kiff` via a `replace` directive, exactly as a
third-party embedder would. The approval store was **empty** throughout —
nothing had ever been approved — and the contract was
`ApprovalRequirement: ApprovalRequired`, `Risk: RiskCritical`.

### 4.0 Baseline — no bypass
```
[1. BASELINE (no bypass)]
  executed=false err=action requires approval
  verdict: REFUSED (boundary held)
```
**Verdict:** held. The harness is sound.

### 4.1 The conformance tests test the wrong property
`pkg/kiff/action/approval_boundary_compile_test.go` covers exactly two fixtures:
a struct literal naming `approved`, and an import of `internal/trust`. Both are
**compile-time**. Reflection and `unsafe` are runtime mechanisms the `internal/`
rule does not constrain, and neither is tested. Two design details make the
bypass work: `GrantApproval` never inspects `Grant.ok`, so a zero-valued grant is
accepted; and `applyApproval` short-circuits on an *inbound* approved bit:

```go
// runtime.go:699-702 (before)
if actionCtx.IsApproved() || actionCtx.ApprovalID == "" || r.Approvals == nil {
    return actionCtx, nil   // ← approval store never consulted
}
```

### 4.2 `unsafe` — write the unexported field
```go
v := reflect.ValueOf(&ac).Elem()
f := v.FieldByName("approved")
reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetBool(true)
```
```
[2. UNSAFE: set unexported `approved` field]
  executed=true err=<nil>
  result="refunded $999999 to attacker"
  verdict: >>> BYPASSED — EXECUTOR RAN WITHOUT APPROVAL <<<
  AUDIT: kind=action_validated actor=agent-1 msg="action validated"
  AUDIT: kind=action_executed  actor=agent-1 msg="action executed"
```
**What the record says:** the trail certifies the action as validated and
executed. **Verdict: broken.**

### 4.3 Pure reflection — no `unsafe` import
The `internal/` package rule is enforced by the compiler; reflection runs after
it. The un-nameable parameter type is recovered from the method's own signature.
```go
m := reflect.ValueOf(&ac).MethodByName("GrantApproval")
m.Call([]reflect.Value{reflect.Zero(m.Type().In(0))})
```
**Verdict: broken.** This is the most damaging variant — four lines, no unusual
imports, and it defeats the exact mechanism the README cites.

### 4.4 Permissive `Validator` via public config
No reflection at all. `runtime.Config.ActionValidator` is a public, documented
extension point, and approval enforcement lives *inside* the swappable interface.
```go
func (yesValidator) Validate(...) (action.ValidationResult, error) {
    return action.ValidationResult{RequiresApproval: false}, nil
}
```
**Verdict: broken.** Enforcement must not be delegated to a replaceable component.

### 4.5 Self-approval over HTTP — shipped example server
Against `examples/refund-agno/server`, started locally. Actor IDs are published
in the example's own source (`refund-agno/domain.go:77-79`), so a compromised
agent simply claims to be the human operator.
```
# 1. request approval as "ops-human"
{"approval":{"id":"appr-pwn","requested_by":"ops-human","status":"pending"}}
# 2. the SAME identity grants it
{"approval":{"requested_by":"ops-human","reviewed_by":"ops-human","status":"granted"}}
# 3. execute
{"outcome":"allowed","result":{"status":"succeeded","executed":true,
  "message":"refund of 25000 cents issued"}}
```
**What the record says:**
```
approval_required   actor=ops-human   approval required
approval_recorded   actor=ops-human   approval recorded
approval_granted    actor=ops-human   approval granted
action_validated    actor=ops-human   action validated
action_executed     actor=ops-human   action executed
```
Nothing objected to `requested_by == reviewed_by`. $250 moved, and the trail is
indistinguishable from a legitimate two-party approval. **Verdict: broken.**

Further: approvals bind to `(approval_id, entity_id, action_name)` — **not** to
parameters, so a grant can be replayed against different values; there is **no
expiry** field; and nothing invalidates an approval when state changes between
grant and execution.

### 4.6 State is asserted, not verified
```
$ grep -n "States\." pkg/kiff/runtime/runtime.go
224,231:  ← IngestEvent
306,314:  ← AllowedActions
(nothing in ValidateAction or ExecuteAction, lines 495-697)
```
`ActionContext.CurrentState` is a caller-supplied string the runtime never
verifies against its own store. Two shipped examples pass a hardcoded constant
(`support-ops/server/main.go:183`). **Verdict: the reality layer validates against
the proposer's claim about reality.**

### 4.7 TOCTOU — not demonstrated
Six concurrent `AUTO_REFUND` calls on one order produced one success and five
`state_not_allowed` refusals. The window did not open because the in-memory store
serialises under a mutex and the executor returns instantly. **This is not
evidence of safety** — there is no lease, version, or CAS, so with the Postgres
store and a network-calling executor the window is the full call duration.
Classified as **unverified risk**, not a confirmed defect.

## 5. Deterministic evidence

```
$ go build ./...        clean (16.9s)
$ go vet ./...          silent
$ go test -race -cover ./...    all packages pass
    pkg/kiff/action     87.0%      pkg/kiff/runtime    80.4%
    pkg/kiff/httpapi    64.8%      pkg/kiff/state      50.0%
    pkg/kiff/store/postgres   0.0%   ← see the correction below
$ govulncheck ./...
    10 reachable vulnerabilities. GO-2026-5004: SQL injection in pgx,
    trace: postgres.ApprovalStore.IsGranted → pgxpool.Pool.QueryRow
```
Integrity search across `pkg/kiff/audit` and `pkg/kiff/store` for
`hash|sha256|hmac|signature|prev_hash|chain|merkle`: only the phrase
"append-only" in six doc comments. No mechanism, no key.

**Correction — the 0.0% Postgres reading was a measurement artifact.** It was
first reported as "the production reference store is untested". It is not. The
suite is env-gated (`conformance_test.go:69` skips without
`KIFF_POSTGRES_TEST_URL`), and CI runs it against a Postgres 16 service
container — with a second step that fails the job if the suite *skipped* rather
than ran, guarding the exact mistake this audit made:

```yaml
- name: Fail if the suite skipped instead of running
  # A misconfigured DSN would make the gated suite skip and the job pass
  # green, which is worse than no job at all.
```

**A skipped test reports as 0.0%, identically to an unwritten one.** Before
reporting a package as untested, check for env gating and read CI. See §6.

**`gosec`** could not run initially (disk full, 407 MiB). Re-run after freeing
space: 23 issues across 102 files, of which **zero are material**. The single
HIGH (G703 path traversal, `cmd/kiff/auth.go:365`) is a false positive — a CLI
joining its own config directory and writing `0600`. The rest are `0o644` port
files in demo scaffolding, plus two genuine but minor hardening items
(G112/G114: example servers set no `ReadHeaderTimeout`). Reported because a
clean-enough result is evidence too: the reachable `govulncheck` findings were
the real ones.

## 6. What already works

- **Replay is real and demonstrated**, not asserted: `{"events_replayed":3,
  "matches":true,"materialized":"REFUNDED","replayed":"REFUNDED"}`.
- **Idempotency is well built** — atomic reserve-or-return with correct release
  on executor failure (`runtime.go:597-614`). It solves retries correctly; it
  simply is not a concurrency control, and the code does not claim to be.
- **The permission model is right**: authority resolves from the policy by actor
  ID, never from caller-supplied `Actor.Roles` — the documented rationale at
  `action.go:96-105` is exactly correct, and it defeated the first impersonation
  attempt.
- **SoD and reviewer authority are implemented carefully** at
  `approval_review.go:77-89`. The defect is the default, not the logic.
- **The scaffolded demo is the best asset in the project**: `make demo` runs an
  unguarded path that double-refunds beside a guarded path that refuses, in one
  ledger. Under 10 seconds from `kiff new` to first refusal.
- **Error taxonomy** (`allowed/blocked/approval_required/invalid`) is consistent
  across HTTP, CLI, and docs.
- **CI is stronger than the audit initially credited**: a Postgres conformance
  job against a real service container, a guard against that suite silently
  skipping, a `vet and gofmt` job, and a CLI scaffold golden-path job that builds
  the generated project and runs its tests.

## 7. Governance maturity by dimension

| Dimension | Status | Basis |
| --- | --- | --- |
| Identity & authentication | **not evidenced** | No authn in `httpapi`; actor read from body |
| Authorization & SoD | **partial** | Permissions correct; SoD implemented but off by default |
| Decision boundary integrity | **not evidenced** | Three working bypasses (§4.2–4.4) |
| State integrity & freshness | **not evidenced** | Caller-asserted state; no TOCTOU control |
| Record integrity & auditability | **partial** | Records are durable and complete; no tamper-evidence |
| Recoverability & replay | **evidenced** | Deterministic replay demonstrated; no schema versioning (P2) |

Note that two dimensions moved *up* after correcting audit errors. Publishing
those corrections is part of the deliverable, not an embarrassment to hide.

## 8. Fixes verified

P0 §4.2–4.4 closed by 41 lines across 3 files:

```
 pkg/kiff/action/action.go        | 13 ++++++++++++-
 pkg/kiff/internal/trust/trust.go |  7 +++++++
 pkg/kiff/runtime/runtime.go      | 23 ++++++++++++++++++++++-
```
`Grant.Valid()` rejects a zero grant; `applyApproval` unconditionally clears the
inbound bit and re-derives it from the store; a non-overridable approval backstop
runs in `ValidateAction` after the pluggable validator returns.

Re-running the attack suite: all four report `REFUSED (boundary held)`.
Re-running `go test -race -cover ./...`: **all packages pass, zero regressions.**

## 9. Remediation plan

1. Land the 41-line patch; add runtime conformance fixtures for reflection,
   `unsafe`, and hostile-validator. **S · before launch: YES**
2. Authenticator on `httpapi`; principal overrides body actor; SoD on by default.
   **M · YES**
3. `pgx@v5.9.2`, toolchain `go1.26.6`; govulncheck in CI. **S · YES**
4. Authoritative state read in `ValidateAction` + `ErrStateMismatch`. **M · YES**
5. Lead the README with the unguarded/guarded A/B; add a tour scene that runs the
   bypass and shows it refused. **S · YES**

## 10. Verdict

**Ship after P0 fixes** — roughly one focused week. The differentiation exists
and is demonstrated by code; it is mispositioned, not absent. But four attacks
reproducible in under a minute, against the one claim the project is named for,
make launching today a conversion of the strongest idea into the most quotable
liability.
