package cloudremediation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/state"
)

const (
	EntityIncident = "CloudIncident"

	EventAlertReceived     = "ALERT_RECEIVED"
	EventTelemetryAttached = "TELEMETRY_ATTACHED"
	EventLowRiskClassified = "LOW_RISK_CLASSIFIED"
	EventReviewRequired    = "REVIEW_REQUIRED"
	EventRestartPrepared   = "RESTART_PREPARED"
	EventRestartExecuted   = "RESTART_EXECUTED"
	EventInstanceIsolated  = "INSTANCE_ISOLATED"
	EventIncidentEscalated = "INCIDENT_ESCALATED"

	StateReceived        = "RECEIVED"
	StateTriaged         = "TRIAGED"
	StateLowRiskReady    = "LOW_RISK_READY"
	StateRestartPrepared = "RESTART_PREPARED"
	StateReviewRequired  = "REVIEW_REQUIRED"
	StateRemediated      = "REMEDIATED"
	StateIsolated        = "ISOLATED"
	StateEscalated       = "ESCALATED"

	ActionAttachTelemetry   = "ATTACH_TELEMETRY"
	ActionAssessRemediation = "ASSESS_REMEDIATION"
	ActionPrepareRestart    = "PREPARE_PROCESS_RESTART"
	ActionExecuteRestart    = "EXECUTE_PROCESS_RESTART"
	ActionHoldForSREReview  = "HOLD_FOR_SRE_REVIEW"
	ActionIsolateInstance   = "ISOLATE_INSTANCE"
	ActionEscalateIncident  = "ESCALATE_INCIDENT"

	RoleOpsAgent     = "ops_agent"
	RoleCloudService = "cloud_automation_service"
	RoleSRELead      = "sre_lead"

	LowRiskScoreLimit = 40
)

const (
	PermissionAttachTelemetry   permission.Permission = "cloud.attach_telemetry"
	PermissionAssessRemediation permission.Permission = "cloud.assess_remediation"
	PermissionPrepareRestart    permission.Permission = "cloud.prepare_restart"
	PermissionExecuteRestart    permission.Permission = "cloud.execute_restart"
	PermissionHoldForSREReview  permission.Permission = "cloud.hold_for_sre_review"
	PermissionIsolateInstance   permission.Permission = "cloud.isolate_instance"
	PermissionEscalateIncident  permission.Permission = "cloud.escalate_incident"
	PermissionReviewIsolation   permission.Permission = "cloud.review_isolation"
)

var (
	OpsAgentActor = actor.Actor{
		ID:          "agent-cloud-ops-1",
		Type:        actor.TypeAgent,
		DisplayName: "Cloud Ops Agent",
		Roles:       []string{RoleOpsAgent},
	}
	CloudAutomationActor = actor.Actor{
		ID:          "cloud-automation-service",
		Type:        actor.TypeService,
		DisplayName: "Cloud Automation Service",
		Roles:       []string{RoleCloudService},
	}
	SRELeadActor = actor.Actor{
		ID:          "sre-lead-ava",
		Type:        actor.TypeHuman,
		DisplayName: "Ava, SRE Lead",
		Roles:       []string{RoleSRELead},
	}
)

type CloudInstruction struct {
	IncidentID     string
	Service        string
	InstanceID     string
	Region         string
	RunbookID      string
	IsolationScope string
	IdempotencyKey string
}

type CloudOperationReceipt struct {
	OperationID    string `json:"operation_id"`
	Operation      string `json:"operation"`
	IncidentID     string `json:"incident_id"`
	Service        string `json:"service"`
	InstanceID     string `json:"instance_id"`
	Region         string `json:"region"`
	RunbookID      string `json:"runbook_id,omitempty"`
	IsolationScope string `json:"isolation_scope,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"`
	CompletedAt    string `json:"completed_at"`
}

type CloudControlGateway interface {
	RestartProcess(context.Context, CloudInstruction) (CloudOperationReceipt, error)
	IsolateInstance(context.Context, CloudInstruction) (CloudOperationReceipt, error)
	List() []CloudOperationReceipt
}

type InMemoryCloudControl struct {
	mu       sync.Mutex
	sequence int
	byKey    map[string]CloudOperationReceipt
	order    []string
}

func NewInMemoryCloudControl() *InMemoryCloudControl {
	return &InMemoryCloudControl{byKey: map[string]CloudOperationReceipt{}}
}

func (c *InMemoryCloudControl) RestartProcess(ctx context.Context, instruction CloudInstruction) (CloudOperationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return CloudOperationReceipt{}, err
	}
	if err := validateRestartInstruction(instruction); err != nil {
		return CloudOperationReceipt{}, err
	}
	return c.record("restart_process", instruction)
}

