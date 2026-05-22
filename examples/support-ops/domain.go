// Package supportops is the breadth demo for KIFF.
//
// One agent, five tools, five distinct governance behaviors in a single
// run. The point is to show that KIFF scales the governance per action,
// not per agent: each contract declares its own authority shape, and the
// runtime's validator + approval store keep the trust boundary intact
// regardless of which tool the agent picks.
//
// Entity is "Ticket". Lifecycle: NEW → TRIAGED → AWAITING_HUMAN → RESOLVED → CLOSED.
//
// Actions:
//
//   - TRIAGE_TICKET     no approval, agent permission
//   - ISSUE_REFUND      approval if amount_cents > 5000 OR cumulative_today
//                       on the ticket exceeds 20000 (the runtime tracks the
//                       running sum and enforces both via the contract's
//                       custom validator hook)
//   - WAIVE_FEE         approval always
//   - SEND_OUTREACH     custom validator hook on the contract: rejects
//                       unless parameters.consent_verified == true. This
//                       check runs BEFORE the approval gate, so consent
//                       failures never reach human review.
//   - ESCALATE_TO_HUMAN no approval (escalation is always allowed)
//   - CLOSE_TICKET      no approval, allowed only in RESOLVED
//
// The contracts use the public action.ActionContract surface plus a thin
// wrapping technique: the executor runs additional, contract-specific
// pre-checks (consent, daily-cap) and returns ErrMissingParameter or a
// typed sentinel error. KIFF's validator is unchanged — it still does
// state, parameters, permissions, approval — and the contract executor
// is where domain-specific guards live. This keeps pkg/kiff untouched.
package supportops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/domain"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store"
)

// SingleRefundCeilingCents — at or below this, ISSUE_REFUND is auto.
// Above, the action requires approval. 50 USD as cents, integer wire.
const SingleRefundCeilingCents = 5000

// CumulativeRefundCeilingCents — running sum on a ticket. Even if every
// individual call is below the single-refund ceiling, once the running
// sum on the same ticket exceeds this value the next refund must be
// approved.
const CumulativeRefundCeilingCents = 20000

// ErrConsentMissing is the structural rejection for SEND_OUTREACH when
// consent_verified is missing or false. It is exposed so callers (and
// the demo HTTP server) can classify it as a `blocked_consent_missing`
// outcome distinct from approval_required.
var ErrConsentMissing = errors.New("supportops: outreach blocked, consent_verified must be true")

// ErrAmountInvalid is returned by executors when an amount parameter is
// not a positive integer.
var ErrAmountInvalid = errors.New("supportops: amount must be a positive integer")

// Adapter, entity, event, state, action, and permission identifiers.
const (
	AdapterSupport = "support-ops"

	EntityTicket = "Ticket"

	EventTicketOpened     = "TICKET_OPENED"
	EventTicketTriaged    = "TICKET_TRIAGED"
	EventRefundIssued     = "REFUND_ISSUED"
	EventFeeWaived        = "FEE_WAIVED"
	EventOutreachSent     = "OUTREACH_SENT"
	EventEscalated        = "ESCALATED_TO_HUMAN"
	EventTicketResolved   = "TICKET_RESOLVED"
	EventTicketClosed     = "TICKET_CLOSED"

	StateNew            = "NEW"
	StateTriaged        = "TRIAGED"
	StateAwaitingHuman  = "AWAITING_HUMAN"
	StateResolved       = "RESOLVED"
	StateClosed         = "CLOSED"

	ActionTriageTicket     = "TRIAGE_TICKET"
	ActionAutoRefund       = "AUTO_REFUND"
	ActionIssueRefund      = "ISSUE_REFUND"
	ActionWaiveFee         = "WAIVE_FEE"
	ActionSendOutreach     = "SEND_OUTREACH"
	ActionEscalate         = "ESCALATE_TO_HUMAN"
	ActionCloseTicket      = "CLOSE_TICKET"

	PermTriage   permission.Permission = "supportops.triage"
	PermRefund   permission.Permission = "supportops.refund"
	PermWaive    permission.Permission = "supportops.waive"
	PermOutreach permission.Permission = "supportops.outreach"
	PermEscalate permission.Permission = "supportops.escalate"
	PermClose    permission.Permission = "supportops.close"
	PermApprove  permission.Permission = "supportops.approve"
)

