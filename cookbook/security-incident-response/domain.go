package securityincident

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
	EntitySecurityIncident = "SecurityIncident"

	EventAlertReceived        = "SECURITY_ALERT_RECEIVED"
	EventSignalsAttached      = "SECURITY_SIGNALS_ATTACHED"
	EventLowRiskClassified    = "LOW_RISK_CLASSIFIED"
	EventReviewRequired       = "ACCESS_REVIEW_REQUIRED"
	EventSessionResetPrepared = "SESSION_RESET_PREPARED"
	EventSessionResetExecuted = "SESSION_RESET_EXECUTED"
	EventAccessRevoked        = "ACCESS_REVOKED"
	EventIncidentEscalated    = "INCIDENT_ESCALATED"

	StateReceived        = "RECEIVED"
	StateSignalsAttached = "SIGNALS_ATTACHED"
	StateLowRiskReady    = "LOW_RISK_READY"
	StateReviewRequired  = "REVIEW_REQUIRED"
	StateResetPrepared   = "RESET_PREPARED"
	StateContained       = "CONTAINED"
	StateEscalated       = "ESCALATED"

	ActionAttachSignals       = "ATTACH_SIGNALS"
	ActionAssessIdentityRisk  = "ASSESS_IDENTITY_RISK"
	ActionPrepareSessionReset = "PREPARE_SESSION_RESET"
	ActionExecuteSessionReset = "EXECUTE_SESSION_RESET"
	ActionHoldForReview       = "HOLD_FOR_SECURITY_REVIEW"
	ActionRevokeUserAccess    = "REVOKE_USER_ACCESS"
	ActionEscalateIncident    = "ESCALATE_INCIDENT"

	RoleSecurityAgent   = "security_agent"
	RoleIdentityService = "identity_service"
	RoleSecurityLead    = "security_lead"

	LowRiskScoreLimit       = 35
	LowRiskFailedLoginLimit = 20
)

const (
	PermissionAttachSignals       permission.Permission = "security.attach_signals"
	PermissionAssessIdentityRisk  permission.Permission = "security.assess_identity_risk"
	PermissionPrepareSessionReset permission.Permission = "security.prepare_session_reset"
	PermissionExecuteSessionReset permission.Permission = "security.execute_session_reset"
	PermissionHoldForReview       permission.Permission = "security.hold_for_review"
	PermissionRevokeUserAccess    permission.Permission = "security.revoke_user_access"
	PermissionEscalateIncident    permission.Permission = "security.escalate_incident"
	PermissionReviewContainment   permission.Permission = "security.review_containment"
)

var (
	SecurityAgentActor = actor.Actor{
		ID:          "agent-security-response-1",
		Type:        actor.TypeAgent,
		DisplayName: "Security Response Agent",
		Roles:       []string{RoleSecurityAgent},
	}
	IdentityServiceActor = actor.Actor{
		ID:          "identity-control-service",
		Type:        actor.TypeService,
		DisplayName: "Identity Control Service",
		Roles:       []string{RoleIdentityService},
	}
	SecurityLeadActor = actor.Actor{
		ID:          "security-lead-ava",
		Type:        actor.TypeHuman,
		DisplayName: "Ava, Security Lead",
		Roles:       []string{RoleSecurityLead},
	}
)

type IdentityInstruction struct {
	IncidentID             string
	UserID                 string
	UserEmail              string
	AccountID              string
	Reason                 string
	RevocationScope        string
	UserTier               string
	BlastRadius            string
	PrivilegedGroupCount   int64
	DataExfiltrationSignal bool
	IdempotencyKey         string
}

type IdentityOperationReceipt struct {
	OperationID     string `json:"operation_id"`
	Operation       string `json:"operation"`
	IncidentID      string `json:"incident_id"`
	UserID          string `json:"user_id"`
	UserEmail       string `json:"user_email"`
	AccountID       string `json:"account_id"`
	RevocationScope string `json:"revocation_scope,omitempty"`
	IdempotencyKey  string `json:"idempotency_key"`
	CompletedAt     string `json:"completed_at"`
}

