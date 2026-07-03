package procurement

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
	EntityPurchaseRequest = "PurchaseRequest"

	EventRequestReceived   = "PURCHASE_REQUEST_RECEIVED"
	EventQuoteAttached     = "QUOTE_ATTACHED"
	EventBudgetChecked     = "BUDGET_CHECKED"
	EventLowRiskClassified = "LOW_RISK_CLASSIFIED"
	EventReviewRequired    = "PROCUREMENT_REVIEW_REQUIRED"
	EventPOPrepared        = "PURCHASE_ORDER_PREPARED"
	EventPOCreated         = "PURCHASE_ORDER_CREATED"
	EventRequestRejected   = "PURCHASE_REQUEST_REJECTED"

	StateReceived       = "RECEIVED"
	StateQuoteAttached  = "QUOTE_ATTACHED"
	StateBudgetVerified = "BUDGET_VERIFIED"
	StateLowRiskReady   = "LOW_RISK_READY"
	StateReviewRequired = "REVIEW_REQUIRED"
	StatePOPrepared     = "PO_PREPARED"
	StateOrdered        = "ORDERED"
	StateRejected       = "REJECTED"

	ActionAttachQuote        = "ATTACH_QUOTE"
	ActionCheckBudget        = "CHECK_BUDGET"
	ActionAssessPurchaseRisk = "ASSESS_PURCHASE_RISK"
	ActionPrepareStandardPO  = "PREPARE_STANDARD_PO"
	ActionCreateStandardPO   = "CREATE_STANDARD_PO"
	ActionCreateApprovedPO   = "CREATE_APPROVED_PO"
	ActionHoldForReview      = "HOLD_FOR_PROCUREMENT_REVIEW"
	ActionRejectRequest      = "REJECT_PURCHASE_REQUEST"

	RoleProcurementAgent   = "procurement_agent"
	RoleERPService         = "erp_service"
	RoleProcurementManager = "procurement_manager"

	AutonomousPOLimitCents = 250000
)

const (
	PermissionAttachQuote        permission.Permission = "procurement.attach_quote"
	PermissionCheckBudget        permission.Permission = "procurement.check_budget"
	PermissionAssessPurchaseRisk permission.Permission = "procurement.assess_purchase_risk"
	PermissionPrepareStandardPO  permission.Permission = "procurement.prepare_standard_po"
	PermissionCreateStandardPO   permission.Permission = "procurement.create_standard_po"
	PermissionCreateApprovedPO   permission.Permission = "procurement.create_approved_po"
	PermissionHoldForReview      permission.Permission = "procurement.hold_for_review"
	PermissionRejectRequest      permission.Permission = "procurement.reject_request"
	PermissionReviewPurchase     permission.Permission = "procurement.review_purchase"
)

var (
	ProcurementAgentActor = actor.Actor{
		ID:          "agent-procurement-1",
		Type:        actor.TypeAgent,
		DisplayName: "Procurement Agent",
		Roles:       []string{RoleProcurementAgent},
	}
	ERPServiceActor = actor.Actor{
		ID:          "erp-purchasing-service",
		Type:        actor.TypeService,
		DisplayName: "ERP Purchasing Service",
		Roles:       []string{RoleERPService},
	}
	ProcurementManagerActor = actor.Actor{
		ID:          "procurement-manager-ava",
		Type:        actor.TypeHuman,
		DisplayName: "Ava, Procurement Manager",
		Roles:       []string{RoleProcurementManager},
	}
)

type POInstruction struct {
	RequestID       string
	RequesterID     string
	Department      string
	VendorID        string
	VendorName      string
	ItemDescription string
	AmountCents     int64
	Currency        string
	CostCenter      string
	IdempotencyKey  string
}

type POReceipt struct {
	PurchaseOrderID string `json:"purchase_order_id"`
	RequestID       string `json:"request_id"`
	VendorID        string `json:"vendor_id"`
	VendorName      string `json:"vendor_name"`
	AmountCents     int64  `json:"amount_cents"`
	Currency        string `json:"currency"`
	CostCenter      string `json:"cost_center"`
	IdempotencyKey  string `json:"idempotency_key"`
	CreatedAt       string `json:"created_at"`
}

type PurchasingGateway interface {
	CreatePurchaseOrder(context.Context, POInstruction) (POReceipt, error)
	List() []POReceipt
}

type InMemoryPurchasingGateway struct {
	mu       sync.Mutex
	sequence int
	orders   []POReceipt
}

