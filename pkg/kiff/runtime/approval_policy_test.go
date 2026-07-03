package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

// dynamicReleaseContract requires approval only when amount_cents > limit,
// with no static ApprovalRequirement set — approval is purely dynamic.
func dynamicReleaseContract(limit int64) action.ActionContract {
	return action.ActionContract{
		Name:          "RELEASE_PAYMENT",
		AllowedStates: []string{"READY"},
		Parameters:    []action.ParameterSpec{action.IntParam("amount_cents")},
		ApprovalPolicy: func(_ context.Context, actx action.ActionContext) (action.ApprovalDecision, error) {
			amt, _ := toInt64Test(actx.Parameters["amount_cents"])
			if amt > limit {
				return action.ApprovalDecision{Required: true, Reason: "amount over limit"}, nil
			}
			return action.ApprovalDecision{Required: false}, nil
		},
		RequiredPermissions: []permission.Permission{"payment.release"},
		Executor:            noopExecutor,
	}
}

func toInt64Test(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

func dynamicReleaseCtx(amount int64, approvalID string) action.ActionContext {
	return action.ActionContext{
		ActionName: "RELEASE_PAYMENT", EntityID: "inv-1", EntityType: "Invoice",
		CurrentState: "READY", Actor: actor.Actor{ID: "svc"},
		ApprovalID: approvalID,
		Parameters: map[string]any{"amount_cents": amount},
	}
}

func dynamicPolicy() permission.Policy {
	p := permission.NewSimplePolicy()
	p.GrantActor("svc", "payment.release")
	return p
}

func TestDynamicApproval_BelowThresholdExecutesWithoutApproval(t *testing.T) {
	rt := mustNew(t, Config{PermissionPolicy: dynamicPolicy()})
	contract := dynamicReleaseContract(50000)
	if _, err := rt.ExecuteAction(context.Background(), dynamicReleaseCtx(4200, ""), contract); err != nil {
		t.Fatalf("below-threshold release should execute, got %v", err)
	}
}

func TestDynamicApproval_AboveThresholdHeldThenExecutesOnGrant(t *testing.T) {
	rt := mustNew(t, Config{PermissionPolicy: dynamicPolicy()})
	contract := dynamicReleaseContract(50000)
	ctx := context.Background()
	actionCtx := dynamicReleaseCtx(99900, "ap-1")

	// Above threshold, no grant yet: held.
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("above-threshold release should be held, got %v", err)
	}

	// RequestApproval must honor the dynamic requirement even though the
	// static ApprovalRequirement is unset.
	requestCtx := actionCtx
	requestCtx.Actor = actor.Actor{ID: "agent"}
	if _, err := rt.RequestApproval(ctx, "ap-1", requestCtx, contract, "over limit"); err != nil {
		t.Fatalf("request approval for dynamic requirement: %v", err)
	}

	// Grant it, then execution proceeds.
	if _, err := rt.ReviewApproval(ctx, "ap-1", "human", approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("review approval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("granted above-threshold release should execute, got %v", err)
	}
}

func TestDynamicApproval_RequestRejectedWhenNotRequired(t *testing.T) {
	rt := mustNew(t, Config{PermissionPolicy: dynamicPolicy()})
	contract := dynamicReleaseContract(50000)
	// Below threshold → not required → RequestApproval refuses.
	_, err := rt.RequestApproval(context.Background(), "ap-1", dynamicReleaseCtx(4200, "ap-1"), contract, "n/a")
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected refusal to request approval for a non-required action, got %v", err)
	}
}

func TestDynamicApproval_PolicyErrorBlocksExecution(t *testing.T) {
	rt := mustNew(t, Config{PermissionPolicy: dynamicPolicy()})
	contract := dynamicReleaseContract(50000)
	contract.ApprovalPolicy = func(context.Context, action.ActionContext) (action.ApprovalDecision, error) {
		return action.ApprovalDecision{}, errors.New("threshold service down")
	}
	_, err := rt.ExecuteAction(context.Background(), dynamicReleaseCtx(4200, "ap-1"), contract)
	if !errors.Is(err, action.ErrApprovalPolicy) {
		t.Fatalf("policy error should block execution with ErrApprovalPolicy, got %v", err)
	}
}
