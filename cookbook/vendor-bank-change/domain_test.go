package vendorbank

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

func TestKnownAccountChangeAppliesWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	master := NewInMemoryVendorMaster()
	rt, err := NewRuntime(master)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	changeID := "vbc-low-1001"
	vendorID := "vendor-northwind"
	vendorName := "Northwind Parts"
	if err := rt.IngestEvent(ctx, NewChangeRequestedEvent(changeID, vendorID, vendorName, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionAttachEvidence, changeID, StateReceived, VendorAgentActor, evidenceParams(changeID, vendorID, vendorName, "acct-known-9912", "US"))
	mustExecute(t, ctx, rt, ActionVerifyVendor, changeID, StateEvidenceAttached, VendorAgentActor, verificationParams(vendorID, true, true, true))
	mustExecute(t, ctx, rt, ActionAssessBankChange, changeID, StateVendorVerified, VendorAgentActor, assessmentParams(18, true, true, 42000, false))
	mustExecute(t, ctx, rt, ActionPrepareKnownAccount, changeID, StateLowRiskReady, VendorAgentActor, instructionParams(changeID, vendorID, vendorName, "acct-known-9912", "US"))
	mustExecute(t, ctx, rt, ActionApplyKnownAccount, changeID, StateChangePrepared, VendorMasterActor, instructionParams(changeID, vendorID, vendorName, "acct-known-9912", "US"))

	current, err := CurrentState(ctx, rt, changeID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateUpdated {
		t.Fatalf("expected %s, got %s", StateUpdated, current.Value)
	}
	if updates := master.List(); len(updates) != 1 {
		t.Fatalf("expected one vendor update, got %d", len(updates))
	}
	timeline, err := rt.Timeline(ctx, changeID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("known-account change should not require approval")
	}
	if !auditHasActorAction(timeline, audit.KindActionExecuted, VendorMasterActor.ID, ActionApplyKnownAccount) {
		t.Fatal("expected vendor-master service execution")
	}
	replayed, err := rt.RebuildState(ctx, changeID)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed.State.Value != StateUpdated {
		t.Fatalf("expected replay state %s, got %s", StateUpdated, replayed.State.Value)
	}
}

func TestNewBankAccountRequiresFinanceApproval(t *testing.T) {
	ctx := context.Background()
	master := NewInMemoryVendorMaster()
	rt, err := NewRuntime(master)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	changeID := "vbc-review-2002"
	vendorID := "vendor-contoso"
	vendorName := "Contoso Manufacturing"
	if err := rt.IngestEvent(ctx, NewChangeRequestedEvent(changeID, vendorID, vendorName, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionAttachEvidence, changeID, StateReceived, VendorAgentActor, evidenceParams(changeID, vendorID, vendorName, "acct-new-8842", "US"))
	mustExecute(t, ctx, rt, ActionVerifyVendor, changeID, StateEvidenceAttached, VendorAgentActor, verificationParams(vendorID, true, true, true))
	mustExecute(t, ctx, rt, ActionAssessBankChange, changeID, StateVendorVerified, VendorAgentActor, assessmentParams(84, false, true, 980000, true))

	contract, err := Contract(rt, ActionApplyApprovedChange)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionApplyApprovedChange,
		EntityID:     changeID,
		EntityType:   EntityVendorBankChange,
		CurrentState: StateReviewRequired,
		Actor:        VendorMasterActor,
		ApprovalID:   "approval-vbc-review-2002",
		Parameters:   instructionParams(changeID, vendorID, vendorName, "acct-new-8842", "US"),
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if updates := master.List(); len(updates) != 0 {
		t.Fatalf("bank change should not apply before approval: %#v", updates)
	}

	requestCtx := actionCtx
	requestCtx.Actor = VendorAgentActor
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, requestCtx, contract, "new bank account and fraud signal require finance approval"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewBankChangeApproval(ctx, rt, actionCtx.ApprovalID, FinanceControllerActor, true, "finance approved after callback and evidence review"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("approved apply: %v", err)
	}

	current, err := CurrentState(ctx, rt, changeID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateUpdated {
		t.Fatalf("expected %s, got %s", StateUpdated, current.Value)
	}
	if updates := master.List(); len(updates) != 1 {
		t.Fatalf("expected one vendor update, got %d", len(updates))
	}
}

func TestAgentCannotSelfApplyBankChangeByAddingServiceRole(t *testing.T) {
	ctx := context.Background()
	master := NewInMemoryVendorMaster()
	rt, err := NewRuntime(master)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	changeID := "vbc-permission-3003"
	vendorID := "vendor-fabrikam"
	vendorName := "Fabrikam Components"
	if err := rt.IngestEvent(ctx, NewChangeRequestedEvent(changeID, vendorID, vendorName, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionAttachEvidence, changeID, StateReceived, VendorAgentActor, evidenceParams(changeID, vendorID, vendorName, "acct-known-5511", "CA"))
	mustExecute(t, ctx, rt, ActionVerifyVendor, changeID, StateEvidenceAttached, VendorAgentActor, verificationParams(vendorID, true, true, true))
	mustExecute(t, ctx, rt, ActionAssessBankChange, changeID, StateVendorVerified, VendorAgentActor, assessmentParams(12, true, true, 15000, false))
	mustExecute(t, ctx, rt, ActionPrepareKnownAccount, changeID, StateLowRiskReady, VendorAgentActor, instructionParams(changeID, vendorID, vendorName, "acct-known-5511", "CA"))

	contract, err := Contract(rt, ActionApplyKnownAccount)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	spoofedAgent := VendorAgentActor
	spoofedAgent.Roles = append(spoofedAgent.Roles, RoleVendorMaster)
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionApplyKnownAccount,
		EntityID:     changeID,
		EntityType:   EntityVendorBankChange,
		CurrentState: StateChangePrepared,
		Actor:        spoofedAgent,
		Parameters:   instructionParams(changeID, vendorID, vendorName, "acct-known-5511", "CA"),
	}, contract)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if updates := master.List(); len(updates) != 0 {
		t.Fatalf("expected no update, got %#v", updates)
	}
}

func TestInvalidCountryIsRejectedBeforeExecutor(t *testing.T) {
	ctx := context.Background()
	master := NewInMemoryVendorMaster()
	rt, err := NewRuntime(master)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	changeID := "vbc-invalid-4004"
	vendorID := "vendor-tailspin"
	vendorName := "Tailspin Supply"
	if err := rt.IngestEvent(ctx, NewChangeRequestedEvent(changeID, vendorID, vendorName, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionAttachEvidence, changeID, StateReceived, VendorAgentActor, evidenceParams(changeID, vendorID, vendorName, "acct-known-4411", "US"))
	mustExecute(t, ctx, rt, ActionVerifyVendor, changeID, StateEvidenceAttached, VendorAgentActor, verificationParams(vendorID, true, true, true))
	mustExecute(t, ctx, rt, ActionAssessBankChange, changeID, StateVendorVerified, VendorAgentActor, assessmentParams(10, true, true, 5000, false))
	mustExecute(t, ctx, rt, ActionPrepareKnownAccount, changeID, StateLowRiskReady, VendorAgentActor, instructionParams(changeID, vendorID, vendorName, "acct-known-4411", "US"))

	contract, err := Contract(rt, ActionApplyKnownAccount)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	params := instructionParams(changeID, vendorID, vendorName, "acct-known-4411", "US")
	params["account_country"] = "ZZ"
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionApplyKnownAccount,
		EntityID:     changeID,
		EntityType:   EntityVendorBankChange,
		CurrentState: StateChangePrepared,
		Actor:        VendorMasterActor,
		Parameters:   params,
	}, contract)
	if !errors.Is(err, action.ErrInvalidParameter) {
		t.Fatalf("expected invalid parameter, got %v", err)
	}
	if updates := master.List(); len(updates) != 0 {
		t.Fatalf("expected no update, got %#v", updates)
	}
}

func TestOnlyFinanceControllerCanReviewBankChangeApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryVendorMaster())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	contract, err := Contract(rt, ActionApplyApprovedChange)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionApplyApprovedChange,
		EntityID:     "vbc-review-auth-5005",
		EntityType:   EntityVendorBankChange,
		CurrentState: StateReviewRequired,
		Actor:        FinanceControllerActor,
		ApprovalID:   "approval-vbc-review-auth-5005",
		Parameters:   instructionParams("vbc-review-auth-5005", "vendor-patel", "Patel Supply", "acct-new-5005", "US"),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "requires finance controller authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewBankChangeApproval(ctx, rt, actionCtx.ApprovalID, VendorAgentActor, true, "agent tried to approve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if _, err := ReviewBankChangeApproval(ctx, rt, actionCtx.ApprovalID, FinanceControllerActor, true, "requester tried to self approve"); !errors.Is(err, approval.ErrSelfReview) {
		t.Fatalf("expected self-review rejection, got %v", err)
	}
}

func TestVendorMasterGatewayUsesIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	master := NewInMemoryVendorMaster()
	instruction := BankChangeInstruction{
		ChangeID:           "vbc-idempotent-6006",
		VendorID:           "vendor-northwind",
		VendorName:         "Northwind Parts",
		AccountFingerprint: "acct-known-9912",
		AccountCountry:     "US",
		EvidencePacketID:   "evidence-idempotent-6006",
		IdempotencyKey:     "vbc-idempotent-6006:vendor-northwind:acct-known-9912",
	}
	first, err := master.ApplyBankChange(ctx, instruction)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := master.ApplyBankChange(ctx, instruction)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if first.UpdateID != second.UpdateID {
		t.Fatalf("expected same update id, got %s and %s", first.UpdateID, second.UpdateID)
	}
	if !second.Duplicate {
		t.Fatal("expected duplicate receipt on second apply")
	}
	if updates := master.List(); len(updates) != 1 {
		t.Fatalf("expected one stored update, got %d", len(updates))
	}
}

func mustExecute(t *testing.T, ctx context.Context, rt *runtime.Runtime, actionName, changeID, currentState string, a actor.Actor, params map[string]any) {
	t.Helper()
	contract, err := Contract(rt, actionName)
	if err != nil {
		t.Fatalf("contract %s: %v", actionName, err)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   actionName,
		EntityID:     changeID,
		EntityType:   EntityVendorBankChange,
		CurrentState: currentState,
		Actor:        a,
		Parameters:   params,
	}, contract); err != nil {
		t.Fatalf("execute %s: %v", actionName, err)
	}
}

