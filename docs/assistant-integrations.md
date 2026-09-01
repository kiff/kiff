# Use KIFF with coding assistants

The `kiff-governance` skill lets Codex, Claude Code, or Kiro scan a Go agent,
explain what it finds, and help fix ungoverned actions.

First, install the CLI:

```bash
go install github.com/kiff/kiff/cmd/kiff@v0.8.0
kiff scan -h
```

The skill needs `kiff` on your `PATH`. It does not bundle the binary.

## Codex

Install the skill directly while the plugin is not yet in a marketplace:

```bash
git clone --depth 1 https://github.com/kiff/kiff.git ~/.local/share/kiff
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
ln -s ~/.local/share/kiff/skills/kiff-governance \
  "${CODEX_HOME:-$HOME/.codex}/skills/kiff-governance"
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

The assistant runs `kiff scan`, reads the reported source, and explains each
finding. If you ask it to make changes, it adds a KIFF decision before the
risky call, runs the relevant Go tests, and scans again. It can also add SARIF
reporting and a CI failure threshold.

Try prompts such as:

```text
Scan this repository. Explain the findings but do not change code.
Fix the high-severity findings, run the tests, and scan again.
Add KIFF SARIF reporting to CI.
```

`kiff scan` currently supports Go. A clean result means the scanner found no
supported ungoverned path; it is not proof that the application is safe. The
KIFF runtime, not the assistant, makes the final allow, block, or approval
decision before an action runs.

The integrations are available from GitHub but are not yet listed in public
assistant marketplaces. See the platform docs for [Claude Code plugins](https://code.claude.com/docs/en/plugins)
and [Kiro Powers](https://kiro.dev/docs/powers/create/).
