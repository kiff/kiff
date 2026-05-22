package action

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
)

var (
	ErrStateNotAllowed  = errors.New("action state not allowed")
	ErrMissingParameter = errors.New("action required parameter missing")
	ErrPermissionDenied = errors.New("action permission denied")
	ErrApprovalRequired = errors.New("action requires approval")
	ErrInvalidContract  = errors.New("invalid action contract")
	ErrDuplicateAction  = errors.New("duplicate action contract")
	ErrExecutorMissing  = errors.New("action contract has no executor")
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
	Name                string
	AllowedStates       []string
	RequiredParameters  []string
	RequiredPermissions []permission.Permission
	Risk                RiskLevel
	ApprovalRequirement ApprovalRequirement
	Executor            func(context.Context, ActionContext) (ActionResult, error)
}

// ActionContext carries the operational facts used to validate an action.
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

// GrantApproval marks the action context as approved. Only the runtime should call this.
// It is exported so the runtime package can set it, but callers should not use it directly.
func (c *ActionContext) GrantApproval() {
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
	for _, required := range contract.RequiredPermissions {
		if policy == nil || !policy.Can(ctx, actionCtx.Actor, required) {
			return ValidationResult{}, fmt.Errorf("%w: %q", ErrPermissionDenied, required)
		}
	}

	result := ValidationResult{RequiresApproval: contract.ApprovalRequirement == ApprovalRequired}
	if result.RequiresApproval && !actionCtx.IsApproved() {
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
