package claimstriage

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
	EntityClaim = "InsuranceClaim"

	EventClaimReceived     = "CLAIM_RECEIVED"
	EventEvidenceRequested = "EVIDENCE_REQUESTED"
	EventEvidenceReceived  = "EVIDENCE_RECEIVED"
	EventCoverageVerified  = "COVERAGE_VERIFIED"
	EventLowRiskAssessed   = "LOW_RISK_ASSESSED"
	EventReviewRequired    = "REVIEW_REQUIRED"
	EventPayoutPrepared    = "PAYOUT_PREPARED"
	EventPayoutIssued      = "PAYOUT_ISSUED"
	EventClaimDenied       = "CLAIM_DENIED"

	StateReceived         = "RECEIVED"
	StateWaitingEvidence  = "WAITING_EVIDENCE"
	StateCoverageVerified = "COVERAGE_VERIFIED"
	StateLowRiskReady     = "LOW_RISK_READY"
	StateReviewRequired   = "REVIEW_REQUIRED"
	StatePayoutPrepared   = "PAYOUT_PREPARED"
	StatePaid             = "PAID"
	StateDenied           = "DENIED"

	ActionRequestEvidence       = "REQUEST_EVIDENCE"
	ActionRecordEvidence        = "RECORD_EVIDENCE"
	ActionVerifyCoverage        = "VERIFY_COVERAGE"
	ActionAssessRisk            = "ASSESS_RISK"
	ActionPrepareLowValuePayout = "PREPARE_LOW_VALUE_PAYOUT"
	ActionHoldForAdjuster       = "HOLD_FOR_ADJUSTER"
	ActionIssueLowValuePayout   = "ISSUE_LOW_VALUE_PAYOUT"
	ActionIssueApprovedPayout   = "ISSUE_APPROVED_PAYOUT"
	ActionDenyClaim             = "DENY_CLAIM"

	RoleClaimsAgent   = "claims_agent"
	RoleClaimsService = "claims_service"
	RoleAdjuster      = "claim_adjuster"

	LowValuePayoutLimitCents = 100000
	LowRiskScoreLimit        = 50
)

const (
	PermissionRequestEvidence       permission.Permission = "claims.request_evidence"
	PermissionRecordEvidence        permission.Permission = "claims.record_evidence"
	PermissionVerifyCoverage        permission.Permission = "claims.verify_coverage"
	PermissionAssessRisk            permission.Permission = "claims.assess_risk"
	PermissionPrepareLowValuePayout permission.Permission = "claims.prepare_low_value_payout"
	PermissionHoldForAdjuster       permission.Permission = "claims.hold_for_adjuster"
	PermissionIssueLowValuePayout   permission.Permission = "claims.issue_low_value_payout"
	PermissionIssueApprovedPayout   permission.Permission = "claims.issue_approved_payout"
	PermissionDenyClaim             permission.Permission = "claims.deny_claim"
	PermissionReviewPayoutApproval  permission.Permission = "claims.review_payout_approval"
)

var (
	ClaimsAgentActor = actor.Actor{
		ID:          "agent-claims-1",
		Type:        actor.TypeAgent,
		DisplayName: "Claims Triage Agent",
		Roles:       []string{RoleClaimsAgent},
	}
	ClaimsServiceActor = actor.Actor{
		ID:          "claims-core-service",
		Type:        actor.TypeService,
		DisplayName: "Claims Core Service",
		Roles:       []string{RoleClaimsService},
	}
	AdjusterActor = actor.Actor{
		ID:          "adjuster-marta",
		Type:        actor.TypeHuman,
		DisplayName: "Marta, Senior Adjuster",
		Roles:       []string{RoleAdjuster},
	}
)

type PayoutInstruction struct {
	ClaimID        string
	ClaimantID     string
	PolicyID       string
	AmountCents    int64
	Currency       string
	IdempotencyKey string
}

type PayoutReceipt struct {
	PayoutID       string `json:"payout_id"`
	ClaimID        string `json:"claim_id"`
	ClaimantID     string `json:"claimant_id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"`
	IssuedAt       string `json:"issued_at"`
}

type PayoutGateway interface {
	Issue(context.Context, PayoutInstruction) (PayoutReceipt, error)
	List() []PayoutReceipt
}

type LedgerPayoutGateway struct {
	mu       sync.Mutex
	sequence int
	byKey    map[string]PayoutReceipt
	order    []string
}

