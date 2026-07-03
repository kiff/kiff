package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	cloud "github.com/kiff/kiff/cookbook/cloud-infra-remediation"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func main() {
	ctx := context.Background()
	control := cloud.NewInMemoryCloudControl()
	rt, err := cloud.NewRuntime(control)
	if err != nil {
		fail("create runtime", err)
	}

	incidentID := "inc-demo-7731"
	service := "payments-api"
	instanceID := "i-0demo7731"
	region := "us-east-1"
	if err := rt.IngestEvent(ctx, cloud.NewAlertReceivedEvent(incidentID, service, instanceID, region, time.Now())); err != nil {
		fail("ingest alert", err)
	}

	agent := &cloud.ScriptedAgent{Proposals: []cloud.AgentProposal{
		{
			ActionName: cloud.ActionAttachTelemetry,
			Parameters: map[string]any{
				"incident_id":        incidentID,
				"service":            service,
				"instance_id":        instanceID,
				"region":             region,
				"cpu_percent":        97,
				"error_rate_per_min": 5200,
				"threat_signal":      true,
				"customer_impact":    "broad",
			},
			ReasoningSummary: "telemetry shows broad customer impact and a threat signal",
			Confidence:       0.93,
		},
		{
			ActionName: cloud.ActionAssessRemediation,
			Parameters: map[string]any{
				"risk_score_percent": 88,
				"customer_impact":    "broad",
				"threat_signal":      true,
			},
			ReasoningSummary: "isolation is safer than restart but requires human authority",
			Confidence:       0.91,
		},
	}}

	fmt.Println("KIFF cloud infrastructure remediation demo")
	fmt.Println()
	fmt.Println(" - alert received for payments-api")

	if err := step(ctx, rt, agent, incidentID, "Attach telemetry from the alert payload."); err != nil {
		fail("attach telemetry", err)
	}
	fmt.Println(" - agent proposed ATTACH_TELEMETRY; KIFF moved state to TRIAGED")

	if err := step(ctx, rt, agent, incidentID, "Assess whether the service can be restarted safely."); err != nil {
		fail("assess remediation", err)
	}
	fmt.Println(" - agent proposed ASSESS_REMEDIATION; KIFF routed incident to REVIEW_REQUIRED")

	contract, err := cloud.Contract(rt, cloud.ActionIsolateInstance)
	if err != nil {
		fail("load isolation contract", err)
	}
	isolationCtx := action.ActionContext{
		ActionName:   cloud.ActionIsolateInstance,
		EntityID:     incidentID,
		EntityType:   cloud.EntityIncident,
		CurrentState: cloud.StateReviewRequired,
		Actor:        cloud.CloudAutomationActor,
		ApprovalID:   "approval-inc-demo-7731",
		Parameters: map[string]any{
			"incident_id":     incidentID,
			"service":         service,
			"instance_id":     instanceID,
			"region":          region,
			"isolation_scope": "instance",
			"idempotency_key": "inc-demo-7731:i-0demo7731:isolate",
		},
	}
	if _, err := rt.ExecuteAction(ctx, isolationCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		fail("approval gate", fmt.Errorf("expected approval required, got %v", err))
	}
	fmt.Println(" - cloud isolation was blocked until SRE approval")

	requestCtx := isolationCtx
	requestCtx.Actor = cloud.OpsAgentActor
	if _, err := rt.RequestApproval(ctx, isolationCtx.ApprovalID, requestCtx, contract, "threat signal and broad impact require SRE approval"); err != nil {
		fail("request approval", err)
	}
	if _, err := cloud.ReviewIsolationApproval(ctx, rt, isolationCtx.ApprovalID, cloud.SRELeadActor, true, "isolate instance and preserve evidence"); err != nil {
		fail("review approval", err)
	}
	fmt.Println(" - SRE lead approved ISOLATE_INSTANCE")

	if _, err := rt.ExecuteAction(ctx, isolationCtx, contract); err != nil {
		fail("isolate instance", err)
	}
	fmt.Println(" - cloud-automation-service isolated the instance through an idempotent gateway")

	current, err := cloud.CurrentState(ctx, rt, incidentID)
	if err != nil {
		fail("current state", err)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", current.Value)
	for _, receipt := range control.List() {
		fmt.Printf("Operation: %s %s %s/%s via %s\n", receipt.OperationID, receipt.Operation, receipt.Region, receipt.InstanceID, receipt.IdempotencyKey)
	}
}

func step(ctx context.Context, rt *runtime.Runtime, agent cloud.Agent, incidentID, input string) error {
	current, err := cloud.CurrentState(ctx, rt, incidentID)
	if err != nil {
		return err
	}
	contracts, err := rt.AllowedActions(ctx, incidentID)
	if err != nil {
		return err
	}
	proposal, err := agent.Propose(ctx, cloud.AgentRequest{
		IncidentID:     incidentID,
		CurrentState:   current.Value,
		AllowedActions: actionNames(contracts),
		OperatorInput:  input,
	})
	if err != nil {
		return err
	}
	if err := cloud.RecordAgentProposal(ctx, rt, incidentID, input, proposal); err != nil {
		return err
	}
	if proposal.ActionName == "NO_ACTION" {
		return nil
	}
	_, err = cloud.ApplyAgentProposal(ctx, rt, incidentID, current.Value, proposal)
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
