# Use KIFF with coding assistants

Two skills ship from this repository. Both work in Codex, Claude Code, and Kiro.

| Skill | What it does | When to reach for it |
| --- | --- | --- |
| [`kiff-governance`](#kiff-governance) | Scans agent code, explains ungoverned actions, and helps fix them | Day-to-day development on your own code |
| [`kiff-audit`](#kiff-audit) | Adversarially audits the guarantees a codebase claims, by attacking them | Pre-launch review, assurance work, verifying an enforcement claim |

`kiff-governance` asks *where is this code ungoverned?* `kiff-audit` asks *does
the protection this project claims actually hold?* — and answers by trying to
break it.

Choose the scanner for your repository:

```bash
go install github.com/kiff/kiff/cmd/kiff@v0.8.0  # Go
uvx kiff-scan scan .                             # Python
```

`kiff scan` analyzes Go. Python uses the separate
[kiff-scan](https://github.com/kiff/kiff-scan) tool, which also provides the
`kiff/kiff-scan@v0.1.1` GitHub Action. Install the scanner you need before
asking the assistant to use it.

<a id="kiff-governance"></a>

## Codex

Install the skills directly while the plugin is not yet in a marketplace:

```bash
git clone --depth 1 https://github.com/kiff/kiff.git ~/.local/share/kiff
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
for skill in kiff-governance kiff-audit; do
  ln -s ~/.local/share/kiff/skills/"$skill" \
    "${CODEX_HOME:-$HOME/.codex}/skills/$skill"
done
```

Start a new session and ask:

```text
$kiff-governance scan this Go agent and explain the findings
```

To update, run `git -C ~/.local/share/kiff pull --ff-only`.

## Claude Code

Clone KIFF, then load it as a plugin:

```bash
git clone --depth 1 https://github.com/kiff/kiff.git ~/.local/share/kiff
claude --plugin-dir ~/.local/share/kiff
```

Then ask:

```text
/kiff-governance:kiff-governance scan this Go agent and fix the high findings
```

To load the plugin automatically in future sessions, clone it into
`~/.claude/skills/kiff-governance` instead.

## Kiro

1. Open **Powers** in the Kiro IDE.
2. Choose **Add Custom Power**, then **Import power from GitHub**.
3. Enter `https://github.com/kiff/kiff`.
4. Ask Kiro to scan your Go agent for ungoverned actions.

## What happens next

The assistant runs the scanner for the repository language, reads the reported
source, and explains each finding. If you ask it to make changes, it adds a
KIFF decision before the risky call, runs the relevant tests, and scans again.
It can also add SARIF reporting and a CI failure threshold.

Try prompts such as:

```text
Scan this repository. Explain the findings but do not change code.
Fix the high-severity findings, run the tests, and scan again.
Add KIFF SARIF reporting to CI.
```

`kiff scan` supports Go and `kiff-scan` supports Python. A clean result means
the scanner found no supported ungoverned path; it is not proof that the
application is safe. The KIFF runtime, not the assistant, makes the final
allow, block, or approval decision before an action runs.

The integrations are available from GitHub but are not yet listed in public
assistant marketplaces. See the platform docs for [Claude Code plugins](https://code.claude.com/docs/en/plugins)
and [Kiro Powers](https://kiro.dev/docs/powers/create/).

<a id="kiff-audit"></a>

## Running a governance audit

`kiff-audit` is a different kind of work from scanning, and it is worth being
deliberate about when you invoke it. It reads what a project claims, tries to
falsify those claims by writing and running attacks, runs the language's test
and vulnerability tooling, and produces a report with P0/P1 findings and a
verdict. Expect it to take a while and to run code.

Ask for it explicitly:

```text
Audit this repository's governance guarantees and try to break them.
Audit the approval boundary specifically. Write the attacks and run them.
Re-audit and verify my fix actually closes the bypass.
```

### Before it runs

The skill opens with a scope-and-authorization step and will ask you to confirm:

- **that you own the target or are authorized to test it.** For a paid
  engagement, the contract is the authorization; it gets named in the report.
- **which repositories and environments are in scope.** Attacks run against a
  local checkout or a disposable instance the skill starts itself. It will not
  attack a production deployment or a live tenant.
- **the exact revision.** It records `git describe --tags HEAD` and checks
  whether your checkout is behind the latest tag, because an audit that reports
  a feature as missing when it shipped two commits later is worse than no audit.

If the target is third-party code you do not own, the skill stops.

### What you get back

The report follows [`report-template.md`](../skills/kiff-audit/report-template.md).
The parts that matter most:

- **Guarantees tested** — every falsifiable claim it extracted, each ending as
  `held`, `broken`, or `not assessable`. No claim is allowed to go unresolved.
- **Attacks executed** — verbatim transcripts, including a baseline that must be
  refused. After a successful bypass it dumps the audit trail, because a bypass
  the trail records as legitimate is far worse than one that errors visibly.
- **What already works** — a required section. An audit that lists only failures
  is not credible and is easy to dismiss.
- **Governance maturity by dimension** — identity, authorization and separation
  of duties, decision-boundary integrity, state integrity, record integrity,
  recoverability.

### How it treats uncertainty

This is the part to check when reading any report it produces:

- A claim is **false until an attack against it has failed.** It will not
  certify a guarantee from reading the code.
- Something it suspects but did not demonstrate is reported as an **unverified
  risk**, not a finding.
- A correct implementation with an overstated claim is a **positioning problem**,
  reported separately from defects. Conflating the two destroys a report.
- A tool that could not run is named as such. It never lets a skipped tool read
  as a clean result.
- Anything outside what the evidence can establish is **not assessable** — never
  silently converted into a pass.

### Reference audit

[`skills/kiff-audit/examples/kiff-framework-audit.md`](../skills/kiff-audit/examples/kiff-framework-audit.md)
is a real engagement against this repository, in which three of four attacks on
the self-approval boundary succeeded. It is the calibration example: read it to
see the expected depth and evidence density, and to see the corrections an
honest audit has to publish about its own mistakes.

### Evidence collectors

The skill is language-neutral and delegates deterministic evidence to whatever
tool fits the repository — `kiff scan` for Go, `kiff-scan` for Python, the
project's own tooling elsewhere. Collectors target the shared contract in
[`skills/kiff-audit/evidence-schema.json`](../skills/kiff-audit/evidence-schema.json),
which fixes one sink taxonomy across languages so findings are comparable in a
polyglot repository. Nothing in the Go framework depends on the Python package.

The canonical category IDs are `MONEY`, `DATA_LOSS`, `DATABASE`, `IDENTITY`,
`COMPUTE`, `DEPLOYMENT`, `NETWORK`, and `EXECUTION`, adopted from `kiff-scan`,
which is the more mature implementation. Each carries a `state_dependent` flag —
a consequence whose safety cannot be decided from the call alone is exactly the
class a governance runtime exists to gate.

**Known gap:** `kiff scan` currently emits display strings (`"money movement"`,
`"data loss"`) rather than these IDs, and collapses `COMPUTE` and `DEPLOYMENT`
into a single `"infrastructure"` value. Until it migrates, an audit spanning
both languages should normalise Go findings before comparing them.
