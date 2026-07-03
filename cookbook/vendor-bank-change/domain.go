package vendorbank

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
	EntityVendorBankChange = "VendorBankChange"

	EventChangeRequested    = "BANK_CHANGE_REQUESTED"
	EventEvidenceAttached   = "EVIDENCE_ATTACHED"
	EventVendorVerified     = "VENDOR_VERIFIED"
	EventLowRiskClassified  = "LOW_RISK_CLASSIFIED"
	EventReviewRequired     = "REVIEW_REQUIRED"
	EventChangePrepared     = "BANK_CHANGE_PREPARED"
	EventBankDetailsUpdated = "BANK_DETAILS_UPDATED"
	EventChangeRejected     = "BANK_CHANGE_REJECTED"

	StateReceived         = "RECEIVED"
	StateEvidenceAttached = "EVIDENCE_ATTACHED"
	StateVendorVerified   = "VENDOR_VERIFIED"
	StateLowRiskReady     = "LOW_RISK_READY"
	StateReviewRequired   = "REVIEW_REQUIRED"
	StateChangePrepared   = "CHANGE_PREPARED"
	StateUpdated          = "UPDATED"
	StateRejected         = "REJECTED"

	ActionAttachEvidence       = "ATTACH_EVIDENCE"
	ActionVerifyVendor         = "VERIFY_VENDOR"
	ActionAssessBankChange     = "ASSESS_BANK_CHANGE"
	ActionPrepareKnownAccount  = "PREPARE_KNOWN_ACCOUNT_CHANGE"
	ActionHoldForFinanceReview = "HOLD_FOR_FINANCE_REVIEW"
	ActionApplyKnownAccount    = "APPLY_KNOWN_ACCOUNT_CHANGE"
	ActionApplyApprovedChange  = "APPLY_APPROVED_BANK_CHANGE"
	ActionRejectChange         = "REJECT_BANK_CHANGE"

	RoleVendorAgent   = "vendor_agent"
	RoleVendorMaster  = "vendor_master_service"
	RoleFinanceReview = "finance_controller"

	LowRiskScoreLimit         = 30
	LowRiskExposureLimitCents = 100000
)

const (
	PermissionAttachEvidence       permission.Permission = "vendors.attach_evidence"
	PermissionVerifyVendor         permission.Permission = "vendors.verify_vendor"
	PermissionAssessBankChange     permission.Permission = "vendors.assess_bank_change"
	PermissionPrepareKnownAccount  permission.Permission = "vendors.prepare_known_account"
	PermissionHoldForFinanceReview permission.Permission = "vendors.hold_for_finance_review"
	PermissionApplyKnownAccount    permission.Permission = "vendors.apply_known_account"
	PermissionApplyApprovedChange  permission.Permission = "vendors.apply_approved_change"
	PermissionRejectChange         permission.Permission = "vendors.reject_change"
	PermissionReviewBankChange     permission.Permission = "vendors.review_bank_change"
)

var (
	VendorAgentActor = actor.Actor{
		ID:          "agent-vendor-risk-1",
		Type:        actor.TypeAgent,
		DisplayName: "Vendor Risk Agent",
		Roles:       []string{RoleVendorAgent},
	}
	VendorMasterActor = actor.Actor{
		ID:          "vendor-master-service",
		Type:        actor.TypeService,
		DisplayName: "Vendor Master Service",
		Roles:       []string{RoleVendorMaster},
	}
	FinanceControllerActor = actor.Actor{
		ID:          "finance-controller-ava",
		Type:        actor.TypeHuman,
		DisplayName: "Ava, Finance Controller",
		Roles:       []string{RoleFinanceReview},
	}
)

type BankChangeInstruction struct {
	ChangeID           string
	VendorID           string
	VendorName         string
	AccountFingerprint string
	AccountCountry     string
	EvidencePacketID   string
	IdempotencyKey     string
}

type BankChangeReceipt struct {
	UpdateID           string `json:"update_id"`
	ChangeID           string `json:"change_id"`
	VendorID           string `json:"vendor_id"`
	VendorName         string `json:"vendor_name"`
	AccountFingerprint string `json:"account_fingerprint"`
	AccountCountry     string `json:"account_country"`
	IdempotencyKey     string `json:"idempotency_key"`
	Duplicate          bool   `json:"duplicate"`
	AppliedAt          string `json:"applied_at"`
}

