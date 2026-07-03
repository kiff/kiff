package payables

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/evidence"
	"github.com/kiff/kiff/pkg/kiff/lifecycle"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/state"
)

type InteractiveApp struct {
	mu         sync.Mutex
	rt         *runtime.Runtime
	agent      Agent
	gateway    *LedgerGateway
	invoiceID  string
	facts      InvoiceFacts
	lines      []AppLine
	held       *HeldPayment
	last       *AgentProposal
	modelLabel string
}

type AppLine struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

type HeldPayment struct {
	ApprovalID string               `json:"approval_id"`
	ActionName string               `json:"action_name"`
	Reason     string               `json:"reason"`
	Context    action.ActionContext `json:"-"`
}

type Snapshot struct {
	InvoiceID      string          `json:"invoice_id"`
	CurrentState   string          `json:"current_state"`
	AllowedActions []string        `json:"allowed_actions"`
	Facts          InvoiceFacts    `json:"facts"`
	Lines          []AppLine       `json:"lines"`
	LastProposal   *AgentProposal  `json:"last_proposal,omitempty"`
	Held           *HeldPayment    `json:"held,omitempty"`
	Timeline       []TimelineEntry `json:"timeline"`
	// Lifecycle is the framework's read-only projection of the invoice's
	// governed history (proposal → validation → approval → execution),
	// assembled by runtime.EntityLifecycle. It replaces the hand-stitched
	// timeline the sandbox app had to build itself (#63).
	Lifecycle lifecycle.Lifecycle `json:"lifecycle"`
	Payments  []PaymentReceipt    `json:"payments"`
	Model     string              `json:"model"`
}

type TimelineEntry struct {
	Kind    audit.Kind `json:"kind"`
	ActorID string     `json:"actor_id,omitempty"`
	Message string     `json:"message"`
	Detail  string     `json:"detail,omitempty"`
}

func NewInteractiveApp(agent Agent, modelLabel string) (*InteractiveApp, error) {
	gateway := NewLedgerGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		return nil, err
	}
	if modelLabel == "" {
		modelLabel = "bedrock"
	}
	return &InteractiveApp{
		rt:         rt,
		agent:      agent,
		gateway:    gateway,
		invoiceID:  "inv-ap-7741",
		facts:      InvoiceFacts{InvoiceID: "inv-ap-7741"},
		modelLabel: modelLabel,
	}, nil
}

