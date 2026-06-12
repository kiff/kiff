package llmbridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/examples/refund"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

// TestBridge_LowRiskFlows verifies a low-risk tool call flows through to
// execution.
func TestBridge_LowRiskFlows(t *testing.T) {
	t.Parallel()
	rt, err := refund.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: "evt-1", Adapter: refund.AdapterRefund, Type: refund.EventOrderPlaced,
		Source: "test", EntityID: "order-1", EntityType: refund.EntityOrder,
		ActorID: refund.SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	b := NewBridge(rt, refund.AgentActor)
	mustRegister(t, b, markPaidTool())

	args, _ := json.Marshal(map[string]any{"payment_id": "pay-7"})
	res, err := b.Invoke(ctx, ToolCall{
		ID:           "call-1",
		Tool:         "mark_paid",
		Arguments:    args,
		EntityID:     "order-1",
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StateCreated,
		Reasoning:    "payment webhook arrived",
		Confidence:   0.95,
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Outcome != "executed" {
		t.Fatalf("expected executed, got %q (err: %s)", res.Outcome, res.ErrorMessage)
	}
}

// TestBridge_ApprovalRequiredBlocks verifies a high-risk tool call without
// approval is blocked with a model-friendly outcome.
func TestBridge_ApprovalRequiredBlocks(t *testing.T) {
	t.Parallel()
	rt, ctx := setupPaidOrder(t, "order-2")
	b := NewBridge(rt, refund.AgentActor)
	mustRegister(t, b, refundTool())

	args, _ := json.Marshal(map[string]any{"amount": 999.0, "reason": "agent unsure"})
	res, _ := b.Invoke(ctx, ToolCall{
		ID:           "call-2",
		Tool:         "refund_order",
		Arguments:    args,
		EntityID:     "order-2",
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StatePaid,
		ApprovalID:   "approval-2",
		Reasoning:    "customer messaged asking for refund",
	})
	if res.Outcome != "approval_required" {
		t.Fatalf("expected approval_required, got %q", res.Outcome)
	}
}

// TestBridge_GrantedApprovalUnblocks verifies the same tool call succeeds
// once a human grants the approval.
func TestBridge_GrantedApprovalUnblocks(t *testing.T) {
	t.Parallel()
	rt, ctx := setupPaidOrder(t, "order-3")
	b := NewBridge(rt, refund.AgentActor)
	mustRegister(t, b, refundTool())

	contract, _ := rt.Actions.Get(refund.ActionRefundOrder)
	actionCtx := action.ActionContext{
		ActionName:   refund.ActionRefundOrder,
		EntityID:     "order-3",
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StatePaid,
		Actor:        refund.AgentActor,
		Parameters:   map[string]any{"amount": 49.0, "reason": "customer happy with refund"},
		ApprovalID:   "approval-3",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "agent requested"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, refund.OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}

	args, _ := json.Marshal(map[string]any{"amount": 49.0, "reason": "customer happy with refund"})
	res, err := b.Invoke(ctx, ToolCall{
		ID:           "call-3",
		Tool:         "refund_order",
		Arguments:    args,
		EntityID:     "order-3",
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StatePaid,
		ApprovalID:   "approval-3",
	})
	if err != nil {
		t.Fatalf("Invoke after grant: %v", err)
	}
	if res.Outcome != "executed" {
		t.Fatalf("expected executed, got %q (err: %s)", res.Outcome, res.ErrorMessage)
	}
}

// TestBridge_UnknownTool returns a model-friendly error.
func TestBridge_UnknownTool(t *testing.T) {
	t.Parallel()
	rt, _ := refund.NewRuntime()
	b := NewBridge(rt, refund.AgentActor)
	res, err := b.Invoke(context.Background(), ToolCall{Tool: "delete_universe"})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
	if res.Outcome != "unknown_tool" {
		t.Fatalf("expected unknown_tool outcome, got %q", res.Outcome)
	}
}

func mustRegister(t *testing.T, b *Bridge, tool Tool) {
	t.Helper()
	if err := b.Register(tool); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func setupPaidOrder(t *testing.T, id string) (*runtime.Runtime, context.Context) {
	t.Helper()
	rt, err := refund.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: id + "-evt", Adapter: refund.AdapterRefund, Type: refund.EventOrderPlaced,
		Source: "test", EntityID: id, EntityType: refund.EntityOrder,
		ActorID: refund.SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}
	markPaid, _ := rt.Actions.Get(refund.ActionMarkPaid)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: refund.ActionMarkPaid, EntityID: id, EntityType: refund.EntityOrder,
		CurrentState: refund.StateCreated, Actor: refund.AgentActor,
		Parameters: map[string]any{"payment_id": "pay-x"},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}
	return rt, ctx
}

// markPaidTool registers MARK_PAID under the LLM-friendly name "mark_paid".
func markPaidTool() Tool {
	return Tool{
		Name:   "mark_paid",
		Action: refund.ActionMarkPaid,
		Translate: func(call ToolCall) (map[string]any, error) {
			var args struct {
				PaymentID string `json:"payment_id"`
			}
			if err := json.Unmarshal(call.Arguments, &args); err != nil {
				return nil, err
			}
			return map[string]any{"payment_id": args.PaymentID}, nil
		},
	}
}

// refundTool registers REFUND_ORDER under "refund_order".
func refundTool() Tool {
	return Tool{
		Name:   "refund_order",
		Action: refund.ActionRefundOrder,
		Translate: func(call ToolCall) (map[string]any, error) {
			var args struct {
				Amount float64 `json:"amount"`
				Reason string  `json:"reason"`
			}
			if err := json.Unmarshal(call.Arguments, &args); err != nil {
				return nil, err
			}
			return map[string]any{
				"amount": args.Amount,
				"reason": args.Reason,
			}, nil
		},
	}
}
