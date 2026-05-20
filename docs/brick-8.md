# Brick 8 - Proposal Boundary

Brick 8 makes the KIFF agent position concrete without adding an LLM integration.

The principle is:

```text
Agents may propose. KIFF validates.
```

An agent, human, service, or deterministic component can propose an action. KIFF records that proposal as an auditable decision. The proposal can then be validated against action contracts, state, permissions, and approvals.

## Action Proposals

An action proposal captures:

- proposal id;
- entity id and type;
- proposed action name;
- actor id;
- parameters;
- evidence references;
- reasoning summary;
- confidence;
- creation time.

The proposal does not execute anything.

## Runtime Flow

The runtime supports:

```text
RecordActionProposal -> decision stored and audited
ValidateActionProposal -> proposal converted into ActionContext and validated
```

This keeps proposal, validation, approval, and execution as separate steps.

## Non-Goals

Brick 8 does not add:

- LLM SDKs;
- prompt templates;
- autonomous execution loops;
- tool calling;
- planner runtimes;
- model configuration;
- background agents.

The framework only needs the protocol boundary. Concrete agent integrations can come later.