func (a *InteractiveApp) ProcessInput(ctx context.Context, input string) (Snapshot, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Snapshot{}, errors.New("input is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.mergeFactsFromText(input)
	if err := a.ensureInvoice(ctx, input); err != nil {
		return Snapshot{}, err
	}

	current, err := a.currentState(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allowed, err := a.allowedActions(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	proposal, err := a.agent.Propose(ctx, AgentRequest{
		InvoiceID:      a.invoiceID,
		CurrentState:   current.Value,
		AllowedActions: allowed,
		InvoiceFacts:   a.facts,
		OperatorInput:  input,
	})
	if err != nil {
		a.addLine("agent", fmt.Sprintf("Claude Haiku error: %v", err))
		return a.snapshot(ctx)
	}

	a.last = &proposal
	a.mergeFactsFromParams(proposal.Parameters)
	if err := a.recordProposal(ctx, proposal, input); err != nil {
		return Snapshot{}, err
	}

	if proposal.ActionName == "NO_ACTION" {
		a.addLine("agent", fmt.Sprintf("proposed NO_ACTION: %s", proposal.ReasoningSummary))
		return a.snapshot(ctx)
	}

	a.addLine("agent", fmt.Sprintf("proposed %s: %s", proposal.ActionName, proposal.ReasoningSummary))
	if err := a.applyProposal(ctx, input, proposal); err != nil {
		a.addLine("kiff", fmt.Sprintf("blocked %s: %v", proposal.ActionName, err))
	}
	return a.snapshot(ctx)
}

func (a *InteractiveApp) ReviewHeld(ctx context.Context, granted bool) (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.held == nil {
		return Snapshot{}, errors.New("no payment is waiting for approval")
	}
	held := a.held

	status := approval.StatusDenied
	reason := "finance manager denied payment release"
	if granted {
		status = approval.StatusGranted
		reason = "finance manager approved payment release"
	}
	if _, err := a.rt.ReviewApproval(ctx, held.ApprovalID, FinanceManagerActor.ID, status, reason); err != nil {
		return Snapshot{}, err
	}

	if !granted {
		a.addLine("human", fmt.Sprintf("denied %s", held.ActionName))
		a.held = nil
		return a.snapshot(ctx)
	}

	contract, err := contract(a.rt, held.ActionName)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := a.rt.ExecuteAction(ctx, held.Context, contract); err != nil {
		return Snapshot{}, err
	}
	a.addLine("human", fmt.Sprintf("approved %s", held.ActionName))
	a.addLine("kiff", "payment-service released funds through the ledger gateway")
	a.held = nil
	return a.snapshot(ctx)
}

func (a *InteractiveApp) Reset(ctx context.Context) (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	gateway := NewLedgerGateway()
	rt, err := NewRuntime(gateway)
	if err != nil {
		return Snapshot{}, err
	}
	a.rt = rt
	a.gateway = gateway
	a.invoiceID = "inv-ap-7741"
	a.facts = InvoiceFacts{InvoiceID: "inv-ap-7741"}
	a.lines = nil
	a.held = nil
	a.last = nil
	return a.snapshot(ctx)
}

func (a *InteractiveApp) Snapshot(ctx context.Context) (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot(ctx)
}

func (a *InteractiveApp) ensureInvoice(ctx context.Context, input string) error {
	if _, ok, err := a.rt.States.Current(ctx, a.invoiceID); err != nil {
		return err
	} else if ok {
		a.addLine("input", input)
		return nil
	}

	ev := event.Event{
		ID:         fmt.Sprintf("evt-%s-received-%d", a.invoiceID, time.Now().UnixNano()),
		Type:       EventInvoiceReceived,
		EntityID:   a.invoiceID,
		EntityType: EntityInvoice,
		Source:     "ap-inbox",
		ActorID:    "ap-inbox",
		OccurredAt: time.Now().UTC(),
		Metadata: event.Metadata{
			TraceID:       fmt.Sprintf("trace-%s", a.invoiceID),
			CorrelationID: a.invoiceID,
			Tags:          map[string]string{"workflow": "accounts-payable"},
		},
		Payload: map[string]any{
			"text":          input,
			"invoice_id":    a.invoiceID,
			"invoice_facts": a.facts,
		},
	}
	if err := a.rt.IngestEvent(ctx, ev); err != nil {
		return err
	}
	a.addLine("input", input)
	a.addLine("kiff", "normalized intake into INVOICE_RECEIVED")
	return nil
}

func (a *InteractiveApp) recordProposal(ctx context.Context, proposal AgentProposal, input string) error {
	actionName := proposal.ActionName
	if actionName == "NO_ACTION" {
		actionName = ""
	}
	return a.rt.ProposeDecision(ctx, decision.Decision{
		ID:             fmt.Sprintf("dec-%s-%d", a.invoiceID, time.Now().UnixNano()),
		EntityID:       a.invoiceID,
		EntityType:     EntityInvoice,
		Kind:           decision.KindActionProposal,
		ProposedAction: actionName,
		Evidence: []evidence.Ref{
			{
				ID:        fmt.Sprintf("input-%d", time.Now().UnixNano()),
				Kind:      evidence.KindSystemData,
				Source:    "operator-input",
				Summary:   truncate(input, 140),
				CreatedAt: time.Now().UTC(),
			},
		},
		ReasoningSummary: proposal.ReasoningSummary,
		Confidence:       proposal.Confidence,
		ActorID:          APAgentActor.ID,
		CreatedAt:        time.Now().UTC(),
	})
}

func (a *InteractiveApp) applyProposal(ctx context.Context, input string, proposal AgentProposal) error {
	contract, err := contract(a.rt, proposal.ActionName)
	if err != nil {
		return err
	}
	current, err := a.currentState(ctx)
	if err != nil {
		return err
	}

	params := a.normalizeParameters(input, proposal)
	actionCtx := action.ActionContext{
		ActionName:   proposal.ActionName,
		EntityID:     a.invoiceID,
		EntityType:   EntityInvoice,
		CurrentState: current.Value,
		Actor:        actorForAction(proposal.ActionName),
		Parameters:   params,
	}

	if proposal.ActionName == ActionHoldForApproval {
		if _, err := a.rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
			return err
		}
		a.addLine("kiff", "moved invoice to PAYMENT_HELD")
		return a.requestReleaseApproval(ctx, proposal)
	}

	if proposal.ActionName == ActionReleaseApprovedPayment {
		actionCtx.ApprovalID = fmt.Sprintf("approval-%s-%d", a.invoiceID, time.Now().UnixNano())
		_, err := a.rt.ExecuteAction(ctx, actionCtx, contract)
		if errors.Is(err, action.ErrApprovalRequired) {
			return a.requestApprovalForContext(ctx, actionCtx, contract, proposal.ReasoningSummary)
		}
		if err != nil {
			return err
		}
		a.addLine("kiff", "payment-service released approved payment")
		return nil
	}

	if _, err := a.rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		return err
	}
	if proposal.ActionName == ActionReleaseLowRiskPayment {
		a.addLine("kiff", "payment-service released low-risk payment through the ledger gateway")
	} else {
		a.addLine("kiff", fmt.Sprintf("validated and executed %s", proposal.ActionName))
	}
	return nil
}

func (a *InteractiveApp) requestReleaseApproval(ctx context.Context, proposal AgentProposal) error {
	current, err := a.currentState(ctx)
	if err != nil {
		return err
	}
	releaseContract, err := contract(a.rt, ActionReleaseApprovedPayment)
	if err != nil {
		return err
	}
	releaseCtx := action.ActionContext{
		ActionName:   ActionReleaseApprovedPayment,
		EntityID:     a.invoiceID,
		EntityType:   EntityInvoice,
		CurrentState: current.Value,
		Actor:        PaymentServiceActor,
		ApprovalID:   fmt.Sprintf("approval-%s-%d", a.invoiceID, time.Now().UnixNano()),
		Parameters:   a.paymentParameters(),
	}
	return a.requestApprovalForContext(ctx, releaseCtx, releaseContract, proposal.ReasoningSummary)
}

func (a *InteractiveApp) requestApprovalForContext(ctx context.Context, releaseCtx action.ActionContext, releaseContract action.ActionContract, reason string) error {
	if reason == "" {
		reason = "payment release requires finance approval"
	}
	requestCtx := releaseCtx
	requestCtx.Actor = APAgentActor
	if _, err := a.rt.RequestApproval(ctx, releaseCtx.ApprovalID, requestCtx, releaseContract, reason); err != nil {
		return err
	}
	a.held = &HeldPayment{
		ApprovalID: releaseCtx.ApprovalID,
		ActionName: releaseCtx.ActionName,
		Reason:     reason,
		Context:    releaseCtx,
	}
	a.addLine("kiff", "held RELEASE_APPROVED_PAYMENT for finance approval")
	return nil
}

func (a *InteractiveApp) currentState(ctx context.Context) (state.State, error) {
	current, ok, err := a.rt.States.Current(ctx, a.invoiceID)
	if err != nil {
		return state.State{}, err
	}
	if !ok {
		return state.State{}, fmt.Errorf("invoice %q has no state", a.invoiceID)
	}
	return current, nil
}

func (a *InteractiveApp) allowedActions(ctx context.Context) ([]string, error) {
	contracts, err := a.rt.AllowedActions(ctx, a.invoiceID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		names = append(names, contract.Name)
	}
	return names, nil
}

func (a *InteractiveApp) snapshot(ctx context.Context) (Snapshot, error) {
	currentState := ""
	if current, ok, err := a.rt.States.Current(ctx, a.invoiceID); err != nil {
		return Snapshot{}, err
	} else if ok {
		currentState = current.Value
	}

	allowed := []string{}
	if currentState != "" {
		var err error
		allowed, err = a.allowedActions(ctx)
		if err != nil {
			return Snapshot{}, err
		}
	}

	records, err := a.rt.Timeline(ctx, a.invoiceID)
	if err != nil {
		return Snapshot{}, err
	}

	life, err := a.rt.EntityLifecycle(ctx, a.invoiceID)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		InvoiceID:      a.invoiceID,
		CurrentState:   currentState,
		AllowedActions: allowed,
		Facts:          a.facts,
		Lines:          append([]AppLine(nil), a.lines...),
		LastProposal:   a.last,
		Held:           a.held,
		Timeline:       timelineEntries(records),
		Lifecycle:      life,
		Payments:       a.gateway.List(),
		Model:          a.modelLabel,
	}, nil
}

