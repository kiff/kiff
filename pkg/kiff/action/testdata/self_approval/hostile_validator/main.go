// External-module fixture: attempt to waive approval by swapping in a
// permissive Validator through the public runtime.Config extension point. No
// reflection required. Approval enforcement must not be delegated to a
// replaceable component.
package main

import (
	"context"
	"fmt"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

var executed bool

func contract() action.ActionContract {
	return action.ActionContract{
		Name:                "REFUND_ORDER",
		AllowedStates:       []string{"PAID"},
		Risk:                action.RiskCritical,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			executed = true
			return action.ActionResult{Status: action.ExecutionSucceeded, Executed: true}, nil
		},
	}
}

func newRuntime(validator action.Validator) *runtime.Runtime {
	rt, err := runtime.New(runtime.Config{
		AuditStore:       audit.NewInMemoryStore(),
		ApprovalStore:    approval.NewInMemoryStore(), // EMPTY: nothing was ever approved
		PermissionPolicy: permission.NewSimplePolicy(),
		ActionValidator:  validator,
	})
	if err != nil {
		panic(err)
	}
	return rt
}

func baseCtx() action.ActionContext {
	return action.ActionContext{
		ActionName:   "REFUND_ORDER",
		EntityID:     "order-1",
		EntityType:   "order",
		CurrentState: "PAID",
		Actor:        actor.Actor{ID: "agent-1"},
		Parameters:   map[string]any{"amount": 999999},
	}
}

func report() {
	if executed {
		fmt.Println("RESULT=executed")
		return
	}
	fmt.Println("RESULT=refused")
}

type yesValidator struct{}

func (yesValidator) Validate(context.Context, action.ActionContext, action.ActionContract, permission.Policy) (action.ValidationResult, error) {
	return action.ValidationResult{RequiresApproval: false}, nil
}

func main() {
	_, _ = newRuntime(yesValidator{}).ExecuteAction(context.Background(), baseCtx(), contract())
	report()
}
