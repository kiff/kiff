# Scaffold a domain with `kiff scaffold`

`kiff new` scaffolds a project around the fixed `tasks` starter domain. When you
already know the domain you want — its entity, lifecycle, actions, and approval
rules — use `kiff scaffold` instead. It reads a small JSON **descriptor** and
emits a framework-faithful `domain/` package: a state machine, one action
contract per action (with TODO executor stubs), a permission policy, and tests
that pass out of the box. You fill in the executor bodies and tighten the policy.

The descriptor is a one-time **code-generation seed**, not a runtime artifact.
The KIFF runtime still builds domains exclusively via `domain.Builder`; the
framework parses no YAML and loads no declarative domain at runtime. The
descriptor type lives under `cmd/kiff`, never `pkg/kiff`, on purpose.

## Usage

Flags must precede the module path (standard Go flag parsing):

```bash
# Full project (starter shell + generated domain/), like `kiff new`:
kiff scaffold -descriptor order.json github.com/acme/orders

# Just the domain/ package, into the current directory:
kiff scaffold -descriptor order.json -domain-only -dir . github.com/acme/orders

# From stdin:
cat order.json | kiff scaffold -descriptor - github.com/acme/orders
```

Flags:

- `-descriptor <file|->` (required): JSON descriptor path, or `-` for stdin.
- `-dir`: output directory (default: last segment of the module path).
- `-domain-only`: emit only `domain/domain.go` and `domain/domain_test.go`.
- `-force`: scaffold into a non-empty directory.
- `-replace-local <path>`: emit a `replace github.com/kiff/kiff => <path>`
  directive in `go.mod` (handy while the framework is unpublished).

After scaffolding:

```bash
cd orders
go mod tidy
go test ./...        # generated tests pass; executor bodies are TODO stubs
go run ./cmd/server  # full-project mode only
```

## Descriptor format

```json
{
  "domain": "refund",
  "entity": "Order",
  "adapter": "refund",
  "events": ["ORDER_PLACED", "ORDER_PAID", "ORDER_REFUNDED"],
  "states": ["CREATED", "PAID", "REFUNDED"],
  "transitions": [
    {"on": "ORDER_PLACED", "from": "", "to": "CREATED"},
    {"on": "ORDER_PAID", "from": "CREATED", "to": "PAID"},
    {"on": "ORDER_REFUNDED", "from": "PAID", "to": "REFUNDED"}
  ],
  "actions": [
    {
      "name": "MARK_PAID",
      "allowed_states": ["CREATED"],
      "required_parameters": ["payment_id"],
      "required_permissions": ["refund.mark_paid"],
      "risk": "low",
      "approval": "never",
      "follow_up_events": ["ORDER_PAID"]
    },
    {
      "name": "REFUND_ORDER",
      "allowed_states": ["PAID"],
      "required_parameters": ["amount", "reason"],
      "required_permissions": ["refund.execute"],
      "risk": "high",
      "approval": "required",
      "follow_up_events": ["ORDER_REFUNDED"]
    }
  ],
  "roles": {
    "agent": ["refund.mark_paid", "refund.execute"],
    "operator": ["refund.approve"]
  }
}
```

Conventions and rules:

- **Events, states, action names** are `UPPER_SNAKE_CASE`; **permissions** are
  `dotted.lowercase`. The generator derives Go constants (`EventOrderPaid`,
  `StatePaid`, `ActionRefundOrder`, `PermRefundExecute`) from these.
- Exactly one **bootstrap transition** with an empty `from` defines the initial
  state; it is the event you ingest to create an entity.
- Each action's `follow_up_events` are emitted by its executor stub and drive
  the state machine forward. The generated happy-path test follows these.
- `risk` is one of `low|medium|high|critical`; `approval` is `never|required`.

## What the generator does and does not do

- It emits a **buildable, tested** domain whose shape matches the starter's
  conventions, validated by `domain.Builder.Build()`.
- Executor bodies are **TODO stubs** — the framework never synthesizes business
  logic. It also never invents thresholds, states, or approvals beyond the
  descriptor.
- The generated `NewPermissionPolicy` assigns every declared role to each demo
  actor so the tests pass; a `TODO` marks where to tighten it to your real
  identity model.

## Generated tests

`domain/domain_test.go` includes:

- `TestLoop_HappyPath`: ingests the bootstrap event, then walks each action
  through the state machine (requesting and granting approvals where required),
  and asserts the final state.
- Per action: an allowed-state case and a blocked-from-wrong-state case. For
  approval-required actions, the allowed-state case asserts the approval gate
  holds (`ErrApprovalRequired`) until a grant exists.

## Verify the domain

`kiff scaffold` leaves executor bodies as TODO stubs. `kiff verify` is how you
know they are finished — a structural check you can run while completing the
domain and in CI:

```bash
kiff verify            # checks ./domain (or the current package)
kiff verify ./orders   # checks ./orders/domain
kiff verify -json .    # machine-readable output for tooling/CI
```

It reports, with a non-zero exit on any error:

- **Executor backing** — any action still bound to a scaffold stub (TODO) or
  missing an executor.
- **State-machine consistency** — actions allowed only from states no
  transition reaches, and transitions referencing undeclared events.
- **Contract completeness** — each action declares a valid `Risk` and
  `ApprovalRequirement` and at least one allowed state.

A freshly scaffolded domain fails `kiff verify` until you implement the
executors — that is the signal that it is not yet ready to govern anything.
