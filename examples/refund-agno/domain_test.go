package refundagno

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

// TestRouteRefund covers the routing rule that turns an agent's amount
// into a KIFF action name.
func TestRouteRefund(t *testing.T) {
	cases := []struct {
		name   string
		amount int64
		want   string
	}{
		{"under ceiling", 4200, ActionAutoRefund},
		{"at ceiling", AutoRefundCeilingCents, ActionAutoRefund},
		{"over ceiling", AutoRefundCeilingCents + 1, ActionRefundOrder},
		{"large", 9999900, ActionRefundOrder},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := RouteRefund(tc.amount); got != tc.want {
				t.Fatalf("RouteRefund(%d) = %q, want %q", tc.amount, got, tc.want)
			}
		})
	}
}

// TestHappyPath_AutoRefund confirms a small refund flows straight through
// without approval and lands in REFUNDED.
func TestHappyPath_AutoRefund(t *testing.T) {
	t.Parallel()
	rt, ctx := paidOrder(t, "order-auto")

	auto, ok := rt.Actions.Get(ActionAutoRefund)
	if !ok {
		t.Fatalf("missing %s contract", ActionAutoRefund)
	}
	res, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionAutoRefund,
		EntityID:     "order-auto",
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": 4200, "reason": "small refund"},
	}, auto)
	if err != nil {
		t.Fatalf("ExecuteAction(AutoRefund): %v", err)
	}
	if res.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %s", res.Status)
	}
	current, _, err := rt.States.Current(ctx, "order-auto")
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StateRefunded {
		t.Fatalf("expected %s, got %s", StateRefunded, current.Value)
	}
}

// TestDeniedPath_LargeRefund verifies a high-value refund stays blocked
// after a denied review, matching the demo narrative.
func TestDeniedPath_LargeRefund(t *testing.T) {
	t.Parallel()
	rt, ctx := paidOrder(t, "order-denied")

	contract, ok := rt.Actions.Get(ActionRefundOrder)
	if !ok {
		t.Fatalf("missing %s contract", ActionRefundOrder)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     "order-denied",
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": 99900, "reason": "agent unsure"},
		ApprovalID:   "approval-denied",
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired before request, got %v", err)
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "high-value refund"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusDenied, "evidence missing"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}
	current, _, err := rt.States.Current(ctx, "order-denied")
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StatePaid {
		t.Fatalf("expected state to remain %s after denial, got %s", StatePaid, current.Value)
	}
}

// TestGrantedPath_LargeRefund verifies the same large refund completes
// once an operator grants approval.
func TestGrantedPath_LargeRefund(t *testing.T) {
	t.Parallel()
	rt, ctx := paidOrder(t, "order-granted")

	contract, _ := rt.Actions.Get(ActionRefundOrder)
	actionCtx := action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     "order-granted",
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": 99900, "reason": "customer unhappy"},
		ApprovalID:   "approval-granted",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "high-value refund"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	res, err := rt.ExecuteAction(ctx, actionCtx, contract)
	if err != nil {
		t.Fatalf("ExecuteAction after grant: %v", err)
	}
	if res.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %s", res.Status)
	}
	current, _, err := rt.States.Current(ctx, "order-granted")
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StateRefunded {
		t.Fatalf("expected %s after granted refund, got %s", StateRefunded, current.Value)
	}
}

// TestStateNotAllowed verifies a refund attempt against a CREATED order
// is rejected before any approval logic.
func TestStateNotAllowed(t *testing.T) {
	t.Parallel()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: "evt-blank", Adapter: AdapterRefund, Type: EventOrderPlaced,
		Source: "test", EntityID: "order-blank", EntityType: EntityOrder,
		ActorID: SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	auto, _ := rt.Actions.Get(ActionAutoRefund)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionAutoRefund,
		EntityID:     "order-blank",
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": 100, "reason": "should not work"},
	}, auto); !errors.Is(err, action.ErrStateNotAllowed) {
		t.Fatalf("expected ErrStateNotAllowed, got %v", err)
	}
}

// TestRebuildState confirms the full event log replays back to REFUNDED
// after a granted high-value refund. This is the explainability story.
func TestRebuildState(t *testing.T) {
	t.Parallel()
	rt, ctx := paidOrder(t, "order-replay")
	contract, _ := rt.Actions.Get(ActionRefundOrder)
	actionCtx := action.ActionContext{
		ActionName: ActionRefundOrder, EntityID: "order-replay", EntityType: EntityOrder,
		CurrentState: StatePaid, Actor: AgentActor,
		Parameters: map[string]any{"amount_cents": 99900, "reason": "ok"},
		ApprovalID: "approval-replay",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}

	replay, err := rt.RebuildState(ctx, "order-replay")
	if err != nil {
		t.Fatalf("RebuildState: %v", err)
	}
	if replay.State.Value != StateRefunded {
		t.Fatalf("expected rebuild = %s, got %s", StateRefunded, replay.State.Value)
	}
	wantTypes := []string{EventOrderPlaced, EventOrderPaid, EventOrderRefunded}
	if len(replay.Steps) != len(wantTypes) {
		t.Fatalf("expected %d replay steps, got %d", len(wantTypes), len(replay.Steps))
	}
	for i, want := range wantTypes {
		if got := replay.Steps[i].EventType; got != want {
			t.Fatalf("step %d: expected %q, got %q", i, want, got)
		}
	}
}

// paidOrder seeds an order in PAID by ingesting ORDER_PLACED then
// executing MARK_PAID. It is used by every approval-flavored test.
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