func (c *InMemoryCloudControl) IsolateInstance(ctx context.Context, instruction CloudInstruction) (CloudOperationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return CloudOperationReceipt{}, err
	}
	if err := validateIsolationInstruction(instruction); err != nil {
		return CloudOperationReceipt{}, err
	}
	return c.record("isolate_instance", instruction)
}

func (c *InMemoryCloudControl) List() []CloudOperationReceipt {
	c.mu.Lock()
	defer c.mu.Unlock()

	receipts := make([]CloudOperationReceipt, 0, len(c.order))
	for _, key := range c.order {
		receipts = append(receipts, c.byKey[key])
	}
	return receipts
}

func (c *InMemoryCloudControl) record(operation string, instruction CloudInstruction) (CloudOperationReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := operation + ":" + instruction.IdempotencyKey
	if existing, ok := c.byKey[key]; ok {
		existing.Duplicate = true
		return existing, nil
	}

	c.sequence++
	receipt := CloudOperationReceipt{
		OperationID:    fmt.Sprintf("cloud-op-%06d", c.sequence),
		Operation:      operation,
		IncidentID:     instruction.IncidentID,
		Service:        instruction.Service,
		InstanceID:     instruction.InstanceID,
		Region:         instruction.Region,
		RunbookID:      instruction.RunbookID,
		IsolationScope: instruction.IsolationScope,
		IdempotencyKey: instruction.IdempotencyKey,
		CompletedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	c.byKey[key] = receipt
	c.order = append(c.order, key)
	return receipt, nil
}

func Definition(gateway CloudControlGateway) (domain.Definition, error) {
	builder := domain.New("cloud-infra-remediation").
		Entity(EntityIncident).
		Event(EventAlertReceived).
		Event(EventTelemetryAttached).
		Event(EventLowRiskClassified).
		Event(EventReviewRequired).
		Event(EventRestartPrepared).
		Event(EventRestartExecuted).
		Event(EventInstanceIsolated).
		Event(EventIncidentEscalated).
		Transition(EventAlertReceived, "", StateReceived).
		Transition(EventTelemetryAttached, StateReceived, StateTriaged).
		Transition(EventLowRiskClassified, StateTriaged, StateLowRiskReady).
		Transition(EventReviewRequired, StateTriaged, StateReviewRequired).
		Transition(EventReviewRequired, StateLowRiskReady, StateReviewRequired).
		Transition(EventRestartPrepared, StateLowRiskReady, StateRestartPrepared).
		Transition(EventRestartExecuted, StateRestartPrepared, StateRemediated).
		Transition(EventInstanceIsolated, StateReviewRequired, StateIsolated).
		Transition(EventIncidentEscalated, StateReceived, StateEscalated).
		Transition(EventIncidentEscalated, StateTriaged, StateEscalated).
		Transition(EventIncidentEscalated, StateLowRiskReady, StateEscalated).
		Transition(EventIncidentEscalated, StateRestartPrepared, StateEscalated).
		Transition(EventIncidentEscalated, StateReviewRequired, StateEscalated).
		Allow(StateReceived, ActionAttachTelemetry, ActionEscalateIncident).
		Allow(StateTriaged, ActionAssessRemediation, ActionHoldForSREReview, ActionEscalateIncident).
		Allow(StateLowRiskReady, ActionPrepareRestart, ActionHoldForSREReview, ActionEscalateIncident).
		Allow(StateRestartPrepared, ActionExecuteRestart, ActionEscalateIncident).
		Allow(StateReviewRequired, ActionIsolateInstance, ActionEscalateIncident)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RoleOpsAgent, PermissionAttachTelemetry)
	policy.GrantRole(RoleOpsAgent, PermissionAssessRemediation)
	policy.GrantRole(RoleOpsAgent, PermissionPrepareRestart)
	policy.GrantRole(RoleOpsAgent, PermissionHoldForSREReview)
	policy.GrantRole(RoleOpsAgent, PermissionEscalateIncident)
	policy.GrantRole(RoleCloudService, PermissionExecuteRestart)
	policy.GrantRole(RoleCloudService, PermissionIsolateInstance)
	policy.GrantRole(RoleSRELead, PermissionReviewIsolation)
	policy.GrantRole(RoleSRELead, PermissionEscalateIncident)

	policy.AssignRole(OpsAgentActor.ID, RoleOpsAgent)
	policy.AssignRole(CloudAutomationActor.ID, RoleCloudService)
	policy.AssignRole(SRELeadActor.ID, RoleSRELead)
	return policy
}