func (a *InteractiveApp) normalizeParameters(input string, proposal AgentProposal) map[string]any {
	params := map[string]any{}
	for key, value := range proposal.Parameters {
		params[key] = value
	}
	switch proposal.ActionName {
	case ActionRequestMissingInfo:
		if _, ok := params["missing_fields"]; !ok {
			params["missing_fields"] = missingFields(a.facts)
		}
	case ActionRecordInfoReceived, ActionVerifyInvoice:
		a.fillInvoiceParams(params)
	case ActionMarkReadyForPayment:
		if _, ok := params["due_date"]; !ok {
			params["due_date"] = fallback(a.facts.DueDate, "2026-07-15")
		}
	case ActionHoldForApproval, ActionRejectInvoice:
		if _, ok := params["reason"]; !ok {
			params["reason"] = fallback(proposal.ReasoningSummary, truncate(input, 100))
		}
	case ActionReleaseLowRiskPayment, ActionReleaseApprovedPayment:
		for key, value := range a.paymentParameters() {
			if _, ok := params[key]; !ok {
				params[key] = value
			}
		}
		if proposal.ActionName == ActionReleaseLowRiskPayment {
			params["trusted_vendor"] = a.facts.TrustedVendor
		}
	}
	return params
}

func (a *InteractiveApp) fillInvoiceParams(params map[string]any) {
	params["invoice_id"] = fallback(stringParam(params, "invoice_id"), a.invoiceID)
	params["vendor_id"] = fallback(stringParam(params, "vendor_id"), a.facts.VendorID)
	params["invoice_number"] = fallback(stringParam(params, "invoice_number"), a.facts.InvoiceNumber)
	if _, ok := params["amount_cents"]; !ok && a.facts.AmountCents > 0 {
		params["amount_cents"] = a.facts.AmountCents
	}
	params["currency"] = fallback(stringParam(params, "currency"), a.facts.Currency)
	params["bank_fingerprint"] = fallback(stringParam(params, "bank_fingerprint"), a.facts.BankFingerprint)
}