func NewInMemoryPurchasingGateway() *InMemoryPurchasingGateway {
	return &InMemoryPurchasingGateway{}
}

func (g *InMemoryPurchasingGateway) CreatePurchaseOrder(ctx context.Context, instruction POInstruction) (POReceipt, error) {
	if err := ctx.Err(); err != nil {
		return POReceipt{}, err
	}
	if err := validatePOInstruction(instruction); err != nil {
		return POReceipt{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	g.sequence++
	receipt := POReceipt{
		PurchaseOrderID: fmt.Sprintf("po-%06d", g.sequence),
		RequestID:       instruction.RequestID,
		VendorID:        instruction.VendorID,
		VendorName:      instruction.VendorName,
		AmountCents:     instruction.AmountCents,
		Currency:        instruction.Currency,
		CostCenter:      instruction.CostCenter,
		IdempotencyKey:  instruction.IdempotencyKey,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	g.orders = append(g.orders, receipt)
	return receipt, nil
}

func (g *InMemoryPurchasingGateway) List() []POReceipt {
	g.mu.Lock()
	defer g.mu.Unlock()

	orders := make([]POReceipt, len(g.orders))
	copy(orders, g.orders)
	return orders
}

func Definition(gateway PurchasingGateway) (domain.Definition, error) {
	builder := domain.New("procurement-purchase-order").
		Entity(EntityPurchaseRequest).
		Event(EventRequestReceived).
		Event(EventQuoteAttached).
		Event(EventBudgetChecked).
		Event(EventLowRiskClassified).
		Event(EventReviewRequired).
		Event(EventPOPrepared).
		Event(EventPOCreated).
		Event(EventRequestRejected).
		Transition(EventRequestReceived, "", StateReceived).
		Transition(EventQuoteAttached, StateReceived, StateQuoteAttached).
		Transition(EventBudgetChecked, StateQuoteAttached, StateBudgetVerified).
		Transition(EventLowRiskClassified, StateBudgetVerified, StateLowRiskReady).
		Transition(EventReviewRequired, StateBudgetVerified, StateReviewRequired).
		Transition(EventReviewRequired, StateLowRiskReady, StateReviewRequired).
		Transition(EventPOPrepared, StateLowRiskReady, StatePOPrepared).
		Transition(EventPOCreated, StatePOPrepared, StateOrdered).
		Transition(EventPOCreated, StateReviewRequired, StateOrdered).
		Transition(EventRequestRejected, StateReceived, StateRejected).
		Transition(EventRequestRejected, StateQuoteAttached, StateRejected).
		Transition(EventRequestRejected, StateBudgetVerified, StateRejected).
		Transition(EventRequestRejected, StateLowRiskReady, StateRejected).
		Transition(EventRequestRejected, StateReviewRequired, StateRejected).
		Transition(EventRequestRejected, StatePOPrepared, StateRejected).
		Allow(StateReceived, ActionAttachQuote, ActionRejectRequest).
		Allow(StateQuoteAttached, ActionCheckBudget, ActionRejectRequest).
		Allow(StateBudgetVerified, ActionAssessPurchaseRisk, ActionHoldForReview, ActionRejectRequest).
		Allow(StateLowRiskReady, ActionPrepareStandardPO, ActionHoldForReview, ActionRejectRequest).
		Allow(StatePOPrepared, ActionCreateStandardPO, ActionRejectRequest).
		Allow(StateReviewRequired, ActionCreateApprovedPO, ActionRejectRequest)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RoleProcurementAgent, PermissionAttachQuote)
	policy.GrantRole(RoleProcurementAgent, PermissionCheckBudget)
	policy.GrantRole(RoleProcurementAgent, PermissionAssessPurchaseRisk)
	policy.GrantRole(RoleProcurementAgent, PermissionPrepareStandardPO)
	policy.GrantRole(RoleProcurementAgent, PermissionHoldForReview)
	policy.GrantRole(RoleProcurementAgent, PermissionRejectRequest)
	policy.GrantRole(RoleERPService, PermissionCreateStandardPO)
	policy.GrantRole(RoleERPService, PermissionCreateApprovedPO)
	policy.GrantRole(RoleProcurementManager, PermissionReviewPurchase)
	policy.GrantRole(RoleProcurementManager, PermissionRejectRequest)

	policy.AssignRole(ProcurementAgentActor.ID, RoleProcurementAgent)
	policy.AssignRole(ERPServiceActor.ID, RoleERPService)
	policy.AssignRole(ProcurementManagerActor.ID, RoleProcurementManager)
	return policy
}

func NewRuntime(gateway PurchasingGateway) (*runtime.Runtime, error) {
	definition, err := Definition(gateway)
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(definition, runtime.Config{PermissionPolicy: Policy()})
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

func positiveIntParam(name string) action.ParameterSpec {
	min := int64(1)
	spec := action.IntParam(name)
	spec.Min = &min
	return spec
}

func quoteParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("request_id"),
		action.StringParam("requester_id"),
		action.StringParam("department"),
		action.StringParam("vendor_id"),
		action.StringParam("vendor_name"),
		action.StringParam("item_description"),
		positiveIntParam("amount_cents"),
		action.EnumParam("currency", "USD", "EUR"),
		action.StringParam("quote_id"),
		boolParamSpec("new_vendor"),
		boolParamSpec("sole_source"),
	}
}

func budgetParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("cost_center"),
		boolParamSpec("budget_available"),
		boolParamSpec("security_review_required"),
	}
}

func assessmentParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		positiveIntParam("amount_cents"),
		action.EnumParam("currency", "USD", "EUR"),
		boolParamSpec("approved_vendor"),
		boolParamSpec("budget_available"),
		boolParamSpec("new_vendor"),
		boolParamSpec("sole_source"),
		boolParamSpec("security_review_required"),
	}
}

func poParameters() []action.ParameterSpec {
	return []action.ParameterSpec{
		action.StringParam("request_id"),
		action.StringParam("requester_id"),
		action.StringParam("department"),
		action.StringParam("vendor_id"),
		action.StringParam("vendor_name"),
		action.StringParam("item_description"),
		positiveIntParam("amount_cents"),
		action.EnumParam("currency", "USD", "EUR"),
		action.StringParam("cost_center"),
		action.StringParam("idempotency_key"),
	}
}

func approvedPOParameters() []action.ParameterSpec {
	return append(poParameters(),
		boolParamSpec("approved_vendor"),
		boolParamSpec("budget_available"),
		boolParamSpec("new_vendor"),
		boolParamSpec("sole_source"),
		boolParamSpec("security_review_required"),
	)
}

func Contracts(gateway PurchasingGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewInMemoryPurchasingGateway()
	}
	return []action.ActionContract{
		{
			Name:                ActionAttachQuote,
			AllowedStates:       []string{StateReceived},
			Parameters:          quoteParameters(),
			RequiredPermissions: []permission.Permission{PermissionAttachQuote},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionAttachQuote, ctx, EventQuoteAttached, "attached supplier quote and sourcing facts", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionCheckBudget,
			AllowedStates:       []string{StateQuoteAttached},
			Parameters:          budgetParameters(),
			RequiredPermissions: []permission.Permission{PermissionCheckBudget},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionCheckBudget, ctx, EventBudgetChecked, "checked budget and security-review requirements", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionAssessPurchaseRisk,
			AllowedStates: []string{StateBudgetVerified},
			Parameters:    assessmentParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := assessmentFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionAssessPurchaseRisk},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				assessment, _ := assessmentFromParams(ctx.Parameters)
				payload := copyParams(ctx.Parameters)
				if assessment.LowRisk() {
					payload["autonomous_po_limit_cents"] = AutonomousPOLimitCents
					return eventResult(ActionAssessPurchaseRisk, ctx, EventLowRiskClassified, "classified purchase as low-risk standard PO", payload), nil
				}
				payload["review_reason"] = assessment.ReviewReason()
				return eventResult(ActionAssessPurchaseRisk, ctx, EventReviewRequired, "routed purchase to procurement review", payload), nil
			},
		},
		{
			Name:          ActionPrepareStandardPO,
			AllowedStates: []string{StateLowRiskReady},
			Parameters:    poParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionPrepareStandardPO},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionPrepareStandardPO, ctx, EventPOPrepared, "prepared standard purchase order", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionCreateStandardPO,
			AllowedStates: []string{StatePOPrepared},
			Parameters:    poParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionCreateStandardPO},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := instructionFromParams(actionCtx.Parameters)
				return createPO(ctx, gateway, ActionCreateStandardPO, actionCtx, instruction)
			},
		},
		{
			Name:                ActionHoldForReview,
			AllowedStates:       []string{StateBudgetVerified, StateLowRiskReady},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionHoldForReview},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForReview, ctx, EventReviewRequired, "held purchase for procurement review", ctx.Parameters), nil
			},
		},
		{
			Name:          ActionCreateApprovedPO,
			AllowedStates: []string{StateReviewRequired},
			Parameters:    approvedPOParameters(),
			ValidateParameters: func(_ context.Context, ctx action.ActionContext) error {
				_, err := instructionFromParams(ctx.Parameters)
				return err
			},
			RequiredPermissions: []permission.Permission{PermissionCreateApprovedPO},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalNever,
			ApprovalPolicy:      approvedPOApprovalPolicy,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, _ := instructionFromParams(actionCtx.Parameters)
				return createPO(ctx, gateway, ActionCreateApprovedPO, actionCtx, instruction)
			},
		},
		{
			Name:                ActionRejectRequest,
			AllowedStates:       []string{StateReceived, StateQuoteAttached, StateBudgetVerified, StateLowRiskReady, StateReviewRequired, StatePOPrepared},
			Parameters:          []action.ParameterSpec{action.StringParam("reason")},
			RequiredPermissions: []permission.Permission{PermissionRejectRequest},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRejectRequest, ctx, EventRequestRejected, "rejected purchase request", ctx.Parameters), nil
			},
		},
	}
}

