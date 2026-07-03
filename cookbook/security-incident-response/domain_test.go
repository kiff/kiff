package securityincident

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

func TestLowRiskSessionResetContainsIncidentWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryIdentityControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "sec-low-1001"
	userID := "user-nguyen"
	userEmail := "nguyen@example.com"
	accountID := "acct-corp"
	seedIncident(t, ctx, rt, incidentID, userID, userEmail, accountID)

	mustExecute(t, ctx, rt, ActionAttachSignals, incidentID, StateReceived, SecurityAgentActor, signalParams(incidentID, userID, userEmail, accountID, 18, 6, "standard", "single_user", 0, false, false, false))
	mustExecute(t, ctx, rt, ActionAssessIdentityRisk, incidentID, StateSignalsAttached, SecurityAgentActor, assessmentParams(18, 6, "standard", "single_user", 0, false, false, false))
	mustExecute(t, ctx, rt, ActionPrepareSessionReset, incidentID, StateLowRiskReady, SecurityAgentActor, resetParams(incidentID, userID, userEmail, accountID))
	mustExecute(t, ctx, rt, ActionExecuteSessionReset, incidentID, StateResetPrepared, IdentityServiceActor, resetParams(incidentID, userID, userEmail, accountID))

	current, err := CurrentState(ctx, rt, incidentID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateContained {
		t.Fatalf("expected %s, got %s", StateContained, current.Value)
	}
	if operations := control.List(); len(operations) != 1 || operations[0].Operation != "reset_sessions" {
		t.Fatalf("expected one session reset operation, got %#v", operations)
	}
	timeline, err := rt.Timeline(ctx, incidentID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("low-risk session reset should not require approval")
	}
	if !auditHasActorAction(timeline, audit.KindActionExecuted, IdentityServiceActor.ID, ActionExecuteSessionReset) {
		t.Fatal("expected identity service to execute the reset")
	}
	lifecycle, err := rt.EntityLifecycle(ctx, incidentID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lifecycle.CurrentState != StateContained || !lifecycle.Executed() || lifecycle.AwaitingApproval() {
		t.Fatalf("unexpected lifecycle view: state=%s disposition=%s awaiting=%v", lifecycle.CurrentState, lifecycle.Disposition(), lifecycle.AwaitingApproval())
	}
	replayed, err := rt.RebuildState(ctx, incidentID)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed.State.Value != StateContained {
		t.Fatalf("expected replay state %s, got %s", StateContained, replayed.State.Value)
	}
}

func TestHighRiskAccessRevocationRequiresSecurityLeadApprovalAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryIdentityControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "sec-review-2002"
	userID := "user-admin"
	userEmail := "admin@example.com"
	accountID := "acct-corp"
	seedIncident(t, ctx, rt, incidentID, userID, userEmail, accountID)

	mustExecute(t, ctx, rt, ActionAttachSignals, incidentID, StateReceived, SecurityAgentActor, signalParams(incidentID, userID, userEmail, accountID, 92, 58, "privileged", "broad", 4, true, true, true))
	mustExecute(t, ctx, rt, ActionAssessIdentityRisk, incidentID, StateSignalsAttached, SecurityAgentActor, assessmentParams(92, 58, "privileged", "broad", 4, true, true, true))

	contract, err := Contract(rt, ActionRevokeUserAccess)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	params := revocationParams(incidentID, userID, userEmail, accountID, "all_access", "privileged", "broad", 4, true)
	actionCtx := action.ActionContext{
		ActionName:     ActionRevokeUserAccess,
		EntityID:       incidentID,
		EntityType:     EntitySecurityIncident,
		CurrentState:   StateReviewRequired,
		Actor:          IdentityServiceActor,
		ApprovalID:     "approval-sec-review-2002",
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if operations := control.List(); len(operations) != 0 {
		t.Fatalf("revocation should not run before approval: %#v", operations)
	}

	requestCtx := actionCtx
	requestCtx.Actor = SecurityAgentActor
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, requestCtx, contract, "privileged broad revocation requires security lead approval"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewContainmentApproval(ctx, rt, actionCtx.ApprovalID, SecurityLeadActor, true, "approve all-access revocation and preserve evidence"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	first, err := rt.ExecuteAction(ctx, actionCtx, contract)
	if err != nil {
		t.Fatalf("approved revocation: %v", err)
	}
	second, err := rt.ExecuteAction(ctx, actionCtx, contract)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if first.EntityID != second.EntityID || second.Status != action.ExecutionSucceeded {
		t.Fatalf("expected idempotent retry to return prior result, got first=%+v second=%+v", first, second)
	}

	current, err := CurrentState(ctx, rt, incidentID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateContained {
		t.Fatalf("expected %s, got %s", StateContained, current.Value)
	}
	if operations := control.List(); len(operations) != 1 || operations[0].Operation != "revoke_access" {
		t.Fatalf("expected one access revocation operation, got %#v", operations)
	}
	timeline, err := rt.Timeline(ctx, incidentID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if !auditHasKind(timeline, audit.KindActionDeduplicated) {
		t.Fatal("expected idempotent retry to be audited as deduplicated")
	}
	lifecycle, err := rt.EntityLifecycle(ctx, incidentID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lifecycle.CurrentState != StateContained || lifecycle.Disposition() != audit.KindActionDeduplicated {
		t.Fatalf("expected contained deduplicated lifecycle, got state=%s disposition=%s", lifecycle.CurrentState, lifecycle.Disposition())
	}
	if !lifecycle.Has(audit.KindApprovalGranted) || !lifecycle.Has(audit.KindActionExecuted) || !lifecycle.Has(audit.KindActionDeduplicated) {
		t.Fatalf("expected approval grant, execution, and deduplicated retry in lifecycle stages: %+v", lifecycle.Stages)
	}
	if len(lifecycle.Approvals) != 1 || lifecycle.Approvals[0].Status != approval.StatusGranted {
		t.Fatalf("expected granted approval attached to lifecycle, got %+v", lifecycle.Approvals)
	}
}

func TestAgentProposalIsVisibleInLifecycle(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryIdentityControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "sec-proposal-6006"
	userID := "user-nguyen"
	userEmail := "nguyen@example.com"
	accountID := "acct-corp"
	seedIncident(t, ctx, rt, incidentID, userID, userEmail, accountID)

	proposal := AgentProposal{
		ActionName:       ActionAttachSignals,
		Parameters:       signalParams(incidentID, userID, userEmail, accountID, 24, 7, "standard", "single_user", 0, false, false, false),
		ReasoningSummary: "attach identity telemetry before deciding containment",
		Confidence:       0.89,
	}
	if err := RecordAgentProposal(ctx, rt, incidentID, "Attach the available identity signals.", proposal); err != nil {
		t.Fatalf("record proposal: %v", err)
	}
	if _, err := ApplyAgentProposal(ctx, rt, incidentID, StateReceived, proposal); err != nil {
		t.Fatalf("apply proposal: %v", err)
	}

	lifecycle, err := rt.EntityLifecycle(ctx, incidentID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lifecycle.CurrentState != StateSignalsAttached {
		t.Fatalf("expected current state %s, got %s", StateSignalsAttached, lifecycle.CurrentState)
	}
	if !lifecycle.Has(audit.KindDecisionProposed) || !lifecycle.Executed() {
		t.Fatalf("expected proposed and executed lifecycle stages, got %+v", lifecycle.Stages)
	}
	if len(lifecycle.Decisions) != 1 || lifecycle.Decisions[0].ProposedAction != ActionAttachSignals {
		t.Fatalf("expected proposal decision attached, got %+v", lifecycle.Decisions)
	}
}

func TestAgentCannotSelfRevokeAccessByAddingServiceRole(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryIdentityControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "sec-permission-3003"
	userID := "user-admin"
	userEmail := "admin@example.com"
	accountID := "acct-corp"
	seedHighRiskReview(t, ctx, rt, incidentID, userID, userEmail, accountID)

	contract, err := Contract(rt, ActionRevokeUserAccess)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	spoofedAgent := SecurityAgentActor
	spoofedAgent.Roles = append(spoofedAgent.Roles, RoleIdentityService)
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:     ActionRevokeUserAccess,
		EntityID:       incidentID,
		EntityType:     EntitySecurityIncident,
		CurrentState:   StateReviewRequired,
		Actor:          spoofedAgent,
		Parameters:     revocationParams(incidentID, userID, userEmail, accountID, "all_access", "privileged", "broad", 2, true),
		IdempotencyKey: "sec-permission-3003:user-admin:all_access",
	}, contract)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if operations := control.List(); len(operations) != 0 {
		t.Fatalf("expected no identity operation, got %#v", operations)
	}
}

func TestInvalidRevocationScopeIsRejectedBeforeExecutor(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryIdentityControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "sec-invalid-4004"
	userID := "user-admin"
	userEmail := "admin@example.com"
	accountID := "acct-corp"
	seedHighRiskReview(t, ctx, rt, incidentID, userID, userEmail, accountID)

	contract, err := Contract(rt, ActionRevokeUserAccess)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	params := revocationParams(incidentID, userID, userEmail, accountID, "all_access", "privileged", "broad", 2, true)
	params["revocation_scope"] = "domain_admin"
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:     ActionRevokeUserAccess,
		EntityID:       incidentID,
		EntityType:     EntitySecurityIncident,
		CurrentState:   StateReviewRequired,
		Actor:          IdentityServiceActor,
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}, contract)
	if !errors.Is(err, action.ErrInvalidParameter) {
		t.Fatalf("expected invalid parameter, got %v", err)
	}
	if operations := control.List(); len(operations) != 0 {
		t.Fatalf("expected no identity operation, got %#v", operations)
	}
}

func TestOnlySecurityLeadCanReviewContainmentApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryIdentityControl())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	contract, err := Contract(rt, ActionRevokeUserAccess)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionRevokeUserAccess,
		EntityID:     "sec-review-auth-5005",
		EntityType:   EntitySecurityIncident,
		CurrentState: StateReviewRequired,
		Actor:        SecurityAgentActor,
		ApprovalID:   "approval-sec-review-auth-5005",
		Parameters:   revocationParams("sec-review-auth-5005", "user-admin", "admin@example.com", "acct-corp", "all_access", "privileged", "broad", 3, true),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "requires security lead authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewContainmentApproval(ctx, rt, actionCtx.ApprovalID, SecurityAgentActor, true, "agent tried to approve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}

	selfCtx := actionCtx
	selfCtx.Actor = SecurityLeadActor
	selfCtx.ApprovalID = "approval-sec-self-review-5006"
	if _, err := rt.RequestApproval(ctx, selfCtx.ApprovalID, selfCtx, contract, "same actor has review authority but requested approval"); err != nil {
		t.Fatalf("request self approval: %v", err)
	}
	if _, err := ReviewContainmentApproval(ctx, rt, selfCtx.ApprovalID, SecurityLeadActor, true, "requester tried to self approve"); !errors.Is(err, approval.ErrSelfReview) {
		t.Fatalf("expected self-review rejection, got %v", err)
	}
}

