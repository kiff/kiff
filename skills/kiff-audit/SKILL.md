---
name: kiff-audit
description: Run an adversarial governance audit of a codebase — extract the guarantees it claims, map its trust boundary, construct and execute attacks that try to break those guarantees, run the language's test and vulnerability tooling, and produce a consultant-grade report with P0/P1 findings and a launch verdict. Use for governance/assurance audits, enforcement-claim verification, pre-launch security review of agent systems, or when asked whether a system's safety claims actually hold. Do not use for routine linting, for a plain dependency scan, or when the user only wants static findings — use the kiff-governance skill for scan-and-fix work.
---

# KIFF Governance Audit

A scanner that is wrong wastes an afternoon. A governance boundary that can be
bypassed is worse than no boundary, because it emits an audit trail asserting the
system was governed when it was not. Audit accordingly: weight anything that
produces a false assurance signal above anything that merely fails loudly.

**A claimed guarantee is false until you have failed to break it.** Do not
certify a claim from reading the code. Write the attack, run it, paste the
output.

## 0. Scope and authorization — do this first, always

Adversarial testing on a codebase requires authorization.

1. Confirm the user owns the target or is authorized to test it. For paid
   engagements the contract is the authorization; name it in the report.
2. Confirm the boundaries: which repositories, which environments. **Never run
   attacks against a production deployment or a live tenant.** Attacks run
   against a local checkout or a disposable instance you started yourself.
3. If the target is third-party code the user does not own, stop and say so.

Record scope, authorization basis, revision audited (`git describe --tags HEAD`),
and toolchain versions. State them in the report — an audit without a pinned
revision is not reproducible.

**Pin the revision before you start.** Run `git describe --tags HEAD` and
`git log --oneline -1`, and check whether the checkout is behind the latest tag
or `origin/HEAD`. Auditing a stale tree and reporting a missing feature that
shipped two commits later is the most common way an audit loses credibility.

## 1. Extract the claimed guarantees

Read the README, the docs directory, the pricing or product pages, and the
package descriptions. Extract every **falsifiable** claim, verbatim, with
`file:line`. Typical shapes:

- "X cannot be forged / self-granted / bypassed"
- "every Y is recorded / signed / append-only"
- "Z is validated before the side effect runs"
- "conformance tests prove ..."

