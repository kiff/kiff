# LLM bridge example

This is the bridge between prompt-land and KIFF.

Most agent frameworks (OpenAI tool calls, Anthropic tool use, LangChain, Agno) converge on the same shape: the model picks a tool name and emits a JSON arguments payload. This example shows how that shape becomes a governed KIFF action without coupling the framework to any specific model SDK.

## Read the test, not the package

The whole point fits in [`bridge_test.go`](./bridge_test.go):

```go
b := llmbridge.NewBridge(rt, agentActor)
b.Register(refundTool())   // maps "refund_order" → REFUND_ORDER

res, _ := b.Invoke(ctx, llmbridge.ToolCall{
    Tool:         "refund_order",
    Arguments:    json.RawMessage(`{"amount": 999, "reason": "customer unsure"}`),
    EntityID:     "order-2",
    CurrentState: "PAID",
    ApprovalID:   "approval-2",
})
// res.Outcome == "approval_required"
```

The agent does not see the runtime. It sees a `Result` struct with a stable `Outcome` string ("executed", "approval_required", "permission_denied", "state_not_allowed", "missing_parameter", "blocked", "unknown_tool"). That is what you hand back to the model so it can decide what to do next.

## What the bridge does

1. Look up the tool in its registry.
2. Translate the JSON arguments into KIFF parameters.
3. Record the proposal as a KIFF decision (preserves agent reasoning, evidence, confidence).
4. Validate via the runtime: state, parameters, permissions, approval.
5. Execute if validation passes; return a typed outcome if not.

The bridge is ~200 lines of Go and depends only on KIFF. Plug your favorite model SDK on top.

## What it is not

- It is not a model wrapper. There is no SDK code here.
- It is not a prompt builder. The model decides what to call.
- It is not a "self-approval" escape. Approvals still flow through the runtime; the bridge just makes the agent's path through them clean.

For more on why this layer matters at all, see [`docs/why.md`](../../docs/why.md). For the trust boundary it preserves, see [`docs/principles/04-approval-is-runtime-controlled.md`](../../docs/principles/04-approval-is-runtime-controlled.md).
