package procurement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func TestLowRiskPurchaseCreatesPOWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	gateway := NewInMemoryPurchasingGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pr-low-1001"
	seedRequest(t, ctx, rt, requestID, "requester-nguyen", "engineering")

	mustExecute(t, ctx, rt, ActionAttachQuote, requestID, StateReceived, ProcurementAgentActor, quoteParams(requestID, 84000, false, false))
	mustExecute(t, ctx, rt, ActionCheckBudget, requestID, StateQuoteAttached, ProcurementAgentActor, budgetParams(true, false))
	mustExecute(t, ctx, rt, ActionAssessPurchaseRisk, requestID, StateBudgetVerified, ProcurementAgentActor, assessmentParams(84000, true, true, false, false, false))
	mustExecute(t, ctx, rt, ActionPrepareStandardPO, requestID, StateLowRiskReady, ProcurementAgentActor, poParams(requestID, 84000))
	mustExecute(t, ctx, rt, ActionCreateStandardPO, requestID, StatePOPrepared, ERPServiceActor, poParams(requestID, 84000))

	current, err := CurrentState(ctx, rt, requestID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateOrdered {
		t.Fatalf("expected %s, got %s", StateOrdered, current.Value)
	}
	if orders := gateway.List(); len(orders) != 1 {
		t.Fatalf("expected one PO, got %d", len(orders))
	}
	timeline, err := rt.Timeline(ctx, requestID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("low-risk purchase should not require approval")
	}
	if !auditHasActorAction(timeline, audit.KindActionExecuted, ERPServiceActor.ID, ActionCreateStandardPO) {
		t.Fatal("expected ERP service to create the PO")
	}
	replayed, err := rt.RebuildState(ctx, requestID)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed.State.Value != StateOrdered {
		t.Fatalf("expected replay state %s, got %s", StateOrdered, replayed.State.Value)
	}
}

func TestHighRiskPurchaseRequiresManagerApprovalAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	gateway := NewInMemoryPurchasingGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pr-review-2002"
	seedHighRiskReview(t, ctx, rt, requestID)

	contract, err := Contract(rt, ActionCreateApprovedPO)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	params := approvedPOParams(requestID, 1842000, false, true, true, true, true)
	actionCtx := action.ActionContext{
		ActionName:     ActionCreateApprovedPO,
		EntityID:       requestID,
		EntityType:     EntityPurchaseRequest,
		CurrentState:   StateReviewRequired,
		Actor:          ERPServiceActor,
		ApprovalID:     "approval-pr-review-2002",
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if orders := gateway.List(); len(orders) != 0 {
		t.Fatalf("PO should not be created before approval: %#v", orders)
	}

	requestCtx := actionCtx
	requestCtx.Actor = ProcurementAgentActor
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, requestCtx, contract, "high-value new vendor purchase requires manager approval"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewPurchaseApproval(ctx, rt, actionCtx.ApprovalID, ProcurementManagerActor, true, "approve source and spend"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("approved PO: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if orders := gateway.List(); len(orders) != 1 {
		t.Fatalf("expected one PO after retry, got %d", len(orders))
	}
	timeline, err := rt.Timeline(ctx, requestID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if !auditHasKind(timeline, audit.KindActionDeduplicated) {
		t.Fatal("expected idempotent retry audit")
	}
}

func TestAgentCannotSelfCreatePOByAddingServiceRole(t *testing.T) {
	ctx := context.Background()
	gateway := NewInMemoryPurchasingGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pr-permission-3003"
	seedHighRiskReview(t, ctx, rt, requestID)

	contract, err := Contract(rt, ActionCreateApprovedPO)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	spoofedAgent := ProcurementAgentActor
	spoofedAgent.Roles = append(spoofedAgent.Roles, RoleERPService)
	params := approvedPOParams(requestID, 1842000, false, true, true, true, true)
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:     ActionCreateApprovedPO,
		EntityID:       requestID,
		EntityType:     EntityPurchaseRequest,
		CurrentState:   StateReviewRequired,
		Actor:          spoofedAgent,
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}, contract)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if orders := gateway.List(); len(orders) != 0 {
		t.Fatalf("expected no PO, got %#v", orders)
	}
}

func TestInvalidCurrencyIsRejectedBeforeExecutor(t *testing.T) {
	ctx := context.Background()
	gateway := NewInMemoryPurchasingGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pr-invalid-4004"
	seedHighRiskReview(t, ctx, rt, requestID)

	contract, err := Contract(rt, ActionCreateApprovedPO)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	params := approvedPOParams(requestID, 1842000, false, true, true, true, true)
	params["currency"] = "BTC"
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:     ActionCreateApprovedPO,
		EntityID:       requestID,
		EntityType:     EntityPurchaseRequest,
		CurrentState:   StateReviewRequired,
		Actor:          ERPServiceActor,
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}, contract)
	if !errors.Is(err, action.ErrInvalidParameter) {
		t.Fatalf("expected invalid parameter, got %v", err)
	}
	if orders := gateway.List(); len(orders) != 0 {
		t.Fatalf("expected no PO, got %#v", orders)
	}
}

