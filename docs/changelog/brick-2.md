# Brick 2 - Approvals And Action Catalogs

Brick 2 adds the smallest useful governance layer on top of the Brick 1 scaffold.

The goal is not to build a workflow engine, approval UI, database adapter, or agent integration. The goal is to make two recurring coordination mechanics explicit:

- domains need a way to register action contracts by name;
- high-risk actions need auditable approval records, not loose booleans.

## Action Catalog

An action catalog is a domain-owned registry of `action.ActionContract` values.

The catalog helps demos, examples, and applications look up contracts without scattering map literals through the codebase. It does not make action names global and it does not move domain vocabulary into the KIFF core.

The catalog should support:

- registering a contract;
- rejecting empty or duplicate action names;
- looking up a contract by name;
- listing contracts in a stable order.

## Approval Records

An approval record captures human authority over a risky action.

An approval should answer:

- which entity was affected;
- which action was reviewed;
- who requested approval;
- who reviewed it;
- whether it is pending, granted, or denied;
- why it was granted or denied;
- when it was created and reviewed.

Runtime action validation may use an approval id from the action context. If the approval store contains a granted approval for the same entity and action, the runtime can validate the action as approved.

## Non-Goals

Brick 2 does not add:

- HTTP routes;
- persistence beyond in-memory stores;
- approval assignment workflows;
- notification systems;
- LLM or agent SDK integrations;
- UI.

The framework remains small, local, and documentation-driven.
