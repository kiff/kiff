# Brick 20 - Depth Demo: refund-agno

Brick 20 adds the first end-to-end demo that pairs KIFF with a real agent framework. It exists to prove the pitch story at depth: the same Agno agent, the same prompt, the same model, run twice — once unguarded, once governed — and you can read the diff in your terminal.

## What Was Added

- `examples/refund-agno/` package containing:
  - `domain.go` / `domain_test.go` — KIFF domain with `RouteRefund`, which picks `AUTO_REFUND` (no approval) or `REFUND_ORDER` (approval required) based on the refund amount. Tests cover happy / denied / state-not-allowed / replay paths.
  - `server/` — `net/http` host exposing `/demo/agent/refund` and `/demo/rebuild`. The server validates state, parameters, and approval through the runtime; it never mutates anything itself. Four integration tests cover the demo HTTP surface.
  - `agent/agent.py` — Agno-shaped agent with two providers, `OfflineProvider` (deterministic, no network) and `BedrockProvider` (live LLM via AWS Bedrock). Both produce structured `_RefundDecision` outputs via Agno's `output_schema`.
  - `agent/run_no_kiff.py` — baseline that lets the agent mutate a mock DB directly.
  - `agent/run_with_kiff.py` — same agent, same prompts, but every action flows through KIFF. The `--auto` flag runs the full happy + denied + audit walk-through.
  - `tickets.json` — three deterministic tickets engineered to hit the auto-refund, granted-approval, and denied-approval paths.
  - `scripts/demo.sh` — spawn the server, run no-kiff, run with-kiff, shut down.
  - `Makefile` — `demo / demo-offline / demo-bedrock / test / build / clean`.
  - `README.md` — canonical offline transcript plus a Live Bedrock section.

## Why

Until now, the framework's pitch lived in `kiff-tour` — narrated, scripted, a known-good path. That makes a great 90-second demo, but it doesn't answer the question every prospective adopter eventually asks: *what happens when a real LLM produces the proposals?*

Brick 20 answers that question. The agent in `examples/refund-agno` is not a stub. It's an Agno agent with a system prompt, a structured output schema, and a real model behind it. The same agent runs twice. In the no-kiff run, it mutates a mock DB whatever it wants. In the with-kiff run, KIFF blocks risky proposals, audits everything, and replays the entity from events to verify the final state.

## Pattern

The demo establishes the pattern every future "agent + KIFF" example should follow:

1. **One agent, two runs.** Same prompt. Same model. Same tickets. The diff between unguarded and governed is the entire pitch.
2. **Offline-first.** The default `make demo` runs without AWS credentials. `make demo-bedrock` opts into a live LLM. New users can experience the framework in 60 seconds; serious evaluators can stress-test it against a real model in another 60.
3. **Structured agent output.** Agno's `output_schema=...` keeps the agent's decisions parseable on the Go side without prompt-engineering JSON conformance. KIFF's `proposal.ActionProposal` is the natural Go-side mirror.
4. **Replay verifies the run.** Every demo ends with `runtime.RebuildState` on each affected entity. Materialized state must match replayed state. If they don't, the demo fails loudly.

## Limitations

- Offline mode uses a deterministic provider, not a local model. It's a fixture, not an inference engine. The point is that the *governance behavior* is identical regardless of whether the proposal came from Bedrock or a stub.
- Bedrock access requires AWS credentials and an enabled foundation model. Pricing and latency are the operator's concern.
- The agent is single-turn. It produces one decision per ticket. Multi-turn agentic workflows (planner / executor / supervisor) are out of scope for this brick.
