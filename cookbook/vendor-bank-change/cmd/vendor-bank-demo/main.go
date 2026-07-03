package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	vendorbank "github.com/kiff/kiff/cookbook/vendor-bank-change"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func main() {
	ctx := context.Background()
	master := vendorbank.NewInMemoryVendorMaster()
	rt, err := vendorbank.NewRuntime(master)
	if err != nil {
		fail("create runtime", err)
	}

	changeID := "vbc-demo-8821"
	vendorID := "vendor-contoso"
	vendorName := "Contoso Manufacturing"
	if err := rt.IngestEvent(ctx, vendorbank.NewChangeRequestedEvent(changeID, vendorID, vendorName, time.Now())); err != nil {
		fail("ingest bank change", err)
	}

	agent := &vendorbank.ScriptedAgent{Proposals: []vendorbank.AgentProposal{
		{
			ActionName: vendorbank.ActionAttachEvidence,
			Parameters: map[string]any{
				"change_id":           changeID,
				"vendor_id":           vendorID,
				"vendor_name":         vendorName,
				"account_fingerprint": "acct-new-8842",
				"account_country":     "US",
				"evidence_packet_id":  "evidence-vbc-demo-8821",
				"requester_email":     "ap-team@example.com",
			},
			ReasoningSummary: "vendor portal request includes bank letter and callback contact",
			Confidence:       0.90,
		},
		{
			ActionName: vendorbank.ActionVerifyVendor,
			Parameters: map[string]any{
				"vendor_id":         vendorID,
				"existing_vendor":   true,
				"tax_id_match":      true,
				"callback_verified": true,
			},
			ReasoningSummary: "vendor identity and callback were verified",
			Confidence:       0.93,
		},
		{
			ActionName: vendorbank.ActionAssessBankChange,
			Parameters: map[string]any{
				"risk_score_percent":          82,
				"known_account":               false,
				"callback_verified":           true,
				"open_invoice_exposure_cents": 760000,
				"fraud_signal":                true,
			},
			ReasoningSummary: "new bank details with high exposure require finance approval",
			Confidence:       0.95,
		},
	}}

	fmt.Println("KIFF vendor bank-change demo")
	fmt.Println()
	fmt.Println(" - vendor submitted bank detail change")

	if err := step(ctx, rt, agent, changeID, "Attach bank-change evidence."); err != nil {
		fail("attach evidence", err)
	}
	fmt.Println(" - agent proposed ATTACH_EVIDENCE; KIFF moved state to EVIDENCE_ATTACHED")

	if err := step(ctx, rt, agent, changeID, "Verify vendor identity and callback."); err != nil {
		fail("verify vendor", err)
	}
	fmt.Println(" - agent proposed VERIFY_VENDOR; KIFF moved state to VENDOR_VERIFIED")

	if err := step(ctx, rt, agent, changeID, "Assess bank-change risk."); err != nil {
		fail("assess bank change", err)
	}
	fmt.Println(" - agent proposed ASSESS_BANK_CHANGE; KIFF routed change to REVIEW_REQUIRED")

	contract, err := vendorbank.Contract(rt, vendorbank.ActionApplyApprovedChange)
	if err != nil {
		fail("load approved change contract", err)
	}
	applyCtx := action.ActionContext{
		ActionName:   vendorbank.ActionApplyApprovedChange,
		EntityID:     changeID,
		EntityType:   vendorbank.EntityVendorBankChange,
		CurrentState: vendorbank.StateReviewRequired,
		Actor:        vendorbank.VendorMasterActor,
		ApprovalID:   "approval-vbc-demo-8821",
		Parameters: map[string]any{
			"change_id":           changeID,
			"vendor_id":           vendorID,
			"vendor_name":         vendorName,
			"account_fingerprint": "acct-new-8842",
			"account_country":     "US",
			"evidence_packet_id":  "evidence-vbc-demo-8821",
			"idempotency_key":     "vbc-demo-8821:vendor-contoso:acct-new-8842",
		},
	}
	if _, err := rt.ExecuteAction(ctx, applyCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		fail("approval gate", fmt.Errorf("expected approval required, got %v", err))
	}
	fmt.Println(" - vendor-master update was blocked until finance approval")

	requestCtx := applyCtx
	requestCtx.Actor = vendorbank.VendorAgentActor
	if _, err := rt.RequestApproval(ctx, applyCtx.ApprovalID, requestCtx, contract, "new account and fraud signal require finance approval"); err != nil {
		fail("request approval", err)
	}
	if _, err := vendorbank.ReviewBankChangeApproval(ctx, rt, applyCtx.ApprovalID, vendorbank.FinanceControllerActor, true, "finance approved after evidence review"); err != nil {
		fail("review approval", err)
	}
	fmt.Println(" - finance controller approved APPLY_APPROVED_BANK_CHANGE")

	if _, err := rt.ExecuteAction(ctx, applyCtx, contract); err != nil {
		fail("apply bank change", err)
	}
	fmt.Println(" - vendor-master-service applied the bank change through an idempotent gateway")

	current, err := vendorbank.CurrentState(ctx, rt, changeID)
	if err != nil {
		fail("current state", err)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", current.Value)
	for _, receipt := range master.List() {
		fmt.Printf("Update: %s vendor=%s account=%s via %s\n", receipt.UpdateID, receipt.VendorID, receipt.AccountFingerprint, receipt.IdempotencyKey)
	}
}

func step(ctx context.Context, rt *runtime.Runtime, agent vendorbank.Agent, changeID, input string) error {
	current, err := vendorbank.CurrentState(ctx, rt, changeID)
	if err != nil {
		return err
	}
	contracts, err := rt.AllowedActions(ctx, changeID)
	if err != nil {
		return err
	}
	proposal, err := agent.Propose(ctx, vendorbank.AgentRequest{
		ChangeID:       changeID,
		CurrentState:   current.Value,
		AllowedActions: actionNames(contracts),
		OperatorInput:  input,
	})
	if err != nil {
		return err
	}
	if err := vendorbank.RecordAgentProposal(ctx, rt, changeID, input, proposal); err != nil {
		return err
	}
	if proposal.ActionName == "NO_ACTION" {
		return nil
	}
	_, err = vendorbank.ApplyAgentProposal(ctx, rt, changeID, current.Value, proposal)
	return err
}

func actionNames(contracts []action.ActionContract) []string {
	names := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		names = append(names, contract.Name)
	}
	return names
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
