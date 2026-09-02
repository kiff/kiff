# The client deliverable

A €15–20k engagement is bought on the report. The Markdown in
`report-template.md` is the *working* artifact — precise, diffable, and what
gets pasted into an issue tracker. It is not what you hand a client.

Produce **both**, from the same findings:

| Artifact | Audience | Format |
| --- | --- | --- |
| `AUDIT-<target>.md` | The engineers who will fix it | `report-template.md`, committed to the repo |
| The audit page | Whoever signed the cheque, and whoever they forward it to | A single self-contained HTML page |

Write the Markdown first. It forces the findings to be precise before any
design decision can hide a weak one. Then build the page from it — never the
reverse, and never let the page carry a claim the Markdown does not.

## What the page is for

It gets forwarded. A CTO opens it, reads the verdict, scrolls the attacks, and
sends the link to two other people. Design for that path:

- **The verdict is above the fold.** Scores, then one paragraph answering the
  question they actually asked. Never open with methodology.
- **Attack transcripts before prose.** Verbatim terminal output with a
  `HELD` / `BYPASSED` chip is the most persuasive thing in the document, and it
  survives forwarding. Analysis that reads well but shows no output does not.
- **The severity table is scannable.** Priority, problem, why it matters,
  evidence, fix, complexity — one row per finding, colour-coded by severity, so
  someone can triage without reading a paragraph.
- **What held gets equal visual weight.** A page that only lists failures reads
  as a hit piece and gets discounted. The section proving you tried to break
  things and failed is what makes the failures credible.

## Building it

Use the artifact tooling available in your host. In Claude Code that is the
Artifact tool: write a single `.html` file and publish it, which returns a
private URL the client can be given directly.

Load the `artifact-design` skill before writing the page. Treatment is
utilitarian-editorial: this is a serious technical document, not a landing page.
Some specifics that matter for this document type:

- **A real type pairing**, not a default sans. A monospace face for verdicts,
  `file:line` references and terminal output is doing semantic work here, not
  decoration — it separates evidence from argument at a glance.
- **Severity as a colour system**, distinct from the accent hue. P0 through P3
  must be distinguishable at a glance and in both light and dark themes.
- **Terminal blocks styled as terminals**, dark ground in both themes, with the
  bypass line highlighted. Wrap them in `overflow-x: auto` — a transcript that
  breaks the page layout undermines the whole impression.
- **Both themes**, because you do not control the reader's machine.
- **Name the page for the engagement**, not the format: `Acme Enforcement Audit`,
  not `Security Report`.

## The honesty rules survive the design pass

Everything in §6 of `SKILL.md` still applies, and design pressure is exactly
where it slips:

- An unverified risk stays labelled as one. It does not become a P1 because a
  table row looks thin.
- A tool that could not run is still named. A gap in the scoreboard is honest;
  a filled cell that implies coverage you do not have is not.
- Scores keep their anchors, and enforcement soundness stays capped while a
  stated guarantee is broken — however good the rest of the page looks.
- **Publish corrections in the page**, not only in the Markdown. A client who
  can watch the audit correct itself trusts the findings that survived; one who
  later discovers a quietly-dropped finding trusts none of them.

## Engagement framing

State scope, authorization basis, revision (`git describe --tags HEAD`) and
toolchain versions on the page itself, in a header block. An audit without a
pinned revision is not reproducible, and a client who cannot tell which commit
you looked at cannot act on it three weeks later.

Name the attack harness path and how to re-run it. The client paid for evidence
they can reproduce, not for your conclusions.
