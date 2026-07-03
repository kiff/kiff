package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	priorauth "github.com/kiff/kiff/cookbook/healthcare-prior-auth"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func main() {
	ctx := context.Background()
	portal := priorauth.NewInMemoryPayerPortal()
	rt, err := priorauth.NewRuntime(portal)
	if err != nil {
		fail("create runtime", err)
	}

	requestID := "pa-demo-8842"
	patientID := "patient-marta"
	payerID := "payer-north"
	procedure := "pt-extended"
	if err := rt.IngestEvent(ctx, priorauth.NewAuthRequestReceivedEvent(requestID, patientID, payerID, procedure, time.Now())); err != nil {
		fail("ingest request", err)
	}

	agent := &priorauth.ScriptedAgent{Proposals: []priorauth.AgentProposal{
		{
			ActionName: priorauth.ActionRecordClinicalEvidence,
			Parameters: map[string]any{
				"request_id":         requestID,
				"patient_id":         patientID,
				"payer_id":           payerID,
				"procedure_code":     procedure,
				"evidence_packet_id": "evidence-pa-demo-8842",
			},
			ReasoningSummary: "chart packet includes diagnosis, notes, and requested service code",
			Confidence:       0.92,
		},
		{
			ActionName: priorauth.ActionCheckPolicyCriteria,
			Parameters: map[string]any{
				"criteria_met":      false,
				"denial_risk_score": 0.68,
				"missing_evidence":  false,
			},
			ReasoningSummary: "payer criteria are ambiguous and denial risk is elevated",
			Confidence:       0.84,
		},
	}}

	fmt.Println("KIFF healthcare prior authorization demo")
	fmt.Println()
	fmt.Println(" - request received for payer authorization")

	if err := step(ctx, rt, agent, requestID, "Clinical packet arrived."); err != nil {
		fail("record clinical evidence", err)
	}
	fmt.Println(" - agent proposed RECORD_CLINICAL_EVIDENCE; KIFF moved state to READY_FOR_CRITERIA")

	if err := step(ctx, rt, agent, requestID, "Check payer policy criteria."); err != nil {
		fail("check policy criteria", err)
	}
	fmt.Println(" - agent proposed CHECK_POLICY_CRITERIA; KIFF routed request to REVIEW_REQUIRED")

	contract, err := priorauth.Contract(rt, priorauth.ActionSubmitReviewedAuthorization)
	if err != nil {
		fail("load reviewed submission contract", err)
	}
	releaseCtx := action.ActionContext{
		ActionName:   priorauth.ActionSubmitReviewedAuthorization,
		EntityID:     requestID,
		EntityType:   priorauth.EntityPriorAuthRequest,
		CurrentState: priorauth.StateReviewRequired,
		Actor:        priorauth.PayerPortalActor,
		ApprovalID:   "approval-pa-demo-8842",
		Parameters: map[string]any{
			"request_id":         requestID,
			"patient_id":         patientID,
			"payer_id":           payerID,
			"procedure_code":     procedure,
			"evidence_packet_id": "evidence-pa-demo-8842",
			"idempotency_key":    "pa-demo-8842:patient-marta:payer-north:PT-EXTENDED",
			"clinician_note":     "clinician reviewed ambiguity and approved submission",
		},
	}
	if _, err := rt.ExecuteAction(ctx, releaseCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		fail("approval gate", fmt.Errorf("expected approval required, got %v", err))
	}
	fmt.Println(" - payer submission was blocked until clinician approval")

	requestCtx := releaseCtx
	requestCtx.Actor = priorauth.PriorAuthAgentActor
	if _, err := rt.RequestApproval(ctx, releaseCtx.ApprovalID, requestCtx, contract, "ambiguous criteria require clinician review"); err != nil {
		fail("request approval", err)
	}
	if _, err := priorauth.ReviewAuthorizationApproval(ctx, rt, releaseCtx.ApprovalID, priorauth.ClinicianReviewerActor, true, "clinician approved payer submission"); err != nil {
		fail("review approval", err)
	}
	fmt.Println(" - clinician approved SUBMIT_REVIEWED_AUTHORIZATION")

	if _, err := rt.ExecuteAction(ctx, releaseCtx, contract); err != nil {
		fail("submit authorization", err)
	}
	fmt.Println(" - payer-portal-service submitted request through idempotent gateway")

	current, err := priorauth.CurrentState(ctx, rt, requestID)
	if err != nil {
		fail("current state", err)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", current.Value)
	for _, receipt := range portal.List() {
		fmt.Printf("Submission: %s procedure=%s reviewed=%t via %s\n", receipt.SubmissionID, receipt.ProcedureCode, receipt.Reviewed, receipt.IdempotencyKey)
	}
}

func step(ctx context.Context, rt *runtime.Runtime, agent priorauth.Agent, requestID, input string) error {
	current, err := priorauth.CurrentState(ctx, rt, requestID)
	if err != nil {
		return err
	}
	contracts, err := rt.AllowedActions(ctx, requestID)
	if err != nil {
		return err
	}
	proposal, err := agent.Propose(ctx, priorauth.AgentRequest{
		RequestID:      requestID,
		CurrentState:   current.Value,
		AllowedActions: actionNames(contracts),
		OperatorInput:  input,
	})
	if err != nil {
		return err
	}
	if err := priorauth.RecordAgentProposal(ctx, rt, requestID, input, proposal); err != nil {
		return err
	}
	if proposal.ActionName == "NO_ACTION" {
		return nil
	}
	_, err = priorauth.ApplyAgentProposal(ctx, rt, requestID, current.Value, proposal)
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
