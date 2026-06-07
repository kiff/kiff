// Package aicafeops is the operational-authority breadth demo for KIFF.
//
// One AI shift manager, five tools, five distinct governance behaviors
// in a single run. The point is to show that KIFF gates AI proposals
// against domain rules — catalog, budget, working hours, approval —
// before any executor touches a real café system.
//
// The example was inspired by the Andon Café experiment (an AI-managed
// café where the AI manager confidently issued operational actions that
// did not make sense). The failure mode is not "model quality"; it is
// "no runtime boundary between proposed and executed". That boundary
// is exactly what KIFF is.
//
// Entity is "Shift". Lifecycle: NEW → OPEN → AWAITING_HUMAN. Most
// actions stay in OPEN; the escalation path parks the shift awaiting
// a human ops review.
//
// Actions:
//
//   - START_SHIFT          no approval, agent permission. Moves NEW → OPEN.
//   - AUTO_ORDER_INVENTORY no approval. Reached only when the order
//                          stays under SingleOrderCeilingCents AND keeps
//                          the running daily total under
//                          DailyOrderCeilingCents.
//   - ORDER_INVENTORY      approval required. Reached when either ceiling
//                          would be exceeded.
//   - REQUEST_SPECIALTY    custom validator on the contract: rejects
//                          unless parameters.item_id is in the catalog.
//                          The check runs BEFORE the approval gate so
//                          out-of-catalog requests never reach human review.
//   - SEND_STAFF_MESSAGE   custom validator on the contract: rejects
//                          unless parameters.sent_at_local is inside the
//                          working-hours window. The check runs BEFORE
//                          approval for the same reason.
//   - ESCALATE_SUPPLIER    no approval. Always allowed in OPEN; parks the
//                          shift in AWAITING_HUMAN.
//
// The contracts use the public action.ActionContract surface plus a thin
// wrapping technique: the executor runs additional, contract-specific
// pre-checks (catalog, working-hours, daily cap) and returns a typed
// sentinel error. KIFF's validator is unchanged — it still does state,
// parameters, permissions, approval — and the contract executor is
// where domain-specific guards live. This keeps pkg/kiff untouched.
package aicafeops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/adapter"
	"github.com/kiffhq/kiff/pkg/kiff/domain"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
	"github.com/kiffhq/kiff/pkg/kiff/runtime"
	"github.com/kiffhq/kiff/pkg/kiff/state"
	"github.com/kiffhq/kiff/pkg/kiff/store"
)

// SingleOrderCeilingCents — at or below this, AUTO_ORDER_INVENTORY runs.
// Above, the order requires approval. 50 EUR as cents, integer wire.
const SingleOrderCeilingCents = 5000

// DailyOrderCeilingCents — running sum on a shift. Even if every
// individual order is below the single ceiling, once the running sum on
// the same shift exceeds this value the next order must be approved.
// 200 EUR as cents.
const DailyOrderCeilingCents = 20000

// WorkingHoursStart and WorkingHoursEnd bound the window in which staff
// messaging is allowed. The check is on the wall-clock hour the proposal
// carries; KIFF does not assume the runtime's clock equals the shop's
// timezone.
const (
	WorkingHoursStart = 7  // 07:00, inclusive
	WorkingHoursEnd   = 22 // 22:00, exclusive
)

// ErrNotInCatalog is the structural rejection for REQUEST_SPECIALTY when
// the requested item is not on the café's allow-list. Exposed so callers
// (and the demo HTTP server) can classify it as a `blocked_not_in_catalog`
// outcome distinct from approval_required.
var ErrNotInCatalog = errors.New("aicafeops: item is not in the café catalog")

// ErrAfterHours is the structural rejection for SEND_STAFF_MESSAGE when
// the proposal's local time is outside working hours.
var ErrAfterHours = errors.New("aicafeops: staff messaging is blocked outside working hours")