func NewLedgerPayoutGateway() *LedgerPayoutGateway {
	return &LedgerPayoutGateway{byKey: map[string]PayoutReceipt{}}
}

func (g *LedgerPayoutGateway) Issue(ctx context.Context, instruction PayoutInstruction) (PayoutReceipt, error) {
	if err := ctx.Err(); err != nil {
		return PayoutReceipt{}, err
	}
	if err := validatePayoutInstruction(instruction); err != nil {
		return PayoutReceipt{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if existing, ok := g.byKey[instruction.IdempotencyKey]; ok {
		existing.Duplicate = true
		return existing, nil
	}

	g.sequence++
	receipt := PayoutReceipt{
		PayoutID:       fmt.Sprintf("claim-pay-%06d", g.sequence),
		ClaimID:        instruction.ClaimID,
		ClaimantID:     instruction.ClaimantID,
		AmountCents:    instruction.AmountCents,
		Currency:       instruction.Currency,
		IdempotencyKey: instruction.IdempotencyKey,
		IssuedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	g.byKey[instruction.IdempotencyKey] = receipt
	g.order = append(g.order, instruction.IdempotencyKey)
	return receipt, nil
}

func (g *LedgerPayoutGateway) List() []PayoutReceipt {
	g.mu.Lock()
	defer g.mu.Unlock()

	receipts := make([]PayoutReceipt, 0, len(g.order))
	for _, key := range g.order {
		receipts = append(receipts, g.byKey[key])
	}
	return receipts
}

func Definition(gateway PayoutGateway) (domain.Definition, error) {
	builder := domain.New("insurance-claims-triage").
		Entity(EntityClaim).
		Event(EventClaimReceived).
		Event(EventEvidenceRequested).
		Event(EventEvidenceReceived).
		Event(EventCoverageVerified).
		Event(EventLowRiskAssessed).
		Event(EventReviewRequired).
		Event(EventPayoutPrepared).
		Event(EventPayoutIssued).
		Event(EventClaimDenied).
		Transition(EventClaimReceived, "", StateReceived).
		Transition(EventEvidenceRequested, StateReceived, StateWaitingEvidence).
		Transition(EventEvidenceReceived, StateWaitingEvidence, StateReceived).
		Transition(EventCoverageVerified, StateReceived, StateCoverageVerified).
		Transition(EventLowRiskAssessed, StateCoverageVerified, StateLowRiskReady).
		Transition(EventReviewRequired, StateCoverageVerified, StateReviewRequired).
		Transition(EventReviewRequired, StateLowRiskReady, StateReviewRequired).
		Transition(EventPayoutPrepared, StateLowRiskReady, StatePayoutPrepared).
		Transition(EventPayoutIssued, StatePayoutPrepared, StatePaid).
		Transition(EventPayoutIssued, StateReviewRequired, StatePaid).
		Transition(EventClaimDenied, StateReceived, StateDenied).
		Transition(EventClaimDenied, StateWaitingEvidence, StateDenied).
		Transition(EventClaimDenied, StateCoverageVerified, StateDenied).
		Transition(EventClaimDenied, StateLowRiskReady, StateDenied).
		Transition(EventClaimDenied, StateReviewRequired, StateDenied).
		Transition(EventClaimDenied, StatePayoutPrepared, StateDenied).
		Allow(StateReceived, ActionRequestEvidence, ActionVerifyCoverage, ActionDenyClaim).
		Allow(StateWaitingEvidence, ActionRecordEvidence, ActionDenyClaim).
		Allow(StateCoverageVerified, ActionAssessRisk, ActionHoldForAdjuster, ActionDenyClaim).
		Allow(StateLowRiskReady, ActionPrepareLowValuePayout, ActionHoldForAdjuster, ActionDenyClaim).
		Allow(StatePayoutPrepared, ActionIssueLowValuePayout, ActionDenyClaim).
		Allow(StateReviewRequired, ActionIssueApprovedPayout, ActionDenyClaim)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RoleClaimsAgent, PermissionRequestEvidence)
	policy.GrantRole(RoleClaimsAgent, PermissionRecordEvidence)
	policy.GrantRole(RoleClaimsAgent, PermissionVerifyCoverage)
	policy.GrantRole(RoleClaimsAgent, PermissionAssessRisk)
	policy.GrantRole(RoleClaimsAgent, PermissionPrepareLowValuePayout)
	policy.GrantRole(RoleClaimsAgent, PermissionHoldForAdjuster)
	policy.GrantRole(RoleClaimsService, PermissionIssueLowValuePayout)
	policy.GrantRole(RoleClaimsService, PermissionIssueApprovedPayout)
	policy.GrantRole(RoleAdjuster, PermissionDenyClaim)
	policy.GrantRole(RoleAdjuster, PermissionReviewPayoutApproval)

	policy.AssignRole(ClaimsAgentActor.ID, RoleClaimsAgent)
	policy.AssignRole(ClaimsServiceActor.ID, RoleClaimsService)
	policy.AssignRole(AdjusterActor.ID, RoleAdjuster)
	return policy
}

func NewRuntime(gateway PayoutGateway) (*runtime.Runtime, error) {
	definition, err := Definition(gateway)
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(definition, runtime.Config{
		PermissionPolicy: Policy(),
	})
}

func payoutParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("claim_id"),
		action.StringParam("claimant_id"),
		action.StringParam("policy_id"),
		positiveIntParam("payout_amount_cents"),
		action.EnumParam("currency", "USD", "EUR"),
		action.StringParam("idempotency_key"),
	}
}

func lowValuePayoutParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("claim_id"),
		action.StringParam("claimant_id"),
		action.StringParam("policy_id"),
		boundedIntParam("payout_amount_cents", 1, LowValuePayoutLimitCents),
		action.EnumParam("currency", "USD", "EUR"),
		action.StringParam("idempotency_key"),
	}
}

func positiveIntParam(name string) action.ParameterSpec {
	min := int64(1)
	spec := action.IntParam(name)
	spec.Min = &min
	return spec
}

func boundedIntParam(name string, min, max int64) action.ParameterSpec {
	spec := action.IntParam(name)
	spec.Min = &min
	spec.Max = &max
	return spec
}

func boolParamSpec(name string) action.ParameterSpec {
	return action.ParameterSpec{Name: name, Type: action.ParamBool, Required: true}
}

func Contracts(gateway PayoutGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewLedgerPayoutGateway()
	}
	return []action.ActionContract{
		{
			Name:                ActionRequestEvidence,
			AllowedStates:       []string{StateReceived},
			Parameters:          []action.ParameterSpec{action.StringParam("missing_evidence")},
			RequiredPermissions: []permission.Permission{PermissionRequestEvidence},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRequestEvidence, ctx, EventEvidenceRequested, "requested missing claim evidence", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionRecordEvidence,
			AllowedStates:       []string{StateWaitingEvidence},
			Parameters:          []action.ParameterSpec{action.StringParam("evidence_received")},
			RequiredPermissions: []permission.Permission{PermissionRecordEvidence},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRecordEvidence, ctx, EventEvidenceReceived, "recorded received claim evidence", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionVerifyCoverage,
			AllowedStates: []string{StateReceived},
			Parameters:    []action.ParameterSpec{action.StringParam("claim_id"), action.StringParam("claimant_id"), action.StringParam("policy_id"), action.StringParam("loss_type"), boolParamSpec("coverage_confirmed")},
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				return validateCoverageParameters(ctx.Parameters)
			},
			RequiredPermissions: []permission.Permission{PermissionVerifyCoverage},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionVerifyCoverage, ctx, EventCoverageVerified, "verified policy coverage for the reported loss", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionAssessRisk,
			AllowedStates: []string{StateCoverageVerified},
			Parameters:    []action.ParameterSpec{boundedIntParam("risk_score", 0, 100), positiveIntParam("payout_amount_cents"), action.EnumParam("currency", "USD", "EUR"), boolParamSpec("fraud_signals")},
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := assessmentFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionAssessRisk},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				assessment, _ := assessmentFromParams(ctx.Parameters)
				if assessment.LowRisk() {
					payload := copyParams(ctx.Parameters)
					payload["low_value_limit_cents"] = LowValuePayoutLimitCents
					return eventResult(ActionAssessRisk, ctx, EventLowRiskAssessed, "assessed claim as low risk and low value", payload), nil
				}
				payload := copyParams(ctx.Parameters)
				payload["review_reason"] = assessment.ReviewReason()
				return eventResult(ActionAssessRisk, ctx, EventReviewRequired, "routed claim to adjuster review", payload), nil
			},
		},
		{
			Name:          ActionPrepareLowValuePayout,
			AllowedStates: []string{StateLowRiskReady},
			Parameters:    lowValuePayoutParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionPrepareLowValuePayout},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionPrepareLowValuePayout, ctx, EventPayoutPrepared, "prepared low-value payout instruction", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionHoldForAdjuster,
			AllowedStates:       []string{StateCoverageVerified, StateLowRiskReady},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionHoldForAdjuster},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForAdjuster, ctx, EventReviewRequired, "held claim for adjuster review", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionIssueLowValuePayout,
			AllowedStates: []string{StatePayoutPrepared},
			Parameters:    lowValuePayoutParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionIssueLowValuePayout},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := instructionFromParams(actionCtx.Parameters)
				return issuePayout(ctx, gateway, ActionIssueLowValuePayout, actionCtx, instruction)
			},
		},
		{
			Name:          ActionIssueApprovedPayout,
			AllowedStates: []string{StateReviewRequired},
			Parameters:    payoutParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionIssueApprovedPayout},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalRequired,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := instructionFromParams(actionCtx.Parameters)
				return issuePayout(ctx, gateway, ActionIssueApprovedPayout, actionCtx, instruction)
			},
		},
		{
			Name:                ActionDenyClaim,
			AllowedStates:       []string{StateReceived, StateWaitingEvidence, StateCoverageVerified, StateLowRiskReady, StateReviewRequired, StatePayoutPrepared},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionDenyClaim},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionDenyClaim, ctx, EventClaimDenied, "denied claim after adjuster review", ctx.Parameters), nil
			},
		},
	}
}

