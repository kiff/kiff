package mission

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/decision"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/evidence"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
)

const (
	EntityTypeMissionAttempt = "MissionAttempt"

	EventMissionSubmitted     = "MISSION_SUBMITTED"
	EventAttemptCreated       = "ATTEMPT_CREATED"
	EventMoveProposed         = "MOVE_PROPOSED"
	EventHumanApprovalGranted = "HUMAN_APPROVAL_GRANTED"
	EventMoveExecuted         = "MOVE_EXECUTED"

	StateSubmitted       = "SUBMITTED"
	StateActive          = "ACTIVE"
	StateWaitingApproval = "WAITING_APPROVAL"
	StateCompleted       = "COMPLETED"

	ActionCreateAttempt        = "CREATE_ATTEMPT"
	ActionProposeMove          = "PROPOSE_MOVE"
	ActionRequestHumanApproval = "REQUEST_HUMAN_APPROVAL"
	ActionExecuteMove          = "EXECUTE_MOVE"

	PermissionCreateAttempt        permission.Permission = "mission.create_attempt"
	PermissionProposeMove          permission.Permission = "mission.propose_move"
	PermissionRequestHumanApproval permission.Permission = "mission.request_human_approval"
	PermissionExecuteMove          permission.Permission = "mission.execute_move"
)

// Actors used by the local mission demo.
var (
	SystemActor = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "KIFF Demo System", Roles: []string{"system"}}
	AgentActor  = actor.Actor{ID: "mission-agent", Type: actor.TypeAgent, DisplayName: "Mission Agent", Roles: []string{"mission_agent"}}
	HumanActor  = actor.Actor{ID: "human-approver", Type: actor.TypeHuman, DisplayName: "Human Approver", Roles: []string{"mission_approver"}}
)

// NewStateMachine creates the mission attempt state machine.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
		state.Transition{EventType: EventMissionSubmitted, From: "", To: StateSubmitted},
		state.Transition{EventType: EventAttemptCreated, From: StateSubmitted, To: StateActive},
		state.Transition{EventType: EventMoveProposed, From: StateActive, To: StateWaitingApproval},
		state.Transition{EventType: EventHumanApprovalGranted, From: StateWaitingApproval, To: StateWaitingApproval},
		state.Transition{EventType: EventMoveExecuted, From: StateWaitingApproval, To: StateCompleted},
	)
	machine.SetAllowedActions(StateSubmitted, []string{ActionCreateAttempt})
	machine.SetAllowedActions(StateActive, []string{ActionProposeMove})
	machine.SetAllowedActions(StateWaitingApproval, []string{ActionRequestHumanApproval, ActionExecuteMove})
	return machine
}

// NewPermissionPolicy creates the mission demo policy.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
	policy.GrantRole("mission_agent", PermissionCreateAttempt)
	policy.GrantRole("mission_agent", PermissionProposeMove)
	policy.GrantRole("mission_agent", PermissionRequestHumanApproval)
	policy.GrantRole("mission_agent", PermissionExecuteMove)
	policy.GrantRole("mission_approver", PermissionRequestHumanApproval)
	policy.GrantRole("system", PermissionCreateAttempt)
	return policy
}

// Contracts returns the mission action contracts.
func Contracts() []action.ActionContract {
	return []action.ActionContract{
		{
			Name:                ActionCreateAttempt,
			AllowedStates:       []string{StateSubmitted},
			RequiredPermissions: []permission.Permission{PermissionCreateAttempt},
			Risk:                action.RiskLow,
			ApprovalRequirement: action.ApprovalNever,
		},
		{
			Name:                ActionProposeMove,
			AllowedStates:       []string{StateActive},
			RequiredParameters:  []string{"move"},
			RequiredPermissions: []permission.Permission{PermissionProposeMove},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
		},
		{
			Name:                ActionRequestHumanApproval,
			AllowedStates:       []string{StateWaitingApproval},
			RequiredPermissions: []permission.Permission{PermissionRequestHumanApproval},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
		},
		{
			Name:                ActionExecuteMove,
			AllowedStates:       []string{StateWaitingApproval},
			RequiredPermissions: []permission.Permission{PermissionExecuteMove},
			Risk:                action.RiskHigh,
			ApprovalRequirement: action.ApprovalRequired,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				move, _ := ctx.Parameters["move"].(string)
				return action.ActionResult{
					ActionName: ActionExecuteMove,
					EntityID:   ctx.EntityID,
					Executed:   true,
					Message:    fmt.Sprintf("move executed: %s", move),
					Output:     map[string]any{"move": move},
					ExecutedAt: time.Now().UTC(),
				}, nil
			},
		},
	}
}

