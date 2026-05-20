package action

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
)

var (
	ErrStateNotAllowed  = errors.New("action state not allowed")
	ErrMissingParameter = errors.New("action required parameter missing")
	ErrPermissionDenied = errors.New("action permission denied")
	ErrApprovalRequired = errors.New("action requires approval")
	ErrInvalidContract  = errors.New("invalid action contract")
	ErrDuplicateAction  = errors.New("duplicate action contract")
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
	Approved     bool
}

// ActionResult records the execution outcome.
type ActionResult struct {
	ActionName string
	EntityID   string
	Executed   bool
	Message    string
	Output     map[string]any
	ExecutedAt time.Time
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
	if result.RequiresApproval && !actionCtx.Approved {
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