func NewRuntime(gateway CloudControlGateway) (*runtime.Runtime, error) {
	definition, err := Definition(gateway)
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(definition, runtime.Config{PermissionPolicy: Policy()})
}

func stringParams(names ...string) []action.ParameterSpec {
	specs := make([]action.ParameterSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, action.StringParam(name))
	}
	return specs
}

func boolParamSpec(name string) action.ParameterSpec {
	return action.ParameterSpec{Name: name, Type: action.ParamBool, Required: true}
}

func boundedIntParam(name string, min, max int64) action.ParameterSpec {
	spec := action.IntParam(name)
	spec.Min = &min
	spec.Max = &max
	return spec
}

func telemetryParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("incident_id"),
		action.StringParam("service"),
		action.StringParam("instance_id"),
		action.StringParam("region"),
		boundedIntParam("cpu_percent", 0, 100),
		boundedIntParam("error_rate_per_min", 0, 100000),
		boolParamSpec("threat_signal"),
		action.EnumParam("customer_impact", "none", "limited", "broad"),
	}
}

func assessmentParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		boundedIntParam("risk_score_percent", 0, 100),
		action.EnumParam("customer_impact", "none", "limited", "broad"),
		boolParamSpec("threat_signal"),
	}
}

func restartParameters() []action.ParameterSpec {
	return stringParams("incident_id", "service", "instance_id", "region", "runbook_id", "idempotency_key")
}

func isolationParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("incident_id"),
		action.StringParam("service"),
		action.StringParam("instance_id"),
		action.StringParam("region"),
		action.EnumParam("isolation_scope", "instance", "security_group"),
		action.StringParam("idempotency_key"),
	}
}

func Contracts(gateway CloudControlGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewInMemoryCloudControl()
	}
	return []action.ActionContract{
		{
			Name:                ActionAttachTelemetry,
			AllowedStates:       []string{StateReceived},
			Parameters:          telemetryParameters(),
			ValidateParameters:  func(_ context.Context, ctx action.ActionContext) error { return validateTelemetry(ctx.Parameters) },
			RequiredPermissions: []permission.Permission{PermissionAttachTelemetry},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionAttachTelemetry, ctx, EventTelemetryAttached, "attached incident telemetry", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionAssessRemediation,
			AllowedStates: []string{StateTriaged},
			Parameters:    assessmentParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := assessmentFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionAssessRemediation},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				assessment, _ := assessmentFromParams(ctx.Parameters)
				payload := copyParams(ctx.Parameters)
				if assessment.LowRisk() {
					payload["low_risk_score_limit"] = LowRiskScoreLimit
					return eventResult(ActionAssessRemediation, ctx, EventLowRiskClassified, "classified incident as safe for low-risk restart", payload), nil
				}
				payload["review_reason"] = assessment.ReviewReason()
				return eventResult(ActionAssessRemediation, ctx, EventReviewRequired, "routed incident to SRE review", payload), nil
			},
		},
		{
			Name:          ActionPrepareRestart,
			AllowedStates: []string{StateLowRiskReady},
			Parameters:    restartParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := restartInstructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionPrepareRestart},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionPrepareRestart, ctx, EventRestartPrepared, "prepared process restart runbook", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionExecuteRestart,
			AllowedStates: []string{StateRestartPrepared},
			Parameters:    restartParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := restartInstructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionExecuteRestart},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := restartInstructionFromParams(actionCtx.Parameters)
				return executeRestart(ctx, gateway, actionCtx, instruction)
			},
		},
		{
			Name:                ActionHoldForSREReview,
			AllowedStates:       []string{StateTriaged, StateLowRiskReady},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionHoldForSREReview},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForSREReview, ctx, EventReviewRequired, "held incident for SRE review", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionIsolateInstance,
			AllowedStates: []string{StateReviewRequired},
			Parameters:    isolationParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := isolationInstructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionIsolateInstance},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalRequired,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := isolationInstructionFromParams(actionCtx.Parameters)
				return isolateInstance(ctx, gateway, actionCtx, instruction)
			},
		},
		{
			Name:                ActionEscalateIncident,
			AllowedStates:       []string{StateReceived, StateTriaged, StateLowRiskReady, StateRestartPrepared, StateReviewRequired},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionEscalateIncident},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionEscalateIncident, ctx, EventIncidentEscalated, "escalated incident to manual response", ctx.Parameters), nil
			},
		},
	}
}