// NewActionCatalog creates the mission action catalog.
func NewActionCatalog() (*action.Catalog, error) {
	catalog := action.NewCatalog()
	for _, contract := range Contracts() {
		if err := catalog.Register(contract); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

// NewRuntime creates a runtime wired for the mission example.
func NewRuntime() (*runtime.Runtime, error) {
	catalog, err := NewActionCatalog()
	if err != nil {
		return nil, err
	}
	return runtime.New(runtime.Config{
		StateMachine:     NewStateMachine(),
		PermissionPolicy: NewPermissionPolicy(),
		ActionCatalog:    catalog,
	}), nil
}

// DemoResult captures the observable outcome of the mission happy path.
type DemoResult struct {
	Lines      []string
	Audit      []audit.Record
	FinalState state.State
}

// RunHappyPath runs a compact mission attempt through the KIFF loop.
func RunHappyPath() (DemoResult, error) {
	attemptID := "mission-attempt-001"
	rt, err := NewRuntime()
	if err != nil {
		return DemoResult{}, err
	}
	lines := []string{}
	contract := func(name string) (action.ActionContract, error) {
		contract, ok := rt.Actions.Get(name)
		if !ok {
			return action.ActionContract{}, fmt.Errorf("missing mission action contract %q", name)
		}
		return contract, nil
	}

	if err := rt.IngestEvent(newEvent("evt-001", EventMissionSubmitted, attemptID, HumanActor.ID, map[string]any{"mission": "cross the line"})); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "event ingested: MISSION_SUBMITTED")
	lines = append(lines, "state changed: SUBMITTED")

	createCtx := action.ActionContext{
		ActionName:   ActionCreateAttempt,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateSubmitted,
		Actor:        AgentActor,
	}
	createContract, err := contract(ActionCreateAttempt)
	if err != nil {
		return DemoResult{}, err
	}
	if _, err := rt.ExecuteAction(createCtx, createContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action validated: CREATE_ATTEMPT")

	if err := rt.IngestEvent(newEvent("evt-002", EventAttemptCreated, attemptID, AgentActor.ID, nil)); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "state changed: ACTIVE")

	if err := rt.ProposeDecision(decision.Decision{
		ID:             "dec-001",
		EntityID:       attemptID,
		EntityType:     EntityTypeMissionAttempt,
		Kind:           decision.KindActionProposal,
		ProposedAction: ActionProposeMove,
		Evidence: []evidence.Ref{
			{ID: "evref-001", Kind: evidence.KindEvent, Source: "event-store", Summary: "mission was submitted", CreatedAt: time.Now().UTC()},
		},
		ReasoningSummary: "The attempt is active, so the next safe step is to propose a move.",
		Confidence:       0.82,
		ActorID:          AgentActor.ID,
		CreatedAt:        time.Now().UTC(),
	}); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "decision proposed: PROPOSE_MOVE")

	proposeCtx := action.ActionContext{
		ActionName:   ActionProposeMove,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateActive,
		Actor:        AgentActor,
		Parameters:   map[string]any{"move": "draft the first bounded move"},
	}
	proposeContract, err := contract(ActionProposeMove)
	if err != nil {
		return DemoResult{}, err
	}
	if err := rt.ValidateAction(proposeCtx, proposeContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action validated: PROPOSE_MOVE")

	if err := rt.IngestEvent(newEvent("evt-003", EventMoveProposed, attemptID, AgentActor.ID, map[string]any{"move": "draft the first bounded move"})); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "state changed: WAITING_APPROVAL")

	executeCtx := action.ActionContext{
		ActionName:   ActionExecuteMove,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateWaitingApproval,
		Actor:        AgentActor,
		Parameters:   map[string]any{"move": "draft the first bounded move"},
		ApprovalID:   "approval-001",
	}
	executeContract, err := contract(ActionExecuteMove)
	if err != nil {
		return DemoResult{}, err
	}
	if err := rt.ValidateAction(executeCtx, executeContract); !errors.Is(err, action.ErrApprovalRequired) {
		return DemoResult{}, fmt.Errorf("expected approval requirement, got %v", err)
	}
	lines = append(lines, "approval required: EXECUTE_MOVE")

	if err := rt.RecordApproval(approval.Approval{
		ID:          executeCtx.ApprovalID,
		EntityID:    attemptID,
		EntityType:  EntityTypeMissionAttempt,
		ActionName:  ActionExecuteMove,
		RequestedBy: AgentActor.ID,
		Status:      approval.StatusPending,
		Reason:      "high-risk move execution requires human authority",
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "approval recorded: pending")

	if err := rt.IngestEvent(newEvent("evt-004", EventHumanApprovalGranted, attemptID, HumanActor.ID, map[string]any{"approved_action": ActionExecuteMove})); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "event ingested: HUMAN_APPROVAL_GRANTED")

	if err := rt.RecordApproval(approval.Approval{
		ID:          executeCtx.ApprovalID,
		EntityID:    attemptID,
		EntityType:  EntityTypeMissionAttempt,
		ActionName:  ActionExecuteMove,
		RequestedBy: AgentActor.ID,
		ReviewedBy:  HumanActor.ID,
		Status:      approval.StatusGranted,
		Reason:      "human approved the bounded move",
		CreatedAt:   time.Now().UTC(),
		ReviewedAt:  time.Now().UTC(),
	}); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "approval granted: EXECUTE_MOVE")

	if _, err := rt.ExecuteAction(executeCtx, executeContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action executed: EXECUTE_MOVE")

	if err := rt.IngestEvent(newEvent("evt-005", EventMoveExecuted, attemptID, AgentActor.ID, map[string]any{"move": "draft the first bounded move"})); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "state changed: COMPLETED")

	records, err := rt.Audit.List(context.Background(), attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	finalState, _, err := rt.States.Current(context.Background(), attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, fmt.Sprintf("audit records created: %d", len(records)))

	return DemoResult{Lines: lines, Audit: records, FinalState: finalState}, nil
}

func newEvent(id, eventType, attemptID, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         id,
		Type:       eventType,
		EntityID:   attemptID,
		EntityType: EntityTypeMissionAttempt,
		Source:     "examples/mission",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}
