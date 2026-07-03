package action

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/permission"
)

// thresholdContract requires approval only when amount_cents exceeds a limit.
func thresholdContract(limit int64) ActionContract {
	return ActionContract{
		Name:          "RELEASE_PAYMENT",
		AllowedStates: []string{"READY"},
		Parameters:    []ParameterSpec{IntParam("amount_cents")},
		ApprovalPolicy: func(_ context.Context, actx ActionContext) (ApprovalDecision, error) {
			amt, _ := toInt64(actx.Parameters["amount_cents"])
			if amt > limit {
				return ApprovalDecision{Required: true, Reason: "amount exceeds autonomous limit"}, nil
			}
			return ApprovalDecision{Required: false}, nil
		},
		Risk:     RiskHigh,
		Executor: func(context.Context, ActionContext) (ActionResult, error) { return ActionResult{}, nil },
	}
}

func releaseCtx(amount int64) ActionContext {
	return ActionContext{
		ActionName: "RELEASE_PAYMENT", EntityID: "inv-1", CurrentState: "READY",
		Parameters: map[string]any{"amount_cents": amount},
	}
}

func TestRequiresApproval_StaticFallback(t *testing.T) {
	// No policy: static requirement decides, exactly as before.
	c := ActionContract{ApprovalRequirement: ApprovalRequired}
	req, _, err := c.RequiresApproval(context.Background(), ActionContext{})
	if err != nil || !req {
		t.Fatalf("static required should be true, got req=%v err=%v", req, err)
	}
	c.ApprovalRequirement = ApprovalNever
	req, _, err = c.RequiresApproval(context.Background(), ActionContext{})
	if err != nil || req {
		t.Fatalf("static never should be false, got req=%v err=%v", req, err)
	}
}

func TestDefaultValidator_DynamicApprovalBelowThresholdAllowed(t *testing.T) {
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	if _, err := v.Validate(context.Background(), releaseCtx(4200), thresholdContract(50000), policy); err != nil {
		t.Fatalf("below threshold should pass without approval, got %v", err)
	}
}

func TestDefaultValidator_DynamicApprovalAboveThresholdHeld(t *testing.T) {
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	result, err := v.Validate(context.Background(), releaseCtx(99900), thresholdContract(50000), policy)
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("above threshold should require approval, got %v", err)
	}
	if !result.RequiresApproval {
		t.Fatal("result should report RequiresApproval")
	}
	// The policy reason rides the error message (no parallel field).
	if !strings.Contains(err.Error(), "exceeds autonomous limit") {
		t.Fatalf("expected policy reason in message, got %v", err)
	}
}

func TestDefaultValidator_DynamicApprovalPolicyErrorFailsSafe(t *testing.T) {
	c := thresholdContract(50000)
	c.ApprovalPolicy = func(context.Context, ActionContext) (ApprovalDecision, error) {
		return ApprovalDecision{}, errors.New("threshold service unavailable")
	}
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	_, err := v.Validate(context.Background(), releaseCtx(4200), c, policy)
	if !errors.Is(err, ErrApprovalPolicy) {
		t.Fatalf("policy error should surface as ErrApprovalPolicy, got %v", err)
	}
}

func TestDefaultValidator_DynamicApprovalSatisfiedWhenApproved(t *testing.T) {
	// When the runtime has resolved a granted approval, an above-threshold
	// action passes. We simulate that with an approved context.
	actx := releaseCtx(99900)
	actx.approved = true
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	if _, err := v.Validate(context.Background(), actx, thresholdContract(50000), policy); err != nil {
		t.Fatalf("approved above-threshold action should pass, got %v", err)
	}
}