// ErrAmountInvalid is returned by executors when an amount parameter is
// not a positive integer.
var ErrAmountInvalid = errors.New("aicafeops: amount must be a positive integer")

// Adapter, entity, event, state, action, and permission identifiers.
const (
	AdapterCafe = "ai-cafe-ops"

	EntityShift = "Shift"

	EventShiftScheduled  = "SHIFT_SCHEDULED"
	EventShiftStarted    = "SHIFT_STARTED"
	EventInventoryOrdered = "INVENTORY_ORDERED"
	EventSpecialtyRequested = "SPECIALTY_REQUESTED"
	EventStaffMessaged    = "STAFF_MESSAGED"
	EventSupplierEscalated = "SUPPLIER_ESCALATED"

	StateNew           = "NEW"
	StateOpen          = "OPEN"
	StateAwaitingHuman = "AWAITING_HUMAN"

	ActionStartShift           = "START_SHIFT"
	ActionAutoOrderInventory   = "AUTO_ORDER_INVENTORY"
	ActionOrderInventory       = "ORDER_INVENTORY"
	ActionRequestSpecialty     = "REQUEST_SPECIALTY"
	ActionSendStaffMessage     = "SEND_STAFF_MESSAGE"
	ActionEscalateSupplier     = "ESCALATE_SUPPLIER"

	PermStart      permission.Permission = "aicafeops.start"
	PermOrder      permission.Permission = "aicafeops.order"
	PermSpecialty  permission.Permission = "aicafeops.specialty"
	PermMessage    permission.Permission = "aicafeops.message"
	PermEscalate   permission.Permission = "aicafeops.escalate"
	PermApprove    permission.Permission = "aicafeops.approve"
)

// Demo actors.
var (
	SystemActor   = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor    = actor.Actor{ID: "shift-manager", Type: actor.TypeAgent, DisplayName: "Shift Manager", Roles: []string{"shift_manager"}}
	OperatorActor = actor.Actor{ID: "ops-human", Type: actor.TypeHuman, DisplayName: "Ops Operator", Roles: []string{"ops_operator"}}
)

// CatalogItems is the seed allow-list the demo ships with. The HTTP
// server exposes this read-only via /demo/catalog so a curious caller
// can see why an item is rejected. Real cafés would source this from
// their POS or supplier integrations; for the demo we keep it flat.
var CatalogItems = []string{
	"napkins",
	"coffee_beans",
	"oat_milk",
	"sugar_packets",
	"paper_cups",
	"to_go_lids",
	"chocolate_powder",
}

// Domain is the demo domain. It exposes a small mutable surface used by
// the contract executors (the running daily order total, the catalog)
// so the demo remains self-contained without leaking back into pkg/kiff.
// One Domain corresponds to one runtime.
type Domain struct {
	mu                sync.Mutex
	ordersByShift     map[string]int64 // running sum of INVENTORY_ORDERED amounts per shift
	catalog           map[string]struct{}
	workingHoursStart int
	workingHoursEnd   int
}

// New returns a fresh Domain with the default catalog and working-hours
// window. Tests and the demo server both call this; production callers
// would build a Domain with their real configuration.
func New() *Domain {
	d := &Domain{
		ordersByShift:     map[string]int64{},
		catalog:           map[string]struct{}{},
		workingHoursStart: WorkingHoursStart,
		workingHoursEnd:   WorkingHoursEnd,
	}
	for _, item := range CatalogItems {
		d.catalog[item] = struct{}{}
	}
	return d
}

// Cumulative returns the running order total for a shift. Used by the
// HTTP server to surface the cap to callers.
func (d *Domain) Cumulative(shiftID string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ordersByShift[shiftID]
}

func (d *Domain) addOrder(shiftID string, amountCents int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ordersByShift[shiftID] += amountCents
	return d.ordersByShift[shiftID]
}

// CatalogList returns the catalog as a sorted slice. The HTTP handler
// snapshots it on each request; the catalog is small and immutable for
// the demo.
func (d *Domain) CatalogList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.catalog))
	for item := range d.catalog {
		out = append(out, item)
	}
	return out
}

