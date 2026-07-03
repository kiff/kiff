package payables

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/event"
)

type queuedAgent struct {
	proposals []AgentProposal
}

func (a *queuedAgent) Propose(context.Context, AgentRequest) (AgentProposal, error) {
	if len(a.proposals) == 0 {
		return AgentProposal{ActionName: "NO_ACTION"}, nil
	}
	next := a.proposals[0]
	a.proposals = a.proposals[1:]
	return next, nil
}

func TestHighRiskPaymentRequiresApprovalAndUsesPaymentService(t *testing.T) {
	ctx := context.Background()
	agent := &queuedAgent{proposals: []AgentProposal{
		{
			ActionName:       ActionVerifyInvoice,
			Parameters:       invoiceParams(1842000),
			ReasoningSummary: "invoice has complete vendor, amount, and rail details",
			Confidence:       0.91,
		},
		{
			ActionName:       ActionMarkReadyForPayment,
			Parameters:       map[string]any{"due_date": "2026-07-15"},
			ReasoningSummary: "verified invoice is ready for payment decision",
			Confidence:       0.88,
		},
		{
			ActionName:       ActionHoldForApproval,
			Parameters:       map[string]any{"reason": "amount is above low-risk payment limit"},
			ReasoningSummary: "high-value payment must be held for finance approval",
			Confidence:       0.94,
		},
		{
			ActionName:       ActionReleaseApprovedPayment,
			Parameters:       invoiceParams(1842000),
			ReasoningSummary: "try to pay again",
			Confidence:       0.5,
		},
	}}

	app, err := NewInteractiveApp(agent, "test-agent")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	first, err := app.ProcessInput(ctx, "Invoice INV-7741 from Northwind Parts for $18,420.00 USD, vendor vendor-northwind, bank ACH-9912, due 2026-07-15.")
	if err != nil {
		t.Fatalf("first input: %v", err)
	}
	if first.CurrentState != StateVerified {
		t.Fatalf("expected %s, got %s", StateVerified, first.CurrentState)
	}

	second, err := app.ProcessInput(ctx, "Mark the verified invoice ready for payment.")
	if err != nil {
		t.Fatalf("second input: %v", err)
	}
	if second.CurrentState != StateReadyForPayment {
		t.Fatalf("expected %s, got %s", StateReadyForPayment, second.CurrentState)
	}

	third, err := app.ProcessInput(ctx, "Pay this invoice today.")
	if err != nil {
		t.Fatalf("third input: %v", err)
	}
	if third.CurrentState != StatePaymentHeld {
		t.Fatalf("expected %s, got %s", StatePaymentHeld, third.CurrentState)
	}
	if third.Held == nil {
		t.Fatal("expected approved payment release to be held")
	}
	if len(third.Payments) != 0 {
		t.Fatalf("payment should not have been released before approval: %#v", third.Payments)
	}

	approved, err := app.ReviewHeld(ctx, true)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.CurrentState != StatePaid {
		t.Fatalf("expected %s, got %s", StatePaid, approved.CurrentState)
	}
	if len(approved.Payments) != 1 {
		t.Fatalf("expected one payment, got %d", len(approved.Payments))
	}
	if !timelineHasActor(approved.Timeline, audit.KindActionExecuted, PaymentServiceActor.ID, ActionReleaseApprovedPayment) {
		t.Fatal("expected approved payment execution by payment-service")
	}

	duplicate, err := app.ProcessInput(ctx, "Pay this invoice again.")
	if err != nil {
		t.Fatalf("duplicate input: %v", err)
	}
	if duplicate.CurrentState != StatePaid {
		t.Fatalf("expected state to remain %s, got %s", StatePaid, duplicate.CurrentState)
	}
	if len(duplicate.Payments) != 1 {
		t.Fatalf("duplicate request should not create another payment, got %d payments", len(duplicate.Payments))
	}
	if !containsLine(duplicate.Lines, "blocked RELEASE_APPROVED_PAYMENT") {
		t.Fatal("expected duplicate release proposal to be blocked by terminal state")
	}
}

func TestLowRiskPaymentCanReleaseWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	agent := &queuedAgent{proposals: []AgentProposal{
		{ActionName: ActionVerifyInvoice, Parameters: invoiceParams(32000), ReasoningSummary: "complete invoice", Confidence: 0.9},
		{ActionName: ActionMarkReadyForPayment, Parameters: map[string]any{"due_date": "2026-07-15"}, ReasoningSummary: "ready", Confidence: 0.9},
		{ActionName: ActionReleaseLowRiskPayment, Parameters: invoiceParams(32000), ReasoningSummary: "trusted vendor and low amount", Confidence: 0.9},
	}}
	agent.proposals[2].Parameters["trusted_vendor"] = true

	app, err := NewInteractiveApp(agent, "test-agent")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Invoice INV-3200 from Northwind Parts for $320.00 USD, vendor vendor-northwind, bank ACH-9912."); err != nil {
		t.Fatalf("verify input: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Mark it ready."); err != nil {
		t.Fatalf("ready input: %v", err)
	}
	released, err := app.ProcessInput(ctx, "Release the low-risk payment.")
	if err != nil {
		t.Fatalf("release input: %v", err)
	}
	if released.CurrentState != StatePaid {
		t.Fatalf("expected %s, got %s", StatePaid, released.CurrentState)
	}
	if len(released.Payments) != 1 {
		t.Fatalf("expected one payment, got %d", len(released.Payments))
	}
	if sawAuditKind(released.Timeline, audit.KindApprovalRequired) {
		t.Fatal("low-risk payment should not require approval")
	}
}

func TestAgentCannotSelfGrantPaymentPermission(t *testing.T) {
	ctx := context.Background()
	gateway := NewLedgerGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	invoiceID := "invoice-permission-test"
	if err := rt.IngestEvent(ctx, event.Event{
		ID:         "evt-invoice-permission-test",
		Type:       EventInvoiceReceived,
		EntityID:   invoiceID,
		EntityType: EntityInvoice,
		Source:     "test",
		ActorID:    "test",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	verify, _ := contract(rt, ActionVerifyInvoice)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionVerifyInvoice,
		EntityID:     invoiceID,
		EntityType:   EntityInvoice,
		CurrentState: StateReceived,
		Actor:        APAgentActor,
		Parameters:   invoiceParams(32000),
	}, verify); err != nil {
		t.Fatalf("verify: %v", err)
	}
	ready, _ := contract(rt, ActionMarkReadyForPayment)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionMarkReadyForPayment,
		EntityID:     invoiceID,
		EntityType:   EntityInvoice,
		CurrentState: StateVerified,
		Actor:        APAgentActor,
		Parameters:   map[string]any{"due_date": "2026-07-15"},
	}, ready); err != nil {
		t.Fatalf("ready: %v", err)
	}

	release, _ := contract(rt, ActionReleaseLowRiskPayment)
	params := invoiceParams(32000)
	params["trusted_vendor"] = true
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionReleaseLowRiskPayment,
		EntityID:     invoiceID,
		EntityType:   EntityInvoice,
		CurrentState: StateReadyForPayment,
		Actor:        APAgentActor,
		Parameters:   params,
	}, release)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied for agent money release, got %v", err)
	}
}

func TestLowRiskExecutorRejectsHighAmountEvenIfAgentChoosesWrongAction(t *testing.T) {
	ctx := context.Background()
	agent := &queuedAgent{proposals: []AgentProposal{
		{ActionName: ActionVerifyInvoice, Parameters: invoiceParams(1842000), ReasoningSummary: "complete invoice", Confidence: 0.9},
		{ActionName: ActionMarkReadyForPayment, Parameters: map[string]any{"due_date": "2026-07-15"}, ReasoningSummary: "ready", Confidence: 0.9},
		{ActionName: ActionReleaseLowRiskPayment, Parameters: invoiceParams(1842000), ReasoningSummary: "wrongly treats high payment as low risk", Confidence: 0.9},
	}}
	agent.proposals[2].Parameters["trusted_vendor"] = true

	app, err := NewInteractiveApp(agent, "test-agent")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Invoice INV-7741 from Northwind Parts for $18,420.00 USD, vendor vendor-northwind, bank ACH-9912."); err != nil {
		t.Fatalf("verify input: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Mark it ready."); err != nil {
		t.Fatalf("ready input: %v", err)
	}
	result, err := app.ProcessInput(ctx, "Release this as low risk.")
	if err != nil {
		t.Fatalf("release input: %v", err)
	}
	if result.CurrentState != StateReadyForPayment {
		t.Fatalf("expected state to remain %s, got %s", StateReadyForPayment, result.CurrentState)
	}
	if len(result.Payments) != 0 {
		t.Fatalf("expected no payment, got %#v", result.Payments)
	}
	if !containsLine(result.Lines, "must be <= 50000") {
		t.Fatal("expected typed parameter validation to block high low-risk payment")
	}
}

