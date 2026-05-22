package refund

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
)

// TestRefundLoop_GrantedApproval walks an order from CREATED through to
// REFUNDED with a granted approval and verifies the final state.
func TestRefundLoop_GrantedApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	orderID := "order-001"

	// Place the order.
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-order-placed",
		Adapter:    AdapterRefund,
		Type:       EventOrderPlaced,
		Source:     "examples/refund/raw",
		EntityID:   orderID,
		EntityType: EntityOrder,
		ActorID:    SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
		Payload:    map[string]any{"total": 49.0},
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	// Mark the order paid.
	markPaid, ok := rt.Actions.Get(ActionMarkPaid)
	if !ok {
		t.Fatalf("missing %s contract", ActionMarkPaid)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionMarkPaid,
		EntityID:     orderID,
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{"payment_id": "pay-9"},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}

	// Try to refund without approval. Must fail.
	refund, ok := rt.Actions.Get(ActionRefundOrder)
	if !ok {
		t.Fatalf("missing %s contract", ActionRefundOrder)
	}
	refundCtx := action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     orderID,
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount": 49.0, "reason": "customer changed mind"},
		ApprovalID:   "approval-001",
	}
	if _, err := rt.ExecuteAction(ctx, refundCtx, refund); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}

	// Request and grant approval.
	if _, err := rt.RequestApproval(ctx, refundCtx.ApprovalID, refundCtx, refund, "agent-initiated refund"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, refundCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "approved"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}

	// Now execution succeeds.
	result, err := rt.ExecuteAction(ctx, refundCtx, refund)
	if err != nil {
		t.Fatalf("ExecuteAction(Refund) after grant: %v", err)
	}
	if result.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %s", result.Status)
	}

	// Final state should be REFUNDED.
	current, _, err := rt.States.Current(ctx, orderID)
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StateRefunded {
		t.Fatalf("expected final state %q, got %q", StateRefunded, current.Value)
	}
}

// TestRefundLoop_DeniedApproval verifies execution stays blocked when the
// operator denies the approval.
func TestRefundLoop_DeniedApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	orderID := "order-002"

	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-order-placed-2",
		Adapter:    AdapterRefund,
		Type:       EventOrderPlaced,
		Source:     "examples/refund/raw",
		EntityID:   orderID,
		EntityType: EntityOrder,
		ActorID:    SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
		Payload:    map[string]any{"total": 999.0},
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	markPaid, _ := rt.Actions.Get(ActionMarkPaid)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionMarkPaid,
		EntityID:     orderID,
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{"payment_id": "pay-77"},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}

	refund, _ := rt.Actions.Get(ActionRefundOrder)
	refundCtx := action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     orderID,
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount": 999.0, "reason": "agent unsure"},
		ApprovalID:   "approval-denied",
	}
	if _, err := rt.RequestApproval(ctx, refundCtx.ApprovalID, refundCtx, refund, "high-value refund"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, refundCtx.ApprovalID, OperatorActor.ID, approval.StatusDenied, "needs more evidence"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}

	if _, err := rt.ExecuteAction(ctx, refundCtx, refund); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}

	current, _, err := rt.States.Current(ctx, orderID)
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StatePaid {
		t.Fatalf("expected state to remain %q after denied refund, got %q", StatePaid, current.Value)
	}
}

// TestRefundLoop_Replay verifies state can be rebuilt from the event log alone.
func TestRefundLoop_Replay(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	orderID := "order-replay"

	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-replay-1",
		Adapter:    AdapterRefund,
		Type:       EventOrderPlaced,
		Source:     "examples/refund/raw",
		EntityID:   orderID,
		EntityType: EntityOrder,
		ActorID:    SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	markPaid, _ := rt.Actions.Get(ActionMarkPaid)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionMarkPaid,
		EntityID:     orderID,
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{"payment_id": "pay-r"},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}

	replay, err := rt.RebuildState(ctx, orderID)
	if err != nil {
		t.Fatalf("RebuildState: %v", err)
	}
	if replay.State.Value != StatePaid {
		t.Fatalf("expected replayed state %q, got %q", StatePaid, replay.State.Value)
	}
	wantTypes := []string{EventOrderPlaced, EventOrderPaid}
	if len(replay.Steps) != len(wantTypes) {
		t.Fatalf("expected %d replay steps, got %d", len(wantTypes), len(replay.Steps))
	}
	for i, want := range wantTypes {
		if got := replay.Steps[i].EventType; got != want {
			t.Fatalf("step %d: expected %q, got %q", i, want, got)
		}
	}
}
