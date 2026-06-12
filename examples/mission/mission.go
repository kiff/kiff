package mission

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/evidence"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/proposal"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/state"
	"github.com/kiff/kiff/pkg/kiff/store"
)

const (
	AdapterMission = "mission"

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
	// Role membership is policy-owned (#19).
	policy.AssignRole(AgentActor.ID, "mission_agent")
	policy.AssignRole(HumanActor.ID, "mission_approver")
	policy.AssignRole(SystemActor.ID, "system")
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
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return action.ActionResult{
					ActionName:     ActionCreateAttempt,
					EntityID:       ctx.EntityID,
					Status:         action.ExecutionSucceeded,
					Executed:       true,
					Message:        "attempt created",
					EffectsSummary: "emitted ATTEMPT_CREATED event",
					FollowUpEvents: []event.Event{
						newEvent("evt-002", EventAttemptCreated, ctx.EntityID, ctx.Actor.ID, nil),
					},
					ExecutedAt: time.Now().UTC(),
				}, nil
			},
		},
		{
			Name:                ActionProposeMove,
			AllowedStates:       []string{StateActive},
			RequiredParameters:  []string{"move"},
			RequiredPermissions: []permission.Permission{PermissionProposeMove},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				move, _ := ctx.Parameters["move"].(string)
				return action.ActionResult{
					ActionName:     ActionProposeMove,
					EntityID:       ctx.EntityID,
					Status:         action.ExecutionSucceeded,
					Executed:       true,
					Message:        fmt.Sprintf("move proposed: %s", move),
					EffectsSummary: "emitted MOVE_PROPOSED event",
					FollowUpEvents: []event.Event{
						newEvent("evt-003", EventMoveProposed, ctx.EntityID, ctx.Actor.ID, map[string]any{"move": move}),
					},
					ExecutedAt: time.Now().UTC(),
				}, nil
			},
		},
		{
			Name:                ActionRequestHumanApproval,
			AllowedStates:       []string{StateWaitingApproval},
			RequiredPermissions: []permission.Permission{PermissionRequestHumanApproval},
			Risk:                action.RiskMedium,
			ApprovalRequirement: action.ApprovalNever,
			Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
				return action.ActionResult{
					ActionName:     ActionRequestHumanApproval,
					EntityID:       ctx.EntityID,
					Status:         action.ExecutionSucceeded,
					Executed:       true,
					Message:        "human approval requested",
					EffectsSummary: "approval request recorded",
					ExecutedAt:     time.Now().UTC(),
				}, nil
			},
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
					ActionName:     ActionExecuteMove,
					EntityID:       ctx.EntityID,
					Status:         action.ExecutionSucceeded,
					Executed:       true,
					Message:        fmt.Sprintf("move executed: %s", move),
					EffectsSummary: "recorded the bounded move execution",
					Output:         map[string]any{"move": move},
					FollowUpEvents: []event.Event{
						newEvent("evt-005", EventMoveExecuted, ctx.EntityID, ctx.Actor.ID, map[string]any{"move": move}),
					},
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

// NewDomainDefinition creates the mission domain definition using the
// domain.Builder so it reads as the canonical example for new domains.
func NewDomainDefinition() (domain.Definition, error) {
	policy := NewPermissionPolicy()
	_ = policy // policy is wired into the runtime, not the domain definition

	b := domain.New("mission").
		Entity(EntityTypeMissionAttempt).
		Event(EventMissionSubmitted).
		Event(EventAttemptCreated).
		Event(EventMoveProposed).
		Event(EventHumanApprovalGranted).
		Event(EventMoveExecuted).
		Transition(EventMissionSubmitted, "", StateSubmitted).
		Transition(EventAttemptCreated, StateSubmitted, StateActive).
		Transition(EventMoveProposed, StateActive, StateWaitingApproval).
		Transition(EventHumanApprovalGranted, StateWaitingApproval, StateWaitingApproval).
		Transition(EventMoveExecuted, StateWaitingApproval, StateCompleted).
		Allow(StateSubmitted, ActionCreateAttempt).
		Allow(StateActive, ActionProposeMove).
		Allow(StateWaitingApproval, ActionRequestHumanApproval, ActionExecuteMove)
	for _, contract := range Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter creates the mission input adapter.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter(AdapterMission)
}

// NewRuntime creates a runtime wired for the mission example using the default
// in-memory stores.
func NewRuntime() (*runtime.Runtime, error) {
	return NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores creates a runtime wired for the mission example using
// the provided store bundle. A nil bundle falls back to in-memory stores.
func NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
	definition, err := NewDomainDefinition()
	if err != nil {
		return nil, err
	}
	inputAdapter, err := NewInputAdapter()
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(definition, runtime.Config{
		PermissionPolicy: NewPermissionPolicy(),
		Adapters:         []adapter.Adapter{inputAdapter},
		Stores:           stores,
	})
}

// DemoResult captures the observable outcome of a mission demo path.
type DemoResult struct {
	Lines      []string
	Audit      []audit.Record
	Timeline   []audit.Record
	FinalState state.State
}

