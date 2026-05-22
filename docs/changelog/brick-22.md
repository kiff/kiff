# Brick 22 - Agentic-Ops Template

Brick 22 adds a second template to the `kiff` scaffolder: `agentic-ops`. The existing `starter` template scaffolds a small Go domain with the HTTP API. The `agentic-ops` template scaffolds a complete agentic-ops project — Go domain, HTTP server, Agno agent (offline + Bedrock), Makefile, demo script, README — that runs the full governed-agent loop end to end in under five minutes from `kiff new`.

## What Was Added

- `cmd/kiff/templates/agentic-ops/` — the embedded template tree:
  - `go.mod.tmpl`
  - `Makefile` (loads `../.env` then `agent/.env`)
  - `README.md`
  - `.gitignore.tmpl`
  - `cmd/server/main.go`
  - `internal/domain/refund.go` (+ `refund_test.go`)
  - `agent/agent.py`, `agent/run_no_kiff.py`, `agent/run_with_kiff.py`
  - `agent/requirements.txt`, `agent/.env.example`
  - `scripts/demo.sh`

- Scaffolder changes in `cmd/kiff/`:
  - `main.go` accepts a `-template` flag (default: `starter`).
  - `new.go` resolves the requested template via `resolveTemplate`, holds two embedded `fs.FS` values (one per template), and threads a `templateSpec` through `scaffold`, `renderFile`, and `rewriteImports`. The scaffolder strips any `.tmpl` suffix on output and writes `*.sh` files with the executable bit set.
  - `new_test.go` adds `TestScaffold_AgenticOps_LayoutAndImports` (asserts the scaffolded layout, import rewrites, and `*.tmpl` suffix handling) and `TestResolveTemplate` (asserts the `-template` flag resolution and the unknown-template error).

## Why

Brick 3 established the first template (`starter`) and the `kiff new <module>` shape. That template proves the loop with the smallest possible Go project. It does not include an agent, because agents pull in Python, an LLM provider, and demo scripts that are out of scope for "the smallest path to a working KIFF backend."

`agentic-ops` is the next size up. It's the template you reach for when you want to demo or evaluate KIFF *as governance for an AI agent*. It includes:

- A working Go domain (refund) with state machine, contracts, permission policy.
- An HTTP server wiring the domain into the runtime + httpapi handler.
- An Agno agent with both offline and Bedrock providers, structured output, and the same `--auto` flow `examples/refund-agno` uses.
- A `make demo` target that spawns the server, runs the agent, prints the audit timeline, and shuts down — under five minutes from a clean directory.

In other words: `agentic-ops` ships the entire `examples/refund-agno` shape as a template, parameterized to the user's module path.

## Two Templates, One Scaffolder

The scaffolder now resolves `-template=<name>` against a small registry. Adding new templates is a known-cost exercise:

1. Add the template tree under `cmd/kiff/templates/<name>/`.
2. Add a second `embed.FS` at the top of `new.go`.
3. Register it in `resolveTemplate`.
4. Add a layout test mirroring `TestScaffold_AgenticOps_LayoutAndImports`.

The `templateSpec` value carries the embedded fs, the in-tree import prefix to rewrite, and the conventional starting point. Everything else (rendering, suffix stripping, executable bits, README templating) is shared.

## Pattern

Brick 22 establishes the rule for future templates: **one template per pitch surface**.

- `starter` → "I want the smallest KIFF project that runs."
- `agentic-ops` → "I want a KIFF project with a real agent in front of it."
- (future) `marketplace-ops`, `fintech-ops`, etc. → vertical accelerators on top of the same `agentic-ops` shape, with domain vocabulary swapped.

Templates are not docs. They are runnable starting points. If a template needs a paragraph of explanation to use, it is doing too much.

## Limitations

- The agent in the scaffolded project is the same offline-first / Bedrock-second shape as `refund-agno`. Other LLM providers (OpenAI, Anthropic native, local Ollama) are not yet templated.
- The template's domain is a refund flow. Users adopting it for a different domain need to rename and rewrite, just like with `starter`. A future brick might templatize the domain vocabulary itself.
- The template uses Python for the agent. Go-only or TypeScript-only agentic templates are possible but not present in this brick.
