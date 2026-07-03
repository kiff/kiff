// Package outcome defines the normalized decision envelope for the governed
// action boundary. It is the single, typed vocabulary every layer speaks when
// it reports what KIFF decided about an action: the Go runtime, the HTTP API,
// scaffolded app APIs, and external integrations (guard/cloud).
//
// The envelope is deliberately small. It answers three questions in a stable
// shape a machine can switch on and a human can read:
//
//   - Outcome: did the action run, does it need approval, or was it stopped?
//   - Reason:  if it did not run, why — with a stable reason code?
//   - NextStep: what the caller can do about it.
//
// The vocabulary maps 1:1 onto the framework's existing action error sentinels
// via Classify, so there is one source of truth rather than a parallel set of
// strings re-invented per caller.
package outcome

import (
	"errors"

	"github.com/kiff/kiff/pkg/kiff/action"
)

// Outcome is the top-level result of evaluating or executing an action. It has
// exactly four values so callers can switch on it exhaustively.
type Outcome string

const (
	// Allowed means the action passed every check (and, if execution was
	// requested, its executor ran).
	Allowed Outcome = "allowed"
	// ApprovalRequired means the action is valid in the current state but a
	// human approval must be granted before it can execute.
	ApprovalRequired Outcome = "approval_required"
	// Blocked means policy or state forbids the action right now: the entity
	// is in the wrong state, or the actor lacks permission.
	Blocked Outcome = "blocked"
	// Invalid means the proposal or contract is malformed: a required
	// parameter is missing, the action is unknown, or the contract has no
	// executor.
	Invalid Outcome = "invalid"
)

// Reason is a stable, machine-switchable code explaining a non-allowed outcome.
type Reason string

const (
	ReasonNone             Reason = ""
	ReasonStateNotAllowed  Reason = "state_not_allowed"
	ReasonMissingParameter Reason = "missing_parameter"
	ReasonInvalidParameter Reason = "invalid_parameter"
	ReasonPermissionDenied Reason = "permission_denied"
	ReasonApprovalRequired Reason = "approval_required"
	ReasonExecutorMissing  Reason = "executor_missing"
	ReasonInvalidContract  Reason = "invalid_contract"
	ReasonUnknownAction    Reason = "unknown_action"
	// ReasonError is a fail-safe reason for an unclassified failure. The
	// outcome for an unclassified failure is Blocked, never Allowed.
	ReasonError Reason = "error"
)

// NextStep names of the conventional follow-up a caller can take.
const (
	NextRequestApproval = "request_approval"
)

// Decision is the normalized envelope returned by the governed action
// boundary. It is JSON-serializable for HTTP and tool responses.
type Decision struct {
	Outcome      Outcome `json:"outcome"`
	Reason       Reason  `json:"reason,omitempty"`
	Action       string  `json:"action,omitempty"`
	EntityID     string  `json:"entity_id,omitempty"`
	CurrentState string  `json:"current_state,omitempty"`
	NextStep     string  `json:"next_step,omitempty"`
	Message      string  `json:"message,omitempty"`
}

// OK reports whether the action is allowed to proceed.
func (d Decision) OK() bool { return d.Outcome == Allowed }

// Classify maps an action validation/execution error to a normalized outcome
// and reason. A nil error is Allowed. An unrecognized error fails safe to
// Blocked so a caller never treats an unknown failure as permission to run.
func Classify(err error) (Outcome, Reason) {
	switch {
	case err == nil:
		return Allowed, ReasonNone
	case errors.Is(err, action.ErrApprovalRequired):
		return ApprovalRequired, ReasonApprovalRequired
	case errors.Is(err, action.ErrStateNotAllowed):
		return Blocked, ReasonStateNotAllowed
	case errors.Is(err, action.ErrPermissionDenied):
		return Blocked, ReasonPermissionDenied
	case errors.Is(err, action.ErrMissingParameter):
		return Invalid, ReasonMissingParameter
	case errors.Is(err, action.ErrInvalidParameter):
		return Invalid, ReasonInvalidParameter
	case errors.Is(err, action.ErrExecutorMissing):
		return Invalid, ReasonExecutorMissing
	case errors.Is(err, action.ErrInvalidContract):
		return Invalid, ReasonInvalidContract
	default:
		// ErrDuplicateAction is intentionally not mapped here: it is a
		// registration-time error that cannot surface through action
		// evaluation. Any unrecognized error fails safe to Blocked so a
		// caller never reads an unknown failure as permission to run.
		return Blocked, ReasonError
	}
}

// FromError builds a Decision from an error and the action context. It is the
// primary constructor: pass the error returned by validation or execution
// along with the identifying facts, and get a fully-formed envelope.
func FromError(err error, actionName, entityID, currentState string) Decision {
	oc, reason := Classify(err)
	d := Decision{
		Outcome:      oc,
		Reason:       reason,
		Action:       actionName,
		EntityID:     entityID,
		CurrentState: currentState,
	}
	if oc == ApprovalRequired {
		d.NextStep = NextRequestApproval
	}
	if err != nil {
		d.Message = err.Error()
	}
	return d
}

// Succeeded builds an Allowed decision for an action that executed.
func Succeeded(actionName, entityID, currentState string) Decision {
	return Decision{
		Outcome:      Allowed,
		Action:       actionName,
		EntityID:     entityID,
		CurrentState: currentState,
	}
}

// UnknownAction builds an Invalid decision for a tool/action name that has no
// registered contract. This case does not surface as an action error (the
// lookup fails before validation), so callers that resolve actions by name
// construct it directly.
func UnknownAction(actionName, entityID string) Decision {
	return Decision{
		Outcome:  Invalid,
		Reason:   ReasonUnknownAction,
		Action:   actionName,
		EntityID: entityID,
		Message:  "no contract registered for action",
	}
}