type PurchaseAssessment struct {
	AmountCents            int64
	Currency               string
	ApprovedVendor         bool
	BudgetAvailable        bool
	NewVendor              bool
	SoleSource             bool
	SecurityReviewRequired bool
}

func (a PurchaseAssessment) LowRisk() bool {
	return a.AmountCents <= AutonomousPOLimitCents &&
		a.ApprovedVendor &&
		a.BudgetAvailable &&
		!a.NewVendor &&
		!a.SoleSource &&
		!a.SecurityReviewRequired
}

func (a PurchaseAssessment) ReviewReason() string {
	reasons := []string{}
	if a.AmountCents > AutonomousPOLimitCents {
		reasons = append(reasons, fmt.Sprintf("amount %d cents exceeds autonomous limit %d", a.AmountCents, AutonomousPOLimitCents))
	}
	if !a.ApprovedVendor {
		reasons = append(reasons, "vendor is not approved")
	}
	if !a.BudgetAvailable {
		reasons = append(reasons, "budget is not available")
	}
	if a.NewVendor {
		reasons = append(reasons, "new vendor")
	}
	if a.SoleSource {
		reasons = append(reasons, "sole-source purchase")
	}
	if a.SecurityReviewRequired {
		reasons = append(reasons, "security review required")
	}
	if len(reasons) == 0 {
		return "procurement review requested"
	}
	return strings.Join(reasons, "; ")
}

func NewPurchaseRequestReceivedEvent(requestID, requesterID, department string, occurredAt time.Time) event.Event {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-purchase-request-%d", requestID, occurredAt.UnixNano()),
		Type:       EventRequestReceived,
		EntityID:   requestID,
		EntityType: EntityPurchaseRequest,
		Source:     "procurement-intake",
		ActorID:    "procurement-intake",
		OccurredAt: occurredAt.UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", requestID),
			CorrelationID: requestID,
			Tags:          map[string]string{"workflow": "procurement-purchase-order"},
		},
		Payload: map[string]any{
			"request_id":   requestID,
			"requester_id": requesterID,
			"department":   department,
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
		return state.State{}, fmt.Errorf("purchase request %q has no state", requestID)
	}
	return current, nil
}