func (a *InteractiveApp) paymentParameters() map[string]any {
	return map[string]any{
		"invoice_id":       a.invoiceID,
		"vendor_id":        a.facts.VendorID,
		"invoice_number":   a.facts.InvoiceNumber,
		"amount_cents":     a.facts.AmountCents,
		"currency":         a.facts.Currency,
		"bank_fingerprint": a.facts.BankFingerprint,
		"idempotency_key":  a.idempotencyKey(),
		"trusted_vendor":   a.facts.TrustedVendor,
	}
}

func (a *InteractiveApp) idempotencyKey() string {
	return fmt.Sprintf("%s:%s:%d:%s", a.invoiceID, a.facts.VendorID, a.facts.AmountCents, a.facts.BankFingerprint)
}

func (a *InteractiveApp) mergeFactsFromText(input string) {
	if invoiceNumber := parseInvoiceNumber(input); invoiceNumber != "" {
		a.facts.InvoiceNumber = invoiceNumber
	}
	if amount := parseAmountCents(input); amount > 0 {
		a.facts.AmountCents = amount
	}
	if strings.Contains(strings.ToUpper(input), " EUR") {
		a.facts.Currency = "EUR"
	} else if strings.Contains(strings.ToUpper(input), " USD") || strings.Contains(input, "$") {
		a.facts.Currency = "USD"
	}
	lower := strings.ToLower(input)
	if strings.Contains(lower, "northwind") {
		a.facts.VendorID = "vendor-northwind"
		a.facts.VendorName = "Northwind Parts"
		a.facts.TrustedVendor = true
	}
	if strings.Contains(lower, "new vendor") || strings.Contains(lower, "unknown vendor") {
		a.facts.VendorID = "vendor-new"
		a.facts.VendorName = "New Vendor"
		a.facts.TrustedVendor = false
	}
	if strings.Contains(lower, "changed bank") || strings.Contains(lower, "new bank") {
		a.facts.BankFingerprint = "bank-new-8842"
		a.facts.TrustedVendor = false
	} else if strings.Contains(lower, "ach-9912") || strings.Contains(lower, "bank ach") {
		a.facts.BankFingerprint = "bank-ach-9912"
	}
	if due := parseDueDate(input); due != "" {
		a.facts.DueDate = due
	}
}