Separate falsifiable claims from positioning ("the reality layer for agentic
systems") — positioning is assessed in the report's differentiation section, not
attacked. Build an explicit claims table; it is the audit's spine, and every
claim must end the audit as `held`, `broken`, or `not assessable`.

Marketing surfaces count. A claim on a pricing page that the open-source code
does not implement is a finding, even when the hosted product implements it.

## 2. Map the trust boundary as implemented

Do not describe the boundary the docs describe. Trace it.

- For each security-relevant decision, find what data it reads and **who supplies
  that data**. Caller-supplied facts that the system could have derived itself
  are the richest source of findings.
- Follow the enforcement path end to end: request → identity → authorization →
  decision → side effect → record. Name the function that makes each call.
- Identify every pluggable interface on the enforcement path. Enforcement
  delegated to a swappable component is enforcement a caller can replace.
- Note where a compile-time or type-level protection is claimed. Those are
  runtime-bypassable by reflection in most languages.

Where a repo-map tool is available (`graft callers <symbol>`, `graft grep`,
`graft skeleton <file>`), use it to trace call graphs instead of reading whole
files — it is faster and covers more ground per token.

## 3. Derive falsification hypotheses

For each claim from §1, write the specific way it could fail. Standard families:

| Family | Ask |
| --- | --- |
| Forged trust token | Can the "you are approved/authenticated" bit be set by the caller? |
| Delegated enforcement | Can a pluggable component waive the check? |
| Asserted state | Does the decision trust a fact the caller supplied? |
| Identity | Is the principal established by transport, or read from the body? |
| Separation of duties | Can one principal both request and approve? |
| Binding | Is approval bound to the payload, or only to an id? Does it expire? |
| TOCTOU | Can state change between decision and side effect? Lease? Version? |
| Record integrity | Can the record be rewritten? Hash-chained? Signed? By what key? |
| Replay | Is the log the source of truth? Deterministic? Schema-versioned? |
| Fail-open | On timeout, error, or misconfiguration, does it allow or refuse? |
| **Never invoked** | **Can the call reach its side effect without the boundary being consulted at all?** |

The last family is the one most often missed, because every other row assumes a
decision happened and was then subverted. Ask it separately and concretely: which
calls are *not* on the enforced path, what happens to a tool/route/handler that
nobody registered, and — critically — **what does the record say about those?** A
system that synthesises an approval for a call it never evaluated is emitting a
false clearance, which is worse than a refusal and worse than silence. Look for
defaults on the unregistered path: an `else` branch, a `None`/`nil` binding, an
unmatched route, a missing config key.

## 4. Construct attacks from an external position

This is the step that separates an audit from a code review.

- Attack from **outside the trust boundary**: a separate module, package, or
  process that consumes the target the way a third party would.
  - **Go**: a separate module with a `replace` directive.
  - **Python**: a separate directory with its own virtualenv, installing the
    **built artifact** (`python -m build` then `pip install dist/*.whl`).
    `pip install -e .` from inside the repo is *not* external — it is the
    project's own development install, and the repo's existing venv usually
    already is one. If you attack from there you have proven nothing.
  - **JavaScript/TypeScript**: `npm pack`, then install the tarball in a
    separate package.
  Never a same-package test: it can reach internals no third party can, so it
  proves nothing about an external caller either way.
- Include a **baseline** attack with no bypass, asserting the boundary refuses
  normally. If the baseline does not refuse, the harness is wrong, not the target.
- Attack the smallest thing that matters. One forged bit that makes a
  money-moving executor run beats ten style findings.
- Make the consequence observable: a flag the executor sets, a ledger row, a
  side effect you can print.
- **Then read the record.** After a successful bypass, dump the audit trail. A
  bypass that the trail reports as legitimate is materially worse than one that
  leaves a visible error, and the report must say so.

For an HTTP surface, start a shipped example server yourself and attack it with
`curl`. Ship-default configurations are in scope: what the project ships is what
users will run.

## 5. Run the tool belt

Deterministic evidence, per language. Capture exact commands and real output.

- **Go**: `go build ./...`, `go vet ./...`, `go test -race -cover ./...`,
  `govulncheck ./...` (reachability-aware — prefer it), `gosec ./...`, and
  `kiff scan -format json -fail-on none .`
- **Python**: `pytest` (or the project's runner), `pip-audit`, and `bandit` or
  `semgrep` for static security analysis. `kiff-scan evidence . --format json`
  when that package is installable — it is a separate PyPI distribution, so
  treat it as optional and say so if you could not obtain it. There is no
  Python equivalent of `-race`; note the absence rather than implying coverage.
- **JavaScript/TypeScript**: `npm audit --omit=dev` (**always pass
  `--omit=dev`** — a bare `npm audit` reports build-tool vulnerabilities that
  never ship, and filing one of those as critical against a library is a
  credibility-destroying false positive), plus the project's test, typecheck,
  and build scripts.
- **Any language**: dependency vulnerability scan, the test suite with coverage,
  a static security analyser, and a concurrency checker where the language has
  one.

**Run the linter the project runs, not the linter you know.** Read CI first. A
bare `ruff`/`eslint` against a project with no configuration for it produces
hundreds of findings the maintainers never opted into; reporting them is noise
that buries the real findings. If CI does not run a linter, say that — it is
itself a small finding — and do not substitute your own defaults for theirs.

Report coverage per package, not just the total — an untested module on the
enforcement path is a finding regardless of the headline number. But **a skipped
test reports as 0.0%, identically to an unwritten one.** Before calling a package
untested, check whether its suite is gated on an environment variable, a build
tag, or a service dependency, and read the CI configuration. A package with no
local coverage may be fully covered in CI against a real backing service. A vulnerability
is only a finding when it is **reachable**; prefer tools that prove reachability
and quote the call trace.

When a tool cannot run (not installed, no disk, no network), say so explicitly
in the report. Never let a tool you did not run read as a clean result.

## 6. Classify honestly

Every finding is exactly one of:

- **Confirmed defect** — you ran it and it broke. Include the reproduction.
- **Unverified risk** — the code path looks wrong but you did not demonstrate it.
  Say "not demonstrated" and explain what would confirm it. Never promote a
  hypothesis to a finding because it is plausible.
- **Positioning problem** — the code is correct; the claim overstates it. The fix
  is wording, not engineering. Keep these separate from defects; conflating them
  destroys the report's credibility.
- **Not assessable** — outside what the evidence can establish. Never convert a
  gap in evidence into a pass.

Also record **what held**. An audit that lists only failures reads as hostile and
gets dismissed. When an attack fails, say the boundary held and why. Read the CI
configuration specifically looking for controls the target already has — crediting
them is what makes the criticisms land.

When you find you were wrong mid-audit, **publish the correction in the report**
rather than quietly deleting the finding. A reader who can see the audit correct
itself trusts the findings that survived.

Severity: **P0** launch blocker — any bypass of a stated enforcement guarantee,
or a remotely exploitable vulnerability. **P1** high leverage before a public
launch. **P2** soon after. **P3** later.

For every P0 and P1 give: exact problem, evidence (`file:line`, command, output),
why it matters to users, what top-1% looks like, the **smallest** change that
closes the gap, files involved, complexity S/M/L, and before-launch YES/NO.

## 7. Verify the fix

For each P0, implement the smallest fix, then re-run **both** the attack and the
baseline — a fix that refuses everything also "closes" the attack, and only the
baseline catches that. Then re-run the full suite and report the diffstat.

**A failing test is not always a broken fix.** If the suite encoded the defect as
correct behaviour — a test asserting the very thing you just stopped — then
inverting that test is part of the fix, not a regression. Say so explicitly in
the report and quote the old assertion: *a passing suite was part of why this
survived* is one of the most useful sentences an audit can contain. Distinguish
that case carefully from a fix that genuinely broke unrelated behaviour.

A P0 with a verified patch is worth far more than a P0 with a suggestion. Leave
the patch uncommitted unless asked, and say plainly where it is.

**Keep the attack harness.** Save it somewhere the reader can run it and name the
path in the report. An audit whose attacks cannot be re-run is a claim; one whose
attacks ship is evidence, and it becomes the regression test for the fix.

## 8. Report

Produce **two** artifacts from one set of findings: the Markdown working report
for the engineers who will fix things, and a self-contained HTML page for
whoever commissioned the audit and whoever they forward it to. Write the
Markdown first — it forces the findings to be precise before any design decision
can dress up a weak one — then build the page from it. See `deliverable.md` in
this skill directory for what the page must do and the honesty rules that
survive the design pass.

Use `report-template.md` in this skill directory. Lead with the executive verdict
and the guarantees tested; put attack transcripts before prose analysis. Score
conservatively — an inflated score makes every other number unreliable.

**Polyglot targets.** When one repository ships the same product in several
languages, audit each implementation separately and expect them to differ — a
defect fixed in one is routinely still live in the other, and that gap is itself
a finding. Say which language each finding applies to, never assume parity, and
if the complete fix spans two languages say which half you verified. Where the
languages disagree on a documented guarantee, the README that ships with each
package governs that package.

`examples/kiff-framework-audit.md` is the reference audit: a real engagement
against `github.com/kiff/kiff` in which three of four attacks on the central
enforcement claim succeeded. Read it before writing your first report to
calibrate depth, tone, and evidence density.

## Boundaries

- Do not run attacks against production or against infrastructure the user does
  not own.
- Do not write exploit code that persists, exfiltrates, or damages data. Attacks
  demonstrate a control failure and stop there.
- Do not claim a guarantee holds because you could not think of an attack. That
  is "not assessable", not "held".
- Do not recommend a rewrite unless a P0 forces one. Prefer small, high-leverage
  changes with named files.
- Do not soften a finding because the project is early or the author is present.
