package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
	"github.com/kiffhq/kiff/pkg/kiff/runtime"
)

// recordingMetrics is a test-only MetricsRecorder that captures every
// Inc call so tests can assert exact names, increments, and attrs.
type recordingMetrics struct {
	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	Name  string
	N     uint64
	Attrs []runtime.Attr
}

func (m *recordingMetrics) Inc(name string, n uint64, attrs ...runtime.Attr) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]runtime.Attr, len(attrs))
	copy(cp, attrs)
	m.calls = append(m.calls, recordedCall{Name: name, N: n, Attrs: cp})
}

func (m *recordingMetrics) snapshot() []recordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *recordingMetrics) countsByName() map[string]uint64 {
	out := map[string]uint64{}
	for _, c := range m.snapshot() {
		out[c.Name] += c.N
	}
	return out
}

func TestNoopMetricsIsDefault(t *testing.T) {
	t.Parallel()

	rt, err := runtime.New(runtime.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Sanity: ingesting an event with the default config does not
	// blow up. The default NoopMetrics absorbs the increment.
	if err := rt.IngestEvent(context.Background(), event.Event{
		ID:         "ev-1",
		Type:       "TICKET_OPENED",
		EntityID:   "ticket-1",
		EntityType: "ticket",
		Source:     "test",
		ActorID:    "user-1",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestEvent with default metrics: %v", err)
	}
}

func TestEntityTypeAttrShape(t *testing.T) {
	t.Parallel()
	a := runtime.EntityType("order")
	if a.Key != "entity_type" {
		t.Fatalf("Key: got %q, want %q", a.Key, "entity_type")
	}
	if a.Value != "order" {
		t.Fatalf("Value: got %q, want %q", a.Value, "order")
	}
}

func TestRuntimeIncrementsEventsIngested(t *testing.T) {
	t.Parallel()

	m := &recordingMetrics{}
	rt, err := runtime.New(runtime.Config{Metrics: m})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := rt.IngestEvent(context.Background(), event.Event{
		ID:         "ev-1",
		Type:       "TICKET_OPENED",
		EntityID:   "ticket-1",
		EntityType: "ticket",
		Source:     "test",
		ActorID:    "user-1",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}

	got := m.countsByName()
	if got[runtime.CounterEventsIngested] != 1 {
		t.Fatalf("events ingested: got %d, want 1; snapshot=%+v", got[runtime.CounterEventsIngested], m.snapshot())
	}

	calls := m.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(calls), calls)
	}
	if len(calls[0].Attrs) != 1 || calls[0].Attrs[0] != runtime.EntityType("ticket") {
		t.Fatalf("expected EntityType('ticket') attr; got %+v", calls[0].Attrs)
	}
}

func TestRuntimeIncrementsDecisionsRecorded(t *testing.T) {
	t.Parallel()

	m := &recordingMetrics{}
	rt, err := runtime.New(runtime.Config{Metrics: m})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := rt.ProposeDecision(context.Background(), decision.Decision{
		ID:             "dec-1",
		EntityID:       "order-1",
		EntityType:     "order",
		ActorID:        "agent-1",
		Kind:           "propose-action",
		ProposedAction: "ISSUE_REFUND",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ProposeDecision: %v", err)
	}

	if got := m.countsByName()[runtime.CounterDecisionsRecorded]; got != 1 {
		t.Fatalf("decisions recorded: got %d, want 1", got)
	}
}

func TestRuntimeIncrementsActionsValidatedAndExecuted(t *testing.T) {
	t.Parallel()

	m := &recordingMetrics{}

	// A trivial action contract: no approval, no parameters, no
	// permission requirements, executor returns success.
	executor := func(ctx context.Context, ac action.ActionContext) (action.ActionResult, error) {
		return action.ActionResult{
			ActionName: ac.ActionName,
			EntityID:   ac.EntityID,
			Status:     action.ExecutionSucceeded,
			Executed:   true,
		}, nil
	}
	contract := action.ActionContract{
		Name:                "MARK_PAID",
		AllowedStates:       []string{"CREATED"},
		ApprovalRequirement: action.ApprovalNever,
		Risk:                action.RiskLow,
		Executor:            executor,
	}

	rt, err := runtime.New(runtime.Config{
		Metrics:          m,
		PermissionPolicy: permission.NewSimplePolicy(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	actionCtx := action.ActionContext{
		ActionName:   "MARK_PAID",
		EntityID:     "order-1",
		EntityType:   "order",
		Actor:        actor.Actor{ID: "user-1", Roles: []string{"ops"}},
		CurrentState: "CREATED",
	}

	if err := rt.ValidateAction(context.Background(), actionCtx, contract); err != nil {
		t.Fatalf("ValidateAction: %v", err)
	}
	if got := m.countsByName()[runtime.CounterActionsValidated]; got != 1 {
		t.Fatalf("actions validated: got %d, want 1", got)
	}

	result, err := rt.ExecuteAction(context.Background(), actionCtx, contract)
	if err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}
	if result.Status != action.ExecutionSucceeded {
		t.Fatalf("status: got %v, want %v", result.Status, action.ExecutionSucceeded)
	}
	// ExecuteAction internally calls ValidateAction, so we expect 2
	// validated counts and 1 executed count.
	counts := m.countsByName()
	if counts[runtime.CounterActionsValidated] != 2 {
		t.Fatalf("actions validated after execute: got %d, want 2", counts[runtime.CounterActionsValidated])
	}
	if counts[runtime.CounterActionsExecuted] != 1 {
		t.Fatalf("actions executed: got %d, want 1", counts[runtime.CounterActionsExecuted])
	}
}

func TestRuntimeIncrementsApprovalsRequestedAndReviewed(t *testing.T) {
	t.Parallel()

	m := &recordingMetrics{}
	rt, err := runtime.New(runtime.Config{Metrics: m})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	contract := action.ActionContract{
		Name:                "ISSUE_REFUND",
		AllowedStates:       []string{"PAID"},
		ApprovalRequirement: action.ApprovalRequired,
		Risk:                action.RiskHigh,
	}
	actionCtx := action.ActionContext{
		ActionName:   "ISSUE_REFUND",
		EntityID:     "order-1",
		EntityType:   "order",
		Actor:        actor.Actor{ID: "user-1"},
		CurrentState: "PAID",
	}

	req, err := rt.RequestApproval(context.Background(), "appr-1", actionCtx, contract, "high-risk refund")
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if got := m.countsByName()[runtime.CounterApprovalsRequested]; got != 1 {
		t.Fatalf("approvals requested: got %d, want 1", got)
	}

	if _, err := rt.ReviewApproval(context.Background(), req.ID, "manager-1", approval.StatusGranted, "ok"); err != nil {
		t.Fatalf("ReviewApproval: %v", err)
	}
	if got := m.countsByName()[runtime.CounterApprovalsReviewed]; got != 1 {
		t.Fatalf("approvals reviewed: got %d, want 1", got)
	}
}

func TestRuntimeDoesNotIncrementOnFailedValidation(t *testing.T) {
	t.Parallel()

	m := &recordingMetrics{}

	// Action contract requires a state we do not have; validation
	// will reject. Counter must not increment.
	contract := action.ActionContract{
		Name:                "ISSUE_REFUND",
		AllowedStates:       []string{"PAID"},
		ApprovalRequirement: action.ApprovalNever,
	}

	rt, err := runtime.New(runtime.Config{
		Metrics:          m,
		PermissionPolicy: permission.NewSimplePolicy(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = rt.ValidateAction(context.Background(), action.ActionContext{
		ActionName:   "ISSUE_REFUND",
		EntityID:     "order-1",
		EntityType:   "order",
		Actor:        actor.Actor{ID: "user-1"},
		CurrentState: "CREATED", // wrong state
	}, contract)
	if err == nil {
		t.Fatal("ValidateAction unexpectedly succeeded for wrong state")
	}
	if got := m.countsByName()[runtime.CounterActionsValidated]; got != 0 {
		t.Fatalf("actions validated on failure: got %d, want 0", got)
	}
}

func TestRuntimeMetricsConcurrentSafety(t *testing.T) {
	t.Parallel()

	m := &recordingMetrics{}
	rt, err := runtime.New(runtime.Config{Metrics: m})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const goroutines = 8
	const each = 25
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				id := strID(g, i)
				if err := rt.IngestEvent(context.Background(), event.Event{
					ID:         "ev-" + id,
					Type:       "TICKET_OPENED",
					EntityID:   "ticket-" + id,
					EntityType: "ticket",
					Source:     "test",
					ActorID:    "user-" + id,
					OccurredAt: time.Now().UTC(),
				}); err != nil {
					t.Errorf("IngestEvent g=%d i=%d: %v", g, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	want := uint64(goroutines * each)
	if got := m.countsByName()[runtime.CounterEventsIngested]; got != want {
		t.Fatalf("events ingested: got %d, want %d", got, want)
	}
}

func strID(g, i int) string {
	return string(rune('a'+g)) + "-" + string(rune('0'+(i/10))) + string(rune('0'+(i%10)))
}
