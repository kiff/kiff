package action

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/internal/trust"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

var (
	ErrStateNotAllowed  = errors.New("action state not allowed")
	ErrMissingParameter = errors.New("action required parameter missing")
	ErrInvalidParameter = errors.New("action parameter invalid")
	ErrPermissionDenied = errors.New("action permission denied")
	ErrApprovalRequired = errors.New("action requires approval")
	// ErrApprovalPolicy wraps a failure from a contract's dynamic
	// ApprovalPolicy evaluator. It fails safe: an action whose approval
	// requirement cannot be decided is never treated as allowed.
	ErrApprovalPolicy  = errors.New("approval policy evaluation failed")
	ErrInvalidContract = errors.New("invalid action contract")
	ErrDuplicateAction = errors.New("duplicate action contract")
	ErrExecutorMissing = errors.New("action contract has no executor")
)

// RiskLevel describes the operational risk of an action.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// ApprovalRequirement describes whether human approval is required.
type ApprovalRequirement string

const (
	ApprovalNever    ApprovalRequirement = "never"
	ApprovalRequired ApprovalRequirement = "required"
)

// ExecutionStatus describes the result status of an action execution.
type ExecutionStatus string

const (
	ExecutionSucceeded ExecutionStatus = "succeeded"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionSkipped   ExecutionStatus = "skipped"
)

// ActionContract describes when and how an action is allowed to run.
type ActionContract struct {
	Name          string
	AllowedStates []string
	// RequiredParameters lists parameter names that must be present and
	// non-nil. It is the simplest, backward-compatible way to declare
	// requirements. For type and constraint checking, use Parameters.
	RequiredParameters []string
	// Parameters optionally declares typed parameter schemas (type,
	// required, and constraints such as min/max, length, regex, allowed
	// values). When set, the default validator checks shape and constraints
	// before the executor runs — a malformed value is an invalid action, not
	// a blocked one. Leaving it empty preserves the pre-schema behavior.
	Parameters []ParameterSpec
	// ValidateParameters is an optional domain-owned semantic hook, run after
	// schema validation passes. Use it for cross-field or business rules that
	// a static schema cannot express (e.g. the amount is within an autonomous
	// release threshold, or the vendor/account pair matches). A non-nil error
	// classifies the action as invalid.
	ValidateParameters func(context.Context, ActionContext) error
	// RequiredPermissions are checked against the actor's policy-assigned
	// roles via the permission.Policy, resolved by Actor.ID — not from
	// Actor.Roles on the caller-built context (#19). The host assigns
	// roles to the policy from an authenticated identity; the framework
	// verifies the actor holds the permission under that trusted
	// membership.
	RequiredPermissions []permission.Permission
	Risk                RiskLevel
	ApprovalRequirement ApprovalRequirement
	// ApprovalPolicy, when set, decides at runtime whether this action needs
	// approval for the concrete context — amount, state, actor, vendor trust,
	// severity — augmenting the static ApprovalRequirement for cases a single
	// flag cannot express. When nil, ApprovalRequirement alone decides, so
	// existing contracts are unaffected. The runtime consults it during both
	// validation and execution, so the evaluator must be pure.
	ApprovalPolicy ApprovalEvaluator
	Executor       func(context.Context, ActionContext) (ActionResult, error)
}

// ActionContext carries the operational facts used to validate an action.
//
// Authority note: the permission check in DefaultValidator resolves the
// actor's roles from the permission.Policy keyed by Actor.ID — it does
// NOT read Actor.Roles. Roles are assigned to the policy from an
// authenticated identity (the host's job); Actor.Roles is descriptive
// metadata for audit/display and carries no authorization power (#19).
// This is the authority counterpart to the self-approval boundary: a
// caller cannot self-grant a permission by putting a role on the actor
// it submits, just as it cannot set the approved bit.
type ActionContext struct {
	ActionName   string
	EntityID     string
	EntityType   string
	CurrentState string
	Actor        actor.Actor
	Parameters   map[string]any
	ApprovalID   string
	approved     bool
}

// IsApproved returns whether the runtime has resolved a granted approval for this context.
func (c ActionContext) IsApproved() bool {
	return c.approved
}

// GrantApproval marks the action context as approved. It requires a
// trust.Grant, which can be minted only from inside the framework's
// trust boundary (the internal/trust package). A caller that merely
// imports the action package cannot construct a Grant — the parameter
// type is un-nameable outside the module — so it cannot self-approve.
// The runtime calls this only after the approval store confirms a real,
// granted approval for the action.
func (c *ActionContext) GrantApproval(trust.Grant) {
	c.approved = true
}

