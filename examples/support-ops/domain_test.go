package supportops

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

// TestTriage_HappyPath confirms TRIAGE_TICKET runs without approval and
// moves the ticket to TRIAGED. This is the "low risk action stays low
// risk" baseline.
func TestTriage_HappyPath(t *testing.T) {
	t.Parallel()
	d, rt, ctx := newTicket(t, "ticket-triage-1")
	_ = d
	triage, ok := rt.Actions.Get(ActionTriageTicket)
	if !ok {
		t.Fatalf("missing %s contract", ActionTriageTicket)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionTriageTicket, EntityID: "ticket-triage-1", EntityType: EntityTicket,
		CurrentState: StateNew, Actor: AgentActor,
		Parameters: map[string]any{"category": "billing"},
	}, triage); err != nil {
		t.Fatalf("ExecuteAction(triage): %v", err)
	}
	current, _, err := rt.States.Current(ctx, "ticket-triage-1")
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StateTriaged {
		t.Fatalf("expected %s, got %s", StateTriaged, current.Value)
	}
}

// TestAutoRefund_BelowCeiling confirms a small refund executes without
// approval and updates the running total.
func TestAutoRefund_BelowCeiling(t *testing.T) {
	t.Parallel()
	d, rt, ctx := triagedTicket(t, "ticket-auto")
	auto, _ := rt.Actions.Get(ActionAutoRefund)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionAutoRefund, EntityID: "ticket-auto", EntityType: EntityTicket,
		CurrentState: StateTriaged, Actor: AgentActor,
		Parameters: map[string]any{"amount_cents": 1500, "reason": "small"},
	}, auto); err != nil {
		t.Fatalf("ExecuteAction(auto refund): %v", err)
	}
	if got := d.Cumulative("ticket-auto"); got != 1500 {
		t.Fatalf("expected running total 1500, got %d", got)
	}
}

// TestIssueRefund_OverCeiling_RequiresApproval covers the per-call
// ceiling: any refund > SingleRefundCeilingCents must hit the approval
// gate before the executor runs.
func TestIssueRefund_OverCeiling_RequiresApproval(t *testing.T) {
	t.Parallel()
	_, rt, ctx := triagedTicket(t, "ticket-big")
	contract, _ := rt.Actions.Get(ActionIssueRefund)
	actionCtx := action.ActionContext{
		ActionName: ActionIssueRefund, EntityID: "ticket-big", EntityType: EntityTicket,
		CurrentState: StateTriaged, Actor: AgentActor,
		Parameters: map[string]any{"amount_cents": SingleRefundCeilingCents + 1, "reason": "x"},
		ApprovalID: "approval-big",
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("ExecuteAction after grant: %v", err)
	}
}

// TestIssueRefund_DeniedStaysBlocked covers the denied path.
func TestIssueRefund_DeniedStaysBlocked(t *testing.T) {
	t.Parallel()
	_, rt, ctx := triagedTicket(t, "ticket-denied")
	contract, _ := rt.Actions.Get(ActionIssueRefund)
	actionCtx := action.ActionContext{
		ActionName: ActionIssueRefund, EntityID: "ticket-denied", EntityType: EntityTicket,
		CurrentState: StateTriaged, Actor: AgentActor,
		Parameters: map[string]any{"amount_cents": SingleRefundCeilingCents + 5000, "reason": "x"},
		ApprovalID: "approval-denied",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusDenied, "no"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}
}

// TestSendOutreach_RejectedWithoutConsent confirms the consent check is
// the structural rejection: the executor itself returns
// ErrConsentMissing when consent_verified is missing or false. With
// ApprovalRequired set, the runtime gate fires first; this test calls
// the executor directly via CheckOutreachConsent to prove the same
// rejection independent of approval state.
func TestSendOutreach_RejectedWithoutConsent(t *testing.T) {
	t.Parallel()
	if err := CheckOutreachConsent(map[string]any{"channel": "email", "message": "hi"}); !errors.Is(err, ErrConsentMissing) {
		t.Fatalf("expected ErrConsentMissing without consent_verified, got %v", err)
	}
	if err := CheckOutreachConsent(map[string]any{"consent_verified": false}); !errors.Is(err, ErrConsentMissing) {
		t.Fatalf("expected ErrConsentMissing with consent_verified=false, got %v", err)
	}
	if err := CheckOutreachConsent(map[string]any{"consent_verified": true}); err != nil {
		t.Fatalf("expected no error with consent_verified=true, got %v", err)
	}
}