type VendorMasterGateway interface {
	ApplyBankChange(context.Context, BankChangeInstruction) (BankChangeReceipt, error)
	List() []BankChangeReceipt
}

type InMemoryVendorMaster struct {
	mu       sync.Mutex
	sequence int
	byKey    map[string]BankChangeReceipt
	order    []string
}

func NewInMemoryVendorMaster() *InMemoryVendorMaster {
	return &InMemoryVendorMaster{byKey: map[string]BankChangeReceipt{}}
}

func (m *InMemoryVendorMaster) ApplyBankChange(ctx context.Context, instruction BankChangeInstruction) (BankChangeReceipt, error) {
	if err := ctx.Err(); err != nil {
		return BankChangeReceipt{}, err
	}
	if err := validateInstruction(instruction); err != nil {
		return BankChangeReceipt{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.byKey[instruction.IdempotencyKey]; ok {
		existing.Duplicate = true
		return existing, nil
	}

	m.sequence++
	receipt := BankChangeReceipt{
		UpdateID:           fmt.Sprintf("vendor-update-%06d", m.sequence),
		ChangeID:           instruction.ChangeID,
		VendorID:           instruction.VendorID,
		VendorName:         instruction.VendorName,
		AccountFingerprint: instruction.AccountFingerprint,
		AccountCountry:     instruction.AccountCountry,
		IdempotencyKey:     instruction.IdempotencyKey,
		AppliedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	m.byKey[instruction.IdempotencyKey] = receipt
	m.order = append(m.order, instruction.IdempotencyKey)
	return receipt, nil
}

func (m *InMemoryVendorMaster) List() []BankChangeReceipt {
	m.mu.Lock()
	defer m.mu.Unlock()

	receipts := make([]BankChangeReceipt, 0, len(m.order))
	for _, key := range m.order {
		receipts = append(receipts, m.byKey[key])
	}
	return receipts
}

func Definition(gateway VendorMasterGateway) (domain.Definition, error) {
	builder := domain.New("vendor-bank-change").
		Entity(EntityVendorBankChange).
		Event(EventChangeRequested).
		Event(EventEvidenceAttached).
		Event(EventVendorVerified).
		Event(EventLowRiskClassified).
		Event(EventReviewRequired).
		Event(EventChangePrepared).
		Event(EventBankDetailsUpdated).
		Event(EventChangeRejected).
		Transition(EventChangeRequested, "", StateReceived).
		Transition(EventEvidenceAttached, StateReceived, StateEvidenceAttached).
		Transition(EventVendorVerified, StateEvidenceAttached, StateVendorVerified).
		Transition(EventLowRiskClassified, StateVendorVerified, StateLowRiskReady).
		Transition(EventReviewRequired, StateVendorVerified, StateReviewRequired).
		Transition(EventReviewRequired, StateLowRiskReady, StateReviewRequired).
		Transition(EventChangePrepared, StateLowRiskReady, StateChangePrepared).
		Transition(EventBankDetailsUpdated, StateChangePrepared, StateUpdated).
		Transition(EventBankDetailsUpdated, StateReviewRequired, StateUpdated).
		Transition(EventChangeRejected, StateReceived, StateRejected).
		Transition(EventChangeRejected, StateEvidenceAttached, StateRejected).
		Transition(EventChangeRejected, StateVendorVerified, StateRejected).
		Transition(EventChangeRejected, StateLowRiskReady, StateRejected).
		Transition(EventChangeRejected, StateReviewRequired, StateRejected).
		Transition(EventChangeRejected, StateChangePrepared, StateRejected).
		Allow(StateReceived, ActionAttachEvidence, ActionRejectChange).
		Allow(StateEvidenceAttached, ActionVerifyVendor, ActionRejectChange).
		Allow(StateVendorVerified, ActionAssessBankChange, ActionHoldForFinanceReview, ActionRejectChange).
		Allow(StateLowRiskReady, ActionPrepareKnownAccount, ActionHoldForFinanceReview, ActionRejectChange).
		Allow(StateChangePrepared, ActionApplyKnownAccount, ActionRejectChange).
		Allow(StateReviewRequired, ActionApplyApprovedChange, ActionRejectChange)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RoleVendorAgent, PermissionAttachEvidence)
	policy.GrantRole(RoleVendorAgent, PermissionVerifyVendor)
	policy.GrantRole(RoleVendorAgent, PermissionAssessBankChange)
	policy.GrantRole(RoleVendorAgent, PermissionPrepareKnownAccount)
	policy.GrantRole(RoleVendorAgent, PermissionHoldForFinanceReview)
	policy.GrantRole(RoleVendorAgent, PermissionRejectChange)
	policy.GrantRole(RoleVendorMaster, PermissionApplyKnownAccount)
	policy.GrantRole(RoleVendorMaster, PermissionApplyApprovedChange)
	policy.GrantRole(RoleFinanceReview, PermissionReviewBankChange)
	policy.GrantRole(RoleFinanceReview, PermissionRejectChange)

	policy.AssignRole(VendorAgentActor.ID, RoleVendorAgent)
	policy.AssignRole(VendorMasterActor.ID, RoleVendorMaster)
	policy.AssignRole(FinanceControllerActor.ID, RoleFinanceReview)
	return policy
}

func NewRuntime(gateway VendorMasterGateway) (*runtime.Runtime, error) {
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

func exposureParam() action.ParameterSpec {
	return boundedIntParam("open_invoice_exposure_cents", 0, 1000000000)
}

func instructionParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("change_id"),
		action.StringParam("vendor_id"),
		action.StringParam("vendor_name"),
		action.StringParam("account_fingerprint"),
		action.EnumParam("account_country", "US", "CA", "GB", "FR", "DE", "MX"),
		action.StringParam("evidence_packet_id"),
		action.StringParam("idempotency_key"),
	}
}

func assessmentParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		boundedIntParam("risk_score_percent", 0, 100),
		boolParamSpec("known_account"),
		boolParamSpec("callback_verified"),
		exposureParam(),
		boolParamSpec("fraud_signal"),
	}
}

