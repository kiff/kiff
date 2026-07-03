package priorauth

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
	EntityPriorAuthRequest = "PriorAuthRequest"

	EventAuthRequestReceived     = "AUTH_REQUEST_RECEIVED"
	EventClinicalEvidenceAsked   = "CLINICAL_EVIDENCE_REQUESTED"
	EventClinicalEvidenceAdded   = "CLINICAL_EVIDENCE_RECEIVED"
	EventCriteriaChecked         = "CRITERIA_CHECKED"
	EventClinicianReviewRequired = "CLINICIAN_REVIEW_REQUIRED"
	EventAuthorizationPrepared   = "AUTHORIZATION_PREPARED"
	EventAuthorizationSubmitted  = "AUTHORIZATION_SUBMITTED"
	EventAuthRequestWithdrawn    = "AUTH_REQUEST_WITHDRAWN"

	StateReceived         = "RECEIVED"
	StateWaitingEvidence  = "WAITING_CLINICAL_EVIDENCE"
	StateReadyForCriteria = "READY_FOR_CRITERIA"
	StateCriteriaMet      = "CRITERIA_MET"
	StateReviewRequired   = "REVIEW_REQUIRED"
	StatePrepared         = "SUBMISSION_PREPARED"
	StateSubmitted        = "SUBMITTED"
	StateWithdrawn        = "WITHDRAWN"

	ActionRequestClinicalEvidence     = "REQUEST_CLINICAL_EVIDENCE"
	ActionRecordClinicalEvidence      = "RECORD_CLINICAL_EVIDENCE"
	ActionCheckPolicyCriteria         = "CHECK_POLICY_CRITERIA"
	ActionPrepareAuthorization        = "PREPARE_AUTHORIZATION_SUBMISSION"
	ActionHoldForClinicianReview      = "HOLD_FOR_CLINICIAN_REVIEW"
	ActionSubmitAuthorization         = "SUBMIT_AUTHORIZATION"
	ActionSubmitReviewedAuthorization = "SUBMIT_REVIEWED_AUTHORIZATION"
	ActionWithdrawRequest             = "WITHDRAW_REQUEST"

	RolePriorAuthAgent = "prior_auth_agent"
	RolePayerPortal    = "payer_portal_service"
	RoleClinician      = "clinician_reviewer"

	LowDenialRiskLimit = 0.50
)

const (
	PermissionRequestClinicalEvidence     permission.Permission = "priorauth.request_clinical_evidence"
	PermissionRecordClinicalEvidence      permission.Permission = "priorauth.record_clinical_evidence"
	PermissionCheckPolicyCriteria         permission.Permission = "priorauth.check_policy_criteria"
	PermissionPrepareAuthorization        permission.Permission = "priorauth.prepare_authorization"
	PermissionHoldForClinicianReview      permission.Permission = "priorauth.hold_for_clinician_review"
	PermissionSubmitAuthorization         permission.Permission = "priorauth.submit_authorization"
	PermissionSubmitReviewedAuthorization permission.Permission = "priorauth.submit_reviewed_authorization"
	PermissionWithdrawRequest             permission.Permission = "priorauth.withdraw_request"
	PermissionReviewAuthorizationApproval permission.Permission = "priorauth.review_authorization_approval"
)

var (
	PriorAuthAgentActor = actor.Actor{
		ID:          "agent-prior-auth-1",
		Type:        actor.TypeAgent,
		DisplayName: "Prior Authorization Agent",
		Roles:       []string{RolePriorAuthAgent},
	}
	PayerPortalActor = actor.Actor{
		ID:          "payer-portal-service",
		Type:        actor.TypeService,
		DisplayName: "Payer Portal Service",
		Roles:       []string{RolePayerPortal},
	}
	ClinicianReviewerActor = actor.Actor{
		ID:          "clinician-dr-santos",
		Type:        actor.TypeHuman,
		DisplayName: "Dr. Santos, Reviewing Clinician",
		Roles:       []string{RoleClinician},
	}
)

type SubmissionInstruction struct {
	RequestID        string
	PatientID        string
	PayerID          string
	ProcedureCode    string
	EvidencePacketID string
	Reviewed         bool
	IdempotencyKey   string
}

