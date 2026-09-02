// External-module fixture: attempt self-approval by calling the exported
// GrantApproval method with a zero value of the un-nameable internal
// trust.Grant type, recovered from the method's own signature. The internal/
// package rule is enforced by the compiler; reflection runs after it, so this
// compiles and runs. It must not result in execution.
package main

import (
	"context"
	"fmt"
	"reflect"

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

func main() {
	ac := baseCtx()
	m := reflect.ValueOf(&ac).MethodByName("GrantApproval")
	m.Call([]reflect.Value{reflect.Zero(m.Type().In(0))})
	_, _ = newRuntime(nil).ExecuteAction(context.Background(), ac, contract())
	report()
}
