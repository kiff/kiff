package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	claims "github.com/kiff/kiff/cookbook/insurance-claims-triage"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func main() {
	ctx := context.Background()
	gateway := claims.NewLedgerPayoutGateway()
	rt, err := claims.NewRuntime(gateway)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create runtime: %v\n", err)
		os.Exit(1)
	}

	claimID := "claim-demo-9917"
	if err := rt.IngestEvent(ctx, claims.NewClaimReceivedEvent(claimID, "claimant-marta", "policy-auto-881", "collision", time.Now())); err != nil {
		fmt.Fprintf(os.Stderr, "ingest claim: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("KIFF insurance claims triage demo")
	fmt.Println()
	fmt.Println(" - input claim received for collision loss")

	if err := execute(ctx, rt, claims.ActionVerifyCoverage, claimID, claims.StateReceived, claims.ClaimsAgentActor, map[string]any{
		"claim_id":           claimID,
		"claimant_id":        "claimant-marta",
		"policy_id":          "policy-auto-881",
		"loss_type":          "collision",
		"coverage_confirmed": true,
	}); err != nil {
		fail("verify coverage", err)
	}
	fmt.Println(" - agent proposed VERIFY_COVERAGE; KIFF validated state and permission")

	if err := execute(ctx, rt, claims.ActionAssessRisk, claimID, claims.StateCoverageVerified, claims.ClaimsAgentActor, map[string]any{
		"claim_id":            claimID,
		"risk_score":          0.79,
		"payout_amount_cents": 420000,
		"currency":            "USD",
		"fraud_signals":       true,
	}); err != nil {
		fail("assess risk", err)
	}
	fmt.Println(" - risk assessment routed claim to REVIEW_REQUIRED")

	release, err := claims.Contract(rt, claims.ActionIssueApprovedPayout)
	if err != nil {
		fail("load approved payout contract", err)
	}
	releaseCtx := action.ActionContext{
		ActionName:   claims.ActionIssueApprovedPayout,
		EntityID:     claimID,
		EntityType:   claims.EntityClaim,
		CurrentState: claims.StateReviewRequired,
		Actor:        claims.ClaimsServiceActor,
		ApprovalID:   "approval-claim-demo-9917",
		Parameters: map[string]any{
			"claim_id":            claimID,
			"claimant_id":         "claimant-marta",
			"policy_id":           "policy-auto-881",
			"payout_amount_cents": 420000,
			"currency":            "USD",
			"idempotency_key":     "claim-demo-9917:claimant-marta:policy-auto-881",
		},
	}
	if _, err := rt.ExecuteAction(ctx, releaseCtx, release); !errors.Is(err, action.ErrApprovalRequired) {
		fail("approval gate", fmt.Errorf("expected approval required, got %v", err))
	}
	fmt.Println(" - claims service payout was blocked until human approval")

	requestCtx := releaseCtx
	requestCtx.Actor = claims.ClaimsAgentActor
	if _, err := rt.RequestApproval(ctx, releaseCtx.ApprovalID, requestCtx, release, "high value claim with fraud signals"); err != nil {
		fail("request approval", err)
	}
	if _, err := claims.ReviewPayoutApproval(ctx, rt, releaseCtx.ApprovalID, claims.AdjusterActor, true, "settlement approved after adjuster review"); err != nil {
		fail("review approval", err)
	}
	fmt.Println(" - senior adjuster approved ISSUE_APPROVED_PAYOUT")

	if _, err := rt.ExecuteAction(ctx, releaseCtx, release); err != nil {
		fail("execute payout", err)
	}
	fmt.Println(" - claims-core-service issued payout through idempotent gateway")

	current, err := claims.CurrentState(ctx, rt, claimID)
	if err != nil {
		fail("current state", err)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", current.Value)
	for _, receipt := range gateway.List() {
		fmt.Printf("Payout: %s %s %.2f via %s\n", receipt.PayoutID, receipt.Currency, float64(receipt.AmountCents)/100, receipt.IdempotencyKey)
	}
}

func execute(ctx context.Context, rt *runtime.Runtime, actionName, claimID, currentState string, a actor.Actor, params map[string]any) error {
	contract, err := claims.Contract(rt, actionName)
	if err != nil {
		return err
	}
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   actionName,
		EntityID:     claimID,
		EntityType:   claims.EntityClaim,
		CurrentState: currentState,
		Actor:        a,
		Parameters:   params,
	}, contract)
	return err
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
