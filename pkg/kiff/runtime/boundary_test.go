package runtime

import (
	"context"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

// spyExecutor records whether it was called. The whole execution-boundary
// guarantee reduces to: this must stay false on every non-allowed path.
type spyExecutor struct{ called bool }

func (s *spyExecutor) exec(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
	s.called = true
	return action.ActionResult{ActionName: ctx.ActionName, EntityID: ctx.EntityID, Executed: true}, nil
}

const entityOrder = "Order"

// markPaidContract is a low-risk contract allowed only from CREATED.
func markPaidContract(exec *spyExecutor) action.ActionContract {
	return action.ActionContract{
		Name:                "MARK_PAID",
		AllowedStates:       []string{"CREATED"},
		RequiredParameters:  []string{"payment_id"},
		RequiredPermissions: []permission.Permission{"orders.mark_paid"},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor:            exec.exec,
	}
}

// refundContract is a high-risk, approval-required contract allowed from PAID.
func refundContract(exec *spyExecutor) action.ActionContract {
	return action.ActionContract{
		Name:                "REFUND_ORDER",
		AllowedStates:       []string{"PAID"},
		RequiredPermissions: []permission.Permission{"orders.refund"},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor:            exec.exec,
	}
}

func boundaryRuntime(t *testing.T) *Runtime {
	t.Helper()
	policy := permission.NewSimplePolicy()
	policy.GrantActor("agent", "orders.mark_paid")
	policy.GrantActor("agent", "orders.refund")
	return mustNew(t, Config{PermissionPolicy: policy})
}

var agentActor = actor.Actor{ID: "agent"}

func markPaidCtx(id, state string, params map[string]any) action.ActionContext {
	return action.ActionContext{
		ActionName:   "MARK_PAID",
		EntityID:     id,
		EntityType:   entityOrder,
		CurrentState: state,
		Actor:        agentActor,
		Parameters:   params,
	}
}

func refundCtx(id string) action.ActionContext {
	return action.ActionContext{
		ActionName:   "REFUND_ORDER",
		EntityID:     id,
		EntityType:   entityOrder,
		CurrentState: "PAID",
		Actor:        agentActor,
		ApprovalID:   "appr-" + id,
	}
}

// TestEvaluateAction_Outcomes checks the read-only decision envelope for each
// outcome, and that Evaluate never runs the executor.
func TestEvaluateAction_Outcomes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		state  string
		params map[string]any
		want   outcome.Outcome
		reason outcome.Reason
	}{
		{"allowed from CREATED", "CREATED", map[string]any{"payment_id": "p1"}, outcome.Allowed, outcome.ReasonNone},
		{"wrong state is blocked", "PAID", map[string]any{"payment_id": "p1"}, outcome.Blocked, outcome.ReasonStateNotAllowed},
		{"missing parameter is invalid", "CREATED", map[string]any{}, outcome.Invalid, outcome.ReasonMissingParameter},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			exec := &spyExecutor{}
			rt := boundaryRuntime(t)
			d := rt.EvaluateAction(ctx, markPaidCtx("order-1", tc.state, tc.params), markPaidContract(exec))
			if d.Outcome != tc.want || d.Reason != tc.reason {
				t.Fatalf("got (%q,%q), want (%q,%q)", d.Outcome, d.Reason, tc.want, tc.reason)
			}
			if exec.called {
				t.Fatalf("EvaluateAction must never run the executor")
			}
		})
	}
}

func TestEvaluateAction_PermissionDeniedIsBlocked(t *testing.T) {
	exec := &spyExecutor{}
	rt := mustNew(t, Config{PermissionPolicy: permission.NewSimplePolicy()}) // no grants
	d := rt.EvaluateAction(context.Background(), markPaidCtx("order-1", "CREATED", map[string]any{"payment_id": "p1"}), markPaidContract(exec))
	if d.Outcome != outcome.Blocked || d.Reason != outcome.ReasonPermissionDenied {
		t.Fatalf("got %+v, want blocked/permission_denied", d)
	}
	if exec.called {
		t.Fatalf("executor must not run")
	}
}

func TestEvaluateAction_ApprovalRequired(t *testing.T) {
	exec := &spyExecutor{}
	rt := boundaryRuntime(t)
	d := rt.EvaluateAction(context.Background(), refundCtx("order-1"), refundContract(exec))
	if d.Outcome != outcome.ApprovalRequired || d.NextStep != outcome.NextRequestApproval {
		t.Fatalf("got %+v, want approval_required + request_approval next step", d)
	}
	if exec.called {
		t.Fatalf("executor must not run for an approval-required evaluation")
	}
}

// TestExecutionBoundary_ExecutorNotCalledOnBlockedPaths is the load-bearing
// guarantee: the side effect only runs after KIFF says allowed.
func TestExecutionBoundary_ExecutorNotCalledOnBlockedPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("wrong state", func(t *testing.T) {
		exec := &spyExecutor{}
		rt := boundaryRuntime(t)
		_, _ = rt.ExecuteAction(ctx, markPaidCtx("o-wrongstate", "PAID", map[string]any{"payment_id": "p1"}), markPaidContract(exec))
		if exec.called {
			t.Fatalf("executor ran from a forbidden state")
		}
	})

	t.Run("missing parameter", func(t *testing.T) {
		exec := &spyExecutor{}
		rt := boundaryRuntime(t)
		_, _ = rt.ExecuteAction(ctx, markPaidCtx("o-missing", "CREATED", map[string]any{}), markPaidContract(exec))
		if exec.called {
			t.Fatalf("executor ran with a missing required parameter")
		}
	})

	t.Run("permission denied", func(t *testing.T) {
		exec := &spyExecutor{}
		rt := mustNew(t, Config{PermissionPolicy: permission.NewSimplePolicy()})
		_, _ = rt.ExecuteAction(ctx, markPaidCtx("o-perm", "CREATED", map[string]any{"payment_id": "p1"}), markPaidContract(exec))
		if exec.called {
			t.Fatalf("executor ran without permission")
		}
	})

	t.Run("approval required, none granted", func(t *testing.T) {
		exec := &spyExecutor{}
		rt := boundaryRuntime(t)
		_, _ = rt.ExecuteAction(ctx, refundCtx("o-noappr"), refundContract(exec))
		if exec.called {
			t.Fatalf("executor ran without an approval")
		}
	})

	t.Run("approval denied", func(t *testing.T) {
		exec := &spyExecutor{}
		rt := boundaryRuntime(t)
		actionCtx := refundCtx("o-denied")
		contract := refundContract(exec)
		if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "needs human"); err != nil {
			t.Fatalf("RequestApproval: %v", err)
		}
		if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, "human", approval.StatusDenied, "too risky"); err != nil {
			t.Fatalf("ReviewApproval: %v", err)
		}
		_, _ = rt.ExecuteAction(ctx, actionCtx, contract)
		if exec.called {
			t.Fatalf("executor ran after a denied approval")
		}
	})

	t.Run("allowed path does run the executor", func(t *testing.T) {
		exec := &spyExecutor{}
		rt := boundaryRuntime(t)
		if _, err := rt.ExecuteAction(ctx, markPaidCtx("o-ok", "CREATED", map[string]any{"payment_id": "p1"}), markPaidContract(exec)); err != nil {
			t.Fatalf("ExecuteAction(allowed): %v", err)
		}
		if !exec.called {
			t.Fatalf("executor should run on the allowed path")
		}
	})
}
