package priorauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func TestCriteriaMetAuthorizationSubmitsWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	portal := NewInMemoryPayerPortal()
	rt, err := NewRuntime(portal)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pa-low-1001"
	patientID := "patient-lee"
	payerID := "payer-north"
	procedure := "mri-lumbar"
	if err := rt.IngestEvent(ctx, NewAuthRequestReceivedEvent(requestID, patientID, payerID, procedure, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionRecordClinicalEvidence, requestID, StateReceived, PriorAuthAgentActor, evidenceParams(requestID, patientID, payerID, procedure, "evidence-low-1001"))
	mustExecute(t, ctx, rt, ActionCheckPolicyCriteria, requestID, StateReadyForCriteria, PriorAuthAgentActor, criteriaParams(true, 12, false))
	mustExecute(t, ctx, rt, ActionPrepareAuthorization, requestID, StateCriteriaMet, PriorAuthAgentActor, submissionParams(requestID, patientID, payerID, procedure, "evidence-low-1001"))
	mustExecute(t, ctx, rt, ActionSubmitAuthorization, requestID, StatePrepared, PayerPortalActor, submissionParams(requestID, patientID, payerID, procedure, "evidence-low-1001"))

	current, err := CurrentState(ctx, rt, requestID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateSubmitted {
		t.Fatalf("expected %s, got %s", StateSubmitted, current.Value)
	}
	if submissions := portal.List(); len(submissions) != 1 {
		t.Fatalf("expected one submission, got %d", len(submissions))
	}
	timeline, err := rt.Timeline(ctx, requestID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("criteria-met request should not require approval")
	}
	if !auditHasActorAction(timeline, audit.KindActionExecuted, PayerPortalActor.ID, ActionSubmitAuthorization) {
		t.Fatal("expected payer portal service execution")
	}
	replayed, err := rt.RebuildState(ctx, requestID)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed.State.Value != StateSubmitted {
		t.Fatalf("expected replay state %s, got %s", StateSubmitted, replayed.State.Value)
	}
}

func TestAmbiguousAuthorizationRequiresClinicianApproval(t *testing.T) {
	ctx := context.Background()
	portal := NewInMemoryPayerPortal()
	rt, err := NewRuntime(portal)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pa-review-2002"
	patientID := "patient-rivera"
	payerID := "payer-west"
	procedure := "pt-extended"
	if err := rt.IngestEvent(ctx, NewAuthRequestReceivedEvent(requestID, patientID, payerID, procedure, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionRecordClinicalEvidence, requestID, StateReceived, PriorAuthAgentActor, evidenceParams(requestID, patientID, payerID, procedure, "evidence-review-2002"))
	mustExecute(t, ctx, rt, ActionCheckPolicyCriteria, requestID, StateReadyForCriteria, PriorAuthAgentActor, criteriaParams(false, 74, false))

	contract, err := Contract(rt, ActionSubmitReviewedAuthorization)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionSubmitReviewedAuthorization,
		EntityID:     requestID,
		EntityType:   EntityPriorAuthRequest,
		CurrentState: StateReviewRequired,
		Actor:        PayerPortalActor,
		ApprovalID:   "approval-pa-review-2002",
		Parameters:   reviewedSubmissionParams(requestID, patientID, payerID, procedure, "evidence-review-2002"),
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if submissions := portal.List(); len(submissions) != 0 {
		t.Fatalf("submission should not happen before approval: %#v", submissions)
	}

	requestCtx := actionCtx
	requestCtx.Actor = PriorAuthAgentActor
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, requestCtx, contract, "criteria are ambiguous and denial risk is high"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewAuthorizationApproval(ctx, rt, actionCtx.ApprovalID, ClinicianReviewerActor, true, "clinician reviewed chart and approved submission"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("approved submission: %v", err)
	}
	current, err := CurrentState(ctx, rt, requestID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateSubmitted {
		t.Fatalf("expected %s, got %s", StateSubmitted, current.Value)
	}
	if submissions := portal.List(); len(submissions) != 1 || !submissions[0].Reviewed {
		t.Fatalf("expected one reviewed submission, got %#v", submissions)
	}
}

func TestAgentCannotSelfSubmitByAddingPortalRole(t *testing.T) {
	ctx := context.Background()
	portal := NewInMemoryPayerPortal()
	rt, err := NewRuntime(portal)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pa-permission-3003"
	patientID := "patient-chen"
	payerID := "payer-south"
	procedure := "infusion-a"
	if err := rt.IngestEvent(ctx, NewAuthRequestReceivedEvent(requestID, patientID, payerID, procedure, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionRecordClinicalEvidence, requestID, StateReceived, PriorAuthAgentActor, evidenceParams(requestID, patientID, payerID, procedure, "evidence-permission-3003"))
	mustExecute(t, ctx, rt, ActionCheckPolicyCriteria, requestID, StateReadyForCriteria, PriorAuthAgentActor, criteriaParams(true, 20, false))
	mustExecute(t, ctx, rt, ActionPrepareAuthorization, requestID, StateCriteriaMet, PriorAuthAgentActor, submissionParams(requestID, patientID, payerID, procedure, "evidence-permission-3003"))

	contract, err := Contract(rt, ActionSubmitAuthorization)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	spoofedAgent := PriorAuthAgentActor
	spoofedAgent.Roles = append(spoofedAgent.Roles, RolePayerPortal)
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionSubmitAuthorization,
		EntityID:     requestID,
		EntityType:   EntityPriorAuthRequest,
		CurrentState: StatePrepared,
		Actor:        spoofedAgent,
		Parameters:   submissionParams(requestID, patientID, payerID, procedure, "evidence-permission-3003"),
	}, contract)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestMalformedCriteriaParameterIsRejectedBeforeExecutor(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryPayerPortal())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	requestID := "pa-invalid-3503"
	patientID := "patient-nguyen"
	payerID := "payer-central"
	procedure := "imaging-c"
	if err := rt.IngestEvent(ctx, NewAuthRequestReceivedEvent(requestID, patientID, payerID, procedure, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionRecordClinicalEvidence, requestID, StateReceived, PriorAuthAgentActor, evidenceParams(requestID, patientID, payerID, procedure, "evidence-invalid-3503"))

	contract, err := Contract(rt, ActionCheckPolicyCriteria)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionCheckPolicyCriteria,
		EntityID:     requestID,
		EntityType:   EntityPriorAuthRequest,
		CurrentState: StateReadyForCriteria,
		Actor:        PriorAuthAgentActor,
		Parameters: map[string]any{
			"criteria_met":      true,
			"denial_risk_score": 101,
			"missing_evidence":  false,
		},
	}, contract)
	if !errors.Is(err, action.ErrInvalidParameter) {
		t.Fatalf("expected invalid parameter, got %v", err)
	}
	current, err := CurrentState(ctx, rt, requestID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateReadyForCriteria {
		t.Fatalf("expected state to remain %s, got %s", StateReadyForCriteria, current.Value)
	}
}

func TestOnlyClinicianCanReviewAuthorizationApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryPayerPortal())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	contract, err := Contract(rt, ActionSubmitReviewedAuthorization)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionSubmitReviewedAuthorization,
		EntityID:     "pa-review-auth-4004",
		EntityType:   EntityPriorAuthRequest,
		CurrentState: StateReviewRequired,
		Actor:        PriorAuthAgentActor,
		ApprovalID:   "approval-pa-review-auth-4004",
		Parameters:   reviewedSubmissionParams("pa-review-auth-4004", "patient-patel", "payer-east", "surgery-b", "evidence-auth-4004"),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "requires clinician authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewAuthorizationApproval(ctx, rt, actionCtx.ApprovalID, PriorAuthAgentActor, true, "agent tried to approve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestPayerPortalUsesIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	portal := NewInMemoryPayerPortal()
	instruction := SubmissionInstruction{
		RequestID:        "pa-idempotent-5005",
		PatientID:        "patient-lee",
		PayerID:          "payer-north",
		ProcedureCode:    "MRI-LUMBAR",
		EvidencePacketID: "evidence-idempotent-5005",
		IdempotencyKey:   "pa-idempotent-5005:patient-lee:payer-north:MRI-LUMBAR",
	}
	first, err := portal.Submit(ctx, instruction)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	second, err := portal.Submit(ctx, instruction)
	if err != nil {
		t.Fatalf("second submit: %v", err)
	}
	if first.SubmissionID != second.SubmissionID {
		t.Fatalf("expected same submission id, got %s and %s", first.SubmissionID, second.SubmissionID)
	}
	if !second.Duplicate {
		t.Fatal("expected duplicate receipt on second submit")
	}
	if submissions := portal.List(); len(submissions) != 1 {
		t.Fatalf("expected one stored submission, got %d", len(submissions))
	}
}