// RunHappyPath runs a compact mission attempt through the KIFF loop with granted approval.
func RunHappyPath() (DemoResult, error) {
	ctx := context.Background()
	attemptID := "mission-attempt-001"
	rt, err := NewRuntime()
	if err != nil {
		return DemoResult{}, err
	}
	lines := []string{}
	contract := func(name string) (action.ActionContract, error) {
		c, ok := rt.Actions.Get(name)
		if !ok {
			return action.ActionContract{}, fmt.Errorf("missing mission action contract %q", name)
		}
		return c, nil
	}

	// 1. Ingest raw event: MISSION_SUBMITTED
	traceID := "trace-mission-001"
	_, err = rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-001",
		Adapter:    AdapterMission,
		Type:       EventMissionSubmitted,
		Source:     "examples/mission/raw",
		EntityID:   attemptID,
		EntityType: EntityTypeMissionAttempt,
		ActorID:    HumanActor.ID,
		ReceivedAt: time.Now().UTC(),
		Metadata:   event.Metadata{TraceID: traceID, CorrelationID: "corr-mission-001"},
		Payload:    map[string]any{"mission": "cross the line"},
	})
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, fmt.Sprintf("raw input normalized: MISSION_SUBMITTED (trace=%s)", traceID))
	lines = append(lines, "event ingested: MISSION_SUBMITTED")
	lines = append(lines, "state changed: SUBMITTED")

	// 2. Execute CREATE_ATTEMPT (has executor, emits ATTEMPT_CREATED follow-up)
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
	if _, err := rt.ExecuteAction(ctx, createCtx, createContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action executed: CREATE_ATTEMPT")
	lines = append(lines, "follow-up event ingested: ATTEMPT_CREATED")
	lines = append(lines, "state changed: ACTIVE")

	// 3. Allowed actions
	allowed, err := rt.AllowedActions(ctx, attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, fmt.Sprintf("allowed actions: %s", actionNames(allowed)))

	// 4. Agent proposes a move (decision + validation)
	moveProposal := proposal.ActionProposal{
		ID:         "dec-001",
		EntityID:   attemptID,
		EntityType: EntityTypeMissionAttempt,
		ActionName: ActionProposeMove,
		Evidence: []evidence.Ref{
			{ID: "evref-001", Kind: evidence.KindEvent, Source: "event-store", Summary: "mission was submitted", CreatedAt: time.Now().UTC()},
		},
		ReasoningSummary: "The attempt is active, so the next safe step is to propose a move.",
		Confidence:       0.82,
		ActorID:          AgentActor.ID,
		CreatedAt:        time.Now().UTC(),
		Parameters:       map[string]any{"move": "draft the first bounded move"},
	}
	if err := rt.RecordActionProposal(ctx, moveProposal); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "decision proposed: PROPOSE_MOVE")

	proposeContract, err := contract(ActionProposeMove)
	if err != nil {
		return DemoResult{}, err
	}
	if err := rt.ValidateActionProposal(ctx, moveProposal, StateActive, AgentActor, proposeContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action validated: PROPOSE_MOVE")

	// 5. Execute PROPOSE_MOVE (emits MOVE_PROPOSED follow-up)
	proposeMoveCtx := action.ActionContext{
		ActionName:   ActionProposeMove,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateActive,
		Actor:        AgentActor,
		Parameters:   map[string]any{"move": "draft the first bounded move"},
	}
	if _, err := rt.ExecuteAction(ctx, proposeMoveCtx, proposeContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action executed: PROPOSE_MOVE")
	lines = append(lines, "follow-up event ingested: MOVE_PROPOSED")
	lines = append(lines, "state changed: WAITING_APPROVAL")

	// 6. Attempt to execute high-risk action without approval
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
	if err := rt.ValidateAction(ctx, executeCtx, executeContract); !errors.Is(err, action.ErrApprovalRequired) {
		return DemoResult{}, fmt.Errorf("expected approval requirement, got %v", err)
	}
	lines = append(lines, "approval required: EXECUTE_MOVE")

	// 7. Request approval
	if _, err := rt.RequestApproval(ctx, executeCtx.ApprovalID, executeCtx, executeContract, "high-risk move execution requires human authority"); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "approval requested: EXECUTE_MOVE")

	// 8. Human grants approval
	if err := rt.IngestEvent(ctx, newEvent("evt-004", EventHumanApprovalGranted, attemptID, HumanActor.ID, map[string]any{"approved_action": ActionExecuteMove})); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "event ingested: HUMAN_APPROVAL_GRANTED")

	if err := rt.RecordApproval(ctx, approval.Approval{
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

	// 9. Execute with granted approval
	executeResult, err := rt.ExecuteAction(ctx, executeCtx, executeContract)
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "action executed: EXECUTE_MOVE")
	lines = append(lines, fmt.Sprintf("execution result: %s (%s)", executeResult.Status, executeResult.EffectsSummary))
	lines = append(lines, "follow-up event ingested: MOVE_EXECUTED")
	lines = append(lines, "state changed: COMPLETED")

	records, err := rt.Audit.List(context.Background(), attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	timeline, err := rt.Timeline(ctx, attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	finalState, _, err := rt.States.Current(context.Background(), attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, fmt.Sprintf("audit records created: %d", len(records)))
	lines = append(lines, fmt.Sprintf("timeline records reconstructed: %d", len(timeline)))

	return DemoResult{Lines: lines, Audit: records, Timeline: timeline, FinalState: finalState}, nil
}

// RunDeniedPath demonstrates that KIFF blocks execution when approval is denied.
func RunDeniedPath() (DemoResult, error) {
	ctx := context.Background()
	attemptID := "mission-attempt-denied"
	rt, err := NewRuntime()
	if err != nil {
		return DemoResult{}, err
	}
	lines := []string{}
	contract := func(name string) (action.ActionContract, error) {
		c, ok := rt.Actions.Get(name)
		if !ok {
			return action.ActionContract{}, fmt.Errorf("missing mission action contract %q", name)
		}
		return c, nil
	}

	// Setup: get to WAITING_APPROVAL state
	_, err = rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-d01",
		Adapter:    AdapterMission,
		Type:       EventMissionSubmitted,
		Source:     "examples/mission/raw",
		EntityID:   attemptID,
		EntityType: EntityTypeMissionAttempt,
		ActorID:    HumanActor.ID,
		ReceivedAt: time.Now().UTC(),
		Payload:    map[string]any{"mission": "risky mission"},
	})
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "state changed: SUBMITTED")

	createContract, err := contract(ActionCreateAttempt)
	if err != nil {
		return DemoResult{}, err
	}
	createCtx := action.ActionContext{
		ActionName:   ActionCreateAttempt,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateSubmitted,
		Actor:        AgentActor,
	}
	if _, err := rt.ExecuteAction(ctx, createCtx, createContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "state changed: ACTIVE")

	proposeContract, err := contract(ActionProposeMove)
	if err != nil {
		return DemoResult{}, err
	}
	proposeMoveCtx := action.ActionContext{
		ActionName:   ActionProposeMove,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateActive,
		Actor:        AgentActor,
		Parameters:   map[string]any{"move": "risky move"},
	}
	if _, err := rt.ExecuteAction(ctx, proposeMoveCtx, proposeContract); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "state changed: WAITING_APPROVAL")

	// Agent tries to execute high-risk action
	executeContract, err := contract(ActionExecuteMove)
	if err != nil {
		return DemoResult{}, err
	}
	executeCtx := action.ActionContext{
		ActionName:   ActionExecuteMove,
		EntityID:     attemptID,
		EntityType:   EntityTypeMissionAttempt,
		CurrentState: StateWaitingApproval,
		Actor:        AgentActor,
		Parameters:   map[string]any{"move": "risky move"},
		ApprovalID:   "approval-denied-001",
	}

	// Request approval
	if _, err := rt.RequestApproval(ctx, executeCtx.ApprovalID, executeCtx, executeContract, "high-risk move needs human authority"); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "approval requested: EXECUTE_MOVE")

	// Human denies approval
	if _, err := rt.ReviewApproval(ctx, executeCtx.ApprovalID, HumanActor.ID, approval.StatusDenied, "move is too risky without more evidence"); err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, "approval denied: EXECUTE_MOVE")

	// Agent tries to execute — should be blocked
	_, execErr := rt.ExecuteAction(ctx, executeCtx, executeContract)
	if !errors.Is(execErr, action.ErrApprovalRequired) {
		return DemoResult{}, fmt.Errorf("expected ErrApprovalRequired after denial, got %v", execErr)
	}
	lines = append(lines, "execution blocked: EXECUTE_MOVE (approval not granted)")

	// Verify audit trail shows the denial
	timeline, err := rt.Timeline(ctx, attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	var sawDenied, sawBlocked bool
	for _, record := range timeline {
		if record.Kind == audit.KindApprovalDenied {
			sawDenied = true
		}
		if record.Kind == audit.KindApprovalRequired {
			sawBlocked = true
		}
	}
	if !sawDenied {
		return DemoResult{}, fmt.Errorf("expected approval denied audit record")
	}
	if !sawBlocked {
		return DemoResult{}, fmt.Errorf("expected approval required audit record after denied execution attempt")
	}
	lines = append(lines, "audit confirms: approval_denied recorded")
	lines = append(lines, "audit confirms: execution blocked after denial")

	finalState, _, err := rt.States.Current(context.Background(), attemptID)
	if err != nil {
		return DemoResult{}, err
	}
	lines = append(lines, fmt.Sprintf("final state: %s (unchanged, execution was prevented)", finalState.Value))

	return DemoResult{Lines: lines, Timeline: timeline, FinalState: finalState}, nil
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

func actionNames(contracts []action.ActionContract) string {
	names := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		names = append(names, contract.Name)
	}
	return fmt.Sprintf("%v", names)
}