func evidenceParams(changeID, vendorID, vendorName, accountFingerprint, country string) map[string]any {
	return map[string]any{
		"change_id":           changeID,
		"vendor_id":           vendorID,
		"vendor_name":         vendorName,
		"account_fingerprint": accountFingerprint,
		"account_country":     country,
		"evidence_packet_id":  "evidence-" + changeID,
		"requester_email":     "ap-team@example.com",
	}
}

func verificationParams(vendorID string, existingVendor, taxIDMatch, callbackVerified bool) map[string]any {
	return map[string]any{
		"vendor_id":         vendorID,
		"existing_vendor":   existingVendor,
		"tax_id_match":      taxIDMatch,
		"callback_verified": callbackVerified,
	}
}

func assessmentParams(score int64, knownAccount, callbackVerified bool, exposure int64, fraudSignal bool) map[string]any {
	return map[string]any{
		"risk_score_percent":          score,
		"known_account":               knownAccount,
		"callback_verified":           callbackVerified,
		"open_invoice_exposure_cents": exposure,
		"fraud_signal":                fraudSignal,
	}
}

func instructionParams(changeID, vendorID, vendorName, accountFingerprint, country string) map[string]any {
	return map[string]any{
		"change_id":           changeID,
		"vendor_id":           vendorID,
		"vendor_name":         vendorName,
		"account_fingerprint": accountFingerprint,
		"account_country":     country,
		"evidence_packet_id":  "evidence-" + changeID,
		"idempotency_key":     changeID + ":" + vendorID + ":" + accountFingerprint,
	}
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
