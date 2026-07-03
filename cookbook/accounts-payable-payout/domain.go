package payables

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

const (
	EntityInvoice = "VendorInvoice"

	EventInvoiceReceived      = "INVOICE_RECEIVED"
	EventMissingInfoRequested = "MISSING_INFO_REQUESTED"
	EventInvoiceInfoReceived  = "INVOICE_INFO_RECEIVED"
	EventInvoiceVerified      = "INVOICE_VERIFIED"
	EventPaymentReadyMarked   = "PAYMENT_READY_MARKED"
	EventPaymentHeld          = "PAYMENT_HELD"
	EventPaymentReleased      = "PAYMENT_RELEASED"
	EventInvoiceRejected      = "INVOICE_REJECTED"

	StateReceived        = "RECEIVED"
	StateNeedsInfo       = "NEEDS_INFO"
	StateVerified        = "VERIFIED"
	StateReadyForPayment = "READY_FOR_PAYMENT"
	StatePaymentHeld     = "PAYMENT_HELD"
	StatePaid            = "PAID"
	StateRejected        = "REJECTED"

	ActionRequestMissingInfo     = "REQUEST_MISSING_INFO"
	ActionRecordInfoReceived     = "RECORD_INFO_RECEIVED"
	ActionVerifyInvoice          = "VERIFY_INVOICE"
	ActionMarkReadyForPayment    = "MARK_READY_FOR_PAYMENT"
	ActionHoldForApproval        = "HOLD_FOR_APPROVAL"
	ActionReleaseLowRiskPayment  = "RELEASE_LOW_RISK_PAYMENT"
	ActionReleaseApprovedPayment = "RELEASE_APPROVED_PAYMENT"
	ActionRejectInvoice          = "REJECT_INVOICE"

	RoleAPAgent        = "ap_agent"
	RolePaymentService = "payment_service"
	RoleFinanceManager = "finance_manager"

	LowRiskLimitCents = 50000
)

const (
	PermissionRequestMissingInfo     permission.Permission = "payables.request_missing_info"
	PermissionRecordInfoReceived     permission.Permission = "payables.record_info_received"
	PermissionVerifyInvoice          permission.Permission = "payables.verify_invoice"
	PermissionMarkReadyForPayment    permission.Permission = "payables.mark_ready_for_payment"
	PermissionHoldForApproval        permission.Permission = "payables.hold_for_approval"
	PermissionReleaseLowRiskPayment  permission.Permission = "payables.release_low_risk_payment"
	PermissionReleaseApprovedPayment permission.Permission = "payables.release_approved_payment"
	PermissionRejectInvoice          permission.Permission = "payables.reject_invoice"
)

var (
	APAgentActor = actor.Actor{
		ID:          "agent-ap-1",
		Type:        actor.TypeAgent,
		DisplayName: "AP Agent",
		Roles:       []string{RoleAPAgent},
	}
	PaymentServiceActor = actor.Actor{
		ID:          "payment-service",
		Type:        actor.TypeService,
		DisplayName: "Payment Service",
		Roles:       []string{RolePaymentService},
	}
	FinanceManagerActor = actor.Actor{
		ID:          "finance-manager-ava",
		Type:        actor.TypeHuman,
		DisplayName: "Ava, Finance Manager",
		Roles:       []string{RoleFinanceManager},
	}
)

type PaymentInstruction struct {
	InvoiceID       string
	VendorID        string
	InvoiceNumber   string
	AmountCents     int64
	Currency        string
	BankFingerprint string
	IdempotencyKey  string
}

type PaymentReceipt struct {
	PaymentID      string `json:"payment_id"`
	InvoiceID      string `json:"invoice_id"`
	VendorID       string `json:"vendor_id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"`
	ReleasedAt     string `json:"released_at"`
}

type PaymentGateway interface {
	Release(context.Context, PaymentInstruction) (PaymentReceipt, error)
	List() []PaymentReceipt
}

type LedgerGateway struct {
	mu       sync.Mutex
	sequence int
	byKey    map[string]PaymentReceipt
	order    []string
}

func NewLedgerGateway() *LedgerGateway {
	return &LedgerGateway{byKey: map[string]PaymentReceipt{}}
}

