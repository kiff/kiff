package claimstriage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func TestLowValueClaimCanIssuePayoutWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	gateway := NewLedgerPayoutGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	claimID := "claim-low-1001"
	claimantID := "claimant-lee"
	policyID := "policy-home-77"
	if err := rt.IngestEvent(ctx, NewClaimReceivedEvent(claimID, claimantID, policyID, "water_damage", time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionVerifyCoverage, claimID, StateReceived, ClaimsAgentActor, coverageParams(claimID, claimantID, policyID, "water_damage"))
	mustExecute(t, ctx, rt, ActionAssessRisk, claimID, StateCoverageVerified, ClaimsAgentActor, riskParams(claimID, 0.18, 84000, false))
	mustExecute(t, ctx, rt, ActionPrepareLowValuePayout, claimID, StateLowRiskReady, ClaimsAgentActor, payoutParams(claimID, claimantID, policyID, 84000))
	mustExecute(t, ctx, rt, ActionIssueLowValuePayout, claimID, StatePayoutPrepared, ClaimsServiceActor, payoutParams(claimID, claimantID, policyID, 84000))

	current, err := CurrentState(ctx, rt, claimID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StatePaid {
		t.Fatalf("expected %s, got %s", StatePaid, current.Value)
	}
	if payouts := gateway.List(); len(payouts) != 1 {
		t.Fatalf("expected one payout, got %d", len(payouts))
	}
	timeline, err := rt.Timeline(ctx, claimID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("low-value payout should not require approval")
	}
	if !auditHasActorAction(timeline, audit.KindActionExecuted, ClaimsServiceActor.ID, ActionIssueLowValuePayout) {
		t.Fatal("expected payout execution by claims service")
	}
}

func TestHighRiskPayoutRequiresAdjusterApproval(t *testing.T) {
	ctx := context.Background()
	gateway := NewLedgerPayoutGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	claimID := "claim-review-2002"
	claimantID := "claimant-rivera"
	policyID := "policy-auto-88"
	if err := rt.IngestEvent(ctx, NewClaimReceivedEvent(claimID, claimantID, policyID, "collision", time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionVerifyCoverage, claimID, StateReceived, ClaimsAgentActor, coverageParams(claimID, claimantID, policyID, "collision"))
	mustExecute(t, ctx, rt, ActionAssessRisk, claimID, StateCoverageVerified, ClaimsAgentActor, riskParams(claimID, 0.82, 420000, true))

	release, err := Contract(rt, ActionIssueApprovedPayout)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionIssueApprovedPayout,
		EntityID:     claimID,
		EntityType:   EntityClaim,
		CurrentState: StateReviewRequired,
		Actor:        ClaimsServiceActor,
		ApprovalID:   "approval-claim-review-2002",
		Parameters:   payoutParams(claimID, claimantID, policyID, 420000),
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, release); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if payouts := gateway.List(); len(payouts) != 0 {
		t.Fatalf("payout should not be issued before approval: %#v", payouts)
	}

	requestCtx := actionCtx
	requestCtx.Actor = ClaimsAgentActor
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, requestCtx, release, "large loss and fraud signals require adjuster approval"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewPayoutApproval(ctx, rt, actionCtx.ApprovalID, AdjusterActor, true, "coverage and settlement amount approved"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, release); err != nil {
		t.Fatalf("approved release: %v", err)
	}

	current, err := CurrentState(ctx, rt, claimID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StatePaid {
		t.Fatalf("expected %s, got %s", StatePaid, current.Value)
	}
	if payouts := gateway.List(); len(payouts) != 1 {
		t.Fatalf("expected one payout, got %d", len(payouts))
	}
	timeline, err := rt.Timeline(ctx, claimID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if !auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("expected approval-required audit record")
	}
	if !auditHasActorAction(timeline, audit.KindApprovalGranted, AdjusterActor.ID, ActionIssueApprovedPayout) {
		t.Fatal("expected adjuster approval audit record")
	}
}

func TestAgentCannotSelfIssuePayoutByAddingServiceRole(t *testing.T) {
	ctx := context.Background()
	gateway := NewLedgerPayoutGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	claimID := "claim-permission-3003"
	claimantID := "claimant-chen"
	policyID := "policy-home-99"
	if err := rt.IngestEvent(ctx, NewClaimReceivedEvent(claimID, claimantID, policyID, "theft", time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionVerifyCoverage, claimID, StateReceived, ClaimsAgentActor, coverageParams(claimID, claimantID, policyID, "theft"))
	mustExecute(t, ctx, rt, ActionAssessRisk, claimID, StateCoverageVerified, ClaimsAgentActor, riskParams(claimID, 0.20, 60000, false))
	mustExecute(t, ctx, rt, ActionPrepareLowValuePayout, claimID, StateLowRiskReady, ClaimsAgentActor, payoutParams(claimID, claimantID, policyID, 60000))

	contract, err := Contract(rt, ActionIssueLowValuePayout)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	spoofedAgent := ClaimsAgentActor
	spoofedAgent.Roles = append(spoofedAgent.Roles, RoleClaimsService)
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionIssueLowValuePayout,
		EntityID:     claimID,
		EntityType:   EntityClaim,
		CurrentState: StatePayoutPrepared,
		Actor:        spoofedAgent,
		Parameters:   payoutParams(claimID, claimantID, policyID, 60000),
	}, contract)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied for spoofed agent, got %v", err)
	}
	if payouts := gateway.List(); len(payouts) != 0 {
		t.Fatalf("expected no payout, got %#v", payouts)
	}
}

