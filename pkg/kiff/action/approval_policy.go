package action

import (
	"context"
	"fmt"
)

// ApprovalDecision is the result of evaluating a dynamic approval policy for a
// concrete action context. Reason is a short, human-readable explanation that
// flows into the approval-required error message and the audit trail — it is
// not a new parallel field, it rides the existing envelope.
type ApprovalDecision struct {
	// Required reports whether human approval must be granted before the
	// action can execute in this context.
	Required bool
	// Reason explains why approval was (or was not) required, e.g. "amount
	// 99900 exceeds the 50000 autonomous release limit". Surfaced on the
	// ErrApprovalRequired message when Required is true.
	Reason string
}

// ApprovalEvaluator decides at runtime whether an action needs approval, given
// the concrete action context. It augments the static ApprovalRequirement for
// parameter/state/actor-sensitive cases a single flag cannot express — amount
// thresholds, untrusted vendors, changed bank details, severity limits.
//
// The evaluator MUST be pure: the runtime consults it during both validation
// and execution, so it must return the same decision for the same context and
// must not perform side effects. Use typed parameters (see ParameterSpec) for
// reliable threshold reads.
type ApprovalEvaluator func(context.Context, ActionContext) (ApprovalDecision, error)

// RequiresApproval reports whether the contract needs approval for the given
// context, along with an optional reason. When an ApprovalPolicy is set it is
// consulted; otherwise the static ApprovalRequirement decides. A policy error
// is wrapped as ErrApprovalPolicy and fails safe — the caller must not treat
// it as permission to run.
func (c ActionContract) RequiresApproval(ctx context.Context, actionCtx ActionContext) (bool, string, error) {
	if c.ApprovalPolicy != nil {
		decision, err := c.ApprovalPolicy(ctx, actionCtx)
		if err != nil {
			return false, "", fmt.Errorf("%w: %s", ErrApprovalPolicy, err.Error())
		}
		return decision.Required, decision.Reason, nil
	}
	return c.ApprovalRequirement == ApprovalRequired, "", nil
}