// InCatalog reports whether the given item is on the allow-list.
func (d *Domain) InCatalog(item string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.catalog[item]
	return ok
}

// WorkingHours returns the configured allowed window.
func (d *Domain) WorkingHours() (startHour, endHour int) {
	return d.workingHoursStart, d.workingHoursEnd
}

// NewPermissionPolicy returns the demo permission policy.
//
// The agent may propose every action and may execute the low-risk ones.
// Only operators carry approve. The runtime, not this policy, enforces
// approval on the contracts that need it.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	for _, perm := range []permission.Permission{
		PermStart, PermOrder, PermSpecialty, PermMessage, PermEscalate,
	} {
		policy.GrantRole("shift_manager", perm)
	}
	for _, perm := range []permission.Permission{
		PermStart, PermOrder, PermSpecialty, PermMessage, PermEscalate, PermApprove,
	} {
		policy.GrantRole("ops_operator", perm)
	}
	policy.GrantRole("system", PermStart)
	// Role membership is policy-owned (#19).
	policy.AssignRole(AgentActor.ID, "shift_manager")
	policy.AssignRole(OperatorActor.ID, "ops_operator")
	policy.AssignRole(SystemActor.ID, "system")
	return policy
}

// Contracts returns the action contracts for the demo. The Domain is
// captured by closure so each executor can consult or update the
// running order total or the catalog without global state.
func (d *Domain) Contracts() []action.ActionContract {
	return []action.ActionContract{
		d.startShiftContract(),
		d.autoOrderContract(),
		d.orderInventoryContract(),
		d.requestSpecialtyContract(),
		d.sendStaffMessageContract(),
		d.escalateSupplierContract(),
	}
}

// NewStateMachine builds the shift state machine.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
		state.Transition{EventType: EventShiftScheduled, From: "", To: StateNew},
		state.Transition{EventType: EventShiftStarted, From: StateNew, To: StateOpen},
		// Inventory / specialty / staff-message events do not move the
		// shift out of OPEN; they describe operational activity within
		// the open shift.
		state.Transition{EventType: EventInventoryOrdered, From: StateOpen, To: StateOpen},
		state.Transition{EventType: EventSpecialtyRequested, From: StateOpen, To: StateOpen},
		state.Transition{EventType: EventStaffMessaged, From: StateOpen, To: StateOpen},
		// Supplier escalation parks the shift awaiting a human review.
		state.Transition{EventType: EventSupplierEscalated, From: StateOpen, To: StateAwaitingHuman},
	)
	machine.SetAllowedActions(StateNew, []string{ActionStartShift})
	machine.SetAllowedActions(StateOpen, []string{
		ActionAutoOrderInventory, ActionOrderInventory, ActionRequestSpecialty,
		ActionSendStaffMessage, ActionEscalateSupplier,
	})
	machine.SetAllowedActions(StateAwaitingHuman, []string{})
	return machine
}

// NewDomainDefinition assembles the domain.
func (d *Domain) NewDomainDefinition() (domain.Definition, error) {
	b := domain.New("ai-cafe-ops").
		Entity(EntityShift).
		Event(EventShiftScheduled).
		Event(EventShiftStarted).
		Event(EventInventoryOrdered).
		Event(EventSpecialtyRequested).
		Event(EventStaffMessaged).
		Event(EventSupplierEscalated).
		Transition(EventShiftScheduled, "", StateNew).
		Transition(EventShiftStarted, StateNew, StateOpen).
		Transition(EventInventoryOrdered, StateOpen, StateOpen).
		Transition(EventSpecialtyRequested, StateOpen, StateOpen).
		Transition(EventStaffMessaged, StateOpen, StateOpen).
		Transition(EventSupplierEscalated, StateOpen, StateAwaitingHuman).
		Allow(StateNew, ActionStartShift).
		Allow(StateOpen, ActionAutoOrderInventory).
		Allow(StateOpen, ActionOrderInventory).
		Allow(StateOpen, ActionRequestSpecialty).
		Allow(StateOpen, ActionSendStaffMessage).
		Allow(StateOpen, ActionEscalateSupplier)
	for _, contract := range d.Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter creates the input adapter for raw events.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterCafe)
}