func mustExecute(t *testing.T, ctx context.Context, rt *runtime.Runtime, actionName, requestID, currentState string, a actor.Actor, params map[string]any) {
	t.Helper()
	contract, err := Contract(rt, actionName)
	if err != nil {
		t.Fatalf("contract %s: %v", actionName, err)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   actionName,
		EntityID:     requestID,
		EntityType:   EntityPriorAuthRequest,
		CurrentState: currentState,
		Actor:        a,
		Parameters:   params,
	}, contract); err != nil {
		t.Fatalf("execute %s: %v", actionName, err)
	}
}

func evidenceParams(requestID, patientID, payerID, procedureCode, evidencePacketID string) map[string]any {
	return map[string]any{
		"request_id":         requestID,
		"patient_id":         patientID,
		"payer_id":           payerID,
		"procedure_code":     procedureCode,
		"evidence_packet_id": evidencePacketID,
	}
}

func criteriaParams(criteriaMet bool, denialRiskScore int64, missingEvidence bool) map[string]any {
	return map[string]any{
		"criteria_met":      criteriaMet,
		"denial_risk_score": denialRiskScore,
		"missing_evidence":  missingEvidence,
	}
}

func submissionParams(requestID, patientID, payerID, procedureCode, evidencePacketID string) map[string]any {
	return map[string]any{
		"request_id":         requestID,
		"patient_id":         patientID,
		"payer_id":           payerID,
		"procedure_code":     procedureCode,
		"evidence_packet_id": evidencePacketID,
		"idempotency_key":    requestID + ":" + patientID + ":" + payerID + ":" + strings.ToUpper(procedureCode),
	}
}

func reviewedSubmissionParams(requestID, patientID, payerID, procedureCode, evidencePacketID string) map[string]any {
	params := submissionParams(requestID, patientID, payerID, procedureCode, evidencePacketID)
	params["clinician_note"] = "reviewed and appropriate for payer submission"
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