func seedIncident(t *testing.T, ctx context.Context, rt *runtime.Runtime, incidentID, userID, userEmail, accountID string) {
	t.Helper()
	if err := rt.IngestEvent(ctx, NewAlertReceivedEvent(incidentID, userID, userEmail, accountID, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

func seedHighRiskReview(t *testing.T, ctx context.Context, rt *runtime.Runtime, incidentID, userID, userEmail, accountID string) {
	t.Helper()
	seedIncident(t, ctx, rt, incidentID, userID, userEmail, accountID)
	mustExecute(t, ctx, rt, ActionAttachSignals, incidentID, StateReceived, SecurityAgentActor, signalParams(incidentID, userID, userEmail, accountID, 88, 44, "privileged", "broad", 2, true, true, true))
	mustExecute(t, ctx, rt, ActionAssessIdentityRisk, incidentID, StateSignalsAttached, SecurityAgentActor, assessmentParams(88, 44, "privileged", "broad", 2, true, true, true))
}

func mustExecute(t *testing.T, ctx context.Context, rt *runtime.Runtime, actionName, incidentID, currentState string, a actor.Actor, params map[string]any) {
	t.Helper()
	contract, err := Contract(rt, actionName)
	if err != nil {
		t.Fatalf("contract %s: %v", actionName, err)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:     actionName,
		EntityID:       incidentID,
		EntityType:     EntitySecurityIncident,
		CurrentState:   currentState,
		Actor:          a,
		Parameters:     params,
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}, contract); err != nil {
		t.Fatalf("execute %s: %v", actionName, err)
	}
}

func signalParams(incidentID, userID, userEmail, accountID string, score, failed int64, tier, blast string, groups int64, impossibleTravel, malware, exfil bool) map[string]any {
	params := assessmentParams(score, failed, tier, blast, groups, impossibleTravel, malware, exfil)
	params["incident_id"] = incidentID
	params["user_id"] = userID
	params["user_email"] = userEmail
	params["account_id"] = accountID
	return params
}

func assessmentParams(score, failed int64, tier, blast string, groups int64, impossibleTravel, malware, exfil bool) map[string]any {
	return map[string]any{
		"risk_score_percent":       score,
		"failed_login_count":       failed,
		"user_tier":                tier,
		"blast_radius":             blast,
		"privileged_group_count":   groups,
		"impossible_travel":        impossibleTravel,
		"malware_signal":           malware,
		"data_exfiltration_signal": exfil,
	}
}

func resetParams(incidentID, userID, userEmail, accountID string) map[string]any {
	return map[string]any{
		"incident_id":     incidentID,
		"user_id":         userID,
		"user_email":      userEmail,
		"account_id":      accountID,
		"reason":          "low-risk credential/session containment",
		"idempotency_key": incidentID + ":" + userID + ":reset_sessions",
	}
}

func revocationParams(incidentID, userID, userEmail, accountID, scope, tier, blast string, groups int64, exfil bool) map[string]any {
	return map[string]any{
		"incident_id":              incidentID,
		"user_id":                  userID,
		"user_email":               userEmail,
		"account_id":               accountID,
		"reason":                   "suspected account compromise",
		"revocation_scope":         scope,
		"user_tier":                tier,
		"blast_radius":             blast,
		"privileged_group_count":   groups,
		"data_exfiltration_signal": exfil,
		"idempotency_key":          incidentID + ":" + userID + ":" + scope,
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