type RemediationAssessment struct {
	RiskScorePercent int64
	CustomerImpact   string
	ThreatSignal     bool
}

func (a RemediationAssessment) LowRisk() bool {
	return a.RiskScorePercent <= LowRiskScoreLimit && a.CustomerImpact != "broad" && !a.ThreatSignal
}

func (a RemediationAssessment) ReviewReason() string {
	reasons := []string{}
	if a.RiskScorePercent > LowRiskScoreLimit {
		reasons = append(reasons, fmt.Sprintf("risk score %d is above %d", a.RiskScorePercent, LowRiskScoreLimit))
	}
	if a.CustomerImpact == "broad" {
		reasons = append(reasons, "customer impact is broad")
	}
	if a.ThreatSignal {
		reasons = append(reasons, "threat signal present")
	}
	if len(reasons) == 0 {
		return "SRE review requested"
	}
	return strings.Join(reasons, "; ")
}

func NewAlertReceivedEvent(incidentID, service, instanceID, region string, occurredAt time.Time) event.Event {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-alert-received-%d", incidentID, occurredAt.UnixNano()),
		Type:       EventAlertReceived,
		EntityID:   incidentID,
		EntityType: EntityIncident,
		Source:     "cloud-alerts",
		ActorID:    "cloud-alerts",
		OccurredAt: occurredAt.UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", incidentID),
			CorrelationID: incidentID,
			Tags:          map[string]string{"workflow": "cloud-infra-remediation"},
		},
		Payload: map[string]any{
			"incident_id": incidentID,
			"service":     service,
			"instance_id": instanceID,
			"region":      region,
		},
	}
}

func Contract(rt *runtime.Runtime, actionName string) (action.ActionContract, error) {
	contract, ok := rt.Actions.Get(actionName)
	if !ok {
		return action.ActionContract{}, fmt.Errorf("missing action contract %q", actionName)
	}
	return contract, nil
}

func CurrentState(ctx context.Context, rt *runtime.Runtime, incidentID string) (state.State, error) {
	current, ok, err := rt.States.Current(ctx, incidentID)
	if err != nil {
		return state.State{}, err
	}
	if !ok {
		return state.State{}, fmt.Errorf("incident %q has no state", incidentID)
	}
	return current, nil
}

func ReviewIsolationApproval(ctx context.Context, rt *runtime.Runtime, approvalID string, reviewer actor.Actor, granted bool, reason string) (approval.Approval, error) {
	if rt == nil {
		return approval.Approval{}, errors.New("runtime is required")
	}
	status := approval.StatusDenied
	if granted {
		status = approval.StatusGranted
	}
	// The runtime enforces reviewer authority (the SRE lead's review
	// permission) and segregation of duties (the requester cannot approve
	// their own isolation) before the approval changes.
	return rt.ReviewApprovalAs(ctx, approvalID, reviewer, runtime.ReviewRequirement{
		Permission:            PermissionReviewIsolation,
		SeparateFromRequester: true,
	}, status, reason)
}

func eventResult(actionName string, ctx action.ActionContext, eventType, summary string, payload map[string]any) action.ActionResult {
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       ctx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output:         copyParams(payload),
		FollowUpEvents: []event.Event{
			newEvent(eventType, ctx.EntityID, ctx.Actor.ID, payload),
		},
		ExecutedAt: time.Now().UTC(),
	}
}

