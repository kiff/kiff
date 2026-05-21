# Brick 18 - Domain Authoring Guide and Builder

Brick 18 lowers the cost of onboarding a new domain onto KIFF.

## Builder

`pkg/kiff/domain` adds a `Builder` that wraps the existing `state.TransitionMachine` and `action.Catalog` so a developer can express an entire domain definition in one chain:

```go
def, err := domain.New("orders").
    Entity("Order").
    Event("ORDER_CREATED").
    Transition("ORDER_CREATED", "", "CREATED").
    Allow("CREATED", "MARK_PAID").
    Action(markPaidContract).
    Build()
```

`Build` validates the resulting `Definition` so misconfiguration surfaces at construction time instead of at first runtime call. `WithStateMachine` is provided as an escape hatch for domains that need a custom `state.StateMachine` implementation.

The builder is purely a convenience layer. It introduces no new behavior beyond what the existing primitives already provide. Domains that prefer to build a `Definition` struct directly remain fully supported.

## Authoring Guide

`docs/build-a-domain.md` walks through modeling a small `Order` domain end to end:

- declaring constants
- the builder chain
- writing an executor that emits a follow-up event
- wiring the runtime
- driving the coordination loop
- using approval requirements for high-risk actions
- using trace correlation to reconstruct one request

The guide stays under 200 lines and does not duplicate the architecture documentation.

## Mission Example

`examples/mission/mission.go` now uses the builder for `NewDomainDefinition`. The previous standalone `NewStateMachine` and `NewActionCatalog` helpers remain so existing tests and external code do not break.

## Why

A new domain author had no documented path before Brick 18. Reading `pkg/kiff` and the mission example was the only option, and the mission example mixes setup with a long demo run. The builder + guide give external testers a productive starting point in well under an hour.
