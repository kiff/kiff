# LLM bridge example

This is the bridge from model tool calls to shippable actions.

Most agent frameworks (OpenAI tool calls, Anthropic tool use, LangChain, Agno)
converge on the same shape: the model picks a tool name and emits a JSON
arguments payload. This example shows how that shape becomes a KIFF action
without coupling the framework to any specific model SDK.

## Start with the useful path

The bridge exists so the agent can do the real work when the contract allows it.
This test path ingests an order, registers the LLM-facing `mark_paid` tool, and
lets the agent's tool call execute:

```go
b := llmbridge.NewBridge(rt, agentActor)
b.Register(markPaidTool()) // maps "mark_paid" -> MARK_PAID

res, _ := b.Invoke(ctx, llmbridge.ToolCall{
    Tool:         "mark_paid",
    Arguments:    json.RawMessage(`{"payment_id": "pay-7"}`),
    EntityID:     "order-1",
    EntityType:   refund.EntityOrder,
    CurrentState: refund.StateCreated,
    Reasoning:    "payment webhook arrived",
})
// res.Outcome == "executed"
```

That is the happy path: the agent proposed a consequential action and KIFF made
it safe enough to run.

## The same bridge handles risky calls

Now register the LLM-facing `refund_order` tool. The action contract says a
refund is high risk and requires human approval, so the same bridge returns a
model-friendly outcome instead of running the executor:

```go
b.Register(refundTool()) // maps "refund_order" -> REFUND_ORDER

res, _ := b.Invoke(ctx, llmbridge.ToolCall{
    Tool:         "refund_order",
    Arguments:    json.RawMessage(`{"amount": 999, "reason": "agent unsure"}`),
    EntityID:     "order-2",
    EntityType:   refund.EntityOrder,
    CurrentState: refund.StatePaid,
    ApprovalID:   "approval-2",
})
// res.Outcome == "approval_required"
```

The agent does not see the runtime. It sees a `Result` struct with a stable
`Outcome` string (`"executed"`, `"approval_required"`, `"permission_denied"`,
`"state_not_allowed"`, `"missing_parameter"`, `"blocked"`, `"unknown_tool"`).
That is what you hand back to the model so it can decide what to do next.

## What the bridge does

1. Look up the tool in its registry.
2. Translate the JSON arguments into KIFF parameters.
3. Record the proposal as a decision (preserves agent reasoning, evidence, confidence).
4. Validate via the runtime: state, parameters, permissions, approval.
5. Execute when validation passes; return a typed outcome when it does not.

The bridge is ~200 lines of Go and depends only on KIFF. Plug your favorite model SDK on top.

## What it is not

- It is not a model wrapper. There is no SDK code here.
- It is not a prompt builder. The model decides what to call.
- It is not a self-approval escape. Approvals still flow through the runtime; the bridge just makes the agent's path through them clean.

For more on why this layer matters at all, see [`docs/why.md`](../../docs/why.md). For the runtime approval boundary it preserves, see [`docs/principles/04-approval-is-runtime-controlled.md`](../../docs/principles/04-approval-is-runtime-controlled.md).
