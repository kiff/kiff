// Package domain is the starter KIFF domain. It models a tiny "task" lifecycle
// (open → in progress → done) with one low-risk action and one
// approval-required action, so it shows the full KIFF loop in under 80 lines.
//
// Replace this file with your own domain when adapting the starter. The shape
// is the convention: one constants block, a state machine, an action catalog,
// a permission policy, and a domain definition assembled with domain.Builder.
package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/adapter"
	kiffdomain "github.com/kiffhq/kiff/pkg/kiff/domain"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
	"github.com/kiffhq/kiff/pkg/kiff/runtime"
	"github.com/kiffhq/kiff/pkg/kiff/state"
	"github.com/kiffhq/kiff/pkg/kiff/store"
)

// Identifiers. KIFF convention: UPPER_SNAKE_CASE for events, states, and
// action names; dotted lowercase for permissions.
const (
	AdapterTasks = "tasks"

	EntityTask = "Task"

	EventTaskCreated   = "TASK_CREATED"
	EventTaskStarted   = "TASK_STARTED"
	EventTaskCompleted = "TASK_COMPLETED"

	StateOpen       = "OPEN"
	StateInProgress = "IN_PROGRESS"
	StateDone       = "DONE"

	ActionStartTask    = "START_TASK"
	ActionCompleteTask = "COMPLETE_TASK"

	PermStartTask    permission.Permission = "tasks.start"
	PermCompleteTask permission.Permission = "tasks.complete"
	PermApprove      permission.Permission = "tasks.approve"
)

// Demo actors. Real applications source these from their identity layer.
var (
	SystemActor = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor  = actor.Actor{ID: "task-agent", Type: actor.TypeAgent, DisplayName: "Task Agent", Roles: []string{"task_agent"}}
	HumanActor  = actor.Actor{ID: "task-operator", Type: actor.TypeHuman, DisplayName: "Task Operator", Roles: []string{"task_operator"}}
)

// NewStateMachine returns the task state machine.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
		state.Transition{EventType: EventTaskCreated, From: "", To: StateOpen},
		state.Transition{EventType: EventTaskStarted, From: StateOpen, To: StateInProgress},
		state.Transition{EventType: EventTaskCompleted, From: StateInProgress, To: StateDone},
	)
	machine.SetAllowedActions(StateOpen, []string{ActionStartTask})
	machine.SetAllowedActions(StateInProgress, []string{ActionCompleteTask})
	return machine
}

// NewPermissionPolicy returns the demo permission policy.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole("task_agent", PermStartTask)
	policy.GrantRole("task_agent", PermCompleteTask)
	policy.GrantRole("task_operator", PermApprove)
	policy.GrantRole("task_operator", PermCompleteTask)
	policy.GrantRole("system", PermStartTask)
	return policy
}

// Contracts returns the action contracts owned by this domain.
func Contracts() []action.ActionContract {
	return []action.ActionContract{startTaskContract(), completeTaskContract()}
}

func startTaskContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionStartTask,
		AllowedStates:       []string{StateOpen},
		RequiredParameters:  []string{"assignee"},
		RequiredPermissions: []permission.Permission{PermStartTask},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			assignee, _ := ctx.Parameters["assignee"].(string)
			return action.ActionResult{
				ActionName:     ActionStartTask,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("task started by %s", assignee),
				EffectsSummary: "task moved to IN_PROGRESS",
				FollowUpEvents: []event.Event{
					taskEvent(ctx.EntityID, EventTaskStarted, ctx.Actor.ID, map[string]any{"assignee": assignee}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

func completeTaskContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionCompleteTask,
		AllowedStates:       []string{StateInProgress},
		RequiredParameters:  []string{"summary"},
		RequiredPermissions: []permission.Permission{PermCompleteTask},
		Risk:                action.RiskHigh,
		ApprovalRequirement: action.ApprovalRequired,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			summary, _ := ctx.Parameters["summary"].(string)
			return action.ActionResult{
				ActionName:     ActionCompleteTask,
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        fmt.Sprintf("task completed: %s", summary),
				EffectsSummary: "task moved to DONE",
				Output:         map[string]any{"summary": summary},
				FollowUpEvents: []event.Event{
					taskEvent(ctx.EntityID, EventTaskCompleted, ctx.Actor.ID, map[string]any{"summary": summary}),
				},
				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}

// NewDefinition returns the domain definition assembled via domain.Builder.
func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("tasks").
		Entity(EntityTask).
		Event(EventTaskCreated).
		Event(EventTaskStarted).
		Event(EventTaskCompleted).
		Transition(EventTaskCreated, "", StateOpen).
		Transition(EventTaskStarted, StateOpen, StateInProgress).
		Transition(EventTaskCompleted, StateInProgress, StateDone).
		Allow(StateOpen, ActionStartTask).
		Allow(StateInProgress, ActionCompleteTask)
	for _, contract := range Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter returns the domain input adapter.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterTasks)
}

// NewRuntime returns a runtime wired for this domain using in-memory stores.
func NewRuntime() (*runtime.Runtime, error) {
	return NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores returns a runtime wired for this domain using the
// provided store bundle. A nil bundle falls back to in-memory stores. Pass a
// file-backed bundle for persistence across restarts; pass a real backend in
// production.
func NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
	def, err := NewDefinition()
	if err != nil {
		return nil, err
	}
	in, err := NewInputAdapter()
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(def, runtime.Config{
		PermissionPolicy: NewPermissionPolicy(),
		Adapters:         []adapter.Adapter{in},
		Stores:           stores,
	})
}

func taskEvent(taskID, eventType, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", eventType, taskID, time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   taskID,
		EntityType: EntityTask,
		Source:     "tasks/executor",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}
