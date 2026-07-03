package cloudremediation

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

func TestLowRiskRestartExecutesWithoutHumanApproval(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryCloudControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "inc-low-1001"
	service := "checkout-api"
	instanceID := "i-0abc123"
	region := "us-east-1"
	if err := rt.IngestEvent(ctx, NewAlertReceivedEvent(incidentID, service, instanceID, region, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionAttachTelemetry, incidentID, StateReceived, OpsAgentActor, telemetryParams(incidentID, service, instanceID, region, 92, 80, false, "limited"))
	mustExecute(t, ctx, rt, ActionAssessRemediation, incidentID, StateTriaged, OpsAgentActor, assessmentParams(24, "limited", false))
	mustExecute(t, ctx, rt, ActionPrepareRestart, incidentID, StateLowRiskReady, OpsAgentActor, restartParams(incidentID, service, instanceID, region))
	mustExecute(t, ctx, rt, ActionExecuteRestart, incidentID, StateRestartPrepared, CloudAutomationActor, restartParams(incidentID, service, instanceID, region))

	current, err := CurrentState(ctx, rt, incidentID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateRemediated {
		t.Fatalf("expected %s, got %s", StateRemediated, current.Value)
	}
	if operations := control.List(); len(operations) != 1 || operations[0].Operation != "restart_process" {
		t.Fatalf("expected one restart operation, got %#v", operations)
	}
	timeline, err := rt.Timeline(ctx, incidentID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if auditHasKind(timeline, audit.KindApprovalRequired) {
		t.Fatal("low-risk restart should not require approval")
	}
	if !auditHasActorAction(timeline, audit.KindActionExecuted, CloudAutomationActor.ID, ActionExecuteRestart) {
		t.Fatal("expected restart execution by cloud automation service")
	}
	replayed, err := rt.RebuildState(ctx, incidentID)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed.State.Value != StateRemediated {
		t.Fatalf("expected replay state %s, got %s", StateRemediated, replayed.State.Value)
	}
}

func TestHighRiskIsolationRequiresSREApproval(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryCloudControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "inc-review-2002"
	service := "payments-api"
	instanceID := "i-0def456"
	region := "us-east-1"
	if err := rt.IngestEvent(ctx, NewAlertReceivedEvent(incidentID, service, instanceID, region, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mustExecute(t, ctx, rt, ActionAttachTelemetry, incidentID, StateReceived, OpsAgentActor, telemetryParams(incidentID, service, instanceID, region, 98, 4500, true, "broad"))
	mustExecute(t, ctx, rt, ActionAssessRemediation, incidentID, StateTriaged, OpsAgentActor, assessmentParams(91, "broad", true))

	contract, err := Contract(rt, ActionIsolateInstance)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionIsolateInstance,
		EntityID:     incidentID,
		EntityType:   EntityIncident,
		CurrentState: StateReviewRequired,
		Actor:        CloudAutomationActor,
		ApprovalID:   "approval-inc-review-2002",
		Parameters:   isolationParams(incidentID, service, instanceID, region, "instance"),
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if operations := control.List(); len(operations) != 0 {
		t.Fatalf("isolation should not run before approval: %#v", operations)
	}

	requestCtx := actionCtx
	requestCtx.Actor = OpsAgentActor
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, requestCtx, contract, "threat signal and broad customer impact require SRE approval"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewIsolationApproval(ctx, rt, actionCtx.ApprovalID, SRELeadActor, true, "isolate the instance and preserve evidence"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("approved isolation: %v", err)
	}
	current, err := CurrentState(ctx, rt, incidentID)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if current.Value != StateIsolated {
		t.Fatalf("expected %s, got %s", StateIsolated, current.Value)
	}
	if operations := control.List(); len(operations) != 1 || operations[0].Operation != "isolate_instance" {
		t.Fatalf("expected one isolation operation, got %#v", operations)
	}
}

func TestAgentCannotSelfIsolateByAddingServiceRole(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryCloudControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "inc-permission-3003"
	service := "identity-api"
	instanceID := "i-0ghi789"
	region := "us-west-2"
	if err := rt.IngestEvent(ctx, NewAlertReceivedEvent(incidentID, service, instanceID, region, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionAttachTelemetry, incidentID, StateReceived, OpsAgentActor, telemetryParams(incidentID, service, instanceID, region, 88, 2200, true, "limited"))
	mustExecute(t, ctx, rt, ActionAssessRemediation, incidentID, StateTriaged, OpsAgentActor, assessmentParams(70, "limited", true))

	contract, err := Contract(rt, ActionIsolateInstance)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	spoofedAgent := OpsAgentActor
	spoofedAgent.Roles = append(spoofedAgent.Roles, RoleCloudService)
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionIsolateInstance,
		EntityID:     incidentID,
		EntityType:   EntityIncident,
		CurrentState: StateReviewRequired,
		Actor:        spoofedAgent,
		Parameters:   isolationParams(incidentID, service, instanceID, region, "instance"),
	}, contract)
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
	if operations := control.List(); len(operations) != 0 {
		t.Fatalf("expected no operation, got %#v", operations)
	}
}

func TestInvalidIsolationScopeIsRejectedBeforeApproval(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryCloudControl()
	rt, err := NewRuntime(control)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	incidentID := "inc-invalid-4004"
	service := "search-api"
	instanceID := "i-0jkl012"
	region := "eu-west-1"
	if err := rt.IngestEvent(ctx, NewAlertReceivedEvent(incidentID, service, instanceID, region, time.Now())); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mustExecute(t, ctx, rt, ActionAttachTelemetry, incidentID, StateReceived, OpsAgentActor, telemetryParams(incidentID, service, instanceID, region, 95, 3200, true, "broad"))
	mustExecute(t, ctx, rt, ActionAssessRemediation, incidentID, StateTriaged, OpsAgentActor, assessmentParams(80, "broad", true))

	contract, err := Contract(rt, ActionIsolateInstance)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	_, err = rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   ActionIsolateInstance,
		EntityID:     incidentID,
		EntityType:   EntityIncident,
		CurrentState: StateReviewRequired,
		Actor:        CloudAutomationActor,
		Parameters:   isolationParams(incidentID, service, instanceID, region, "vpc"),
	}, contract)
	if !errors.Is(err, action.ErrInvalidParameter) {
		t.Fatalf("expected invalid parameter, got %v", err)
	}
	if operations := control.List(); len(operations) != 0 {
		t.Fatalf("expected no operation, got %#v", operations)
	}
}

func TestOnlySRELeadCanReviewIsolationApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryCloudControl())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	contract, err := Contract(rt, ActionIsolateInstance)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionIsolateInstance,
		EntityID:     "inc-review-auth-5005",
		EntityType:   EntityIncident,
		CurrentState: StateReviewRequired,
		Actor:        OpsAgentActor,
		ApprovalID:   "approval-inc-review-auth-5005",
		Parameters:   isolationParams("inc-review-auth-5005", "billing-api", "i-0mno345", "us-east-2", "instance"),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "requires SRE lead authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewIsolationApproval(ctx, rt, actionCtx.ApprovalID, OpsAgentActor, true, "agent tried to approve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestRequesterCannotReviewOwnIsolationApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(NewInMemoryCloudControl())
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	contract, err := Contract(rt, ActionIsolateInstance)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	actionCtx := action.ActionContext{
		ActionName:   ActionIsolateInstance,
		EntityID:     "inc-self-review-5006",
		EntityType:   EntityIncident,
		CurrentState: StateReviewRequired,
		Actor:        SRELeadActor,
		ApprovalID:   "approval-inc-self-review-5006",
		Parameters:   isolationParams("inc-self-review-5006", "billing-api", "i-0pqr678", "us-east-2", "instance"),
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "same actor has review authority but requested this approval"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	if _, err := ReviewIsolationApproval(ctx, rt, actionCtx.ApprovalID, SRELeadActor, true, "requester tried to approve"); !errors.Is(err, approval.ErrSelfReview) {
		t.Fatalf("expected self-review rejection, got %v", err)
	}
}

func TestCloudControlGatewayUsesIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	control := NewInMemoryCloudControl()
	instruction := CloudInstruction{
		IncidentID:     "inc-idempotent-6006",
		Service:        "checkout-api",
		InstanceID:     "i-0abc123",
		Region:         "us-east-1",
		RunbookID:      "runbook-safe-restart",
		IdempotencyKey: "inc-idempotent-6006:i-0abc123:restart",
	}
	first, err := control.RestartProcess(ctx, instruction)
	if err != nil {
		t.Fatalf("first restart: %v", err)
	}
	second, err := control.RestartProcess(ctx, instruction)
	if err != nil {
		t.Fatalf("second restart: %v", err)
	}
	if first.OperationID != second.OperationID {
		t.Fatalf("expected same operation id, got %s and %s", first.OperationID, second.OperationID)
	}
	if !second.Duplicate {
		t.Fatal("expected duplicate receipt on second restart")
	}
	if operations := control.List(); len(operations) != 1 {
		t.Fatalf("expected one stored operation, got %d", len(operations))
	}
}

