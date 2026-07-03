package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
)

func emitEvent(eventType, entityID string) []event.Event {
	return []event.Event{{
		ID: eventType + "-" + entityID, Type: eventType, EntityID: entityID,
		EntityType: "Order", Source: "test", ActorID: "svc", OccurredAt: time.Now().UTC(),
	}}
}

func lifecycleRuntime(t *testing.T) (*Runtime, action.ActionContract, action.ActionContract) {
	t.Helper()
	markPaid := action.ActionContract{
		Name: "MARK_PAID", AllowedStates: []string{"CREATED"}, Risk: action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{ActionName: "MARK_PAID", EntityID: ctx.EntityID, Status: action.ExecutionSucceeded, Executed: true, FollowUpEvents: emitEvent("PAID", ctx.EntityID)}, nil
		},
	}
	refund := action.ActionContract{
		Name: "REFUND", AllowedStates: []string{"PAID"}, Risk: action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{ActionName: "REFUND", EntityID: ctx.EntityID, Status: action.ExecutionSucceeded, Executed: true, FollowUpEvents: emitEvent("REFUNDED", ctx.EntityID)}, nil
		},
	}
	def, err := domain.New("orders").
		Entity("Order").
		Event("CREATED").Event("PAID").Event("REFUNDED").
		Transition("CREATED", "", "CREATED").
		Transition("PAID", "CREATED", "PAID").
		Transition("REFUNDED", "PAID", "REFUNDED").
		Action(markPaid).Action(refund).
		Build()
	if err != nil {
		t.Fatalf("domain: %v", err)
	}
	rt, err := NewForDomain(def, Config{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	return rt, markPaid, refund
}

func seedOrder(t *testing.T, rt *Runtime, id string) {
	t.Helper()
	if err := rt.IngestEvent(context.Background(), event.Event{
		ID: "CREATED-" + id, Type: "CREATED", EntityID: id, EntityType: "Order",
		Source: "test", ActorID: "sys", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func orderCtx(id, actionName, state, approvalID string) action.ActionContext {
	return action.ActionContext{
		ActionName: actionName, EntityID: id, EntityType: "Order",
		CurrentState: state, Actor: actor.Actor{ID: "agent"}, ApprovalID: approvalID,
	}
}

func propose(t *testing.T, rt *Runtime, id, act string) {
	t.Helper()
	if err := rt.ProposeDecision(context.Background(), decision.Decision{
		ID: "dec-" + id, EntityID: id, EntityType: "Order", Kind: decision.KindActionProposal,
		ProposedAction: act, ActorID: "agent", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("propose: %v", err)
	}
}

func TestEntityLifecycle_Executed(t *testing.T) {
	ctx := context.Background()
	rt, markPaid, _ := lifecycleRuntime(t)
	seedOrder(t, rt, "o-exec")
	propose(t, rt, "o-exec", "MARK_PAID")
	if _, err := rt.ExecuteAction(ctx, orderCtx("o-exec", "MARK_PAID", "CREATED", ""), markPaid); err != nil {
		t.Fatalf("execute: %v", err)
	}
	lc, err := rt.EntityLifecycle(ctx, "o-exec")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if !lc.Has(audit.KindDecisionProposed) || !lc.Executed() {
		t.Fatalf("expected proposed + executed stages, got %+v", lc.Stages)
	}
	if lc.CurrentState != "PAID" {
		t.Fatalf("expected current state PAID, got %q", lc.CurrentState)
	}
	if len(lc.Decisions) != 1 {
		t.Fatalf("expected the proposal decision attached")
	}
}

func TestEntityLifecycle_Blocked(t *testing.T) {
	ctx := context.Background()
	rt, _, refund := lifecycleRuntime(t)
	seedOrder(t, rt, "o-block")
	// REFUND requires PAID; the order is CREATED → blocked before execution.
	if _, err := rt.ExecuteAction(ctx, orderCtx("o-block", "REFUND", "CREATED", ""), refund); !errors.Is(err, action.ErrStateNotAllowed) {
		t.Fatalf("expected state-not-allowed, got %v", err)
	}
	lc, _ := rt.EntityLifecycle(ctx, "o-block")
	if !lc.Has(audit.KindActionFailed) {
		t.Fatal("expected a failed (blocked) stage")
	}
	if lc.Executed() {
		t.Fatal("blocked action must not show as executed")
	}
}

func TestEntityLifecycle_ApprovalHeld(t *testing.T) {
	ctx := context.Background()
	rt, markPaid, refund := lifecycleRuntime(t)
	seedOrder(t, rt, "o-held")
	if _, err := rt.ExecuteAction(ctx, orderCtx("o-held", "MARK_PAID", "CREATED", ""), markPaid); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	// REFUND is approval-required; execute with an approval id but no grant.
	if _, err := rt.ExecuteAction(ctx, orderCtx("o-held", "REFUND", "PAID", "ap-held"), refund); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	lc, _ := rt.EntityLifecycle(ctx, "o-held")
	if !lc.AwaitingApproval() {
		t.Fatalf("expected AwaitingApproval, stages=%+v", lc.Stages)
	}
}

func TestEntityLifecycle_Denied(t *testing.T) {
	ctx := context.Background()
	rt, markPaid, refund := lifecycleRuntime(t)
	seedOrder(t, rt, "o-deny")
	_, _ = rt.ExecuteAction(ctx, orderCtx("o-deny", "MARK_PAID", "CREATED", ""), markPaid)
	held := orderCtx("o-deny", "REFUND", "PAID", "ap-deny")
	if _, err := rt.RequestApproval(ctx, "ap-deny", held, refund, "needs review"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, "ap-deny", "manager", approval.StatusDenied, "not this time"); err != nil {
		t.Fatalf("review: %v", err)
	}
	lc, _ := rt.EntityLifecycle(ctx, "o-deny")
	if !lc.Has(audit.KindApprovalDenied) {
		t.Fatal("expected an approval-denied stage")
	}
	if lc.AwaitingApproval() || lc.Executed() {
		t.Fatal("denied lifecycle is neither awaiting nor executed")
	}
	if len(lc.Approvals) != 1 || lc.Approvals[0].Status != approval.StatusDenied {
		t.Fatalf("expected the denied approval attached, got %+v", lc.Approvals)
	}
}

func TestEntityLifecycle_ApprovedThenExecuted(t *testing.T) {
	ctx := context.Background()
	rt, markPaid, refund := lifecycleRuntime(t)
	seedOrder(t, rt, "o-ok")
	_, _ = rt.ExecuteAction(ctx, orderCtx("o-ok", "MARK_PAID", "CREATED", ""), markPaid)
	held := orderCtx("o-ok", "REFUND", "PAID", "ap-ok")
	if _, err := rt.RequestApproval(ctx, "ap-ok", held, refund, "needs review"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, "ap-ok", "manager", approval.StatusGranted, "approved"); err != nil {
		t.Fatalf("review: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, held, refund); err != nil {
		t.Fatalf("execute after approval: %v", err)
	}
	lc, _ := rt.EntityLifecycle(ctx, "o-ok")
	if !lc.Has(audit.KindApprovalGranted) || !lc.Executed() {
		t.Fatalf("expected granted + executed, stages=%+v", lc.Stages)
	}
	if lc.CurrentState != "REFUNDED" {
		t.Fatalf("expected current state REFUNDED, got %q", lc.CurrentState)
	}
}