type IdentityControlGateway interface {
	ResetSessions(context.Context, IdentityInstruction) (IdentityOperationReceipt, error)
	RevokeAccess(context.Context, IdentityInstruction) (IdentityOperationReceipt, error)
	List() []IdentityOperationReceipt
}

type InMemoryIdentityControl struct {
	mu         sync.Mutex
	sequence   int
	operations []IdentityOperationReceipt
}

func NewInMemoryIdentityControl() *InMemoryIdentityControl {
	return &InMemoryIdentityControl{}
}

func (g *InMemoryIdentityControl) ResetSessions(ctx context.Context, instruction IdentityInstruction) (IdentityOperationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return IdentityOperationReceipt{}, err
	}
	if err := validateResetInstruction(instruction); err != nil {
		return IdentityOperationReceipt{}, err
	}
	return g.record("reset_sessions", instruction)
}

func (g *InMemoryIdentityControl) RevokeAccess(ctx context.Context, instruction IdentityInstruction) (IdentityOperationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return IdentityOperationReceipt{}, err
	}
	if err := validateRevocationInstruction(instruction); err != nil {
		return IdentityOperationReceipt{}, err
	}
	return g.record("revoke_access", instruction)
}

func (g *InMemoryIdentityControl) List() []IdentityOperationReceipt {
	g.mu.Lock()
	defer g.mu.Unlock()

	receipts := make([]IdentityOperationReceipt, len(g.operations))
	copy(receipts, g.operations)
	return receipts
}

func (g *InMemoryIdentityControl) record(operation string, instruction IdentityInstruction) (IdentityOperationReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.sequence++
	receipt := IdentityOperationReceipt{
		OperationID:     fmt.Sprintf("identity-op-%06d", g.sequence),
		Operation:       operation,
		IncidentID:      instruction.IncidentID,
		UserID:          instruction.UserID,
		UserEmail:       instruction.UserEmail,
		AccountID:       instruction.AccountID,
		RevocationScope: instruction.RevocationScope,
		IdempotencyKey:  instruction.IdempotencyKey,
		CompletedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	g.operations = append(g.operations, receipt)
	return receipt, nil
}

func Definition(gateway IdentityControlGateway) (domain.Definition, error) {
	builder := domain.New("security-incident-response").
		Entity(EntitySecurityIncident).
		Event(EventAlertReceived).
		Event(EventSignalsAttached).
		Event(EventLowRiskClassified).
		Event(EventReviewRequired).
		Event(EventSessionResetPrepared).
		Event(EventSessionResetExecuted).
		Event(EventAccessRevoked).
		Event(EventIncidentEscalated).
		Transition(EventAlertReceived, "", StateReceived).
		Transition(EventSignalsAttached, StateReceived, StateSignalsAttached).
		Transition(EventLowRiskClassified, StateSignalsAttached, StateLowRiskReady).
		Transition(EventReviewRequired, StateSignalsAttached, StateReviewRequired).
		Transition(EventReviewRequired, StateLowRiskReady, StateReviewRequired).
		Transition(EventSessionResetPrepared, StateLowRiskReady, StateResetPrepared).
		Transition(EventSessionResetExecuted, StateResetPrepared, StateContained).
		Transition(EventAccessRevoked, StateReviewRequired, StateContained).
		Transition(EventIncidentEscalated, StateReceived, StateEscalated).
		Transition(EventIncidentEscalated, StateSignalsAttached, StateEscalated).
		Transition(EventIncidentEscalated, StateLowRiskReady, StateEscalated).
		Transition(EventIncidentEscalated, StateResetPrepared, StateEscalated).
		Transition(EventIncidentEscalated, StateReviewRequired, StateEscalated).
		Allow(StateReceived, ActionAttachSignals, ActionEscalateIncident).
		Allow(StateSignalsAttached, ActionAssessIdentityRisk, ActionHoldForReview, ActionEscalateIncident).
		Allow(StateLowRiskReady, ActionPrepareSessionReset, ActionHoldForReview, ActionEscalateIncident).
		Allow(StateResetPrepared, ActionExecuteSessionReset, ActionEscalateIncident).
		Allow(StateReviewRequired, ActionRevokeUserAccess, ActionEscalateIncident)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RoleSecurityAgent, PermissionAttachSignals)
	policy.GrantRole(RoleSecurityAgent, PermissionAssessIdentityRisk)
	policy.GrantRole(RoleSecurityAgent, PermissionPrepareSessionReset)
	policy.GrantRole(RoleSecurityAgent, PermissionHoldForReview)
	policy.GrantRole(RoleSecurityAgent, PermissionEscalateIncident)
	policy.GrantRole(RoleIdentityService, PermissionExecuteSessionReset)
	policy.GrantRole(RoleIdentityService, PermissionRevokeUserAccess)
	policy.GrantRole(RoleSecurityLead, PermissionReviewContainment)
	policy.GrantRole(RoleSecurityLead, PermissionEscalateIncident)

	policy.AssignRole(SecurityAgentActor.ID, RoleSecurityAgent)
	policy.AssignRole(IdentityServiceActor.ID, RoleIdentityService)
	policy.AssignRole(SecurityLeadActor.ID, RoleSecurityLead)
	return policy
}

