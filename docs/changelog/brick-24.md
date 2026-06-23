# Brick 24 - Scaffold a Domain from a Descriptor

Brick 24 adds a second scaffolding path to the `kiff` CLI: `kiff scaffold`. Where `kiff new` scaffolds a project around the fixed `tasks` starter domain, `kiff scaffold` takes a JSON **domain descriptor** and emits a framework-faithful `domain/` package — state machine, action contracts with TODO executor stubs, permission policy, and passing tests — optionally inside the same starter project shell. It is the on-ramp for adopters who already know the domain they want, and a single canonical command any upstream generator (an AI coding agent, a template tool) can target.

## What Was Added

- `cmd/kiff/scaffold.go` — the `kiff scaffold` command and its generator:
  - `scaffoldDescriptor` (+ `descriptorTransition`, `descriptorAction`): the codegen seed type, decoded with `DisallowUnknownFields` and validated (unknown events/states, missing bootstrap transition, bad `risk`/`approval` enums, follow-up events that are not declared).
  - Identifier derivation: `ORDER_PLACED → EventOrderPlaced`, `PAID → StatePaid`, `REFUND_ORDER → ActionRefundOrder`, `refund.execute → PermRefundExecute`, with a collision guard.
  - A generation model plus two `text/template`s rendered through `go/format`, so emitted Go is always gofmt-clean.
  - `runScaffold`: flag parsing (`-descriptor`, `-dir`, `-force`, `-replace-local`, `-domain-only`), reading the descriptor from a file or stdin, and writing the output (reusing the existing `scaffold()` machinery for the full-project shell).

- `cmd/kiff/main.go` — registers the `scaffold` subcommand and documents it in the usage text.

- `cmd/kiff/scaffold_test.go` — golden tests against a fixed descriptor, a determinism check, a gofmt-clean check, descriptor-validation cases, and domain-only / full-project layout tests.

- `cmd/kiff/testdata/scaffold/` — a representative order/refund descriptor (`order-refund.json`) with one low-risk action and one approval-required action, plus the reviewed golden output (`*.domain.go.golden`, `*.domain_test.go.golden`).

- `docs/scaffold-a-domain.md` — usage and descriptor reference.

## What It Emits

The generated `domain/domain.go` mirrors the starter convention exactly so the output reads as idiomatic KIFF, not generated soup:

- One constants block — UPPER_SNAKE events/states/actions, dotted.lowercase permissions.
- `NewStateMachine()` with the declared transitions and `SetAllowedActions`.
- `NewPermissionPolicy()` that grants each role its permissions and assigns roles to actors along the proposer/approver line (see below).
- One `ActionContract` per action via a `xContract()` builder, with `AllowedStates`, `RequiredParameters`, `RequiredPermissions`, `Risk`, `ApprovalRequirement`, and an executor **stub** that returns a TODO-marked `ActionResult` plus the declared follow-up events.
- `NewDefinition()` / `NewInputAdapter()` / `NewRuntime()` / `NewRuntimeWithStores()` assembled with `domain.Builder`.

The generated `domain/domain_test.go` exercises the loop and the contracts:

- `TestLoop_HappyPath` ingests the bootstrap event, then walks each action through the state machine — requesting and granting approval where required — and asserts the final state.
- Per action: an allowed-state case and a blocked-from-wrong-state case. For approval-required actions the allowed-state case asserts the approval gate holds (`ErrApprovalRequired`) until a grant exists.

A freshly scaffolded project passes `go build ./...` and `go test ./...` out of the box, with only the executor bodies left as TODO.

## Why

A KIFF domain is executable software: a `state.TransitionMachine`, a set of `action.ActionContract`s whose executors are Go closures, and a `permission.Policy`, assembled with `domain.Builder`. Before this brick, the only path from "I know my domain" to "a buildable KIFF project" was `kiff new`, then deleting the `tasks` domain and hand-writing the builder calls and contracts. That translation is mechanical and error-prone — exactly the skeleton a generator (or an AI coding agent) should produce so the developer focuses on the executor bodies and the real policy.

Owning that scaffold in the framework means the emitted shape is faithful by construction: same conventions as the starter, validated by `domain.Builder.Build()`. It also gives any upstream tool one canonical, framework-owned command to turn a domain description into a buildable project, instead of each tool inventing its own emitter.

## Go-First, by Design

The framework parses **no YAML** and stays that way. The descriptor is a one-time code-generation seed, not a runtime artifact:

- The descriptor type lives under `cmd/kiff`, never `pkg/kiff`, so it cannot be mistaken for a declarative runtime loader. `domain.Builder.Build()` remains the only structural source of truth at runtime.
- Generation is pure local codegen — no network, no remote fetch.
- Executors are stubs. The framework never synthesizes business logic, and the scaffolder never invents thresholds, states, or approvals beyond what the descriptor states.

## Permission Policy Keeps the Boundary

The generated `NewPermissionPolicy()` does not grant every role to every actor. Roles are classified by what they grant: a role that grants any action's required permission is a proposer role (assigned to the agent and system actors), and the remaining oversight/approval roles go to the human operator. No actor holds both, so the generated default demonstrates proposer ≠ approver rather than collapsing it — the framework's crown-jewel property, visible in the very artifact meant to teach it. A TODO marks where to refine role membership for a real identity model.

## Two Commands, One Scaffold Machinery

`kiff scaffold` reuses the `starter` template and the existing `scaffold()` / `renderFile()` / `rewriteImports()` path for the project shell, then overlays the generated `domain/` package. The full-project layout is therefore identical to `kiff new`, with the import prefix rewritten to the user's module path; `-domain-only` skips the shell and emits just the domain package into an existing project.

- `kiff new` → "give me the reference project; I'll rename the starter domain."
- `kiff scaffold` → "I already know my domain; emit it faithfully and let me fill the executors."

## Limitations

- Executor bodies and the real policy are stubs the developer (or their agent) fills in. The scaffolder produces structure, not behavior.
- The descriptor is intentionally minimal. Richer domain shapes (multi-entity, guards, parameter typing) are out of scope; they belong in the executor bodies and the builder calls the developer extends.
- The happy-path test follows declared follow-up events deterministically; domains whose progression depends on runtime data still need the developer to flesh out scenario tests beyond the generated allowed/blocked cases.