func (g *LedgerGateway) Release(ctx context.Context, instruction PaymentInstruction) (PaymentReceipt, error) {
	if err := ctx.Err(); err != nil {
		return PaymentReceipt{}, err
	}
	if err := validatePaymentInstruction(instruction); err != nil {
		return PaymentReceipt{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if existing, ok := g.byKey[instruction.IdempotencyKey]; ok {
		existing.Duplicate = true
		return existing, nil
	}

	g.sequence++
	receipt := PaymentReceipt{
		PaymentID:      fmt.Sprintf("pay-%06d", g.sequence),
		InvoiceID:      instruction.InvoiceID,
		VendorID:       instruction.VendorID,
		AmountCents:    instruction.AmountCents,
		Currency:       instruction.Currency,
		IdempotencyKey: instruction.IdempotencyKey,
		ReleasedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	g.byKey[instruction.IdempotencyKey] = receipt
	g.order = append(g.order, instruction.IdempotencyKey)
	return receipt, nil
}

func (g *LedgerGateway) List() []PaymentReceipt {
	g.mu.Lock()
	defer g.mu.Unlock()

	receipts := make([]PaymentReceipt, 0, len(g.order))
	for _, key := range g.order {
		receipts = append(receipts, g.byKey[key])
	}
	return receipts
}

func Definition(gateway PaymentGateway) (domain.Definition, error) {
	builder := domain.New("accounts-payable").
		Entity(EntityInvoice).
		Event(EventInvoiceReceived).
		Event(EventMissingInfoRequested).
		Event(EventInvoiceInfoReceived).
		Event(EventInvoiceVerified).
		Event(EventPaymentReadyMarked).
		Event(EventPaymentHeld).
		Event(EventPaymentReleased).
		Event(EventInvoiceRejected).
		Transition(EventInvoiceReceived, "", StateReceived).
		Transition(EventMissingInfoRequested, StateReceived, StateNeedsInfo).
		Transition(EventInvoiceInfoReceived, StateNeedsInfo, StateReceived).
		Transition(EventInvoiceVerified, StateReceived, StateVerified).
		Transition(EventPaymentReadyMarked, StateVerified, StateReadyForPayment).
		Transition(EventPaymentHeld, StateReadyForPayment, StatePaymentHeld).
		Transition(EventPaymentReleased, StateReadyForPayment, StatePaid).
		Transition(EventPaymentReleased, StatePaymentHeld, StatePaid).
		Transition(EventInvoiceRejected, StateReceived, StateRejected).
		Transition(EventInvoiceRejected, StateNeedsInfo, StateRejected).
		Transition(EventInvoiceRejected, StateVerified, StateRejected).
		Transition(EventInvoiceRejected, StateReadyForPayment, StateRejected).
		Transition(EventInvoiceRejected, StatePaymentHeld, StateRejected).
		Allow(StateReceived, ActionRequestMissingInfo, ActionVerifyInvoice, ActionRejectInvoice).
		Allow(StateNeedsInfo, ActionRecordInfoReceived, ActionRejectInvoice).
		Allow(StateVerified, ActionMarkReadyForPayment, ActionRejectInvoice).
		Allow(StateReadyForPayment, ActionReleaseLowRiskPayment, ActionHoldForApproval, ActionRejectInvoice).
		Allow(StatePaymentHeld, ActionReleaseApprovedPayment, ActionRejectInvoice)

	for _, contract := range Contracts(gateway) {
		builder = builder.Action(contract)
	}
	return builder.Build()
}

func Policy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole(RoleAPAgent, PermissionRequestMissingInfo)
	policy.GrantRole(RoleAPAgent, PermissionRecordInfoReceived)
	policy.GrantRole(RoleAPAgent, PermissionVerifyInvoice)
	policy.GrantRole(RoleAPAgent, PermissionMarkReadyForPayment)
	policy.GrantRole(RoleAPAgent, PermissionHoldForApproval)
	policy.GrantRole(RoleAPAgent, PermissionRejectInvoice)
	policy.GrantRole(RolePaymentService, PermissionReleaseLowRiskPayment)
	policy.GrantRole(RolePaymentService, PermissionReleaseApprovedPayment)

	policy.AssignRole(APAgentActor.ID, RoleAPAgent)
	policy.AssignRole(PaymentServiceActor.ID, RolePaymentService)
	policy.AssignRole(FinanceManagerActor.ID, RoleFinanceManager)
	return policy
}

func NewRuntime(gateway PaymentGateway) (*runtime.Runtime, error) {
	definition, err := Definition(gateway)
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(definition, runtime.Config{
		PermissionPolicy: Policy(),
	})
}

func Contracts(gateway PaymentGateway) []action.ActionContract {
	if gateway == nil {
		gateway = NewLedgerGateway()
	}
	return []action.ActionContract{
		{
			Name:                ActionRequestMissingInfo,
			AllowedStates:       []string{StateReceived},
			RequiredParameters:  []string{"missing_fields"},
			RequiredPermissions: []permission.Permission{PermissionRequestMissingInfo},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRequestMissingInfo, ctx, EventMissingInfoRequested, "requested missing invoice information", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionRecordInfoReceived,
			AllowedStates:       []string{StateNeedsInfo},
			RequiredParameters:  []string{"invoice_id", "vendor_id", "invoice_number", "amount_cents", "currency", "bank_fingerprint"},
			RequiredPermissions: []permission.Permission{PermissionRecordInfoReceived},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRecordInfoReceived, ctx, EventInvoiceInfoReceived, "recorded missing invoice information", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionVerifyInvoice,
			AllowedStates:       []string{StateReceived},
			RequiredParameters:  []string{"invoice_id", "vendor_id", "invoice_number", "amount_cents", "currency", "bank_fingerprint"},
			RequiredPermissions: []permission.Permission{PermissionVerifyInvoice},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				if err := validateInvoiceParameters(ctx.Parameters); err != nil {
					return action.ActionResult{}, err
				}
				return eventResult(ActionVerifyInvoice, ctx, EventInvoiceVerified, "verified invoice identity, amount, and payment rails", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionMarkReadyForPayment,
			AllowedStates:       []string{StateVerified},
			RequiredParameters:  []string{"due_date"},
			RequiredPermissions: []permission.Permission{PermissionMarkReadyForPayment},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionMarkReadyForPayment, ctx, EventPaymentReadyMarked, "marked invoice ready for payment decision", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionHoldForApproval,
			AllowedStates:       []string{StateReadyForPayment},
			RequiredParameters:  []string{"reason"},
			RequiredPermissions: []permission.Permission{PermissionHoldForApproval},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionHoldForApproval, ctx, EventPaymentHeld, "held payment for finance approval", ctx.Parameters), nil
			},
		},
		{
			Name:                ActionReleaseLowRiskPayment,
			AllowedStates:       []string{StateReadyForPayment},
			RequiredParameters:  []string{"invoice_id", "vendor_id", "amount_cents", "currency", "bank_fingerprint", "trusted_vendor", "idempotency_key"},
			RequiredPermissions: []permission.Permission{PermissionReleaseLowRiskPayment},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, err := instructionFromParams(actionCtx.Parameters)
				if err != nil {
					return action.ActionResult{}, err
				}
				if instruction.AmountCents > LowRiskLimitCents {
					return action.ActionResult{}, fmt.Errorf("low-risk payment limit exceeded: %d cents > %d cents", instruction.AmountCents, LowRiskLimitCents)
				}
				if !boolParam(actionCtx.Parameters, "trusted_vendor") {
					return action.ActionResult{}, errors.New("low-risk payment requires trusted_vendor=true")
				}
				return releasePayment(ctx, gateway, ActionReleaseLowRiskPayment, actionCtx, instruction)
			},
		},
		{
			Name:                ActionReleaseApprovedPayment,
			AllowedStates:       []string{StatePaymentHeld},
			RequiredParameters:  []string{"invoice_id", "vendor_id", "amount_cents", "currency", "bank_fingerprint", "idempotency_key"},
			RequiredPermissions: []permission.Permission{PermissionReleaseApprovedPayment},
			Risk:                action.RiskCritical,
			ApprovalRequirement: action.ApprovalRequired,
			Executor: func(ctx context.Context, actionCtx action.ActionContext) (action.ActionResult, error) {
				instruction, err := instructionFromParams(actionCtx.Parameters)
				if err != nil {
					return action.ActionResult{}, err
				}
				return releasePayment(ctx, gateway, ActionReleaseApprovedPayment, actionCtx, instruction)
			},
		},
		{
			Name:                ActionRejectInvoice,
			AllowedStates:       []string{StateReceived, StateNeedsInfo, StateVerified, StateReadyForPayment, StatePaymentHeld},
			RequiredParameters:  []string{"reason"},
			RequiredPermissions: []permission.Permission{PermissionRejectInvoice},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return eventResult(ActionRejectInvoice, ctx, EventInvoiceRejected, "rejected invoice", ctx.Parameters), nil
			},
		},
	}
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