func NewRuntime(gateway IdentityControlGateway) (*runtime.Runtime, error) {
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

func signalParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("incident_id"),
		action.StringParam("user_id"),
		action.StringParam("user_email"),
		action.StringParam("account_id"),
		boundedIntParam("risk_score_percent", 0, 100),
		boundedIntParam("failed_login_count", 0, 100000),
		action.EnumParam("user_tier", "standard", "privileged", "service_account"),
		action.EnumParam("blast_radius", "single_user", "team", "broad"),
		boundedIntParam("privileged_group_count", 0, 1000),
		boolParamSpec("impossible_travel"),
		boolParamSpec("malware_signal"),
		boolParamSpec("data_exfiltration_signal"),
	}
}

func assessmentParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		boundedIntParam("risk_score_percent", 0, 100),
		boundedIntParam("failed_login_count", 0, 100000),
		action.EnumParam("user_tier", "standard", "privileged", "service_account"),
		action.EnumParam("blast_radius", "single_user", "team", "broad"),
		boundedIntParam("privileged_group_count", 0, 1000),
		boolParamSpec("impossible_travel"),
		boolParamSpec("malware_signal"),
		boolParamSpec("data_exfiltration_signal"),
	}
}

func resetParameters() []action.ParameterSpec {
	return stringParams("incident_id", "user_id", "user_email", "account_id", "reason", "idempotency_key")
}

func revocationParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("incident_id"),
		action.StringParam("user_id"),
		action.StringParam("user_email"),
		action.StringParam("account_id"),
		action.StringParam("reason"),
		action.EnumParam("revocation_scope", "session_tokens", "privileged_roles", "all_access"),
		action.EnumParam("user_tier", "standard", "privileged", "service_account"),
		action.EnumParam("blast_radius", "single_user", "team", "broad"),
		boundedIntParam("privileged_group_count", 0, 1000),
		boolParamSpec("data_exfiltration_signal"),
		action.StringParam("idempotency_key"),
	}
}