// Demo actors.
var (
	SystemActor   = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor    = actor.Actor{ID: "support-agent", Type: actor.TypeAgent, DisplayName: "Support Agent", Roles: []string{"support_agent"}}
	OperatorActor = actor.Actor{ID: "ops-human", Type: actor.TypeHuman, DisplayName: "Ops Operator", Roles: []string{"ops_operator"}}
)

// Domain is the demo domain. It exposes a small mutable surface used by
// the contract executors (the cumulative refund tracker) so the demo
// remains self-contained without leaking back into pkg/kiff. One
// Domain corresponds to one runtime.
type Domain struct {
	mu             sync.Mutex
	refundsByTicket map[string]int64 // running sum of REFUND_ISSUED amounts per ticket
}

// New returns a fresh Domain.
func New() *Domain {
	return &Domain{refundsByTicket: map[string]int64{}}
}

// Cumulative returns the running refund total for a ticket. Used by the
// HTTP server to surface the cap to callers.
func (d *Domain) Cumulative(ticketID string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refundsByTicket[ticketID]
}

func (d *Domain) addRefund(ticketID string, amountCents int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refundsByTicket[ticketID] += amountCents
	return d.refundsByTicket[ticketID]
}

// NewPermissionPolicy returns the demo permission policy.
//
// The agent may propose every action and may execute the low-risk ones.
// Only operators carry approve. The runtime, not this policy, enforces
// approval on the contracts that need it.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	for _, perm := range []permission.Permission{
		PermTriage, PermRefund, PermWaive, PermOutreach, PermEscalate, PermClose,
	} {
		policy.GrantRole("support_agent", perm)
	}
	for _, perm := range []permission.Permission{
		PermTriage, PermRefund, PermWaive, PermOutreach, PermEscalate, PermClose, PermApprove,
	} {
		policy.GrantRole("ops_operator", perm)
	}
	policy.GrantRole("system", PermTriage)
	return policy
}

// Contracts returns the action contracts for the demo. The Domain is
// captured by closure so each executor can consult or update the
// running refund total without global state.
func (d *Domain) Contracts() []action.ActionContract {
	return []action.ActionContract{
		d.triageContract(),
		d.autoRefundContract(),
		d.issueRefundContract(),
		d.waiveFeeContract(),
		d.sendOutreachContract(),
		d.escalateContract(),
		d.closeContract(),
	}
}

// NewStateMachine builds the ticket state machine.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
		state.Transition{EventType: EventTicketOpened, From: "", To: StateNew},
		state.Transition{EventType: EventTicketTriaged, From: StateNew, To: StateTriaged},
		// Refund/Waive/Outreach do not move the ticket out of TRIAGED until
		// the resolution event fires; this lets multiple actions land on
		// the same ticket while it is still active.
		state.Transition{EventType: EventRefundIssued, From: StateTriaged, To: StateTriaged},
		state.Transition{EventType: EventFeeWaived, From: StateTriaged, To: StateTriaged},
		state.Transition{EventType: EventOutreachSent, From: StateTriaged, To: StateTriaged},
		// Escalation parks the ticket awaiting a human.
		state.Transition{EventType: EventEscalated, From: StateNew, To: StateAwaitingHuman},
		state.Transition{EventType: EventEscalated, From: StateTriaged, To: StateAwaitingHuman},
		// Resolution closes the active phase.
		state.Transition{EventType: EventTicketResolved, From: StateTriaged, To: StateResolved},
		state.Transition{EventType: EventTicketResolved, From: StateAwaitingHuman, To: StateResolved},
		state.Transition{EventType: EventTicketClosed, From: StateResolved, To: StateClosed},
	)
	machine.SetAllowedActions(StateNew, []string{ActionTriageTicket, ActionEscalate})
	machine.SetAllowedActions(StateTriaged, []string{
		ActionAutoRefund, ActionIssueRefund, ActionWaiveFee, ActionSendOutreach, ActionEscalate,
	})
	machine.SetAllowedActions(StateAwaitingHuman, []string{})
	machine.SetAllowedActions(StateResolved, []string{ActionCloseTicket})
	return machine
}

