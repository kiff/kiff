package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	security "github.com/kiff/kiff/cookbook/security-incident-response"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func main() {
	ctx := context.Background()
	control := security.NewInMemoryIdentityControl()
	rt, err := security.NewRuntime(control)
	if err != nil {
		fail("create runtime", err)
	}

	incidentID := "sec-demo-8842"
	userID := "user-admin"
	userEmail := "admin@example.com"
	accountID := "acct-corp"
	if err := rt.IngestEvent(ctx, security.NewAlertReceivedEvent(incidentID, userID, userEmail, accountID, time.Now())); err != nil {
		fail("ingest alert", err)
	}

	agent := &security.ScriptedAgent{Proposals: []security.AgentProposal{
		{
			ActionName: security.ActionAttachSignals,
			Parameters: map[string]any{
				"incident_id":              incidentID,
				"user_id":                  userID,
				"user_email":               userEmail,
				"account_id":               accountID,
				"risk_score_percent":       94,
				"failed_login_count":       61,
				"user_tier":                "privileged",
				"blast_radius":             "broad",
				"privileged_group_count":   4,
				"impossible_travel":        true,
				"malware_signal":           true,
				"data_exfiltration_signal": true,
			},
			ReasoningSummary: "identity telemetry links impossible travel, malware, and privileged group exposure",
			Confidence:       0.94,
		},
		{
			ActionName: security.ActionAssessIdentityRisk,
			Parameters: map[string]any{
				"risk_score_percent":       94,
				"failed_login_count":       61,
				"user_tier":                "privileged",
				"blast_radius":             "broad",
				"privileged_group_count":   4,
				"impossible_travel":        true,
				"malware_signal":           true,
				"data_exfiltration_signal": true,
			},
			ReasoningSummary: "privileged account with broad blast radius must be routed to security review",
			Confidence:       0.97,
		},
	}}

	fmt.Println("KIFF security incident response demo")
	fmt.Println()
	fmt.Println(" - security alert received for privileged user")

	if err := step(ctx, rt, agent, incidentID, "Attach identity, EDR, and access graph signals."); err != nil {
		fail("attach signals", err)
	}
	fmt.Println(" - agent proposed ATTACH_SIGNALS; KIFF moved state to SIGNALS_ATTACHED")

	if err := step(ctx, rt, agent, incidentID, "Assess containment risk."); err != nil {
		fail("assess risk", err)
	}
	fmt.Println(" - agent proposed ASSESS_IDENTITY_RISK; KIFF routed incident to REVIEW_REQUIRED")

	contract, err := security.Contract(rt, security.ActionRevokeUserAccess)
	if err != nil {
		fail("load revocation contract", err)
	}
	params := map[string]any{
		"incident_id":              incidentID,
		"user_id":                  userID,
		"user_email":               userEmail,
		"account_id":               accountID,
		"reason":                   "suspected privileged account compromise",
		"revocation_scope":         "all_access",
		"user_tier":                "privileged",
		"blast_radius":             "broad",
		"privileged_group_count":   4,
		"data_exfiltration_signal": true,
		"idempotency_key":          "sec-demo-8842:user-admin:all_access",
	}
	revokeCtx := action.ActionContext{
		ActionName:     security.ActionRevokeUserAccess,
		EntityID:       incidentID,
		EntityType:     security.EntitySecurityIncident,
		CurrentState:   security.StateReviewRequired,
		Actor:          security.IdentityServiceActor,
		ApprovalID:     "approval-sec-demo-8842",
		Parameters:     params,
		IdempotencyKey: "sec-demo-8842:user-admin:all_access",
	}
	if _, err := rt.ExecuteAction(ctx, revokeCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		fail("approval gate", fmt.Errorf("expected approval required, got %v", err))
	}
	fmt.Println(" - identity-service revocation was blocked by dynamic approval policy")

	requestCtx := revokeCtx
	requestCtx.Actor = security.SecurityAgentActor
	if _, err := rt.RequestApproval(ctx, revokeCtx.ApprovalID, requestCtx, contract, "privileged all-access revocation requires security lead approval"); err != nil {
		fail("request approval", err)
	}
	if _, err := security.ReviewContainmentApproval(ctx, rt, revokeCtx.ApprovalID, security.SecurityLeadActor, true, "approve immediate all-access revocation"); err != nil {
		fail("review approval", err)
	}
	fmt.Println(" - security lead approved REVOKE_USER_ACCESS")

	if _, err := rt.ExecuteAction(ctx, revokeCtx, contract); err != nil {
		fail("revoke access", err)
	}
	if _, err := rt.ExecuteAction(ctx, revokeCtx, contract); err != nil {
		fail("idempotent retry", err)
	}
	fmt.Println(" - retry returned the prior result without a second identity operation")

	current, err := security.CurrentState(ctx, rt, incidentID)
	if err != nil {
		fail("current state", err)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", current.Value)
	for _, receipt := range control.List() {
		fmt.Printf("Operation: %s %s user=%s via %s\n", receipt.OperationID, receipt.Operation, receipt.UserID, receipt.IdempotencyKey)
	}
	if err := printLifecycle(ctx, rt, incidentID); err != nil {
		fail("lifecycle", err)
	}
}

func step(ctx context.Context, rt *runtime.Runtime, agent security.Agent, incidentID, input string) error {
	current, err := security.CurrentState(ctx, rt, incidentID)
	if err != nil {
		return err
	}
	contracts, err := rt.AllowedActions(ctx, incidentID)
	if err != nil {
		return err
	}
	proposal, err := agent.Propose(ctx, security.AgentRequest{
		IncidentID:     incidentID,
		CurrentState:   current.Value,
		AllowedActions: actionNames(contracts),
		OperatorInput:  input,
	})
	if err != nil {
		return err
	}
	if err := security.RecordAgentProposal(ctx, rt, incidentID, input, proposal); err != nil {
		return err
	}
	if proposal.ActionName == "NO_ACTION" {
		return nil
	}
	_, err = security.ApplyAgentProposal(ctx, rt, incidentID, current.Value, proposal)
	return err
}

func actionNames(contracts []action.ActionContract) []string {
	names := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		names = append(names, contract.Name)
	}
	return names
}

func printLifecycle(ctx context.Context, rt *runtime.Runtime, incidentID string) error {
	lifecycle, err := rt.EntityLifecycle(ctx, incidentID)
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