type RiskAssessment struct {
	Score             int64
	PayoutAmountCents int64
	Currency          string
	FraudSignals      bool
}

func (a RiskAssessment) LowRisk() bool {
	return a.Score < LowRiskScoreLimit && a.PayoutAmountCents <= LowValuePayoutLimitCents && !a.FraudSignals
}

func (a RiskAssessment) ReviewReason() string {
	reasons := []string{}
	if a.Score >= LowRiskScoreLimit {
		reasons = append(reasons, fmt.Sprintf("risk score %d is above %d", a.Score, LowRiskScoreLimit))
	}
	if a.PayoutAmountCents > LowValuePayoutLimitCents {
		reasons = append(reasons, fmt.Sprintf("payout %d cents is above low-value limit %d", a.PayoutAmountCents, LowValuePayoutLimitCents))
	}
	if a.FraudSignals {
		reasons = append(reasons, "fraud signals present")
	}
	if len(reasons) == 0 {
		return "adjuster review requested"
	}
	return strings.Join(reasons, "; ")
}

func NewClaimReceivedEvent(claimID, claimantID, policyID, lossType string, occurredAt time.Time) event.Event {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-claim-received-%d", claimID, occurredAt.UnixNano()),
		Type:       EventClaimReceived,
		EntityID:   claimID,
		EntityType: EntityClaim,
		Source:     "claims-intake",
		ActorID:    "claims-intake",
		OccurredAt: occurredAt.UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", claimID),
			CorrelationID: claimID,
			Tags:          map[string]string{"workflow": "insurance-claims-triage"},
		},
		Payload: map[string]any{
			"claim_id":    claimID,
			"claimant_id": claimantID,
			"policy_id":   policyID,
			"loss_type":   lossType,
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

func CurrentState(ctx context.Context, rt *runtime.Runtime, claimID string) (state.State, error) {
	current, ok, err := rt.States.Current(ctx, claimID)
	if err != nil {
		return state.State{}, err
	}
	if !ok {
		return state.State{}, fmt.Errorf("claim %q has no state", claimID)
	}
	return current, nil
}

func ReviewPayoutApproval(ctx context.Context, rt *runtime.Runtime, approvalID string, reviewer actor.Actor, granted bool, reason string) (approval.Approval, error) {
	if rt == nil {
		return approval.Approval{}, errors.New("runtime is required")
	}
	status := approval.StatusDenied
	if granted {
		status = approval.StatusGranted
	}
	// The runtime enforces reviewer authority (the adjuster's review
	// permission) and segregation of duties (the requester cannot approve
	// their own payout) before the approval changes.
	return rt.ReviewApprovalAs(ctx, approvalID, reviewer, runtime.ReviewRequirement{
		Permission:            PermissionReviewPayoutApproval,
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

func issuePayout(ctx context.Context, gateway PayoutGateway, actionName string, actionCtx action.ActionContext, instruction PayoutInstruction) (action.ActionResult, error) {
	receipt, err := gateway.Issue(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	summary := "issued claim payout through payout gateway"
	if receipt.Duplicate {
		summary = "idempotent payout issue returned existing receipt"
	}
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output: map[string]any{
			"payout_id":       receipt.PayoutID,
			"amount_cents":    receipt.AmountCents,
			"currency":        receipt.Currency,
			"idempotency_key": receipt.IdempotencyKey,
			"duplicate":       receipt.Duplicate,
		},
		FollowUpEvents: []event.Event{
			newEvent(EventPayoutIssued, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"payout_id":       receipt.PayoutID,
				"claimant_id":     receipt.ClaimantID,
				"amount_cents":    receipt.AmountCents,
				"currency":        receipt.Currency,
				"idempotency_key": receipt.IdempotencyKey,
				"duplicate":       receipt.Duplicate,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func validateCoverageParameters(params map[string]any) error {
	if strings.TrimSpace(stringParam(params, "claim_id")) == "" {
		return errors.New("claim_id is required")
	}
	if strings.TrimSpace(stringParam(params, "claimant_id")) == "" {
		return errors.New("claimant_id is required")
	}
	if strings.TrimSpace(stringParam(params, "policy_id")) == "" {
		return errors.New("policy_id is required")
	}
	if strings.TrimSpace(stringParam(params, "loss_type")) == "" {
		return errors.New("loss_type is required")
	}
	if !boolParam(params, "coverage_confirmed") {
		return errors.New("coverage_confirmed must be true")
	}
	return nil
}

func validatePayoutInstruction(instruction PayoutInstruction) error {
	if strings.TrimSpace(instruction.ClaimID) == "" {
		return errors.New("claim_id is required")
	}
	if strings.TrimSpace(instruction.ClaimantID) == "" {
		return errors.New("claimant_id is required")
	}
	if strings.TrimSpace(instruction.PolicyID) == "" {
		return errors.New("policy_id is required")
	}
	if instruction.AmountCents <= 0 {
		return errors.New("payout_amount_cents must be positive")
	}
	if instruction.Currency != "USD" && instruction.Currency != "EUR" {
		return fmt.Errorf("unsupported currency %q", instruction.Currency)
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func instructionFromParams(params map[string]any) (PayoutInstruction, error) {
	amount, err := int64Param(params, "payout_amount_cents")
	if err != nil {
		return PayoutInstruction{}, err
	}
	instruction := PayoutInstruction{
		ClaimID:        stringParam(params, "claim_id"),
		ClaimantID:     stringParam(params, "claimant_id"),
		PolicyID:       stringParam(params, "policy_id"),
		AmountCents:    amount,
		Currency:       strings.ToUpper(stringParam(params, "currency")),
		IdempotencyKey: stringParam(params, "idempotency_key"),
	}
	if err := validatePayoutInstruction(instruction); err != nil {
		return PayoutInstruction{}, err
	}
	return instruction, nil
}

func assessmentFromParams(params map[string]any) (RiskAssessment, error) {
	score, err := int64Param(params, "risk_score")
	if err != nil {
		return RiskAssessment{}, err
	}
	if score < 0 || score > 100 {
		return RiskAssessment{}, errors.New("risk_score must be between 0 and 100")
	}
	amount, err := int64Param(params, "payout_amount_cents")
	if err != nil {
		return RiskAssessment{}, err
	}
	if amount <= 0 {
		return RiskAssessment{}, errors.New("payout_amount_cents must be positive")
	}
	currency := strings.ToUpper(stringParam(params, "currency"))
	if currency != "USD" && currency != "EUR" {
		return RiskAssessment{}, fmt.Errorf("unsupported currency %q", currency)
	}
	return RiskAssessment{
		Score:             score,
		PayoutAmountCents: amount,
		Currency:          currency,
		FraudSignals:      boolParam(params, "fraud_signals"),
	}, nil
}

func newEvent(eventType, claimID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", claimID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   claimID,
		EntityType: EntityClaim,
		Source:     "claims/executor",
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