func Contracts(gateway IdentityControlGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewInMemoryIdentityControl()
	}
	return []action.ActionContract{
		{
			Name:                ActionAttachSignals,
			AllowedStates:       []string{StateReceived},
			Parameters:          signalParameters(),
			RequiredPermissions: []permission.Permission{PermissionAttachSignals},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionAttachSignals, ctx, EventSignalsAttached, "attached identity and detection signals", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionAssessIdentityRisk,
			AllowedStates: []string{StateSignalsAttached},
			Parameters:    assessmentParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := assessmentFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionAssessIdentityRisk},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				assessment, _ := assessmentFromParams(ctx.Parameters)
				payload := copyParams(ctx.Parameters)
				if assessment.LowRisk() {
					payload["low_risk_score_limit"] = LowRiskScoreLimit
					payload["low_risk_failed_login_limit"] = LowRiskFailedLoginLimit
					return eventResult(ActionAssessIdentityRisk, ctx, EventLowRiskClassified, "classified incident as safe for session reset", payload), nil
				}
				payload["review_reason"] = assessment.ReviewReason()
				return eventResult(ActionAssessIdentityRisk, ctx, EventReviewRequired, "routed incident to security review", payload), nil
			},
		},
		{
			Name:          ActionPrepareSessionReset,
			AllowedStates: []string{StateLowRiskReady},
			Parameters:    resetParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := resetInstructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionPrepareSessionReset},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionPrepareSessionReset, ctx, EventSessionResetPrepared, "prepared identity session reset", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionExecuteSessionReset,
			AllowedStates: []string{StateResetPrepared},
			Parameters:    resetParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := resetInstructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionExecuteSessionReset},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := resetInstructionFromParams(actionCtx.Parameters)
				return resetSessions(ctx, gateway, actionCtx, instruction)
			},
		},
		{
			Name:                ActionHoldForReview,
			AllowedStates:       []string{StateSignalsAttached, StateLowRiskReady},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionHoldForReview},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForReview, ctx, EventReviewRequired, "held incident for security review", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionRevokeUserAccess,
			AllowedStates: []string{StateReviewRequired},
			Parameters:    revocationParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := revocationInstructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionRevokeUserAccess},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalNever,
			ApprovalPolicy:      revocationApprovalPolicy,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := revocationInstructionFromParams(actionCtx.Parameters)
				return revokeAccess(ctx, gateway, actionCtx, instruction)
			},
		},
		{
			Name:                ActionEscalateIncident,
			AllowedStates:       []string{StateReceived, StateSignalsAttached, StateLowRiskReady, StateResetPrepared, StateReviewRequired},
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

type IdentityRiskAssessment struct {
	RiskScorePercent       int64
	FailedLoginCount       int64
	UserTier               string
	BlastRadius            string
	PrivilegedGroupCount   int64
	ImpossibleTravel       bool
	MalwareSignal          bool
	DataExfiltrationSignal bool
}

func (a IdentityRiskAssessment) LowRisk() bool {
	return a.RiskScorePercent <= LowRiskScoreLimit &&
		a.FailedLoginCount <= LowRiskFailedLoginLimit &&
		a.UserTier == "standard" &&
		a.BlastRadius == "single_user" &&
		a.PrivilegedGroupCount == 0 &&
		!a.ImpossibleTravel &&
		!a.MalwareSignal &&
		!a.DataExfiltrationSignal
}

func (a IdentityRiskAssessment) ReviewReason() string {
	reasons := []string{}
	if a.RiskScorePercent > LowRiskScoreLimit {
		reasons = append(reasons, fmt.Sprintf("risk score %d is above %d", a.RiskScorePercent, LowRiskScoreLimit))
	}
	if a.FailedLoginCount > LowRiskFailedLoginLimit {
		reasons = append(reasons, fmt.Sprintf("failed login count %d is above %d", a.FailedLoginCount, LowRiskFailedLoginLimit))
	}
	if a.UserTier != "standard" {
		reasons = append(reasons, "user tier is "+a.UserTier)
	}
	if a.BlastRadius != "single_user" {
		reasons = append(reasons, "blast radius is "+a.BlastRadius)
	}
	if a.PrivilegedGroupCount > 0 {
		reasons = append(reasons, fmt.Sprintf("user has %d privileged groups", a.PrivilegedGroupCount))
	}
	if a.ImpossibleTravel {
		reasons = append(reasons, "impossible travel signal present")
	}
	if a.MalwareSignal {
		reasons = append(reasons, "malware signal present")
	}
	if a.DataExfiltrationSignal {
		reasons = append(reasons, "data exfiltration signal present")
	}
	if len(reasons) == 0 {
		return "security review requested"
	}
	return strings.Join(reasons, "; ")
}

func NewAlertReceivedEvent(incidentID, userID, userEmail, accountID string, occurredAt time.Time) event.Event {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-security-alert-%d", incidentID, occurredAt.UnixNano()),
		Type:       EventAlertReceived,
		EntityID:   incidentID,
		EntityType: EntitySecurityIncident,
		Source:     "security-alerts",
		ActorID:    "security-alerts",
		OccurredAt: occurredAt.UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", incidentID),
			CorrelationID: incidentID,
			Tags:          map[string]string{"workflow": "security-incident-response"},
		},
		Payload: map[string]any{
			"incident_id": incidentID,
			"user_id":     userID,
			"user_email":  userEmail,
			"account_id":  accountID,
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
		return state.State{}, fmt.Errorf("security incident %q has no state", incidentID)
	}
	return current, nil
}