func TestMalformedInvoiceAmountIsInvalidBeforeExecutor(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewLedgerGateway())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	invoiceID := "invoice-invalid-amount"
	if err := rt.IngestEvent(ctx, event.Event{
		ID:         "evt-invoice-invalid-amount",
		Type:       EventInvoiceReceived,
		EntityID:   invoiceID,
		EntityType: EntityInvoice,
		Source:     "test",
		ActorID:    "test",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	verify, _ := contract(rt, ActionVerifyInvoice)
	params := invoiceParams(32000)
	params["amount_cents"] = "12.34"
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionVerifyInvoice,
		EntityID:     invoiceID,
		EntityType:   EntityInvoice,
		CurrentState: StateReceived,
		Actor:        APAgentActor,
		Parameters:   params,
	}, verify)
	if !errors.Is(err, action.ErrInvalidParameter) {
		t.Fatalf("expected invalid parameter, got %v", err)
	}
	current, ok, err := rt.States.Current(ctx, invoiceID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !ok || current.Value != StateReceived {
		t.Fatalf("expected state to remain %s, got %+v", StateReceived, current)
	}
}

func invoiceParams(amountCents int64) map[string]any {
	return map[string]any{
		"invoice_id":       "inv-ap-7741",
		"vendor_id":        "vendor-northwind",
		"invoice_number":   "INV-7741",
		"amount_cents":     amountCents,
		"currency":         "USD",
		"bank_fingerprint": "bank-ach-9912",
		"idempotency_key":  fmt.Sprintf("inv-ap-7741:vendor-northwind:%d:bank-ach-9912", amountCents),
	}
}

func containsLine(lines []AppLine, fragment string) bool {
	for _, line := range lines {
		if strings.Contains(line.Text, fragment) {
			return true
		}
	}
	return false
}

func sawAuditKind(entries []TimelineEntry, kind audit.Kind) bool {
	for _, entry := range entries {
		if entry.Kind == kind {
			return true
		}
	}
	return false
}

func timelineHasActor(entries []TimelineEntry, kind audit.Kind, actorID, detail string) bool {
	for _, entry := range entries {
		if entry.Kind == kind && entry.ActorID == actorID && entry.Detail == detail {
			return true
		}
	}
	return false
}

// TestCapstoneLifecycleViewReflectsGovernedHistory is the capstone assertion:
// after a high-value invoice is verified, held, approved, and paid, the
// framework's EntityLifecycle view (surfaced on the snapshot) shows the whole
// governed history — proposal, approval hold, approval grant, execution — and
// the derived current status, without the app stitching it together by hand.
func TestCapstoneLifecycleViewReflectsGovernedHistory(t *testing.T) {
	ctx := context.Background()
	agent := &queuedAgent{proposals: []AgentProposal{
		{ActionName: ActionVerifyInvoice, Parameters: invoiceParams(1842000), ReasoningSummary: "complete invoice details", Confidence: 0.9},
		{ActionName: ActionMarkReadyForPayment, Parameters: map[string]any{"due_date": "2026-07-15"}, ReasoningSummary: "ready for payment", Confidence: 0.9},
		{ActionName: ActionHoldForApproval, Parameters: map[string]any{"reason": "above low-risk limit"}, ReasoningSummary: "high value, hold for finance", Confidence: 0.95},
	}}

	app, err := NewInteractiveApp(agent, "test-agent")
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Invoice INV-7741 from Northwind Parts for $18,420.00 USD, vendor vendor-northwind, bank ACH-9912, due 2026-07-15."); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Mark the verified invoice ready for payment."); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if _, err := app.ProcessInput(ctx, "Pay this invoice today."); err != nil {
		t.Fatalf("hold: %v", err)
	}
	final, err := app.ReviewHeld(ctx, true)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	lc := final.Lifecycle
	if lc.EntityID != final.InvoiceID {
		t.Fatalf("lifecycle entity %q != invoice %q", lc.EntityID, final.InvoiceID)
	}
	if lc.CurrentState != StatePaid {
		t.Fatalf("expected lifecycle current state %s, got %q", StatePaid, lc.CurrentState)
	}
	if !lc.Has(audit.KindDecisionProposed) {
		t.Fatal("lifecycle should record the agent proposals")
	}
	// The app holds by recording a pending approval, then granting it — so the
	// governed history shows the recorded hold and the grant.
	if !lc.Has(audit.KindApprovalRecorded) || !lc.Has(audit.KindApprovalGranted) {
		t.Fatal("lifecycle should record the approval hold and grant")
	}
	if !lc.Executed() {
		t.Fatalf("lifecycle should show the latest action executed, disposition=%q", lc.Disposition())
	}
	if len(lc.Approvals) == 0 {
		t.Fatal("lifecycle should attach the approval record")
	}
	if len(lc.Decisions) == 0 {
		t.Fatal("lifecycle should attach the proposal decisions")
	}
}