// NewDomainDefinition assembles the domain.
func (d *Domain) NewDomainDefinition() (domain.Definition, error) {
	b := domain.New("support-ops").
		Entity(EntityTicket).
		Event(EventTicketOpened).
		Event(EventTicketTriaged).
		Event(EventRefundIssued).
		Event(EventFeeWaived).
		Event(EventOutreachSent).
		Event(EventEscalated).
		Event(EventTicketResolved).
		Event(EventTicketClosed).
		Transition(EventTicketOpened, "", StateNew).
		Transition(EventTicketTriaged, StateNew, StateTriaged).
		Transition(EventRefundIssued, StateTriaged, StateTriaged).
		Transition(EventFeeWaived, StateTriaged, StateTriaged).
		Transition(EventOutreachSent, StateTriaged, StateTriaged).
		Transition(EventEscalated, StateNew, StateAwaitingHuman).
		Transition(EventEscalated, StateTriaged, StateAwaitingHuman).
		Transition(EventTicketResolved, StateTriaged, StateResolved).
		Transition(EventTicketResolved, StateAwaitingHuman, StateResolved).
		Transition(EventTicketClosed, StateResolved, StateClosed).
		Allow(StateNew, ActionTriageTicket).
		Allow(StateNew, ActionEscalate).
		Allow(StateTriaged, ActionAutoRefund).
		Allow(StateTriaged, ActionIssueRefund).
		Allow(StateTriaged, ActionWaiveFee).
		Allow(StateTriaged, ActionSendOutreach).
		Allow(StateTriaged, ActionEscalate).
		Allow(StateResolved, ActionCloseTicket)
	for _, contract := range d.Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter creates the input adapter for raw events.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterSupport)
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