func mustExecute(t *testing.T, ctx context.Context, rt *runtime.Runtime, actionName, incidentID, currentState string, a actor.Actor, params map[string]any) {
	t.Helper()
	contract, err := Contract(rt, actionName)
	if err != nil {
		t.Fatalf("contract %s: %v", actionName, err)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   actionName,
		EntityID:     incidentID,
		EntityType:   EntityIncident,
		CurrentState: currentState,
		Actor:        a,
		Parameters:   params,
	}, contract); err != nil {
		t.Fatalf("execute %s: %v", actionName, err)
	}
}

func telemetryParams(incidentID, service, instanceID, region string, cpuPercent, errorRate int64, threatSignal bool, impact string) map[string]any {
	return map[string]any{
		"incident_id":        incidentID,
		"service":            service,
		"instance_id":        instanceID,
		"region":             region,
		"cpu_percent":        cpuPercent,
		"error_rate_per_min": errorRate,
		"threat_signal":      threatSignal,
		"customer_impact":    impact,
	}
}

func assessmentParams(riskScore int64, impact string, threatSignal bool) map[string]any {
	return map[string]any{
		"risk_score_percent": riskScore,
		"customer_impact":    impact,
		"threat_signal":      threatSignal,
	}
}

func restartParams(incidentID, service, instanceID, region string) map[string]any {
	return map[string]any{
		"incident_id":     incidentID,
		"service":         service,
		"instance_id":     instanceID,
		"region":          region,
		"runbook_id":      "runbook-safe-process-restart",
		"idempotency_key": incidentID + ":" + instanceID + ":restart",
	}
}

func isolationParams(incidentID, service, instanceID, region, scope string) map[string]any {
	return map[string]any{
		"incident_id":     incidentID,
		"service":         service,
		"instance_id":     instanceID,
		"region":          region,
		"isolation_scope": scope,
		"idempotency_key": incidentID + ":" + instanceID + ":isolate",
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