func (a *InteractiveApp) mergeFactsFromParams(params map[string]any) {
	if params == nil {
		return
	}
	if value := stringParam(params, "invoice_id"); value != "" {
		a.facts.InvoiceID = value
	}
	if value := stringParam(params, "vendor_id"); value != "" {
		a.facts.VendorID = value
	}
	if value := stringParam(params, "invoice_number"); value != "" {
		a.facts.InvoiceNumber = value
	}
	if value, err := int64Param(params, "amount_cents"); err == nil && value > 0 {
		a.facts.AmountCents = value
	}
	if value := strings.ToUpper(stringParam(params, "currency")); value != "" {
		a.facts.Currency = value
	}
	if value := stringParam(params, "bank_fingerprint"); value != "" {
		a.facts.BankFingerprint = value
	}
	if value := stringParam(params, "due_date"); value != "" {
		a.facts.DueDate = value
	}
	if _, ok := params["trusted_vendor"]; ok {
		a.facts.TrustedVendor = boolParam(params, "trusted_vendor")
	}
}

func contract(rt *runtime.Runtime, actionName string) (action.ActionContract, error) {
	contract, ok := rt.Actions.Get(actionName)
	if !ok {
		return action.ActionContract{}, fmt.Errorf("missing action contract %q", actionName)
	}
	return contract, nil
}

func actorForAction(actionName string) actor.Actor {
	switch actionName {
	case ActionReleaseLowRiskPayment, ActionReleaseApprovedPayment:
		return PaymentServiceActor
	default:
		return APAgentActor
	}
}

func timelineEntries(records []audit.Record) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(records))
	for _, record := range records {
		detail := ""
		if actionName, ok := record.Data["action"].(string); ok {
			detail = actionName
		}
		if eventType, ok := record.Data["event_type"].(string); ok {
			detail = eventType
		}
		entries = append(entries, TimelineEntry{
			Kind:    record.Kind,
			ActorID: record.ActorID,
			Message: record.Message,
			Detail:  detail,
		})
	}
	return entries
}

func (a *InteractiveApp) addLine(kind, text string) {
	a.lines = append(a.lines, AppLine{
		Time: time.Now().UTC(),
		Kind: kind,
		Text: text,
	})
}

func parseInvoiceNumber(input string) string {
	re := regexp.MustCompile(`(?i)\bINV[-\s]?[0-9][0-9A-Z]*\b`)
	match := re.FindString(input)
	if match == "" {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(match, " ", "-"))
}

func parseAmountCents(input string) int64 {
	re := regexp.MustCompile(`[$€]?\s*([0-9][0-9,]*)(?:\.([0-9]{2}))?`)
	matches := re.FindAllStringSubmatch(input, -1)
	var best int64
	for _, match := range matches {
		whole := strings.ReplaceAll(match[1], ",", "")
		var dollars int64
		if _, err := fmt.Sscanf(whole, "%d", &dollars); err != nil {
			continue
		}
		cents := int64(0)
		if len(match) > 2 && match[2] != "" {
			_, _ = fmt.Sscanf(match[2], "%d", &cents)
		}
		value := dollars*100 + cents
		if value > best {
			best = value
		}
	}
	return best
}

func parseDueDate(input string) string {
	re := regexp.MustCompile(`\b20[0-9]{2}-[0-9]{2}-[0-9]{2}\b`)
	return re.FindString(input)
}

func missingFields(facts InvoiceFacts) []string {
	var fields []string
	if facts.VendorID == "" {
		fields = append(fields, "vendor_id")
	}
	if facts.InvoiceNumber == "" {
		fields = append(fields, "invoice_number")
	}
	if facts.AmountCents == 0 {
		fields = append(fields, "amount_cents")
	}
	if facts.Currency == "" {
		fields = append(fields, "currency")
	}
	if facts.BankFingerprint == "" {
		fields = append(fields, "bank_fingerprint")
	}
	return fields
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