func executeRestart(ctx context.Context, gateway CloudControlGateway, actionCtx action.ActionContext, instruction CloudInstruction) (action.ActionResult, error) {
	receipt, err := gateway.RestartProcess(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	return cloudResult(ActionExecuteRestart, actionCtx, EventRestartExecuted, receipt)
}

func isolateInstance(ctx context.Context, gateway CloudControlGateway, actionCtx action.ActionContext, instruction CloudInstruction) (action.ActionResult, error) {
	receipt, err := gateway.IsolateInstance(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	return cloudResult(ActionIsolateInstance, actionCtx, EventInstanceIsolated, receipt)
}

func cloudResult(actionName string, actionCtx action.ActionContext, eventType string, receipt CloudOperationReceipt) (action.ActionResult, error) {
	summary := "executed cloud control operation"
	if receipt.Duplicate {
		summary = "idempotent cloud control operation returned existing receipt"
	}
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output: map[string]any{
			"operation_id":    receipt.OperationID,
			"operation":       receipt.Operation,
			"idempotency_key": receipt.IdempotencyKey,
			"duplicate":       receipt.Duplicate,
		},
		FollowUpEvents: []event.Event{
			newEvent(eventType, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"operation_id":    receipt.OperationID,
				"operation":       receipt.Operation,
				"service":         receipt.Service,
				"instance_id":     receipt.InstanceID,
				"region":          receipt.Region,
				"runbook_id":      receipt.RunbookID,
				"isolation_scope": receipt.IsolationScope,
				"idempotency_key": receipt.IdempotencyKey,
				"duplicate":       receipt.Duplicate,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func validateTelemetry(params map[string]any) error {
	if strings.TrimSpace(stringParam(params, "incident_id")) == "" {
		return errors.New("incident_id is required")
	}
	if strings.TrimSpace(stringParam(params, "service")) == "" {
		return errors.New("service is required")
	}
	if strings.TrimSpace(stringParam(params, "instance_id")) == "" {
		return errors.New("instance_id is required")
	}
	if strings.TrimSpace(stringParam(params, "region")) == "" {
		return errors.New("region is required")
	}
	return nil
}

func validateRestartInstruction(instruction CloudInstruction) error {
	if strings.TrimSpace(instruction.IncidentID) == "" {
		return errors.New("incident_id is required")
	}
	if strings.TrimSpace(instruction.Service) == "" {
		return errors.New("service is required")
	}
	if strings.TrimSpace(instruction.InstanceID) == "" {
		return errors.New("instance_id is required")
	}
	if strings.TrimSpace(instruction.Region) == "" {
		return errors.New("region is required")
	}
	if strings.TrimSpace(instruction.RunbookID) == "" {
		return errors.New("runbook_id is required")
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func validateIsolationInstruction(instruction CloudInstruction) error {
	if strings.TrimSpace(instruction.IncidentID) == "" {
		return errors.New("incident_id is required")
	}
	if strings.TrimSpace(instruction.Service) == "" {
		return errors.New("service is required")
	}
	if strings.TrimSpace(instruction.InstanceID) == "" {
		return errors.New("instance_id is required")
	}
	if strings.TrimSpace(instruction.Region) == "" {
		return errors.New("region is required")
	}
	if instruction.IsolationScope != "instance" && instruction.IsolationScope != "security_group" {
		return fmt.Errorf("unsupported isolation_scope %q", instruction.IsolationScope)
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func restartInstructionFromParams(params map[string]any) (CloudInstruction, error) {
	instruction := CloudInstruction{
		IncidentID:     stringParam(params, "incident_id"),
		Service:        stringParam(params, "service"),
		InstanceID:     stringParam(params, "instance_id"),
		Region:         stringParam(params, "region"),
		RunbookID:      stringParam(params, "runbook_id"),
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}
	if err := validateRestartInstruction(instruction); err != nil {
		return CloudInstruction{}, err
	}
	return instruction, nil
}

func isolationInstructionFromParams(params map[string]any) (CloudInstruction, error) {
	instruction := CloudInstruction{
		IncidentID:     stringParam(params, "incident_id"),
		Service:        stringParam(params, "service"),
		InstanceID:     stringParam(params, "instance_id"),
		Region:         stringParam(params, "region"),
		IsolationScope: stringParam(params, "isolation_scope"),
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}
	if err := validateIsolationInstruction(instruction); err != nil {
		return CloudInstruction{}, err
	}
	return instruction, nil
}

func assessmentFromParams(params map[string]any) (RemediationAssessment, error) {
	score, err := int64Param(params, "risk_score_percent")
	if err != nil {
		return RemediationAssessment{}, err
	}
	if score < 0 || score > 100 {
		return RemediationAssessment{}, errors.New("risk_score_percent must be between 0 and 100")
	}
	impact := stringParam(params, "customer_impact")
	if impact != "none" && impact != "limited" && impact != "broad" {
		return RemediationAssessment{}, fmt.Errorf("unsupported customer_impact %q", impact)
	}
	return RemediationAssessment{
		RiskScorePercent: score,
		CustomerImpact:   impact,
		ThreatSignal:     boolParam(params, "threat_signal"),
	}, nil
}

func newEvent(eventType, incidentID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", incidentID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   incidentID,
		EntityType: EntityIncident,
		Source:     "cloud/executor",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    copyParams(payload),
	}
}

func copyParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	copied := make(map[string]any, len(params))
	for key, value := range params {
		copied[key] = value
	}
	return copied
}

func stringParam(params map[string]any, key string) string {
	if value, ok := params[key]; ok {
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	return ""
}

func boolParam(params map[string]any, key string) bool {
	value, ok := params[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true") || strings.EqualFold(strings.TrimSpace(typed), "yes")
	default:
		return false
	}
}

func int64Param(params map[string]any, key string) (int64, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		return int64(typed), nil
	case string:
		var parsed int64
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}