type SubmissionReceipt struct {
	SubmissionID     string `json:"submission_id"`
	RequestID        string `json:"request_id"`
	PatientID        string `json:"patient_id"`
	PayerID          string `json:"payer_id"`
	ProcedureCode    string `json:"procedure_code"`
	EvidencePacketID string `json:"evidence_packet_id"`
	Reviewed         bool   `json:"reviewed"`
	IdempotencyKey   string `json:"idempotency_key"`
	Duplicate        bool   `json:"duplicate"`
	SubmittedAt      string `json:"submitted_at"`
}

type PayerPortalGateway interface {
	Submit(context.Context, SubmissionInstruction) (SubmissionReceipt, error)
	List() []SubmissionReceipt
}

type InMemoryPayerPortal struct {
	mu       sync.Mutex
	sequence int
	byKey    map[string]SubmissionReceipt
	order    []string
}

func NewInMemoryPayerPortal() *InMemoryPayerPortal {
	return &InMemoryPayerPortal{byKey: map[string]SubmissionReceipt{}}
}

func (p *InMemoryPayerPortal) Submit(ctx context.Context, instruction SubmissionInstruction) (SubmissionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return SubmissionReceipt{}, err
	}
	if err := validateSubmissionInstruction(instruction); err != nil {
		return SubmissionReceipt{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.byKey[instruction.IdempotencyKey]; ok {
		existing.Duplicate = true
		return existing, nil
	}

	p.sequence++
	receipt := SubmissionReceipt{
		SubmissionID:     fmt.Sprintf("pa-sub-%06d", p.sequence),
		RequestID:        instruction.RequestID,
		PatientID:        instruction.PatientID,
		PayerID:          instruction.PayerID,
		ProcedureCode:    instruction.ProcedureCode,
		EvidencePacketID: instruction.EvidencePacketID,
		Reviewed:         instruction.Reviewed,
		IdempotencyKey:   instruction.IdempotencyKey,
		SubmittedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	p.byKey[instruction.IdempotencyKey] = receipt
	p.order = append(p.order, instruction.IdempotencyKey)
	return receipt, nil
}

func (p *InMemoryPayerPortal) List() []SubmissionReceipt {
	p.mu.Lock()
	defer p.mu.Unlock()

	receipts := make([]SubmissionReceipt, 0, len(p.order))
	for _, key := range p.order {
		receipts = append(receipts, p.byKey[key])
	}
	return receipts
}

func Definition(gateway PayerPortalGateway) (domain.Definition, error) {
	builder := domain.New("healthcare-prior-auth").
		Entity(EntityPriorAuthRequest).
		Event(EventAuthRequestReceived).
		Event(EventClinicalEvidenceAsked).
		Event(EventClinicalEvidenceAdded).
		Event(EventCriteriaChecked).
		Event(EventClinicianReviewRequired).
		Event(EventAuthorizationPrepared).
		Event(EventAuthorizationSubmitted).
		Event(EventAuthRequestWithdrawn).
		Transition(EventAuthRequestReceived, "", StateReceived).
		Transition(EventClinicalEvidenceAsked, StateReceived, StateWaitingEvidence).
		Transition(EventClinicalEvidenceAdded, StateReceived, StateReadyForCriteria).
		Transition(EventClinicalEvidenceAdded, StateWaitingEvidence, StateReadyForCriteria).
		Transition(EventCriteriaChecked, StateReadyForCriteria, StateCriteriaMet).
		Transition(EventClinicianReviewRequired, StateReadyForCriteria, StateReviewRequired).
		Transition(EventClinicianReviewRequired, StateCriteriaMet, StateReviewRequired).
		Transition(EventAuthorizationPrepared, StateCriteriaMet, StatePrepared).
		Transition(EventAuthorizationSubmitted, StatePrepared, StateSubmitted).
		Transition(EventAuthorizationSubmitted, StateReviewRequired, StateSubmitted).
		Transition(EventAuthRequestWithdrawn, StateReceived, StateWithdrawn).
		Transition(EventAuthRequestWithdrawn, StateWaitingEvidence, StateWithdrawn).
		Transition(EventAuthRequestWithdrawn, StateReadyForCriteria, StateWithdrawn).
		Transition(EventAuthRequestWithdrawn, StateCriteriaMet, StateWithdrawn).
		Transition(EventAuthRequestWithdrawn, StateReviewRequired, StateWithdrawn).
		Transition(EventAuthRequestWithdrawn, StatePrepared, StateWithdrawn).
		Allow(StateReceived, ActionRequestClinicalEvidence, ActionRecordClinicalEvidence, ActionWithdrawRequest).
		Allow(StateWaitingEvidence, ActionRecordClinicalEvidence, ActionWithdrawRequest).
		Allow(StateReadyForCriteria, ActionCheckPolicyCriteria, ActionHoldForClinicianReview, ActionWithdrawRequest).
		Allow(StateCriteriaMet, ActionPrepareAuthorization, ActionHoldForClinicianReview, ActionWithdrawRequest).
		Allow(StatePrepared, ActionSubmitAuthorization, ActionWithdrawRequest).
		Allow(StateReviewRequired, ActionSubmitReviewedAuthorization, ActionWithdrawRequest)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RolePriorAuthAgent, PermissionRequestClinicalEvidence)
	policy.GrantRole(RolePriorAuthAgent, PermissionRecordClinicalEvidence)
	policy.GrantRole(RolePriorAuthAgent, PermissionCheckPolicyCriteria)
	policy.GrantRole(RolePriorAuthAgent, PermissionPrepareAuthorization)
	policy.GrantRole(RolePriorAuthAgent, PermissionHoldForClinicianReview)
	policy.GrantRole(RolePriorAuthAgent, PermissionWithdrawRequest)
	policy.GrantRole(RolePayerPortal, PermissionSubmitAuthorization)
	policy.GrantRole(RolePayerPortal, PermissionSubmitReviewedAuthorization)
	policy.GrantRole(RoleClinician, PermissionReviewAuthorizationApproval)
	policy.GrantRole(RoleClinician, PermissionWithdrawRequest)

	policy.AssignRole(PriorAuthAgentActor.ID, RolePriorAuthAgent)
	policy.AssignRole(PayerPortalActor.ID, RolePayerPortal)
	policy.AssignRole(ClinicianReviewerActor.ID, RoleClinician)
	return policy
}

func NewRuntime(gateway PayerPortalGateway) (*runtime.Runtime, error) {
	definition, err := Definition(gateway)
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(definition, runtime.Config{PermissionPolicy: Policy()})
}

func Contracts(gateway PayerPortalGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewInMemoryPayerPortal()
	}
	return []action.ActionContract{
		{
			Name:                ActionRequestClinicalEvidence,
			AllowedStates:       []string{StateReceived},
			RequiredParameters:  []string{"missing_items"},
			RequiredPermissions: []permission.Permission{PermissionRequestClinicalEvidence},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRequestClinicalEvidence, ctx, EventClinicalEvidenceAsked, "requested missing clinical evidence", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionRecordClinicalEvidence,
			AllowedStates:       []string{StateReceived, StateWaitingEvidence},
			RequiredParameters:  []string{"patient_id", "payer_id", "procedure_code", "evidence_packet_id"},
			RequiredPermissions: []permission.Permission{PermissionRecordClinicalEvidence},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				if err := validateEvidenceParameters(ctx.Parameters); err != nil {
					return action.ActionResult{}, err
				}
				return eventResult(ActionRecordClinicalEvidence, ctx, EventClinicalEvidenceAdded, "recorded clinical evidence packet", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionCheckPolicyCriteria,
			AllowedStates:       []string{StateReadyForCriteria},
			RequiredParameters:  []string{"criteria_met", "denial_risk_score", "missing_evidence"},
			RequiredPermissions: []permission.Permission{PermissionCheckPolicyCriteria},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				check, err := criteriaCheckFromParams(ctx.Parameters)
				if err != nil {
					return action.ActionResult{}, err
				}
				payload := copyParams(ctx.Parameters)
				if check.Automatable() {
					payload["low_denial_risk_limit"] = LowDenialRiskLimit
					return eventResult(ActionCheckPolicyCriteria, ctx, EventCriteriaChecked, "policy criteria met with low denial risk", payload), nil
				}
				payload["review_reason"] = check.ReviewReason()
				return eventResult(ActionCheckPolicyCriteria, ctx, EventClinicianReviewRequired, "routed authorization request to clinician review", payload), nil
			},
		},
		{
			Name:                ActionPrepareAuthorization,
			AllowedStates:       []string{StateCriteriaMet},
			RequiredParameters:  []string{"request_id", "patient_id", "payer_id", "procedure_code", "evidence_packet_id", "idempotency_key"},
			RequiredPermissions: []permission.Permission{PermissionPrepareAuthorization},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				if _, err := instructionFromParams(ctx.Parameters, false); err != nil {
					return action.ActionResult{}, err
				}
				return eventResult(ActionPrepareAuthorization, ctx, EventAuthorizationPrepared, "prepared payer authorization submission", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionHoldForClinicianReview,
			AllowedStates:       []string{StateReadyForCriteria, StateCriteriaMet},
			RequiredParameters:  []string{"reason"},
			RequiredPermissions: []permission.Permission{PermissionHoldForClinicianReview},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForClinicianReview, ctx, EventClinicianReviewRequired, "held request for clinician review", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionSubmitAuthorization,
			AllowedStates:       []string{StatePrepared},
			RequiredParameters:  []string{"request_id", "patient_id", "payer_id", "procedure_code", "evidence_packet_id", "idempotency_key"},
			RequiredPermissions: []permission.Permission{PermissionSubmitAuthorization},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, err := instructionFromParams(actionCtx.Parameters, false)
				if err != nil {
					return action.ActionResult{}, err
				}
				return submitAuthorization(ctx, gateway, ActionSubmitAuthorization, actionCtx, instruction)
			},
		},
		{
			Name:                ActionSubmitReviewedAuthorization,
			AllowedStates:       []string{StateReviewRequired},
			RequiredParameters:  []string{"request_id", "patient_id", "payer_id", "procedure_code", "evidence_packet_id", "idempotency_key", "clinician_note"},
			RequiredPermissions: []permission.Permission{PermissionSubmitReviewedAuthorization},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalRequired,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, err := instructionFromParams(actionCtx.Parameters, true)
				if err != nil {
					return action.ActionResult{}, err
				}
				return submitAuthorization(ctx, gateway, ActionSubmitReviewedAuthorization, actionCtx, instruction)
			},
		},
		{
			Name:                ActionWithdrawRequest,
			AllowedStates:       []string{StateReceived, StateWaitingEvidence, StateReadyForCriteria, StateCriteriaMet, StateReviewRequired, StatePrepared},
			RequiredParameters:  []string{"reason"},
			RequiredPermissions: []permission.Permission{PermissionWithdrawRequest},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionWithdrawRequest, ctx, EventAuthRequestWithdrawn, "withdrew authorization request", ctx.Parameters), nil
			},
		},
	}
}

