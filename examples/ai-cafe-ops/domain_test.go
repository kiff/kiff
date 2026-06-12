package aicafeops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

// TestStartShift_HappyPath confirms START_SHIFT runs without approval
// and moves the shift NEW → OPEN. This is the "low risk action stays
// low risk" baseline.
func TestStartShift_HappyPath(t *testing.T) {
	t.Parallel()
	d, rt, ctx := newShift(t, "shift-start-1")
	_ = d
	contract, ok := rt.Actions.Get(ActionStartShift)
	if !ok {
		t.Fatalf("missing %s contract", ActionStartShift)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionStartShift, EntityID: "shift-start-1", EntityType: EntityShift,
		CurrentState: StateNew, Actor: AgentActor,
		Parameters: map[string]any{"opened_by": "shift-manager"},
	}, contract); err != nil {
		t.Fatalf("ExecuteAction(start_shift): %v", err)
	}
	current, _, err := rt.States.Current(ctx, "shift-start-1")
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != StateOpen {
		t.Fatalf("expected %s, got %s", StateOpen, current.Value)
	}
}

// TestAutoOrder_BelowCeiling confirms a small in-catalog order executes
// without approval and updates the running total.
func TestAutoOrder_BelowCeiling(t *testing.T) {
	t.Parallel()
	d, rt, ctx := openShift(t, "shift-auto")
	auto, _ := rt.Actions.Get(ActionAutoOrderInventory)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionAutoOrderInventory, EntityID: "shift-auto", EntityType: EntityShift,
		CurrentState: StateOpen, Actor: AgentActor,
		Parameters: map[string]any{"item_id": "napkins", "quantity": 200, "amount_cents": 1500},
	}, auto); err != nil {
		t.Fatalf("ExecuteAction(auto order): %v", err)
	}
	if got := d.Cumulative("shift-auto"); got != 1500 {
		t.Fatalf("expected running total 1500, got %d", got)
	}
}

// TestOrderInventory_OverCeiling_RequiresApproval covers the per-call
// ceiling: any order > SingleOrderCeilingCents must hit the approval
// gate before the executor runs.
func TestOrderInventory_OverCeiling_RequiresApproval(t *testing.T) {
	t.Parallel()
	_, rt, ctx := openShift(t, "shift-big")
	contract, _ := rt.Actions.Get(ActionOrderInventory)
	actionCtx := action.ActionContext{
		ActionName: ActionOrderInventory, EntityID: "shift-big", EntityType: EntityShift,
		CurrentState: StateOpen, Actor: AgentActor,
		Parameters: map[string]any{
			"item_id":      "napkins",
			"quantity":     6000,
			"amount_cents": SingleOrderCeilingCents + 5000,
		},
		ApprovalID: "approval-big",
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); err != nil {
		t.Fatalf("ExecuteAction after grant: %v", err)
	}
}

// TestOrderInventory_DeniedStaysBlocked covers the denied path.
func TestOrderInventory_DeniedStaysBlocked(t *testing.T) {
	t.Parallel()
	_, rt, ctx := openShift(t, "shift-denied")
	contract, _ := rt.Actions.Get(ActionOrderInventory)
	actionCtx := action.ActionContext{
		ActionName: ActionOrderInventory, EntityID: "shift-denied", EntityType: EntityShift,
		CurrentState: StateOpen, Actor: AgentActor,
		Parameters: map[string]any{
			"item_id":      "napkins",
			"quantity":     8000,
			"amount_cents": SingleOrderCeilingCents + 9000,
		},
		ApprovalID: "approval-denied",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusDenied, "no"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if _, err := rt.ExecuteAction(ctx, actionCtx, contract); !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired after denial, got %v", err)
	}
}

// TestRequestSpecialty_RejectedNotInCatalog confirms the catalog check
// is the structural rejection: the executor itself returns
// ErrNotInCatalog when item_id is missing or off the allow-list.
// CheckCatalog is exercised directly to prove the same rejection
// independent of approval state.
func TestRequestSpecialty_RejectedNotInCatalog(t *testing.T) {
	t.Parallel()
	d := New()
	if err := d.CheckCatalog(map[string]any{"rationale": "trial"}); !errors.Is(err, ErrNotInCatalog) {
		t.Fatalf("expected ErrNotInCatalog without item_id, got %v", err)
	}
	if err := d.CheckCatalog(map[string]any{"item_id": "yuzu_concentrate"}); !errors.Is(err, ErrNotInCatalog) {
		t.Fatalf("expected ErrNotInCatalog for unknown item, got %v", err)
	}
	if err := d.CheckCatalog(map[string]any{"item_id": "napkins"}); err != nil {
		t.Fatalf("expected no error for catalog item, got %v", err)
	}
}