// ActionResult records the execution outcome.
type ActionResult struct {
	ActionName     string          `json:"action_name"`
	EntityID       string          `json:"entity_id"`
	Status         ExecutionStatus `json:"status"`
	Executed       bool            `json:"executed"`
	Message        string          `json:"message,omitempty"`
	Error          string          `json:"error,omitempty"`
	EffectsSummary string          `json:"effects_summary,omitempty"`
	Output         map[string]any  `json:"output,omitempty"`
	FollowUpEvents []event.Event   `json:"follow_up_events,omitempty"`
	ExecutedAt     time.Time       `json:"executed_at"`
}

// Normalize fills default status and timestamp fields.
func (r ActionResult) Normalize() ActionResult {
	if r.Status == "" {
		if r.Executed {
			r.Status = ExecutionSucceeded
		} else {
			r.Status = ExecutionSkipped
		}
	}
	if r.Status == ExecutionSucceeded {
		r.Executed = true
	}
	if r.ExecutedAt.IsZero() {
		r.ExecutedAt = time.Now().UTC()
	}
	return r
}

// FailedResult creates a failed execution result.
func FailedResult(actionName, entityID string, err error) ActionResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return ActionResult{
		ActionName: actionName,
		EntityID:   entityID,
		Status:     ExecutionFailed,
		Executed:   false,
		Message:    "action execution failed",
		Error:      message,
		ExecutedAt: time.Now().UTC(),
	}
}

// ValidationResult records validation facts that may be audited.
type ValidationResult struct {
	RequiresApproval bool
}

// Validator checks whether an action context satisfies an action contract.
type Validator interface {
	Validate(context.Context, ActionContext, ActionContract, permission.Policy) (ValidationResult, error)
}

// ActionValidator is kept as an explicit alias for readability.
type ActionValidator = Validator

// DefaultValidator applies the core KIFF action checks.
type DefaultValidator struct{}

// NewDefaultValidator creates a default action validator.
func NewDefaultValidator() DefaultValidator {
	return DefaultValidator{}
}

// Validate checks state, required parameters, permissions, and approval.
func (DefaultValidator) Validate(ctx context.Context, actionCtx ActionContext, contract ActionContract, policy permission.Policy) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	if contract.Name != "" && actionCtx.ActionName != "" && actionCtx.ActionName != contract.Name {
		return ValidationResult{}, fmt.Errorf("action context %q does not match contract %q", actionCtx.ActionName, contract.Name)
	}
	if len(contract.AllowedStates) > 0 && !contains(contract.AllowedStates, actionCtx.CurrentState) {
		return ValidationResult{}, fmt.Errorf("%w: %q", ErrStateNotAllowed, actionCtx.CurrentState)
	}
	for _, name := range contract.RequiredParameters {
		value, ok := actionCtx.Parameters[name]
		if !ok || value == nil {
			return ValidationResult{}, fmt.Errorf("%w: %q", ErrMissingParameter, name)
		}
	}
	// Typed parameter schemas: shape and constraints are checked here, before
	// the executor, so a malformed value is an invalid action (not a blocked
	// one) with a precise reason.
	if err := validateParams(contract.Parameters, actionCtx.Parameters); err != nil {
		return ValidationResult{}, err
	}
	// Domain-owned semantic validation runs last in the parameter step. Its
	// failures are invalid input; wrap so they classify as invalid_parameter.
	if contract.ValidateParameters != nil {
		if err := contract.ValidateParameters(ctx, actionCtx); err != nil {
			return ValidationResult{}, fmt.Errorf("%w: %s", ErrInvalidParameter, err.Error())
		}
	}
	for _, required := range contract.RequiredPermissions {
		if policy == nil || !policy.Can(ctx, actionCtx.Actor, required) {
			return ValidationResult{}, fmt.Errorf("%w: %q", ErrPermissionDenied, required)
		}
	}

	required, reason, err := contract.RequiresApproval(ctx, actionCtx)
	if err != nil {
		return ValidationResult{}, err
	}
	result := ValidationResult{RequiresApproval: required}
	if required && !actionCtx.IsApproved() {
		if reason != "" {
			return result, fmt.Errorf("%w: %s", ErrApprovalRequired, reason)
		}
		return result, ErrApprovalRequired
	}
	return result, nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