func Contracts(gateway VendorMasterGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewInMemoryVendorMaster()
	}
	return []action.ActionContract{
		{
			Name:                ActionAttachEvidence,
			AllowedStates:       []string{StateReceived},
			Parameters:          stringParams("change_id", "vendor_id", "vendor_name", "account_fingerprint", "account_country", "evidence_packet_id", "requester_email"),
			ValidateParameters:  func(_ context.Context, ctx action.ActionContext) error { return validateEvidence(ctx.Parameters) },
			RequiredPermissions: []permission.Permission{PermissionAttachEvidence},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionAttachEvidence, ctx, EventEvidenceAttached, "attached vendor bank-change evidence", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionVerifyVendor,
			AllowedStates: []string{StateEvidenceAttached},
			Parameters: []action.ParameterSpec{
				action.StringParam("vendor_id"),
				boolParamSpec("existing_vendor"),
				boolParamSpec("tax_id_match"),
				boolParamSpec("callback_verified"),
			},
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				return validateVendorVerification(ctx.Parameters)
			},
			RequiredPermissions: []permission.Permission{PermissionVerifyVendor},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionVerifyVendor, ctx, EventVendorVerified, "verified vendor identity and callback", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionAssessBankChange,
			AllowedStates: []string{StateVendorVerified},
			Parameters:    assessmentParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := assessmentFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionAssessBankChange},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				assessment, _ := assessmentFromParams(ctx.Parameters)
				payload := copyParams(ctx.Parameters)
				if assessment.LowRisk() {
					payload["low_risk_score_limit"] = LowRiskScoreLimit
					payload["low_risk_exposure_limit_cents"] = LowRiskExposureLimitCents
					return eventResult(ActionAssessBankChange, ctx, EventLowRiskClassified, "classified change as low risk known-account update", payload), nil
				}
				payload["review_reason"] = assessment.ReviewReason()
				return eventResult(ActionAssessBankChange, ctx, EventReviewRequired, "routed bank change to finance review", payload), nil
			},
		},
		{
			Name:          ActionPrepareKnownAccount,
			AllowedStates: []string{StateLowRiskReady},
			Parameters:    instructionParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionPrepareKnownAccount},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionPrepareKnownAccount, ctx, EventChangePrepared, "prepared known-account vendor bank update", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionHoldForFinanceReview,
			AllowedStates:       []string{StateVendorVerified, StateLowRiskReady},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionHoldForFinanceReview},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForFinanceReview, ctx, EventReviewRequired, "held bank change for finance review", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionApplyKnownAccount,
			AllowedStates: []string{StateChangePrepared},
			Parameters:    instructionParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionApplyKnownAccount},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := instructionFromParams(actionCtx.Parameters)
				return applyBankChange(ctx, gateway, ActionApplyKnownAccount, actionCtx, instruction)
			},
		},
		{
			Name:          ActionApplyApprovedChange,
			AllowedStates: []string{StateReviewRequired},
			Parameters:    instructionParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionApplyApprovedChange},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalRequired,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := instructionFromParams(actionCtx.Parameters)
				return applyBankChange(ctx, gateway, ActionApplyApprovedChange, actionCtx, instruction)
			},
		},
		{
			Name:                ActionRejectChange,
			AllowedStates:       []string{StateReceived, StateEvidenceAttached, StateVendorVerified, StateLowRiskReady, StateReviewRequired, StateChangePrepared},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionRejectChange},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRejectChange, ctx, EventChangeRejected, "rejected vendor bank change", ctx.Parameters), nil
			},
		},
	}
}