func TestOnlyProcurementManagerCanReviewPurchaseApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryPurchasingGateway())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	contract, err := Contract(rt, ActionCreateApprovedPO)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionCreateApprovedPO,
		EntityID:     "pr-review-auth-5005",
		EntityType:   EntityPurchaseRequest,
		CurrentState: StateReviewRequired,
		Actor:        ProcurementAgentActor,
		ApprovalID:   "approval-pr-review-auth-5005",
		Parameters:   approvedPOParams("pr-review-auth-5005", 1842000, false, true, true, true, true),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "requires procurement manager authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewPurchaseApproval(ctx, rt, actionCtx.ApprovalID, ProcurementAgentActor, true, "agent tried to approve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}

	selfCtx := actionCtx
	selfCtx.Actor = ProcurementManagerActor
	selfCtx.ApprovalID = "approval-pr-self-review-5006"
	if _, err := rt.RequestApproval(ctx, selfCtx.ApprovalID, selfCtx, contract, "same actor has review authority but requested approval"); err != nil {
		t.Fatalf("request self approval: %v", err)
	}
	if _, err := ReviewPurchaseApproval(ctx, rt, selfCtx.ApprovalID, ProcurementManagerActor, true, "requester tried to self approve"); !errors.Is(err, approval.ErrSelfReview) {
		t.Fatalf("expected self-review rejection, got %v", err)
	}
}

func seedRequest(t *testing.T, ctx context.Context, rt *runtime.Runtime, requestID, requesterID, department string) {
	t.Helper()
	if err := rt.IngestEvent(ctx, NewPurchaseRequestReceivedEvent(requestID, requesterID, department, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

func seedHighRiskReview(t *testing.T, ctx context.Context, rt *runtime.Runtime, requestID string) {
	t.Helper()
	seedRequest(t, ctx, rt, requestID, "requester-nguyen", "engineering")
	mustExecute(t, ctx, rt, ActionAttachQuote, requestID, StateReceived, ProcurementAgentActor, quoteParams(requestID, 1842000, true, true))
	mustExecute(t, ctx, rt, ActionCheckBudget, requestID, StateQuoteAttached, ProcurementAgentActor, budgetParams(true, true))
	mustExecute(t, ctx, rt, ActionAssessPurchaseRisk, requestID, StateBudgetVerified, ProcurementAgentActor, assessmentParams(1842000, false, true, true, true, true))
}

func mustExecute(t *testing.T, ctx context.Context, rt *runtime.Runtime, actionName, requestID, currentState string, a actor.Actor, params map[string]any) {
	t.Helper()
	contract, err := Contract(rt, actionName)
	if err != nil {
		t.Fatalf("contract %s: %v", actionName, err)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:     actionName,
		EntityID:       requestID,
		EntityType:     EntityPurchaseRequest,
		CurrentState:   currentState,
		Actor:          a,
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}, contract); err != nil {
		t.Fatalf("execute %s: %v", actionName, err)
	}
}

func quoteParams(requestID string, amount int64, newVendor, soleSource bool) map[string]any {
	return map[string]any{
		"request_id":       requestID,
		"requester_id":     "requester-nguyen",
		"department":       "engineering",
		"vendor_id":        "vendor-northwind",
		"vendor_name":      "Northwind Supply",
		"item_description": "observability seats",
		"amount_cents":     amount,
		"currency":         "USD",
		"quote_id":         "quote-" + requestID,
		"new_vendor":       newVendor,
		"sole_source":      soleSource,
	}
}

func budgetParams(available, securityReview bool) map[string]any {
	return map[string]any{
		"cost_center":              "eng-platform",
		"budget_available":         available,
		"security_review_required": securityReview,
	}
}

func assessmentParams(amount int64, approvedVendor, budgetAvailable, newVendor, soleSource, securityReview bool) map[string]any {
	return map[string]any{
		"amount_cents":             amount,
		"currency":                 "USD",
		"approved_vendor":          approvedVendor,
		"budget_available":         budgetAvailable,
		"new_vendor":               newVendor,
		"sole_source":              soleSource,
		"security_review_required": securityReview,
	}
}

func poParams(requestID string, amount int64) map[string]any {
	return map[string]any{
		"request_id":       requestID,
		"requester_id":     "requester-nguyen",
		"department":       "engineering",
		"vendor_id":        "vendor-northwind",
		"vendor_name":      "Northwind Supply",
		"item_description": "observability seats",
		"amount_cents":     amount,
		"currency":         "USD",
		"cost_center":      "eng-platform",
		"idempotency_key":  requestID + ":vendor-northwind:" + "observability",
	}
}

func approvedPOParams(requestID string, amount int64, approvedVendor, budgetAvailable, newVendor, soleSource, securityReview bool) map[string]any {
	params := poParams(requestID, amount)
	params["approved_vendor"] = approvedVendor
	params["budget_available"] = budgetAvailable
	params["new_vendor"] = newVendor
	params["sole_source"] = soleSource
	params["security_review_required"] = securityReview
	return params
}

func auditHasKind(records []audit.Record, kind audit.Kind) bool {
	for _, record := range records {
		if record.Kind == kind {
			return true
		}
	}
	return false
}

func auditHasActorAction(records []audit.Record, kind audit.Kind, actorID, actionName string) bool {
	for _, record := range records {
		if record.Kind != kind || record.ActorID != actorID || record.Data == nil {
			continue
		}
		if got, ok := record.Data["action"].(string); ok && got == actionName {
			return true
		}
	}
	return false
}
