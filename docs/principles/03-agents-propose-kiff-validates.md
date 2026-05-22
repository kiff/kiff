# Agents propose, KIFF validates

> AI agents may propose decisions or actions; the runtime validates whether those actions are allowed.

This is the trust boundary. It is the moment in [the tour](../../cmd/kiff-tour/main.go) where an agent confidently proposes a $999 refund and the runtime stops it cold.

## The shape

An agent does not "execute an action" in KIFF. It records a *proposal*:

```go
moveProposal := proposal.ActionProposal{
    ID:               "dec-001",
    EntityID:         "order-1",
    EntityType:       "Order",
    ActionName:       "REFUND_ORDER",
    Evidence:         []evidence.Ref{ /* what the agent looked at */ },
    ReasoningSummary: "Customer requested refund in chat at 14:02",
    Confidence:       0.71,
    ActorID:          "ops-agent",
    CreatedAt:        time.Now().UTC(),
    Parameters:       map[string]any{"amount": 999.0, "reason": "customer request"},
}
if err := rt.RecordActionProposal(ctx, moveProposal); err != nil {
    return err
}
```

Recording the proposal is auditable but does *nothing* to the entity. State does not change. The action does not run. KIFF stores the agent's reasoning, evidence, and confidence as a decision record, then waits for someone to actually try to execute the action.

When execution is attempted, it goes through the runtime:

```go
result, err := rt.ExecuteAction(ctx, actionCtx, contract)
```

The runtime checks state, parameters, permissions, and approvals. If anything fails, the executor never runs and the failure is audited with a typed error. If everything passes, the executor runs and the result is audited with what changed.

## What this prevents

A confident wrong agent. The single most common failure mode in AI features is an agent that is *certain* about the wrong action. With a free-form tool layer, that certainty becomes side effects. With KIFF, that certainty is a proposal that gets rejected, with the rejection itself becoming part of the audit trail.

Three concrete things this changes:

- **You can ship agents earlier.** The proposal layer is safe by default. An untrustworthy agent can write proposals all day and the system stays correct.
- **You can debug agents better.** Every rejected proposal has a typed error. Patterns appear in the data: this agent gets `ErrPermissionDenied` 30% of the time on `REFUND_ORDER`. That is fixable.
- **You can mix actors.** The same proposal API works for humans, agents, services, and integrations. The runtime does not care who proposed; it only cares whether the proposal validates.

## Why proposals and actions are separate

A proposal is a *recorded intent*. An action is *attempted execution*. Keeping them separate has three effects.

It makes proposals cheap. An agent can record many proposals while reasoning, and the runtime never has to roll anything back, because nothing happened.

It makes execution explicit. The transition from "the agent thought about this" to "the system tried this" is visible in the audit trail. You can answer "did the agent only suggest, or did it actually try?"

It lets humans and agents share a workflow. A human reviewer can read the agent's recorded proposal, including its reasoning and evidence, before deciding whether to execute it. The proposal is the artifact the human reviews.

## What this looks like end to end

```text
agent reasons        →  rt.RecordActionProposal()       (decision recorded)
something attempts   →  rt.ExecuteAction()              (validation gate)
runtime says no      →  audit: action_validation_failed
runtime says yes     →  executor runs, follow-up events ingested, audit recorded
```

If you collapse the proposal step out of the diagram, you get a system where the agent's intent is invisible until execution happens. That is fine for low-risk actions. For risky ones, the proposal record is what makes "the agent kept trying to refund and we said no five times" something you can prove.

## When to break it

You can skip `RecordActionProposal` for deterministic, low-risk actions where there is no judgment to record. A scheduled `CLEANUP_TEMP_FILES` job does not have reasoning to capture. Just call `ExecuteAction` directly.

You should not skip the validation gate. Ever. The runtime is the only place that knows the current state and the active permission policy. If you find yourself wanting to bypass `ExecuteAction`, you are usually trying to do something that is not actually a domain action — extract it.

The principle in one line: **the agent's job is to think; the runtime's job is to decide**.
