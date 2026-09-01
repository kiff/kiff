# Use KIFF with coding assistants

KIFF ships one portable `kiff-governance` skill for Codex, Claude Code, and
Kiro. The skill teaches an assistant to run the native Go scanner, inspect each
reported path, explain the risk, and help place the call behind a real KIFF
decision boundary.

The assistant does not replace KIFF. It helps find and fix source-level gaps;
the KIFF runtime remains the authority that allows, blocks, or holds an action
for approval before execution.

## Prerequisite

Install the CLI and confirm that the scanner is available:

```bash
go install github.com/kiff/kiff/cmd/kiff@v0.8.0
kiff scan -h
```

The assistant packages do not bundle the binary. The `kiff` command must be on
the assistant's `PATH`.

## Codex

Until the plugin is listed in a Codex marketplace, install the skill directly:

```bash
git clone --depth 1 https://github.com/kiff/kiff.git ~/.local/share/kiff
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
ln -s ~/.local/share/kiff/skills/kiff-governance \
  "${CODEX_HOME:-$HOME/.codex}/skills/kiff-governance"
```

Start a new Codex session, then invoke it explicitly or describe a matching
task:

```text
$kiff-governance scan this Go agent and explain every ungoverned action
```

Update it with:

```bash
git -C ~/.local/share/kiff pull --ff-only
```

The repository also contains `.codex-plugin/plugin.json` for marketplace
packaging.

## Claude Code

Claude Code can load the repository as a plugin for one session:

```bash
git clone --depth 1 https://github.com/kiff/kiff.git ~/.local/share/kiff
claude --plugin-dir ~/.local/share/kiff
```

Invoke the namespaced skill:

```text
/kiff-governance:kiff-governance scan this Go agent and help fix the findings
```

For automatic loading in future sessions, clone the repository under Claude's
personal skills directory instead:

```bash
git clone --depth 1 https://github.com/kiff/kiff.git \
  ~/.claude/skills/kiff-governance
```

The `.claude-plugin/plugin.json` manifest and root-level `skills/` directory
are discovered by Claude Code.

## Kiro

Kiro uses the root `plugin.json` as an Agent Plugins Power:

1. Open the **Powers** panel in the Kiro IDE.
2. Select **Add Custom Power**.
3. Select **Import power from GitHub**.
4. Enter `https://github.com/kiff/kiff`.
5. Ask Kiro to scan a Go agent for ungoverned consequential actions.

Kiro activates the Power from matching terms such as `kiff`, `agent tools`,
`governance`, `static analysis`, and `sarif`. No MCP server or credentials are
required for scanning.

## What the assistant does

For a scan request, the skill tells the assistant to:

1. Run `kiff scan -format json -fail-on none .`.
2. Inspect the tool, consequential call, file, and line for each finding.
3. Explain what is ungoverned and the scanner's limits.
4. Modify code only when asked, placing a real KIFF decision before the side
   effect and preserving fail-safe behavior.
5. Run relevant Go tests and scan again.
6. Help add SARIF reporting and enforcement to CI when requested.

Useful prompts include:

```text
Scan this repository and explain the KIFF findings without changing code.
Fix the high-severity KIFF findings, then run the tests and scan again.
Add KIFF SARIF reporting and a high-severity CI gate.
```

## Current limits

- Native `kiff scan` currently analyzes Go source.
- A clean scan means no supported path was found; it is not a security proof.
- Framework-specific tool registration may require `-tool FunctionName` or a
  `//kiff:tool` marker.
- The assistant integrations are installable from GitHub but are not yet
  listed in the Codex, Claude Code, or Kiro public marketplaces.

Platform references: [Claude Code plugins](https://code.claude.com/docs/en/plugins)
and [Kiro Powers](https://kiro.dev/docs/powers/create/).