type BankChangeAssessment struct {
	RiskScorePercent         int64
	KnownAccount             bool
	CallbackVerified         bool
	OpenInvoiceExposureCents int64
	FraudSignal              bool
}

func (a BankChangeAssessment) LowRisk() bool {
	return a.RiskScorePercent <= LowRiskScoreLimit &&
		a.KnownAccount &&
		a.CallbackVerified &&
		a.OpenInvoiceExposureCents <= LowRiskExposureLimitCents &&
		!a.FraudSignal
}

func (a BankChangeAssessment) ReviewReason() string {
	reasons := []string{}
	if a.RiskScorePercent > LowRiskScoreLimit {
		reasons = append(reasons, fmt.Sprintf("risk score %d is above %d", a.RiskScorePercent, LowRiskScoreLimit))
	}
	if !a.KnownAccount {
		reasons = append(reasons, "account is not previously known")
	}
	if !a.CallbackVerified {
		reasons = append(reasons, "vendor callback is not verified")
	}
	if a.OpenInvoiceExposureCents > LowRiskExposureLimitCents {
		reasons = append(reasons, fmt.Sprintf("open invoice exposure %d cents is above %d", a.OpenInvoiceExposureCents, LowRiskExposureLimitCents))
	}
	if a.FraudSignal {
		reasons = append(reasons, "fraud signal present")
	}
	if len(reasons) == 0 {
		return "finance review requested"
	}
	return strings.Join(reasons, "; ")
}