func ReviewPurchaseApproval(ctx context.Context, rt *runtime.Runtime, approvalID string, reviewer actor.Actor, granted bool, reason string) (approval.Approval, error) {
	if rt == nil {
		return approval.Approval{}, errors.New("runtime is required")
	}
	status := approval.StatusDenied
	if granted {
		status = approval.StatusGranted
	}
	return rt.ReviewApprovalAs(ctx, approvalID, reviewer, runtime.ReviewRequirement{
		Permission:            PermissionReviewPurchase,
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
		FollowUpEvents: []event.Event{newEvent(eventType, ctx.EntityID, ctx.Actor.ID, payload)},
		ExecutedAt:     time.Now().UTC(),
	}
}

func createPO(ctx context.Context, gateway PurchasingGateway, actionName string, actionCtx action.ActionContext, instruction POInstruction) (action.ActionResult, error) {
	receipt, err := gateway.CreatePurchaseOrder(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: "created purchase order through ERP purchasing service",
		Output: map[string]any{
			"purchase_order_id": receipt.PurchaseOrderID,
			"vendor_id":         receipt.VendorID,
			"amount_cents":      receipt.AmountCents,
			"currency":          receipt.Currency,
			"idempotency_key":   receipt.IdempotencyKey,
		},
		FollowUpEvents: []event.Event{
			newEvent(EventPOCreated, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"purchase_order_id": receipt.PurchaseOrderID,
				"vendor_id":         receipt.VendorID,
				"vendor_name":       receipt.VendorName,
				"amount_cents":      receipt.AmountCents,
				"currency":          receipt.Currency,
				"cost_center":       receipt.CostCenter,
				"idempotency_key":   receipt.IdempotencyKey,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func approvedPOApprovalPolicy(_ context.Context, actionCtx action.ActionContext) (action.ApprovalDecision, error) {
	params := actionCtx.Parameters
	reasons := []string{}
	if amount, err := int64Param(params, "amount_cents"); err == nil && amount > AutonomousPOLimitCents {
		reasons = append(reasons, fmt.Sprintf("amount %d cents exceeds autonomous limit %d", amount, AutonomousPOLimitCents))
	}
	if !boolParam(params, "approved_vendor") {
		reasons = append(reasons, "vendor is not approved")
	}
	if !boolParam(params, "budget_available") {
		reasons = append(reasons, "budget is not available")
	}
	if boolParam(params, "new_vendor") {
		reasons = append(reasons, "new vendor")
	}
	if boolParam(params, "sole_source") {
		reasons = append(reasons, "sole-source purchase")
	}
	if boolParam(params, "security_review_required") {
		reasons = append(reasons, "security review required")
	}
	if len(reasons) == 0 {
		return action.ApprovalDecision{Required: false, Reason: "purchase is within autonomous policy"}, nil
	}
	return action.ApprovalDecision{Required: true, Reason: strings.Join(reasons, "; ")}, nil
}

func validatePOInstruction(instruction POInstruction) error {
	if strings.TrimSpace(instruction.RequestID) == "" {
		return errors.New("request_id is required")
	}
	if strings.TrimSpace(instruction.RequesterID) == "" {
		return errors.New("requester_id is required")
	}
	if strings.TrimSpace(instruction.Department) == "" {
		return errors.New("department is required")
	}
	if strings.TrimSpace(instruction.VendorID) == "" {
		return errors.New("vendor_id is required")
	}
	if strings.TrimSpace(instruction.VendorName) == "" {
		return errors.New("vendor_name is required")
	}
	if strings.TrimSpace(instruction.ItemDescription) == "" {
		return errors.New("item_description is required")
	}
	if instruction.AmountCents <= 0 {
		return errors.New("amount_cents must be positive")
	}
	if instruction.Currency != "USD" && instruction.Currency != "EUR" {
		return fmt.Errorf("unsupported currency %q", instruction.Currency)
	}
	if strings.TrimSpace(instruction.CostCenter) == "" {
		return errors.New("cost_center is required")
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func instructionFromParams(params map[string]any) (POInstruction, error) {
	amount, err := int64Param(params, "amount_cents")
	if err != nil {
		return POInstruction{}, err
	}
	instruction := POInstruction{
		RequestID:       stringParam(params, "request_id"),
		RequesterID:     stringParam(params, "requester_id"),
		Department:      stringParam(params, "department"),
		VendorID:        stringParam(params, "vendor_id"),
		VendorName:      stringParam(params, "vendor_name"),
		ItemDescription: stringParam(params, "item_description"),
		AmountCents:     amount,
		Currency:        stringParam(params, "currency"),
		CostCenter:      stringParam(params, "cost_center"),
		IdempotencyKey:  stringParam(params, "idempotency_key"),
	}
	if err := validatePOInstruction(instruction); err != nil {
		return POInstruction{}, err
	}
	return instruction, nil
}

func assessmentFromParams(params map[string]any) (PurchaseAssessment, error) {
	amount, err := int64Param(params, "amount_cents")
	if err != nil {
		return PurchaseAssessment{}, err
	}
	currency := stringParam(params, "currency")
	if currency != "USD" && currency != "EUR" {
		return PurchaseAssessment{}, fmt.Errorf("unsupported currency %q", currency)
	}
	return PurchaseAssessment{
		AmountCents:            amount,
		Currency:               currency,
		ApprovedVendor:         boolParam(params, "approved_vendor"),
		BudgetAvailable:        boolParam(params, "budget_available"),
		NewVendor:              boolParam(params, "new_vendor"),
		SoleSource:             boolParam(params, "sole_source"),
		SecurityReviewRequired: boolParam(params, "security_review_required"),
	}, nil
}

func newEvent(eventType, requestID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", requestID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   requestID,
		EntityType: EntityPurchaseRequest,
		Source:     "procurement/executor",
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
