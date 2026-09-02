// External-module fixture: attempt self-approval by writing the unexported
// `approved` field directly through unsafe. A compile-time boundary cannot
// prevent this, so the runtime must not trust an inbound approved bit.
package main

import (
	"context"
	"fmt"
	"reflect"
	"unsafe"

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
	f := reflect.ValueOf(&ac).Elem().FieldByName("approved")
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetBool(true)
	_, _ = newRuntime(nil).ExecuteAction(context.Background(), ac, contract())
	report()
}
