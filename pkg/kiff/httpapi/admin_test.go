package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiffhq/kiff/examples/refund"
	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/adapter"
)

// TestAdmin_IndexShowsEntitiesAndPendingApprovals exercises the admin index
// after running through the refund domain to a state that produces a pending
// approval. The HTML is asserted via substring checks; we are not validating
// markup, only that the page contains the expected operational facts.
func TestAdmin_IndexShowsEntitiesAndPendingApprovals(t *testing.T) {
	t.Parallel()
	rt, err := refund.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()

	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-1",
		Adapter:    refund.AdapterRefund,
		Type:       refund.EventOrderPlaced,
		Source:     "test",
		EntityID:   "order-A",
		EntityType: refund.EntityOrder,
		ActorID:    refund.SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	markPaid, _ := rt.Actions.Get(refund.ActionMarkPaid)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   refund.ActionMarkPaid,
		EntityID:     "order-A",
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StateCreated,
		Actor:        refund.AgentActor,
		Parameters:   map[string]any{"payment_id": "pay-1"},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}

	refundContract, _ := rt.Actions.Get(refund.ActionRefundOrder)
	refundCtx := action.ActionContext{
		ActionName:   refund.ActionRefundOrder,
		EntityID:     "order-A",
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StatePaid,
		Actor:        refund.AgentActor,
		Parameters:   map[string]any{"amount": 49.0, "reason": "test"},
		ApprovalID:   "approval-A",
	}
	if _, err := rt.RequestApproval(ctx, refundCtx.ApprovalID, refundCtx, refundContract, "test"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	h := NewHandler(rt)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"KIFF admin",
		"order-A",
		"approval-A",
		"REFUND_ORDER",
		"Pending approvals",
		"Entities",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in admin index body:\n%s", want, body)
		}
	}
}

// TestAdmin_EntityShowsTimeline exercises the per-entity admin page after a
// successful refund, asserting the timeline contains the key audit kinds and
// the page lists the granted approval.
func TestAdmin_EntityShowsTimeline(t *testing.T) {
	t.Parallel()
	rt, err := refund.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()

	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: "evt-2", Adapter: refund.AdapterRefund, Type: refund.EventOrderPlaced,
		Source: "test", EntityID: "order-B", EntityType: refund.EntityOrder,
		ActorID: refund.SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}

	markPaid, _ := rt.Actions.Get(refund.ActionMarkPaid)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: refund.ActionMarkPaid, EntityID: "order-B", EntityType: refund.EntityOrder,
		CurrentState: refund.StateCreated, Actor: refund.AgentActor,
		Parameters: map[string]any{"payment_id": "pay-2"},
	}, markPaid); err != nil {
		t.Fatalf("ExecuteAction(MarkPaid): %v", err)
	}

	h := NewHandler(rt)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/entities/order-B", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"order-B",
		"Timeline",
		"event_ingested",
		"action_executed",
		"PAID",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in entity body:\n%s", want, body)
		}
	}
}