func TestLowValueExecutorRejectsHighAmountEvenInPreparedState(t *testing.T) {
	ctx := context.Background()
	gateway := NewLedgerPayoutGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	claimID := "claim-limit-4004"
	claimantID := "claimant-owens"
	policyID := "policy-home-55"
	if err := rt.IngestEvent(ctx, NewClaimReceivedEvent(claimID, claimantID, policyID, "wind_damage", time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionVerifyCoverage, claimID, StateReceived, ClaimsAgentActor, coverageParams(claimID, claimantID, policyID, "wind_damage"))
	mustExecute(t, ctx, rt, ActionAssessRisk, claimID, StateCoverageVerified, ClaimsAgentActor, riskParams(claimID, 0.10, 90000, false))
	mustExecute(t, ctx, rt, ActionPrepareLowValuePayout, claimID, StateLowRiskReady, ClaimsAgentActor, payoutParams(claimID, claimantID, policyID, 90000))

	contract, err := Contract(rt, ActionIssueLowValuePayout)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionIssueLowValuePayout,
		EntityID:     claimID,
		EntityType:   EntityClaim,
		CurrentState: StatePayoutPrepared,
		Actor:        ClaimsServiceActor,
		Parameters:   payoutParams(claimID, claimantID, policyID, 180000),
	}, contract)
	if err == nil {
		t.Fatal("expected high low-value payout to fail")
	}
	if payouts := gateway.List(); len(payouts) != 0 {
		t.Fatalf("expected no payout, got %#v", payouts)
	}
	current, err := CurrentState(ctx, rt, claimID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StatePayoutPrepared {
		t.Fatalf("expected state to remain %s, got %s", StatePayoutPrepared, current.Value)
	}
}

func TestOnlyAdjusterCanReviewPayoutApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewLedgerPayoutGateway())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	claimID := "claim-review-auth-5005"
	release, err := Contract(rt, ActionIssueApprovedPayout)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionIssueApprovedPayout,
		EntityID:     claimID,
		EntityType:   EntityClaim,
		CurrentState: StateReviewRequired,
		Actor:        ClaimsAgentActor,
		ApprovalID:   "approval-review-auth-5005",
		Parameters:   payoutParams(claimID, "claimant-patel", "policy-home-22", 240000),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, release, "requires adjuster authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewPayoutApproval(ctx, rt, actionCtx.ApprovalID, ClaimsAgentActor, true, "agent tried to approve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestPayoutGatewayUsesIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	gateway := NewLedgerPayoutGateway()
	instruction := PayoutInstruction{
		ClaimID:        "claim-idempotent-6006",
		ClaimantID:     "claimant-lee",
		PolicyID:       "policy-home-77",
		AmountCents:    50000,
		Currency:       "USD",
		IdempotencyKey: "claim-idempotent-6006:claimant-lee:policy-home-77",
	}
	first, err := gateway.Issue(ctx, instruction)
	if err != nil {
		t.Fatalf("first issue: %v", err)
	}
	second, err := gateway.Issue(ctx, instruction)
	if err != nil {
		t.Fatalf("second issue: %v", err)
	}
	if first.PayoutID != second.PayoutID {
		t.Fatalf("expected same payout id, got %s and %s", first.PayoutID, second.PayoutID)
	}
	if !second.Duplicate {
		t.Fatal("expected duplicate receipt on second issue")
	}
	if payouts := gateway.List(); len(payouts) != 1 {
		t.Fatalf("expected one stored payout, got %d", len(payouts))
	}
}

func mustExecute(t *testing.T, ctx context.Context, rt *runtime.Runtime, actionName, claimID, currentState string, a actor.Actor, params map[string]any) {
	t.Helper()
	contract, err := Contract(rt, actionName)
	if err != nil {
		t.Fatalf("contract %s: %v", actionName, err)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   actionName,
		EntityID:     claimID,
		EntityType:   EntityClaim,
		CurrentState: currentState,
		Actor:        a,
		Parameters:   params,
	}, contract); err != nil {
		t.Fatalf("execute %s: %v", actionName, err)
	}
}

func coverageParams(claimID, claimantID, policyID, lossType string) map[string]any {
	return map[string]any{
		"claim_id":           claimID,
		"claimant_id":        claimantID,
		"policy_id":          policyID,
		"loss_type":          lossType,
		"coverage_confirmed": true,
	}
}

func riskParams(claimID string, score float64, amount int64, fraudSignals bool) map[string]any {
	return map[string]any{
		"claim_id":            claimID,
		"risk_score":          score,
		"payout_amount_cents": amount,
		"currency":            "USD",
		"fraud_signals":       fraudSignals,
	}
}

func payoutParams(claimID, claimantID, policyID string, amount int64) map[string]any {
	return map[string]any{
		"claim_id":            claimID,
		"claimant_id":         claimantID,
		"policy_id":           policyID,
		"payout_amount_cents": amount,
		"currency":            "USD",
		"idempotency_key":     claimID + ":" + claimantID + ":" + policyID,
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
		if record.Kind != kind || record.ActorID != actorID {
			continue
		}
		if record.Data == nil {
			continue
		}
		if got, ok := record.Data["action"].(string); ok && got == actionName {
			return true
		}
	}
	return false
}