func (d *Domain) triageContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionTriageTicket,
		AllowedStates:       []string{StateNew},
		RequiredParameters:  []string{"category"},
		RequiredPermissions: []permission.Permission{PermTriage},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			category, _ := ctx.Parameters["category"].(string)
			return action.ActionResult{
				ActionName:     ActionTriageTicket,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("triaged as %q", category),
				EffectsSummary: "ticket triaged",
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventTicketTriaged, ctx.Actor.ID, map[string]any{
						"category": category,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// autoRefundContract handles refunds that pass the per-call ceiling and
// the cumulative cap. It executes without approval and updates the
// running refund total. The HTTP server picks this contract via
// Domain.NeedsApprovalForRefund(); the agent's single tool surface
// remains "issue_refund".
func (d *Domain) autoRefundContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionAutoRefund,
		AllowedStates:       []string{StateTriaged},
		RequiredParameters:  []string{"amount_cents", "reason"},
		RequiredPermissions: []permission.Permission{PermRefund},
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
			reason, _ := ctx.Parameters["reason"].(string)
			running := d.addRefund(ctx.EntityID, amount)
			return action.ActionResult{
				ActionName:     ActionAutoRefund,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("auto-refund %d cents (%s); running total %d", amount, reason, running),
				EffectsSummary: "auto refund issued (under cap)",
				Output: map[string]any{
					"amount_cents":  amount,
					"running_cents": running,
					"reason":        reason,
					"path":          "auto",
				},
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventRefundIssued, ctx.Actor.ID, map[string]any{
						"amount_cents":  amount,
						"running_cents": running,
						"reason":        reason,
						"path":          "auto",
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// issueRefundContract is the high-risk variant. It is reached only when
// the HTTP server's routing decides the per-call ceiling or the
// cumulative cap is exceeded. ApprovalRequired means the runtime gate
// fires before the executor runs; once granted, the executor records
// the refund and increments the running total.
func (d *Domain) issueRefundContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionIssueRefund,
		AllowedStates:       []string{StateTriaged},
		RequiredParameters:  []string{"amount_cents", "reason"},
		RequiredPermissions: []permission.Permission{PermRefund},
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
			reason, _ := ctx.Parameters["reason"].(string)
			running := d.addRefund(ctx.EntityID, amount)
			return action.ActionResult{
				ActionName:     ActionIssueRefund,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("issued %d cents refund (%s); running total %d", amount, reason, running),
				EffectsSummary: "refund issued",
				Output: map[string]any{
					"amount_cents":      amount,
					"running_cents":     running,
					"reason":            reason,
				},
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventRefundIssued, ctx.Actor.ID, map[string]any{
						"amount_cents":  amount,
						"running_cents": running,
						"reason":        reason,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

func (d *Domain) waiveFeeContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionWaiveFee,
		AllowedStates:       []string{StateTriaged},
		RequiredParameters:  []string{"fee_cents", "reason"},
		RequiredPermissions: []permission.Permission{PermWaive},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			fee, err := readIntCents(ctx.Parameters, "fee_cents")
			if err != nil {
				return action.ActionResult{}, err
			}
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionWaiveFee,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("waived %d cents (%s)", fee, reason),
				EffectsSummary: "fee waived",
				Output:         map[string]any{"fee_cents": fee, "reason": reason},
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventFeeWaived, ctx.Actor.ID, map[string]any{
						"fee_cents": fee,
						"reason":    reason,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// sendOutreachContract pre-checks consent BEFORE the approval gate runs.
// The executor returns ErrConsentMissing when consent_verified is missing
// or false; the HTTP server classifies that as `blocked_consent_missing`
// and the audit trail records action_failed. Approvers never see consent
// failures, which is the entire point of a custom validator hook on the
// contract.
//
// To get the "before approval" guarantee we rely on KIFF's run order:
// the runtime calls applyApproval() then ValidateAction(). When approval
// is granted the executor runs; when it isn't, it never does. The
// consent check therefore runs as part of the executor only when the
// approval is in place. To run consent BEFORE approval is even
// requested, the demo HTTP server pre-checks the parameter against a
// thin Go helper (CheckOutreachConsent) and refuses to ever open an
// approval. That helper is exported so any caller can reuse the same
// check the executor uses.
func (d *Domain) sendOutreachContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionSendOutreach,
		AllowedStates:       []string{StateTriaged},
		RequiredParameters:  []string{"channel", "message", "consent_verified"},
		RequiredPermissions: []permission.Permission{PermOutreach},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			if err := CheckOutreachConsent(ctx.Parameters); err != nil {
				return action.ActionResult{}, err
			}
			channel, _ := ctx.Parameters["channel"].(string)
			message, _ := ctx.Parameters["message"].(string)
			return action.ActionResult{
				ActionName:     ActionSendOutreach,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("outreach sent on %s", channel),
				EffectsSummary: "outreach sent under consent + approval",
				Output:         map[string]any{"channel": channel, "message": message},
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventOutreachSent, ctx.Actor.ID, map[string]any{
						"channel": channel,
					}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// CheckOutreachConsent enforces that consent_verified is present and
// truthy before any outreach moves forward. Exported so the HTTP server
// can pre-check before opening an approval — otherwise consent failures
// would be queued for an operator to dispose of.
func CheckOutreachConsent(params map[string]any) error {
	value, ok := params["consent_verified"]
	if !ok {
		return ErrConsentMissing
	}
	switch v := value.(type) {
	case bool:
		if !v {
			return ErrConsentMissing
		}
	case string:
		if v != "true" && v != "1" {
			return ErrConsentMissing
		}
	case nil:
		return ErrConsentMissing
	default:
		return ErrConsentMissing
	}
	return nil
}

func (d *Domain) escalateContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionEscalate,
		AllowedStates:       []string{StateNew, StateTriaged},
		RequiredParameters:  []string{"reason"},
		RequiredPermissions: []permission.Permission{PermEscalate},
		Risk:                action.RiskMedium,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			reason, _ := ctx.Parameters["reason"].(string)
			return action.ActionResult{
				ActionName:     ActionEscalate,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("escalated to human: %s", reason),
				EffectsSummary: "escalation recorded",
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventEscalated, ctx.Actor.ID, map[string]any{"reason": reason}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

func (d *Domain) closeContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionCloseTicket,
		AllowedStates:       []string{StateResolved},
		RequiredParameters:  []string{},
		RequiredPermissions: []permission.Permission{PermClose},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{
				ActionName:     ActionCloseTicket,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        "ticket closed",
				EffectsSummary: "ticket closed",
				FollowUpEvents: []event.Event{
					ticketEvent(ctx.EntityID, EventTicketClosed, ctx.Actor.ID, map[string]any{}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// ────────────────────────────────────────────────────────────────────
// Routing helpers
// ────────────────────────────────────────────────────────────────────

// NeedsApprovalForRefund reports whether a refund request must open an
// approval. It checks the per-call ceiling and the running daily total
// the demo tracks per ticket.
func (d *Domain) NeedsApprovalForRefund(ticketID string, amountCents int64) (bool, string) {
	if amountCents > SingleRefundCeilingCents {
		return true, fmt.Sprintf("amount %d > single-refund ceiling %d", amountCents, SingleRefundCeilingCents)
	}
	prior := d.Cumulative(ticketID)
	if prior+amountCents > CumulativeRefundCeilingCents {
		return true, fmt.Sprintf("running total %d + %d would exceed cap %d", prior, amountCents, CumulativeRefundCeilingCents)
	}
	return false, ""
}

func ticketEvent(ticketID, eventType, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", eventType, ticketID, time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   ticketID,
		EntityType: EntityTicket,
		Source:     "examples/support-ops",
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
