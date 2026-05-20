# Brick 3 - Domain Definitions And Allowed Actions

Brick 3 improves domain ergonomics without adding external infrastructure.

Brick 1 proved the local KIFF loop. Brick 2 made approvals and action catalogs explicit. Brick 3 adds a small domain definition object so a domain can bundle its coordination vocabulary in one place.

## Domain Definition

A domain definition describes how a domain plugs into KIFF mechanics:

- domain name;
- known entity types;
- known event types;
- state machine;
- action catalog.

The definition is not a business ontology. It does not attempt to normalize what a mission, case, order, dispute, or provider means. It only packages the coordination pieces that KIFF needs to run a domain locally.

## Runtime Allowed Actions

The runtime should be able to answer:

```text
Given this entity's current state, which action contracts are currently allowed?
```

That lookup uses:

- the current state from the domain state machine;
- allowed action names declared by the state machine;
- action contracts from the domain action catalog.

This keeps the framework aligned with the KIFF principle:

```text
State before action.
```

## Non-Goals

Brick 3 does not add:

- persistence;
- HTTP routes;
- background workers;
- adapters;
- LLM or agent SDK integrations;
- UI;
- generated domain schemas.

The goal is still a small Go framework that is pleasant to extend with a real domain.