func ReviewContainmentApproval(ctx context.Context, rt *runtime.Runtime, approvalID string, reviewer actor.Actor, granted bool, reason string) (approval.Approval, error) {
	if rt == nil {
		return approval.Approval{}, errors.New("runtime is required")
	}
	status := approval.StatusDenied
	if granted {
		status = approval.StatusGranted
	}
	return rt.ReviewApprovalAs(ctx, approvalID, reviewer, runtime.ReviewRequirement{
		Permission:            PermissionReviewContainment,
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

func resetSessions(ctx context.Context, gateway IdentityControlGateway, actionCtx action.ActionContext, instruction IdentityInstruction) (action.ActionResult, error) {
	receipt, err := gateway.ResetSessions(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	return identityResult(ActionExecuteSessionReset, actionCtx, receipt, EventSessionResetExecuted, "reset user sessions through identity control")
}

func revokeAccess(ctx context.Context, gateway IdentityControlGateway, actionCtx action.ActionContext, instruction IdentityInstruction) (action.ActionResult, error) {
	receipt, err := gateway.RevokeAccess(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	return identityResult(ActionRevokeUserAccess, actionCtx, receipt, EventAccessRevoked, "revoked user access through identity control")
}

func identityResult(actionName string, actionCtx action.ActionContext, receipt IdentityOperationReceipt, eventType, summary string) (action.ActionResult, error) {
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output: map[string]any{
			"operation_id":    receipt.OperationID,
			"operation":       receipt.Operation,
			"user_id":         receipt.UserID,
			"idempotency_key": receipt.IdempotencyKey,
		},
		FollowUpEvents: []event.Event{
			newEvent(eventType, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"operation_id":     receipt.OperationID,
				"operation":        receipt.Operation,
				"user_id":          receipt.UserID,
				"user_email":       receipt.UserEmail,
				"account_id":       receipt.AccountID,
				"revocation_scope": receipt.RevocationScope,
				"idempotency_key":  receipt.IdempotencyKey,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func revocationApprovalPolicy(_ context.Context, actionCtx action.ActionContext) (action.ApprovalDecision, error) {
	params := actionCtx.Parameters
	reasons := []string{}
	if scope := stringParam(params, "revocation_scope"); scope == "all_access" {
		reasons = append(reasons, "all-access revocation")
	} else if scope == "privileged_roles" {
		reasons = append(reasons, "privileged-role revocation")
	}
	if tier := stringParam(params, "user_tier"); tier == "privileged" || tier == "service_account" {
		reasons = append(reasons, "user tier is "+tier)
	}
	if blast := stringParam(params, "blast_radius"); blast == "team" || blast == "broad" {
		reasons = append(reasons, "blast radius is "+blast)
	}
	if groups, err := int64Param(params, "privileged_group_count"); err == nil && groups > 0 {
		reasons = append(reasons, fmt.Sprintf("%d privileged groups", groups))
	}
	if boolParam(params, "data_exfiltration_signal") {
		reasons = append(reasons, "data exfiltration signal present")
	}
	if len(reasons) == 0 {
		return action.ApprovalDecision{Required: false, Reason: "standard session-token revocation stays within automation authority"}, nil
	}
	return action.ApprovalDecision{Required: true, Reason: strings.Join(reasons, "; ")}, nil
}

func validateResetInstruction(instruction IdentityInstruction) error {
	if strings.TrimSpace(instruction.IncidentID) == "" {
		return errors.New("incident_id is required")
	}
	if strings.TrimSpace(instruction.UserID) == "" {
		return errors.New("user_id is required")
	}
	if strings.TrimSpace(instruction.UserEmail) == "" {
		return errors.New("user_email is required")
	}
	if strings.TrimSpace(instruction.AccountID) == "" {
		return errors.New("account_id is required")
	}
	if strings.TrimSpace(instruction.Reason) == "" {
		return errors.New("reason is required")
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func validateRevocationInstruction(instruction IdentityInstruction) error {
	if err := validateResetInstruction(instruction); err != nil {
		return err
	}
	if !validRevocationScope(instruction.RevocationScope) {
		return fmt.Errorf("unsupported revocation_scope %q", instruction.RevocationScope)
	}
	if !validUserTier(instruction.UserTier) {
		return fmt.Errorf("unsupported user_tier %q", instruction.UserTier)
	}
	if !validBlastRadius(instruction.BlastRadius) {
		return fmt.Errorf("unsupported blast_radius %q", instruction.BlastRadius)
	}
	if instruction.PrivilegedGroupCount < 0 {
		return errors.New("privileged_group_count must be non-negative")
	}
	return nil
}

func resetInstructionFromParams(params map[string]any) (IdentityInstruction, error) {
	instruction := IdentityInstruction{
		IncidentID:     stringParam(params, "incident_id"),
		UserID:         stringParam(params, "user_id"),
		UserEmail:      stringParam(params, "user_email"),
		AccountID:      stringParam(params, "account_id"),
		Reason:         stringParam(params, "reason"),
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}
	if err := validateResetInstruction(instruction); err != nil {
		return IdentityInstruction{}, err
	}
	return instruction, nil
}

func revocationInstructionFromParams(params map[string]any) (IdentityInstruction, error) {
	groups, err := int64Param(params, "privileged_group_count")
	if err != nil {
		return IdentityInstruction{}, err
	}
	instruction := IdentityInstruction{
		IncidentID:             stringParam(params, "incident_id"),
		UserID:                 stringParam(params, "user_id"),
		UserEmail:              stringParam(params, "user_email"),
		AccountID:              stringParam(params, "account_id"),
		Reason:                 stringParam(params, "reason"),
		RevocationScope:        stringParam(params, "revocation_scope"),
		UserTier:               stringParam(params, "user_tier"),
		BlastRadius:            stringParam(params, "blast_radius"),
		PrivilegedGroupCount:   groups,
		DataExfiltrationSignal: boolParam(params, "data_exfiltration_signal"),
		IdempotencyKey:         stringParam(params, "idempotency_key"),
	}
	if err := validateRevocationInstruction(instruction); err != nil {
		return IdentityInstruction{}, err
	}
	return instruction, nil
}

func assessmentFromParams(params map[string]any) (IdentityRiskAssessment, error) {
	score, err := int64Param(params, "risk_score_percent")
	if err != nil {
		return IdentityRiskAssessment{}, err
	}
	failed, err := int64Param(params, "failed_login_count")
	if err != nil {
		return IdentityRiskAssessment{}, err
	}
	groups, err := int64Param(params, "privileged_group_count")
	if err != nil {
		return IdentityRiskAssessment{}, err
	}
	assessment := IdentityRiskAssessment{
		RiskScorePercent:       score,
		FailedLoginCount:       failed,
		UserTier:               stringParam(params, "user_tier"),
		BlastRadius:            stringParam(params, "blast_radius"),
		PrivilegedGroupCount:   groups,
		ImpossibleTravel:       boolParam(params, "impossible_travel"),
		MalwareSignal:          boolParam(params, "malware_signal"),
		DataExfiltrationSignal: boolParam(params, "data_exfiltration_signal"),
	}
	if !validUserTier(assessment.UserTier) {
		return IdentityRiskAssessment{}, fmt.Errorf("unsupported user_tier %q", assessment.UserTier)
	}
	if !validBlastRadius(assessment.BlastRadius) {
		return IdentityRiskAssessment{}, fmt.Errorf("unsupported blast_radius %q", assessment.BlastRadius)
	}
	return assessment, nil
}

func validRevocationScope(scope string) bool {
	switch scope {
	case "session_tokens", "privileged_roles", "all_access":
		return true
	default:
		return false
	}
}

func validUserTier(tier string) bool {
	switch tier {
	case "standard", "privileged", "service_account":
		return true
	default:
		return false
	}
}

func validBlastRadius(radius string) bool {
	switch radius {
	case "single_user", "team", "broad":
		return true
	default:
		return false
	}
}

func newEvent(eventType, incidentID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", incidentID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   incidentID,
		EntityType: EntitySecurityIncident,
		Source:     "security/executor",
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
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
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
