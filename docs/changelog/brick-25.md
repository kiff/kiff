# Brick 25 - Verify a Domain Package

Brick 25 adds `kiff verify`: a command that checks a domain package is complete and internally consistent before it governs anything — the design-time counterpart to `kiff new` / `kiff scaffold`. `kiff scaffold` (brick 24) emits a domain skeleton with **TODO executor stubs**; `kiff verify` is how a developer (or the AI agent finishing the domain with them) knows that skeleton is actually done and sound: every action bound to a real executor, no orphan stubs, the state machine and each action's allowed states mutually consistent.

## What Was Added

- `cmd/kiff/verify.go` — static analysis of a domain package into `domainFacts`:
  - Parses the package's `.go` files (two passes: collect string constants, then extract facts), resolving identifier constants to their values.
  - Recognizes the conventional shape `kiff new`/`kiff scaffold` and the examples emit: `action.ActionContract` literals (both standalone and inline `[]action.ActionContract{ {…}, … }` elements with elided types), `state.Transition{…}` literals, and the `domain.Builder` chain (`.Event`, `.Transition`, `.Allow`, `.SetAllowedActions`).
  - Detects a stub executor by scanning the executor `FuncLit` body for a `TODO` marker; skips zero-value `ActionContract{}` literals (e.g. lookup-helper returns) and de-duplicates by name.

- `cmd/kiff/verify_checks.go` — the checks, report, and command:
  - **Executor backing** — flags any action still bound to a scaffold stub (TODO) or with no executor.
  - **State-machine consistency** — flags actions allowed from states no transition reaches (reachability walk from the bootstrap transition), transitions referencing undeclared events, and a missing bootstrap transition.
  - **Contract completeness** — flags missing/invalid `Risk` or `ApprovalRequirement`, empty `AllowedStates`, and allowed actions with no contract.
  - Emits a single `not_a_domain` finding when a path has no domain marker at all, so "this isn't a domain" never reads like "this domain is broken."
  - Human-readable output by default; `-json` for tooling/CI; **non-zero exit on any error finding** (warnings do not fail).
  - `resolveDomainDir` probes `<path>`, `<path>/domain`, and `<path>/internal/domain`, preferring the candidate that declares contracts, so `kiff verify <project-root>` works for both the starter and agentic-ops layouts.

- `cmd/kiff/verify_test.go` — a complete domain (pass), a stub executor (fail), an orphan allowed state (fail), an undeclared event (fail), the inline-slice catalog shape (pass), `internal/domain` resolution, the `not_a_domain` signal, and a cross-check that freshly scaffolded output fails verify until the executors are implemented.

- `main.go` registers the `verify` subcommand; `docs/scaffold-a-domain.md` gains a short "verify the domain" section.

## Why

`kiff scaffold` generates a domain skeleton with TODO executor stubs. `go build` and `go test` catch compile and test failures, but not "this action contract still resolves to a generated stub" or "this action is allowed only from a state nothing transitions into." Those are exactly the gaps a half-finished domain leaves, and they are silent — the project compiles and its generated tests pass while the executors are still placeholders.

`kiff verify` turns "is this domain real and complete?" into one runnable check. It is useful in two places: while finishing a scaffolded domain (run it until it stops complaining), and in CI (the `-json` output is meant for machines, not just humans, and the non-zero exit fails the build on a regression).

## Static Analysis, on Purpose

`verify` does not build or run the domain. Two facts forced this and make it the right call:

- The runtime's `state.TransitionMachine` keeps its transitions and allowed-action map private, and the `StateMachine` interface exposes neither — so a built `domain.Definition` cannot answer "which states are reachable?"
- An executor is a Go `func` value. Nothing at runtime can tell a real implementation from a stub without **executing** it, and executing arbitrary executors to probe them is exactly the side-effecting behavior a verifier must not do.

Static analysis sidesteps both: it is safe (no user code runs), fast, works even when the package's imports are not resolvable, and can see the `TODO` marker a stub leaves behind. The cost is that it targets the conventional construction `kiff new`/`kiff scaffold` emit (and that the in-repo examples follow); a domain that assembles its state machine fully dynamically is not visible to it. That tradeoff matches KIFF's Go-first, convention-driven shape.

## Pairs With Scaffold

The two commands bracket the authoring loop:

- `kiff scaffold` → emit a faithful domain skeleton with TODO executors and passing tests.
- *(write the executor bodies and the real policy)*
- `kiff verify` → confirm nothing is still a stub and the state machine is sound.

A freshly scaffolded domain **fails** `kiff verify` by design — that failure is the signal it is not yet ready to govern anything. When `verify` passes, the TODO seams are gone.

## Limitations

- Targets the conventional domain shape (identifier constants, `ActionContract` literals, `state.Transition` / builder calls). A domain that computes transitions or contracts dynamically is not fully visible to static analysis.
- Stub detection keys on a `TODO` marker in the executor body — the signal `kiff scaffold` leaves. An executor that is incomplete but carries no `TODO` is not flagged; that is a judgment call left to tests and review.
- `verify` checks structure, not behavior. It confirms a domain is wired and complete; whether the executor bodies do the right thing is what the domain's own tests are for.