// TestSendOutreach_ConsentPlusApproval_Executes covers the success path:
// consent verified, approval granted, executor runs.
func TestSendOutreach_ConsentPlusApproval_Executes(t *testing.T) {
	t.Parallel()
	_, rt, ctx := triagedTicket(t, "ticket-outreach")
	contract, _ := rt.Actions.Get(ActionSendOutreach)
	actionCtx := action.ActionContext{
		ActionName: ActionSendOutreach, EntityID: "ticket-outreach", EntityType: EntityTicket,
		CurrentState: StateTriaged, Actor: AgentActor,
		Parameters: map[string]any{
			"channel": "email", "message": "follow up",
			"consent_verified": true,
		},
		ApprovalID: "approval-outreach",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	res, err := rt.ExecuteAction(ctx, actionCtx, contract)
	if err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}
	if res.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %s", res.Status)
	}
}

// TestEscalate_NoApproval confirms ESCALATE_TO_HUMAN executes without
// approval and parks the ticket in AWAITING_HUMAN.
func TestEscalate_NoApproval(t *testing.T) {
	t.Parallel()
	_, rt, ctx := newTicket(t, "ticket-escalate")
	contract, _ := rt.Actions.Get(ActionEscalate)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionEscalate, EntityID: "ticket-escalate", EntityType: EntityTicket,
		CurrentState: StateNew, Actor: AgentActor,
		Parameters: map[string]any{"reason": "ambiguous"},
	}, contract); err != nil {
		t.Fatalf("ExecuteAction(escalate): %v", err)
	}
	current, _, _ := rt.States.Current(ctx, "ticket-escalate")
	if current.Value != StateAwaitingHuman {
		t.Fatalf("expected %s, got %s", StateAwaitingHuman, current.Value)
	}
}

// TestNeedsApprovalForRefund covers the routing rule.
func TestNeedsApprovalForRefund(t *testing.T) {
	t.Parallel()
	d := New()
	if needs, _ := d.NeedsApprovalForRefund("t1", SingleRefundCeilingCents); needs {
		t.Fatalf("at-ceiling refund should not need approval")
	}
	if needs, _ := d.NeedsApprovalForRefund("t1", SingleRefundCeilingCents+1); !needs {
		t.Fatalf("over-ceiling refund must need approval")
	}
	// Stack four 4500-cent refunds; the next 4500 would push the running
	// total over CumulativeRefundCeilingCents and must require approval
	// even though each individual call is under SingleRefundCeilingCents.
	for i := 0; i < 4; i++ {
		d.addRefund("t2", 4500)
	}
	if needs, _ := d.NeedsApprovalForRefund("t2", 4500); !needs {
		t.Fatalf("cumulative cap should trigger approval requirement")
	}
}

// helpers

func newTicket(t *testing.T, id string) (*Domain, *runtime.Runtime, context.Context) {
	t.Helper()
	d := New()
	rt, err := d.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: id + "-evt", Adapter: AdapterSupport, Type: EventTicketOpened,
		Source: "test", EntityID: id, EntityType: EntityTicket,
		ActorID: SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}
	return d, rt, ctx
}

func triagedTicket(t *testing.T, id string) (*Domain, *runtime.Runtime, context.Context) {
	t.Helper()
	d, rt, ctx := newTicket(t, id)
	triage, _ := rt.Actions.Get(ActionTriageTicket)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionTriageTicket, EntityID: id, EntityType: EntityTicket,
		CurrentState: StateNew, Actor: AgentActor,
		Parameters: map[string]any{"category": "billing"},
	}, triage); err != nil {
		t.Fatalf("ExecuteAction(triage): %v", err)
	}
	return d, rt, ctx
}
