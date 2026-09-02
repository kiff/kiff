# Governance Audit — <target>

> Replace every angle-bracket placeholder. Delete sections that do not apply
> rather than filling them with "N/A" prose. Attack transcripts are verbatim
> tool output, never paraphrased.

## Engagement

| | |
| --- | --- |
| Target | `<repo>` |
| Revision | `<git describe --tags HEAD>` (`<short sha>`) |
| Scope | `<repos, environments, what was excluded>` |
| Authorization | `<contract / ownership basis>` |
| Toolchain | `<go version, python version, os/arch>` |
| Date | `<YYYY-MM-DD>` |

## 1. Executive verdict

Scores 1–10, conservative: product value · enforcement soundness · technical
quality · UX/DX · differentiation · launch readiness.

One paragraph answering the question the client actually asked (ship it? trust
it? buy it?). Lead with the single most consequential finding.

## 2. Guarantees tested

The spine of the audit. Every falsifiable claim extracted in §1 of the method,
with its verdict. No claim may be missing a verdict.

| Claim (verbatim) | Source | Verdict | Evidence |
| --- | --- | --- | --- |
| "<claim>" | `<file:line>` | held / **broken** / not assessable | §4.1 |

## 3. Critical gaps

| Priority | Problem | Why it matters | Evidence | Fix | Complexity |
| --- | --- | --- | --- | --- | --- |

## 4. Attacks executed

One subsection per attack. Always include the baseline first.

### 4.0 Baseline — no bypass
**Position:** `<external module / separate package / unauthenticated HTTP>`
**Expectation:** refused.

```
<verbatim output>
```
**Verdict:** held.

### 4.1 `<attack name>`
**Hypothesis:** `<which claim this tries to falsify>`
**Attack:**
```
<the code or commands>
```
**Result:**
```
<verbatim output>
```
**What the record says:** `<dump the audit trail — did it report the bypass as legitimate?>`
**Verdict:** **broken** / held.
**Smallest fix:** `<change>` in `<files>`.

## 5. Deterministic evidence

Exact commands and real output: build, vet, tests with race and coverage,
vulnerability scan, agent-tool scan. Coverage per package on the enforcement
path, not only the total.

**Tools that could not run:** `<name them and why — never let a skipped tool
read as clean>`

## 6. What already works

Non-negotiable section. Name the controls that held, the tests that are
genuinely good, and the design decisions worth keeping. An audit with no
positive findings is not credible.

## 7. Governance maturity by dimension

| Dimension | Status | Basis |
| --- | --- | --- |
| Identity & authentication | evidenced / partial / not evidenced / not assessable | |
| Authorization & separation of duties | | |
| Decision boundary integrity | | |
| State integrity & freshness | | |
| Record integrity & auditability | | |
| Recoverability & replay | | |

Use `not assessable` where evidence cannot establish the answer. Never convert
absent evidence into a pass.

## 8. Fixes verified

For each P0 fixed during the engagement: the diffstat, the re-run attack result,
and the full-suite regression result. State where the patch lives and whether it
is committed.

## 9. Remediation plan

The five highest-leverage items, ranked. Each: objective, why now, exact scope,
acceptance criteria, files, S/M/L, before-launch YES/NO.

## 10. Verdict

One of: **Ship** · **Ship after P0 fixes** · **Do not ship — <reason>**.
Then the shortest defensible path to the next state.
