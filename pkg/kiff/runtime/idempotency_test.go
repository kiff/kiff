package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
)

// countingExecutor records how many times it ran and emits one follow-up event.
type countingExecutor struct {
	calls int
	fail  bool
}

func (e *countingExecutor) exec(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
	e.calls++
	if e.fail {
		return action.ActionResult{}, errors.New("gateway boom")
	}
	return action.ActionResult{
		ActionName: ctx.ActionName,
		EntityID:   ctx.EntityID,
		Status:     action.ExecutionSucceeded,
		Executed:   true,
		FollowUpEvents: []event.Event{{
			ID:         "evt-paid-" + ctx.EntityID,
			Type:       "PAID",
			EntityID:   ctx.EntityID,
			EntityType: "Invoice",
			Source:     "test",
			ActorID:    ctx.Actor.ID,
			OccurredAt: time.Now().UTC(),
		}},
	}, nil
}

// idempotencyRuntime builds a tiny pay domain: RECEIVED --RELEASE--> PAID.
func idempotencyRuntime(t *testing.T, exec *countingExecutor) (*Runtime, action.ActionContract) {
	t.Helper()
	contract := action.ActionContract{
		Name:                "RELEASE",
		AllowedStates:       []string{"RECEIVED"},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor:            exec.exec,
	}
	def, err := domain.New("pay").
		Entity("Invoice").
		Event("INIT").
		Event("PAID").
		Transition("INIT", "", "RECEIVED").
		Transition("PAID", "RECEIVED", "PAID").
		Action(contract).
		Build()
	if err != nil {
		t.Fatalf("domain build: %v", err)
	}
	rt, err := NewForDomain(def, Config{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	return rt, contract
}

func seedInvoice(t *testing.T, rt *Runtime, id string) {
	t.Helper()
	if err := rt.IngestEvent(context.Background(), event.Event{
		ID: "evt-init-" + id, Type: "INIT", EntityID: id, EntityType: "Invoice",
		Source: "test", ActorID: "sys", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func releaseCtxIdem(id, key string) action.ActionContext {
	return action.ActionContext{
		ActionName: "RELEASE", EntityID: id, EntityType: "Invoice",
		CurrentState: "RECEIVED", Actor: actor.Actor{ID: "svc"},
		IdempotencyKey: key,
	}
}

func TestExecute_DuplicateKeyReturnsPriorResultWithoutReexecuting(t *testing.T) {
	ctx := context.Background()
	exec := &countingExecutor{}
	rt, contract := idempotencyRuntime(t, exec)
	seedInvoice(t, rt, "inv-1")

	first, err := rt.ExecuteAction(ctx, releaseCtxIdem("inv-1", "req-1"), contract)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}

	// Retry with the same key. State has advanced to PAID (RELEASE no longer
	// allowed) — idempotency must return the prior result anyway.
	second, err := rt.ExecuteAction(ctx, releaseCtxIdem("inv-1", "req-1"), contract)
	if err != nil {
		t.Fatalf("duplicate execute should return prior result, got %v", err)
	}

	if exec.calls != 1 {
		t.Fatalf("executor must run once, ran %d times", exec.calls)
	}
	if first.EntityID != second.EntityID || second.Status != action.ExecutionSucceeded {
		t.Fatalf("replay result mismatch: %+v vs %+v", first, second)
	}
	// Follow-up events must not be re-emitted: exactly INIT + one PAID.
	events, err := rt.Events.List(ctx, "inv-1")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	paid := 0
	for _, e := range events {
		if e.Type == "PAID" {
			paid++
		}
	}
	if paid != 1 {
		t.Fatalf("expected exactly one PAID event, got %d (total %d)", paid, len(events))
	}
	// The replay is audited distinctly.
	records, _ := rt.Timeline(ctx, "inv-1")
	var executed, dedup int
	for _, r := range records {
		switch r.Kind {
		case audit.KindActionExecuted:
			executed++
		case audit.KindActionDeduplicated:
			dedup++
		}
	}
	if executed != 1 || dedup != 1 {
		t.Fatalf("expected 1 executed + 1 deduplicated audit, got executed=%d dedup=%d", executed, dedup)
	}
}

func currentStateValue(t *testing.T, rt *Runtime, id string) string {
	t.Helper()
	s, ok, err := rt.States.Current(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("current state %q: ok=%v err=%v", id, ok, err)
	}
	return s.Value
}

func TestExecute_NoKeyIsUnchanged(t *testing.T) {
	ctx := context.Background()
	exec := &countingExecutor{}
	rt, contract := idempotencyRuntime(t, exec)
	seedInvoice(t, rt, "inv-1")

	// No idempotency key: executes normally. The caller reads real state each
	// time, so the second call is refused by state (PAID) — proving no dedup
	// path engaged.
	first := releaseCtxIdem("inv-1", "")
	first.CurrentState = currentStateValue(t, rt, "inv-1")
	if _, err := rt.ExecuteAction(ctx, first, contract); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second := releaseCtxIdem("inv-1", "")
	second.CurrentState = currentStateValue(t, rt, "inv-1")
	_, err := rt.ExecuteAction(ctx, second, contract)
	if !errors.Is(err, action.ErrStateNotAllowed) {
		t.Fatalf("without a key the retry should hit state refusal, got %v", err)
	}
	if exec.calls != 1 {
		t.Fatalf("executor should have run once (second blocked by state), ran %d", exec.calls)
	}
}

func TestExecute_DuplicateAfterFailureRetries(t *testing.T) {
	ctx := context.Background()
	exec := &countingExecutor{fail: true}
	rt, contract := idempotencyRuntime(t, exec)
	seedInvoice(t, rt, "inv-1")

	// First attempt fails: the reservation is released, not cached.
	if _, err := rt.ExecuteAction(ctx, releaseCtxIdem("inv-1", "req-1"), contract); err == nil {
		t.Fatal("expected executor failure")
	}
	// Recover and retry with the same key: it must run again, not replay a
	// frozen failure.
	exec.fail = false
	if _, err := rt.ExecuteAction(ctx, releaseCtxIdem("inv-1", "req-1"), contract); err != nil {
		t.Fatalf("retry after failure should execute: %v", err)
	}
	if exec.calls != 2 {
		t.Fatalf("executor should have run twice (fail then succeed), ran %d", exec.calls)
	}
}

func TestExecute_SameKeyDifferentEntityIsDistinct(t *testing.T) {
	ctx := context.Background()
	exec := &countingExecutor{}
	rt, contract := idempotencyRuntime(t, exec)
	seedInvoice(t, rt, "inv-1")
	seedInvoice(t, rt, "inv-2")

	if _, err := rt.ExecuteAction(ctx, releaseCtxIdem("inv-1", "shared"), contract); err != nil {
		t.Fatalf("inv-1: %v", err)
	}
	// Same idempotency value, different entity → distinct key, executes.
	if _, err := rt.ExecuteAction(ctx, releaseCtxIdem("inv-2", "shared"), contract); err != nil {
		t.Fatalf("inv-2 should execute (distinct key): %v", err)
	}
	if exec.calls != 2 {
		t.Fatalf("both distinct-key actions should run, ran %d", exec.calls)
	}
}