func NewChangeRequestedEvent(changeID, vendorID, vendorName string, occurredAt time.Time) event.Event {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-bank-change-requested-%d", changeID, occurredAt.UnixNano()),
		Type:       EventChangeRequested,
		EntityID:   changeID,
		EntityType: EntityVendorBankChange,
		Source:     "vendor-portal",
		ActorID:    "vendor-portal",
		OccurredAt: occurredAt.UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", changeID),
			CorrelationID: changeID,
			Tags:          map[string]string{"workflow": "vendor-bank-change"},
		},
		Payload: map[string]any{
			"change_id":   changeID,
			"vendor_id":   vendorID,
			"vendor_name": vendorName,
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

func CurrentState(ctx context.Context, rt *runtime.Runtime, changeID string) (state.State, error) {
	current, ok, err := rt.States.Current(ctx, changeID)
	if err != nil {
		return state.State{}, err
	}
	if !ok {
		return state.State{}, fmt.Errorf("vendor bank change %q has no state", changeID)
	}
	return current, nil
}

func ReviewBankChangeApproval(ctx context.Context, rt *runtime.Runtime, approvalID string, reviewer actor.Actor, granted bool, reason string) (approval.Approval, error) {
	if rt == nil {
		return approval.Approval{}, errors.New("runtime is required")
	}
	status := approval.StatusDenied
	if granted {
		status = approval.StatusGranted
	}
	return rt.ReviewApprovalAs(ctx, approvalID, reviewer, runtime.ReviewRequirement{
		Permission:            PermissionReviewBankChange,
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

func applyBankChange(ctx context.Context, gateway VendorMasterGateway, actionName string, actionCtx action.ActionContext, instruction BankChangeInstruction) (action.ActionResult, error) {
	receipt, err := gateway.ApplyBankChange(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	summary := "applied vendor bank detail change"
	if receipt.Duplicate {
		summary = "idempotent vendor bank change returned existing receipt"
	}
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output: map[string]any{
			"update_id":       receipt.UpdateID,
			"vendor_id":       receipt.VendorID,
			"idempotency_key": receipt.IdempotencyKey,
			"duplicate":       receipt.Duplicate,
		},
		FollowUpEvents: []event.Event{
			newEvent(EventBankDetailsUpdated, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"update_id":           receipt.UpdateID,
				"vendor_id":           receipt.VendorID,
				"vendor_name":         receipt.VendorName,
				"account_fingerprint": receipt.AccountFingerprint,
				"account_country":     receipt.AccountCountry,
				"idempotency_key":     receipt.IdempotencyKey,
				"duplicate":           receipt.Duplicate,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func validateEvidence(params map[string]any) error {
	if strings.TrimSpace(stringParam(params, "requester_email")) == "" {
		return errors.New("requester_email is required")
	}
	if country := stringParam(params, "account_country"); !validCountry(country) {
		return fmt.Errorf("unsupported account_country %q", country)
	}
	return nil
}

func validateVendorVerification(params map[string]any) error {
	if !boolParam(params, "existing_vendor") {
		return errors.New("existing_vendor must be true")
	}
	if !boolParam(params, "tax_id_match") {
		return errors.New("tax_id_match must be true")
	}
	if !boolParam(params, "callback_verified") {
		return errors.New("callback_verified must be true")
	}
	return nil
}

func validateInstruction(instruction BankChangeInstruction) error {
	if strings.TrimSpace(instruction.ChangeID) == "" {
		return errors.New("change_id is required")
	}
	if strings.TrimSpace(instruction.VendorID) == "" {
		return errors.New("vendor_id is required")
	}
	if strings.TrimSpace(instruction.VendorName) == "" {
		return errors.New("vendor_name is required")
	}
	if strings.TrimSpace(instruction.AccountFingerprint) == "" {
		return errors.New("account_fingerprint is required")
	}
	if !validCountry(instruction.AccountCountry) {
		return fmt.Errorf("unsupported account_country %q", instruction.AccountCountry)
	}
	if strings.TrimSpace(instruction.EvidencePacketID) == "" {
		return errors.New("evidence_packet_id is required")
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func instructionFromParams(params map[string]any) (BankChangeInstruction, error) {
	instruction := BankChangeInstruction{
		ChangeID:           stringParam(params, "change_id"),
		VendorID:           stringParam(params, "vendor_id"),
		VendorName:         stringParam(params, "vendor_name"),
		AccountFingerprint: stringParam(params, "account_fingerprint"),
		AccountCountry:     stringParam(params, "account_country"),
		EvidencePacketID:   stringParam(params, "evidence_packet_id"),
		IdempotencyKey:     stringParam(params, "idempotency_key"),
	}
	if err := validateInstruction(instruction); err != nil {
		return BankChangeInstruction{}, err
	}
	return instruction, nil
}

func assessmentFromParams(params map[string]any) (BankChangeAssessment, error) {
	score, err := int64Param(params, "risk_score_percent")
	if err != nil {
		return BankChangeAssessment{}, err
	}
	exposure, err := int64Param(params, "open_invoice_exposure_cents")
	if err != nil {
		return BankChangeAssessment{}, err
	}
	return BankChangeAssessment{
		RiskScorePercent:         score,
		KnownAccount:             boolParam(params, "known_account"),
		CallbackVerified:         boolParam(params, "callback_verified"),
		OpenInvoiceExposureCents: exposure,
		FraudSignal:              boolParam(params, "fraud_signal"),
	}, nil
}

func validCountry(country string) bool {
	switch country {
	case "US", "CA", "GB", "FR", "DE", "MX":
		return true
	default:
		return false
	}
}

func newEvent(eventType, changeID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", changeID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   changeID,
		EntityType: EntityVendorBankChange,
		Source:     "vendorbank/executor",
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
