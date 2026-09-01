# Brick 29 - Scan Go agent actions from the KIFF CLI

Brick 29 adds `kiff scan [path]`, a dependency-free static analyzer for Go
agent code. It finds explicit tool entry points that can reach consequential
operations without a recognized KIFF decision earlier in the function.

## What Was Added

- Go AST analysis for `//kiff:tool`, common tool-registration calls, common
  handler fields, and explicit `-tool` entry points.
- Text, JSON, and SARIF output with configurable CI failure thresholds.
- A `kiff-governance` Agent Skill that runs the CLI, explains source-backed
  findings, and helps move consequential calls behind a real KIFF boundary.
- Package manifests for Codex, Claude Code, and Kiro/Agent Plugins.

## Scope

The scanner is intentionally conservative and Go-first. A clean result means
no supported path was found; it is not proof that every framework-specific
registration shape or runtime path is governed. Runtime enforcement still
belongs at the KIFF decision boundary before the side effect executes.