func releasePayment(ctx context.Context, gateway PaymentGateway, actionName string, actionCtx action.ActionContext, instruction PaymentInstruction) (action.ActionResult, error) {
	receipt, err := gateway.Release(ctx, instruction)
	if err != nil {
		return action.ActionResult{}, err
	}
	summary := "released payment through ledger gateway"
	if receipt.Duplicate {
		summary = "idempotent payment release returned existing receipt"
	}
	return action.ActionResult{
		ActionName:     actionName,
		EntityID:       actionCtx.EntityID,
		Status:         action.ExecutionSucceeded,
		Executed:       true,
		EffectsSummary: summary,
		Output: map[string]any{
			"payment_id":      receipt.PaymentID,
			"amount_cents":    receipt.AmountCents,
			"currency":        receipt.Currency,
			"idempotency_key": receipt.IdempotencyKey,
			"duplicate":       receipt.Duplicate,
		},
		FollowUpEvents: []event.Event{
			newEvent(EventPaymentReleased, actionCtx.EntityID, actionCtx.Actor.ID, map[string]any{
				"payment_id":      receipt.PaymentID,
				"vendor_id":       receipt.VendorID,
				"amount_cents":    receipt.AmountCents,
				"currency":        receipt.Currency,
				"idempotency_key": receipt.IdempotencyKey,
				"duplicate":       receipt.Duplicate,
			}),
		},
		ExecutedAt: time.Now().UTC(),
	}, nil
}