// TestSendStaffMessage_BlockedAfterHours covers the working-hours guard.
// CheckWorkingHours rejects timestamps outside the configured window
// before any approval is opened.
func TestSendStaffMessage_BlockedAfterHours(t *testing.T) {
	t.Parallel()
	d := New()
	if err := d.CheckWorkingHours(map[string]any{}); !errors.Is(err, ErrAfterHours) {
		t.Fatalf("expected ErrAfterHours without sent_at_local, got %v", err)
	}
	if err := d.CheckWorkingHours(map[string]any{"sent_at_local": "02:14"}); !errors.Is(err, ErrAfterHours) {
		t.Fatalf("expected ErrAfterHours at 02:14, got %v", err)
	}
	if err := d.CheckWorkingHours(map[string]any{"sent_at_local": 23}); !errors.Is(err, ErrAfterHours) {
		t.Fatalf("expected ErrAfterHours at hour 23, got %v", err)
	}
	if err := d.CheckWorkingHours(map[string]any{"sent_at_local": "14:30"}); err != nil {
		t.Fatalf("expected no error at 14:30, got %v", err)
	}
}

// TestSendStaffMessage_InHoursPlusApprovalExecutes covers the success
// path: working hours OK, approval granted, executor runs.
func TestSendStaffMessage_InHoursPlusApprovalExecutes(t *testing.T) {
	t.Parallel()
	_, rt, ctx := openShift(t, "shift-message")
	contract, _ := rt.Actions.Get(ActionSendStaffMessage)
	actionCtx := action.ActionContext{
		ActionName: ActionSendStaffMessage, EntityID: "shift-message", EntityType: EntityShift,
		CurrentState: StateOpen, Actor: AgentActor,
		Parameters: map[string]any{
			"recipient":     "barista-team",
			"message":       "extra rush expected after 4pm",
			"sent_at_local": "15:30",
		},
		ApprovalID: "approval-message",
	}
	if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "x"); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	res, err := rt.ExecuteAction(ctx, actionCtx, contract)
	if err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}
	if res.Status != action.ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %s", res.Status)
	}
}

// TestEscalateSupplier_NoApproval confirms ESCALATE_SUPPLIER executes
// without approval and parks the shift in AWAITING_HUMAN.
func TestEscalateSupplier_NoApproval(t *testing.T) {
	t.Parallel()
	_, rt, ctx := openShift(t, "shift-escalate")
	contract, _ := rt.Actions.Get(ActionEscalateSupplier)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionEscalateSupplier, EntityID: "shift-escalate", EntityType: EntityShift,
		CurrentState: StateOpen, Actor: AgentActor,
		Parameters: map[string]any{"supplier_id": "mercato-supplies", "reason": "missed delivery window"},
	}, contract); err != nil {
		t.Fatalf("ExecuteAction(escalate): %v", err)
	}
	current, _, _ := rt.States.Current(ctx, "shift-escalate")
	if current.Value != StateAwaitingHuman {
		t.Fatalf("expected %s, got %s", StateAwaitingHuman, current.Value)
	}
}

// TestNeedsApprovalForOrder covers the routing rule.
func TestNeedsApprovalForOrder(t *testing.T) {
	t.Parallel()
	d := New()
	if needs, _ := d.NeedsApprovalForOrder("s1", SingleOrderCeilingCents); needs {
		t.Fatalf("at-ceiling order should not need approval")
	}
	if needs, _ := d.NeedsApprovalForOrder("s1", SingleOrderCeilingCents+1); !needs {
		t.Fatalf("over-ceiling order must need approval")
	}
	// Stack four 4500-cent orders; the next 4500 would push the running
	// total over DailyOrderCeilingCents and must require approval even
	// though each individual call is under SingleOrderCeilingCents.
	for i := 0; i < 4; i++ {
		d.addOrder("s2", 4500)
	}
	if needs, _ := d.NeedsApprovalForOrder("s2", 4500); !needs {
		t.Fatalf("daily cap should trigger approval requirement")
	}
}

// helpers

func newShift(t *testing.T, id string) (*Domain, *runtime.Runtime, context.Context) {
	t.Helper()
	d := New()
	rt, err := d.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	ctx := context.Background()
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID: id + "-evt", Adapter: AdapterCafe, Type: EventShiftScheduled,
		Source: "test", EntityID: id, EntityType: EntityShift,
		ActorID: SystemActor.ID, ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw: %v", err)
	}
	return d, rt, ctx
}

func openShift(t *testing.T, id string) (*Domain, *runtime.Runtime, context.Context) {
	t.Helper()
	d, rt, ctx := newShift(t, id)
	contract, _ := rt.Actions.Get(ActionStartShift)
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName: ActionStartShift, EntityID: id, EntityType: EntityShift,
		CurrentState: StateNew, Actor: AgentActor,
		Parameters: map[string]any{"opened_by": "shift-manager"},
	}, contract); err != nil {
		t.Fatalf("ExecuteAction(start_shift): %v", err)
	}
	return d, rt, ctx
}
