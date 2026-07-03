package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	procurement "github.com/kiff/kiff/cookbook/procurement-purchase-order"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func main() {
	ctx := context.Background()
	gateway := procurement.NewInMemoryPurchasingGateway()
	rt, err := procurement.NewRuntime(gateway)
	if err != nil {
		fail("create runtime", err)
	}

	requestID := "pr-demo-7712"
	if err := rt.IngestEvent(ctx, procurement.NewPurchaseRequestReceivedEvent(requestID, "requester-nguyen", "engineering", time.Now())); err != nil {
		fail("ingest request", err)
	}

	agent := &procurement.ScriptedAgent{Proposals: []procurement.AgentProposal{
		{
			ActionName: procurement.ActionAttachQuote,
			Parameters: map[string]any{
				"request_id":       requestID,
				"requester_id":     "requester-nguyen",
				"department":       "engineering",
				"vendor_id":        "vendor-northwind",
				"vendor_name":      "Northwind Supply",
				"item_description": "observability platform seats",
				"amount_cents":     1842000,
				"currency":         "USD",
				"quote_id":         "quote-pr-demo-7712",
				"new_vendor":       true,
				"sole_source":      true,
			},
			ReasoningSummary: "quote is complete but vendor is new and sole-source",
			Confidence:       0.91,
		},
		{
			ActionName: procurement.ActionCheckBudget,
			Parameters: map[string]any{
				"cost_center":              "eng-platform",
				"budget_available":         true,
				"security_review_required": true,
			},
			ReasoningSummary: "budget exists, but the SaaS purchase needs security review evidence",
			Confidence:       0.88,
		},
		{
			ActionName: procurement.ActionAssessPurchaseRisk,
			Parameters: map[string]any{
				"amount_cents":             1842000,
				"currency":                 "USD",
				"approved_vendor":          false,
				"budget_available":         true,
				"new_vendor":               true,
				"sole_source":              true,
				"security_review_required": true,
			},
			ReasoningSummary: "high-value new-vendor sole-source purchase requires procurement approval",
			Confidence:       0.96,
		},
	}}

	fmt.Println("KIFF procurement purchase-order demo")
	fmt.Println()
	fmt.Println(" - purchase request received")

	if err := step(ctx, rt, agent, requestID, "Attach supplier quote."); err != nil {
		fail("attach quote", err)
	}
	fmt.Println(" - agent proposed ATTACH_QUOTE; KIFF moved state to QUOTE_ATTACHED")

	if err := step(ctx, rt, agent, requestID, "Check budget and security requirements."); err != nil {
		fail("check budget", err)
	}
	fmt.Println(" - agent proposed CHECK_BUDGET; KIFF moved state to BUDGET_VERIFIED")

	if err := step(ctx, rt, agent, requestID, "Assess purchase risk."); err != nil {
		fail("assess purchase", err)
	}
	fmt.Println(" - agent proposed ASSESS_PURCHASE_RISK; KIFF routed request to REVIEW_REQUIRED")

	contract, err := procurement.Contract(rt, procurement.ActionCreateApprovedPO)
	if err != nil {
		fail("load PO contract", err)
	}
	params := map[string]any{
		"request_id":               requestID,
		"requester_id":             "requester-nguyen",
		"department":               "engineering",
		"vendor_id":                "vendor-northwind",
		"vendor_name":              "Northwind Supply",
		"item_description":         "observability platform seats",
		"amount_cents":             1842000,
		"currency":                 "USD",
		"cost_center":              "eng-platform",
		"idempotency_key":          "pr-demo-7712:vendor-northwind:observability",
		"approved_vendor":          false,
		"budget_available":         true,
		"new_vendor":               true,
		"sole_source":              true,
		"security_review_required": true,
	}
	createCtx := action.ActionContext{
		ActionName:     procurement.ActionCreateApprovedPO,
		EntityID:       requestID,
		EntityType:     procurement.EntityPurchaseRequest,
		CurrentState:   procurement.StateReviewRequired,
		Actor:          procurement.ERPServiceActor,
		ApprovalID:     "approval-pr-demo-7712",
		Parameters:     params,
		IdempotencyKey: "pr-demo-7712:vendor-northwind:observability",
	}
	if _, err := rt.ExecuteAction(ctx, createCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		fail("approval gate", fmt.Errorf("expected approval required, got %v", err))
	}
	fmt.Println(" - ERP purchase-order creation was blocked by dynamic approval policy")

	requestCtx := createCtx
	requestCtx.Actor = procurement.ProcurementAgentActor
	if _, err := rt.RequestApproval(ctx, createCtx.ApprovalID, requestCtx, contract, "new-vendor high-value purchase requires procurement approval"); err != nil {
		fail("request approval", err)
	}
	if _, err := procurement.ReviewPurchaseApproval(ctx, rt, createCtx.ApprovalID, procurement.ProcurementManagerActor, true, "approve spend and sourcing exception"); err != nil {
		fail("review approval", err)
	}
	fmt.Println(" - procurement manager approved CREATE_APPROVED_PO")

	if _, err := rt.ExecuteAction(ctx, createCtx, contract); err != nil {
		fail("create PO", err)
	}
	if _, err := rt.ExecuteAction(ctx, createCtx, contract); err != nil {
		fail("idempotent retry", err)
	}
	fmt.Println(" - retry returned the prior result without a second ERP write")

	current, err := procurement.CurrentState(ctx, rt, requestID)
	if err != nil {
		fail("current state", err)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", current.Value)
	for _, receipt := range gateway.List() {
		fmt.Printf("PO: %s vendor=%s amount=%s %.2f via %s\n", receipt.PurchaseOrderID, receipt.VendorID, receipt.Currency, float64(receipt.AmountCents)/100, receipt.IdempotencyKey)
	}
	if err := printLifecycle(ctx, rt, requestID); err != nil {
		fail("lifecycle", err)
	}
}

func step(ctx context.Context, rt *runtime.Runtime, agent procurement.Agent, requestID, input string) error {
	current, err := procurement.CurrentState(ctx, rt, requestID)
	if err != nil {
		return err
	}
	contracts, err := rt.AllowedActions(ctx, requestID)
	if err != nil {
		return err
	}
	proposal, err := agent.Propose(ctx, procurement.AgentRequest{
		RequestID:      requestID,
		CurrentState:   current.Value,
		AllowedActions: actionNames(contracts),
		OperatorInput:  input,
	})
	if err != nil {
		return err
	}
	if err := procurement.RecordAgentProposal(ctx, rt, requestID, input, proposal); err != nil {
		return err
	}
	if proposal.ActionName == "NO_ACTION" {
		return nil
	}
	_, err = procurement.ApplyAgentProposal(ctx, rt, requestID, current.Value, proposal)
	return err
}

func actionNames(contracts []action.ActionContract) []string {
	names := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		names = append(names, contract.Name)
	}
	return names
}

func printLifecycle(ctx context.Context, rt *runtime.Runtime, requestID string) error {
	lifecycle, err := rt.EntityLifecycle(ctx, requestID)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("Lifecycle view: state=%s disposition=%s decisions=%d approvals=%d stages=%d\n",
		lifecycle.CurrentState, lifecycle.Disposition(), len(lifecycle.Decisions), len(lifecycle.Approvals), len(lifecycle.Stages))
	for _, stage := range lifecycle.Stages {
		if !showLifecycleStage(stage.Kind) {
			continue
		}
		detail := stage.Action
		if detail == "" {
			detail = stage.Message
		}
		fmt.Printf(" - %s actor=%s detail=%s\n", stage.Kind, stage.ActorID, detail)
	}
	return nil
}

func showLifecycleStage(kind audit.Kind) bool {
	switch kind {
	case audit.KindDecisionProposed,
		audit.KindApprovalRequired,
		audit.KindApprovalGranted,
		audit.KindActionExecuted,
		audit.KindActionDeduplicated:
		return true
	default:
		return false
	}
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