// NewRuntime returns a runtime wired for this demo using in-memory stores.
func (d *Domain) NewRuntime() (*runtime.Runtime, error) {
	return d.NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores returns a runtime wired for this demo using the
// provided store bundle. A nil bundle falls back to in-memory stores.
func (d *Domain) NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
	def, err := d.NewDomainDefinition()
	if err != nil {
		return nil, err
	}
	in, err := NewInputAdapter()
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(def, runtime.Config{
		PermissionPolicy: NewPermissionPolicy(),
		Adapters:         []adapter.Adapter{in},
		Stores:           stores,
	})
}

// ────────────────────────────────────────────────────────────────────
// Contracts
// ────────────────────────────────────────────────────────────────────

func (d *Domain) startShiftContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionStartShift,
		AllowedStates:       []string{StateNew},
		RequiredParameters:  []string{"opened_by"},
		RequiredPermissions: []permission.Permission{PermStart},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			openedBy, _ := ctx.Parameters["opened_by"].(string)
			return action.ActionResult{
				ActionName:     ActionStartShift,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("shift started by %s", openedBy),
				EffectsSummary: "shift opened",
				FollowUpEvents: []event.Event{
					shiftEvent(ctx.EntityID, EventShiftStarted, ctx.Actor.ID, map[string]any{
						"opened_by": openedBy,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// autoOrderContract handles inventory orders that pass the per-call
// ceiling and the cumulative cap. It executes without approval and
// updates the running total. The HTTP server picks this contract via
// Domain.NeedsApprovalForOrder(); the agent's single tool surface
// remains "order_inventory".
func (d *Domain) autoOrderContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionAutoOrderInventory,
		AllowedStates:       []string{StateOpen},
		RequiredParameters:  []string{"item_id", "quantity", "amount_cents"},
		RequiredPermissions: []permission.Permission{PermOrder},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			amount, err := readIntCents(ctx.Parameters, "amount_cents")
			if err != nil {
				return action.ActionResult{}, err
			}
			if amount <= 0 {
				return action.ActionResult{}, ErrAmountInvalid
			}
			itemID, _ := ctx.Parameters["item_id"].(string)
			quantity := readQuantity(ctx.Parameters)
			running := d.addOrder(ctx.EntityID, amount)
			return action.ActionResult{
				ActionName:     ActionAutoOrderInventory,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("auto-order %d × %s for %d cents; running total %d", quantity, itemID, amount, running),
				EffectsSummary: "auto inventory order placed (under cap)",
				Output: map[string]any{
					"item_id":       itemID,
					"quantity":      quantity,
					"amount_cents":  amount,
					"running_cents": running,
					"path":          "auto",
				},
				FollowUpEvents: []event.Event{
					shiftEvent(ctx.EntityID, EventInventoryOrdered, ctx.Actor.ID, map[string]any{
						"item_id":       itemID,
						"quantity":      quantity,
						"amount_cents":  amount,
						"running_cents": running,
						"path":          "auto",
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// orderInventoryContract is the high-risk variant. It is reached only
// when the HTTP server's routing decides the per-call ceiling or the
// cumulative cap is exceeded. ApprovalRequired means the runtime gate
// fires before the executor runs; once granted, the executor records
// the order and increments the running total.
func (d *Domain) orderInventoryContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionOrderInventory,
		AllowedStates:       []string{StateOpen},
		RequiredParameters:  []string{"item_id", "quantity", "amount_cents"},
		RequiredPermissions: []permission.Permission{PermOrder},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			amount, err := readIntCents(ctx.Parameters, "amount_cents")
			if err != nil {
				return action.ActionResult{}, err
			}
			if amount <= 0 {
				return action.ActionResult{}, ErrAmountInvalid
			}
			itemID, _ := ctx.Parameters["item_id"].(string)
			quantity := readQuantity(ctx.Parameters)
			running := d.addOrder(ctx.EntityID, amount)
			return action.ActionResult{
				ActionName:     ActionOrderInventory,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("ordered %d × %s for %d cents (approved); running total %d", quantity, itemID, amount, running),
				EffectsSummary: "approved inventory order placed",
				Output: map[string]any{
					"item_id":       itemID,
					"quantity":      quantity,
					"amount_cents":  amount,
					"running_cents": running,
					"path":          "approved",
				},
				FollowUpEvents: []event.Event{
					shiftEvent(ctx.EntityID, EventInventoryOrdered, ctx.Actor.ID, map[string]any{
						"item_id":       itemID,
						"quantity":      quantity,
						"amount_cents":  amount,
						"running_cents": running,
						"path":          "approved",
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// requestSpecialtyContract pre-checks the catalog allow-list BEFORE the
// approval gate runs. The executor returns ErrNotInCatalog when item_id
// is missing or unknown; the HTTP server classifies that as
// `blocked_not_in_catalog` and the audit trail records action_failed.
// Approvers never see catalog failures.
//
// To get the "before approval" guarantee the demo HTTP server pre-checks
// the parameter against a thin Go helper (CheckCatalog) and refuses to
// ever open an approval. That helper is exported so any caller can reuse
// the same check the executor uses.
func (d *Domain) requestSpecialtyContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionRequestSpecialty,
		AllowedStates:       []string{StateOpen},
		RequiredParameters:  []string{"item_id", "rationale"},
		RequiredPermissions: []permission.Permission{PermSpecialty},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			if err := d.CheckCatalog(ctx.Parameters); err != nil {
				return action.ActionResult{}, err
			}
			itemID, _ := ctx.Parameters["item_id"].(string)
			rationale, _ := ctx.Parameters["rationale"].(string)
			return action.ActionResult{
				ActionName:     ActionRequestSpecialty,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("specialty request recorded for %s", itemID),
				EffectsSummary: "specialty request approved",
				Output:         map[string]any{"item_id": itemID, "rationale": rationale},
				FollowUpEvents: []event.Event{
					shiftEvent(ctx.EntityID, EventSpecialtyRequested, ctx.Actor.ID, map[string]any{
						"item_id":   itemID,
						"rationale": rationale,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// CheckCatalog enforces that item_id is present and on the allow-list.
// Exported so the HTTP server can pre-check before opening an approval.
func (d *Domain) CheckCatalog(params map[string]any) error {
	value, ok := params["item_id"]
	if !ok {
		return ErrNotInCatalog
	}
	itemID, ok := value.(string)
	if !ok || itemID == "" {
		return ErrNotInCatalog
	}
	if !d.InCatalog(itemID) {
		return fmt.Errorf("%w: %q", ErrNotInCatalog, itemID)
	}
	return nil
}

// sendStaffMessageContract pre-checks the working-hours window. The
// executor returns ErrAfterHours when sent_at_local is missing or
// outside the configured window. The HTTP server classifies that as
// `blocked_after_hours`.
func (d *Domain) sendStaffMessageContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionSendStaffMessage,
		AllowedStates:       []string{StateOpen},
		RequiredParameters:  []string{"recipient", "message", "sent_at_local"},
		RequiredPermissions: []permission.Permission{PermMessage},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			if err := d.CheckWorkingHours(ctx.Parameters); err != nil {
				return action.ActionResult{}, err
			}
			recipient, _ := ctx.Parameters["recipient"].(string)
			message, _ := ctx.Parameters["message"].(string)
			return action.ActionResult{
				ActionName:     ActionSendStaffMessage,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("staff message sent to %s", recipient),
				EffectsSummary: "staff message sent",
				Output:         map[string]any{"recipient": recipient, "message": message},
				FollowUpEvents: []event.Event{
					shiftEvent(ctx.EntityID, EventStaffMessaged, ctx.Actor.ID, map[string]any{
						"recipient": recipient,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// CheckWorkingHours enforces that sent_at_local sits inside the
// configured working window. Accepts either an integer hour (0..23)
// or an RFC3339 timestamp string from which the hour is read.
func (d *Domain) CheckWorkingHours(params map[string]any) error {
	value, ok := params["sent_at_local"]
	if !ok {
		return ErrAfterHours
	}
	hour, ok := readHour(value)
	if !ok {
		return fmt.Errorf("%w: cannot read hour from sent_at_local", ErrAfterHours)
	}
	if hour < d.workingHoursStart || hour >= d.workingHoursEnd {
		return fmt.Errorf("%w: hour %02d is outside %02d:00–%02d:00", ErrAfterHours, hour, d.workingHoursStart, d.workingHoursEnd)
	}
	return nil
}

func (d *Domain) escalateSupplierContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionEscalateSupplier,
		AllowedStates:       []string{StateOpen},
		RequiredParameters:  []string{"supplier_id", "reason"},
		RequiredPermissions: []permission.Permission{PermEscalate},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			supplierID, _ := ctx.Parameters["supplier_id"].(string)
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionEscalateSupplier,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("supplier %s escalated: %s", supplierID, reason),
				EffectsSummary: "supplier escalation recorded; shift parked awaiting human",
				FollowUpEvents: []event.Event{
					shiftEvent(ctx.EntityID, EventSupplierEscalated, ctx.Actor.ID, map[string]any{
						"supplier_id": supplierID,
						"reason":      reason,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// ────────────────────────────────────────────────────────────────────
// Routing helpers
// ────────────────────────────────────────────────────────────────────

// NeedsApprovalForOrder reports whether an inventory order must open an
// approval. It checks the per-call ceiling and the running daily total
// the demo tracks per shift.
func (d *Domain) NeedsApprovalForOrder(shiftID string, amountCents int64) (bool, string) {
	if amountCents > SingleOrderCeilingCents {
		return true, fmt.Sprintf("amount %d > single-order ceiling %d", amountCents, SingleOrderCeilingCents)
	}
	prior := d.Cumulative(shiftID)
	if prior+amountCents > DailyOrderCeilingCents {
		return true, fmt.Sprintf("running total %d + %d would exceed daily cap %d", prior, amountCents, DailyOrderCeilingCents)
	}
	return false, ""
}

func shiftEvent(shiftID, eventType, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", eventType, shiftID, time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   shiftID,
		EntityType: EntityShift,
		Source:     "examples/ai-cafe-ops",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}

func readIntCents(params map[string]any, key string) (int64, error) {
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q", action.ErrMissingParameter, key)
	}
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%s must be a number, got %T", key, value)
	}
}

func readQuantity(params map[string]any) int64 {
	switch v := params["quantity"].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

// readHour extracts an hour-of-day (0..23) from either an integer-shaped
// value or an RFC3339 string. Returns (hour, ok).
func readHour(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		if v >= 0 && v <= 23 {
			return v, true
		}
		return 0, false
	case int64:
		if v >= 0 && v <= 23 {
			return int(v), true
		}
		return 0, false
	case float64:
		hour := int(v)
		if hour >= 0 && hour <= 23 {
			return hour, true
		}
		return 0, false
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.Hour(), true
		}
		// Allow "HH:MM" too for the offline fixture's convenience.
		if len(v) >= 2 {
			var h, m int
			if _, err := fmt.Sscanf(v, "%d:%d", &h, &m); err == nil && h >= 0 && h <= 23 {
				return h, true
			}
		}
		return 0, false
	}
	return 0, false
}