type CriteriaCheck struct {
	CriteriaMet     bool
	DenialRiskScore float64
	MissingEvidence bool
}

func (c CriteriaCheck) Automatable() bool {
	return c.CriteriaMet && c.DenialRiskScore < LowDenialRiskLimit && !c.MissingEvidence
}

func (c CriteriaCheck) ReviewReason() string {
	reasons := []string{}
	if !c.CriteriaMet {
		reasons = append(reasons, "policy criteria not fully met")
	}
	if c.DenialRiskScore >= LowDenialRiskLimit {
		reasons = append(reasons, fmt.Sprintf("denial risk %.2f is above %.2f", c.DenialRiskScore, LowDenialRiskLimit))
	}
	if c.MissingEvidence {
		reasons = append(reasons, "clinical evidence is incomplete")
	}
	if len(reasons) == 0 {
		return "clinician review requested"
	}
	return strings.Join(reasons, "; ")
}

func NewAuthRequestReceivedEvent(requestID, patientID, payerID, procedureCode string, occurredAt time.Time) event.Event {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-auth-received-%d", requestID, occurredAt.UnixNano()),
		Type:       EventAuthRequestReceived,
		EntityID:   requestID,
		EntityType: EntityPriorAuthRequest,
		Source:     "prior-auth-intake",
		ActorID:    "prior-auth-intake",
		OccurredAt: occurredAt.UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", requestID),
			CorrelationID: requestID,
			Tags:          map[string]string{"workflow": "healthcare-prior-auth"},
		},
		Payload: map[string]any{
			"request_id":     requestID,
			"patient_id":     patientID,
			"payer_id":       payerID,
			"procedure_code": procedureCode,
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

func CurrentState(ctx context.Context, rt *runtime.Runtime, requestID string) (state.State, error) {
	current, ok, err := rt.States.Current(ctx, requestID)
	if err != nil {
		return state.State{}, err
	}
	if !ok {
		return state.State{}, fmt.Errorf("prior auth request %q has no state", requestID)
	}
	return current, nil
}

func ReviewAuthorizationApproval(ctx context.Context, rt *runtime.Runtime, approvalID string, reviewer actor.Actor, granted bool, reason string) (approval.Approval, error) {
	if rt == nil {
		return approval.Approval{}, errors.New("runtime is required")
	}
	if !rt.Permissions.Can(ctx, reviewer, PermissionReviewAuthorizationApproval) {
		return approval.Approval{}, fmt.Errorf("%w: %q", action.ErrPermissionDenied, PermissionReviewAuthorizationApproval)
	}
	status := approval.StatusDenied
	if granted {
		status = approval.StatusGranted
	}
	return rt.ReviewApproval(ctx, approvalID, reviewer.ID, status, reason)
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

func submitAuthorization(ctx context.Context, gateway PayerPortalGateway, actionName string, actionCtx action.ActionContext, instruction SubmissionInstruction) (action.ActionResult, error) {
	receipt, err := gateway.Submit(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	summary := "submitted authorization request through payer portal"
	if receipt.Duplicate {
		summary = "idempotent authorization submission returned existing receipt"
	}
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output: map[string]any{
			"submission_id":   receipt.SubmissionID,
			"procedure_code":  receipt.ProcedureCode,
			"idempotency_key": receipt.IdempotencyKey,
			"duplicate":       receipt.Duplicate,
		},
		FollowUpEvents: []event.Event{
			newEvent(EventAuthorizationSubmitted, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"submission_id":      receipt.SubmissionID,
				"patient_id":         receipt.PatientID,
				"payer_id":           receipt.PayerID,
				"procedure_code":     receipt.ProcedureCode,
				"evidence_packet_id": receipt.EvidencePacketID,
				"reviewed":           receipt.Reviewed,
				"idempotency_key":    receipt.IdempotencyKey,
				"duplicate":          receipt.Duplicate,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func validateEvidenceParameters(params map[string]any) error {
	if strings.TrimSpace(stringParam(params, "patient_id")) == "" {
		return errors.New("patient_id is required")
	}
	if strings.TrimSpace(stringParam(params, "payer_id")) == "" {
		return errors.New("payer_id is required")
	}
	if strings.TrimSpace(stringParam(params, "procedure_code")) == "" {
		return errors.New("procedure_code is required")
	}
	if strings.TrimSpace(stringParam(params, "evidence_packet_id")) == "" {
		return errors.New("evidence_packet_id is required")
	}
	return nil
}

func validateSubmissionInstruction(instruction SubmissionInstruction) error {
	if strings.TrimSpace(instruction.RequestID) == "" {
		return errors.New("request_id is required")
	}
	if strings.TrimSpace(instruction.PatientID) == "" {
		return errors.New("patient_id is required")
	}
	if strings.TrimSpace(instruction.PayerID) == "" {
		return errors.New("payer_id is required")
	}
	if strings.TrimSpace(instruction.ProcedureCode) == "" {
		return errors.New("procedure_code is required")
	}
	if strings.TrimSpace(instruction.EvidencePacketID) == "" {
		return errors.New("evidence_packet_id is required")
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func instructionFromParams(params map[string]any, reviewed bool) (SubmissionInstruction, error) {
	instruction := SubmissionInstruction{
		RequestID:        stringParam(params, "request_id"),
		PatientID:        stringParam(params, "patient_id"),
		PayerID:          stringParam(params, "payer_id"),
		ProcedureCode:    strings.ToUpper(stringParam(params, "procedure_code")),
		EvidencePacketID: stringParam(params, "evidence_packet_id"),
		Reviewed:         reviewed,
		IdempotencyKey:   stringParam(params, "idempotency_key"),
	}
	if err := validateSubmissionInstruction(instruction); err != nil {
		return SubmissionInstruction{}, err
	}
	return instruction, nil
}

func criteriaCheckFromParams(params map[string]any) (CriteriaCheck, error) {
	score, err := float64Param(params, "denial_risk_score")
	if err != nil {
		return CriteriaCheck{}, err
	}
	if score < 0 || score > 1 {
		return CriteriaCheck{}, errors.New("denial_risk_score must be between 0 and 1")
	}
	return CriteriaCheck{
		CriteriaMet:     boolParam(params, "criteria_met"),
		DenialRiskScore: score,
		MissingEvidence: boolParam(params, "missing_evidence"),
	}, nil
}

func newEvent(eventType, requestID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", requestID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   requestID,
		EntityType: EntityPriorAuthRequest,
		Source:     "priorauth/executor",
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

func float64Param(params map[string]any, key string) (float64, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%f", &parsed); err != nil {
			return 0, fmt.Errorf("%s must be a number", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}