func validateInvoiceParameters(params map[string]any) error {
	if strings.TrimSpace(stringParam(params, "invoice_id")) == "" {
		return errors.New("invoice_id is required")
	}
	if strings.TrimSpace(stringParam(params, "vendor_id")) == "" {
		return errors.New("vendor_id is required")
	}
	if strings.TrimSpace(stringParam(params, "invoice_number")) == "" {
		return errors.New("invoice_number is required")
	}
	amount, err := int64Param(params, "amount_cents")
	if err != nil {
		return err
	}
	if amount <= 0 {
		return errors.New("amount_cents must be positive")
	}
	currency := strings.ToUpper(stringParam(params, "currency"))
	if currency != "USD" && currency != "EUR" {
		return fmt.Errorf("unsupported currency %q", currency)
	}
	if len(strings.TrimSpace(stringParam(params, "bank_fingerprint"))) < 6 {
		return errors.New("bank_fingerprint must identify trusted payment rails")
	}
	return nil
}

func validatePaymentInstruction(instruction PaymentInstruction) error {
	if strings.TrimSpace(instruction.InvoiceID) == "" {
		return errors.New("invoice_id is required")
	}
	if strings.TrimSpace(instruction.VendorID) == "" {
		return errors.New("vendor_id is required")
	}
	if instruction.AmountCents <= 0 {
		return errors.New("amount_cents must be positive")
	}
	if instruction.Currency != "USD" && instruction.Currency != "EUR" {
		return fmt.Errorf("unsupported currency %q", instruction.Currency)
	}
	if len(strings.TrimSpace(instruction.BankFingerprint)) < 6 {
		return errors.New("bank_fingerprint must identify trusted payment rails")
	}
	if strings.TrimSpace(instruction.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	return nil
}

func instructionFromParams(params map[string]any) (PaymentInstruction, error) {
	amount, err := int64Param(params, "amount_cents")
	if err != nil {
		return PaymentInstruction{}, err
	}
	return PaymentInstruction{
		InvoiceID:       stringParam(params, "invoice_id"),
		VendorID:        stringParam(params, "vendor_id"),
		InvoiceNumber:   stringParam(params, "invoice_number"),
		AmountCents:     amount,
		Currency:        strings.ToUpper(stringParam(params, "currency")),
		BankFingerprint: stringParam(params, "bank_fingerprint"),
		IdempotencyKey:  stringParam(params, "idempotency_key"),
	}, nil
}

func newEvent(eventType, invoiceID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", invoiceID, strings.ToLower(eventType), time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   invoiceID,
		EntityType: EntityInvoice,
		Source:     "payables/executor",
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
	case jsonNumber:
		return typed.Int64()
	case string:
		var parsed int64
		if _, err := fmt.Sscanf(typed, "%d", &parsed); err == nil {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("%s must be an integer number of cents", key)
}

type jsonNumber interface {
	Int64() (int64, error)
}
