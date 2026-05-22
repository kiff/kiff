package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
)

// TestRefund_RequiresApproval confirms a high-risk refund stays blocked
// until an operator grants the approval.
func TestRefund_RequiresApproval(t *testing.T) {
	t.Parallel()
	rt, ctx := paidOrder(t, "order-1")
	contract, _ := rt.Actions.Get(ActionRefundOrder)
	actionCtx := action.ActionContext{
		ActionName: ActionRefundOrder, EntityID: "order-1", EntityType: EntityOrder,
		CurrentState: StatePaid, Actor: AgentActor,
		Parameters: map[string]any{"amount_cents": 9900, "reason": "broken product"},
		ApprovalID: "approval-1",
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, HumanActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("ExecuteAction after grant: %v", err)
	}
}

// TestRefund_DeniedStaysBlocked covers the denied path.
func TestRefund_DeniedStaysBlocked(t *testing.T) {
	t.Parallel()
	rt, ctx := paidOrder(t, "order-2")
	contract, _ := rt.Actions.Get(ActionRefundOrder)
	actionCtx := action.ActionContext{
		ActionName: ActionRefundOrder, EntityID: "order-2", EntityType: EntityOrder,
		CurrentState: StatePaid, Actor: AgentActor,
		Parameters: map[string]any{"amount_cents": 5000, "reason": "x"},
		ApprovalID: "approval-2",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, HumanActor.ID, approval.StatusDenied, "no"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}
}

// paidOrder seeds an order in PAID by ingesting ORDER_PLACED then
// executing MARK_PAID. Used by the refund tests.
func paidOrder(t *testing.T, id string) (*runtime.Runtime, context.Context) {
	t.Helper()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: id + "-evt", Adapter: AdapterRefund, Type: EventOrderPlaced,
		Source: "test", EntityID: id, EntityType: EntityOrder,
		ActorID: SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}
	markPaid, _ := rt.Actions.Get(ActionMarkPaid)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionMarkPaid, EntityID: id, EntityType: EntityOrder,
		CurrentState: StateCreated, Actor: AgentActor,
		Parameters: map[string]any{"payment_id": "pay-" + id},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}
	return rt, ctx
}
