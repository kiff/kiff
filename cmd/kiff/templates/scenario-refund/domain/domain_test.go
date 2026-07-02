package domain

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

type runtimeFixture struct {
	rt *runtime.Runtime
}

func newFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return &runtimeFixture{rt: rt}
}

// newOrder ingests ORDER_PLACED so the order exists in CREATED.
func newOrder(t *testing.T, id string) *runtimeFixture {
	t.Helper()
	f := newFixture(t)
	if _, err := f.rt.IngestRaw(context.Background(), adapter.RawInput{
		ID:         "evt-" + id,
		Adapter:    AdapterRefund,
		Type:       EventOrderPlaced,
		Source:     "test",
		EntityID:   id,
		EntityType: EntityOrder,
		ActorID:    SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
		Payload:    map[string]any{"total_cents": 4200},
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}
	return f
}

func (f *runtimeFixture) markPaid(t *testing.T, id string) {
	t.Helper()
	markPaid, _ := f.rt.Actions.Get(ActionMarkPaid)
	if _, err := f.rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   ActionMarkPaid,
		EntityID:     id,
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{"payment_id": "pay-" + id},
	}, markPaid); err != nil {
		t.Fatalf("markPaid: %v", err)
	}
}

func (f *runtimeFixture) state(t *testing.T, id string) string {
	t.Helper()
	current, _, err := f.rt.States.Current(context.Background(), id)
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	return current.Value
}

// TestUsefulPath is the enablement story: the agent marks the order paid and
// issues a refund once a human approves. Both actions execute.
func TestUsefulPath(t *testing.T) {
	ctx := context.Background()
	f := newOrder(t, "order-useful")
	f.markPaid(t, "order-useful")

	refund, _ := f.rt.Actions.Get(ActionRefundOrder)
	refundCtx := action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     "order-useful",
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": int64(4200), "reason": "customer eligible"},
		ApprovalID:   "appr-useful",
	}
	if _, err := f.rt.RequestApproval(ctx, refundCtx.ApprovalID, refundCtx, refund, "agent requested"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := f.rt.ReviewApproval(ctx, refundCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "approved"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	res, err := f.rt.ExecuteAction(ctx, refundCtx, refund)
	if err != nil {
		t.Fatalf("refund after grant: %v", err)
	}
	if res.Status != action.ExecutionSucceeded {
		t.Fatalf("expected refund success, got %s", res.Status)
	}
	if got := f.state(t, "order-useful"); got != StateRefunded {
		t.Fatalf("expected REFUNDED, got %s", got)
	}
}

// TestWrongStateDenied: refund from CREATED (not PAID) is blocked.
func TestWrongStateDenied(t *testing.T) {
	f := newOrder(t, "order-ws")
	refund, _ := f.rt.Actions.Get(ActionRefundOrder)
	_, err := f.rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     "order-ws",
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": int64(4200), "reason": "too early"},
		ApprovalID:   "appr-ws",
	}, refund)
	if !errors.Is(err, action.ErrStateNotAllowed) {
		t.Fatalf("expected ErrStateNotAllowed, got %v", err)
	}
}

// TestMissingParameterDenied: mark paid without payment_id is rejected.
func TestMissingParameterDenied(t *testing.T) {
	f := newOrder(t, "order-mp")
	markPaid, _ := f.rt.Actions.Get(ActionMarkPaid)
	_, err := f.rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   ActionMarkPaid,
		EntityID:     "order-mp",
		EntityType:   EntityOrder,
		CurrentState: StateCreated,
		Actor:        AgentActor,
		Parameters:   map[string]any{},
	}, markPaid)
	if !errors.Is(err, action.ErrMissingParameter) {
		t.Fatalf("expected ErrMissingParameter, got %v", err)
	}
}

// TestApprovalRequiredHeld: refund without approval is held, not executed.
func TestApprovalRequiredHeld(t *testing.T) {
	f := newOrder(t, "order-hold")
	f.markPaid(t, "order-hold")
	refund, _ := f.rt.Actions.Get(ActionRefundOrder)
	_, err := f.rt.ExecuteAction(context.Background(), action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     "order-hold",
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": int64(4200), "reason": "no approval yet"},
		ApprovalID:   "appr-hold",
	}, refund)
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	if got := f.state(t, "order-hold"); got != StatePaid {
		t.Fatalf("expected state to remain PAID, got %s", got)
	}
}

// TestDeniedApprovalDoesNotExecute: a denied approval keeps the refund blocked.
func TestDeniedApprovalDoesNotExecute(t *testing.T) {
	ctx := context.Background()
	f := newOrder(t, "order-deny")
	f.markPaid(t, "order-deny")
	refund, _ := f.rt.Actions.Get(ActionRefundOrder)
	refundCtx := action.ActionContext{
		ActionName:   ActionRefundOrder,
		EntityID:     "order-deny",
		EntityType:   EntityOrder,
		CurrentState: StatePaid,
		Actor:        AgentActor,
		Parameters:   map[string]any{"amount_cents": int64(99900), "reason": "high value"},
		ApprovalID:   "appr-deny",
	}
	if _, err := f.rt.RequestApproval(ctx, refundCtx.ApprovalID, refundCtx, refund, "agent requested"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := f.rt.ReviewApproval(ctx, refundCtx.ApprovalID, OperatorActor.ID, approval.StatusDenied, "not eligible"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := f.rt.ExecuteAction(ctx, refundCtx, refund); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}
	if got := f.state(t, "order-deny"); got != StatePaid {
		t.Fatalf("expected state to remain PAID after denial, got %s", got)
	}
}

// TestReplayMatchesState: rebuilding from events yields the materialized state.
func TestReplayMatchesState(t *testing.T) {
	ctx := context.Background()
	f := newOrder(t, "order-replay")
	f.markPaid(t, "order-replay")

	replay, err := f.rt.RebuildState(ctx, "order-replay")
	if err != nil {
		t.Fatalf("RebuildState: %v", err)
	}
	if replay.State.Value != StatePaid {
		t.Fatalf("expected replayed state PAID, got %s", replay.State.Value)
	}
	if got := f.state(t, "order-replay"); got != replay.State.Value {
		t.Fatalf("materialized %s != replayed %s", got, replay.State.Value)
	}
}
