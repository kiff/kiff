package kifftest

import (
	"strings"
	"testing"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
)

func TestEventBuilder_Defaults(t *testing.T) {
	t.Parallel()
	ev := NewEvent("ORDER_PLACED").Build()

	if ev.Type != "ORDER_PLACED" {
		t.Fatalf("expected type ORDER_PLACED, got %q", ev.Type)
	}
	if ev.EntityID != DefaultEntityID || ev.EntityType != DefaultEntityType {
		t.Fatalf("expected defaults, got entity=%q type=%q", ev.EntityID, ev.EntityType)
	}
	if ev.Source != DefaultSource || ev.ActorID != DefaultActorID {
		t.Fatalf("expected defaults, got source=%q actor=%q", ev.Source, ev.ActorID)
	}
	if ev.OccurredAt.IsZero() {
		t.Fatal("expected non-zero OccurredAt")
	}
	if !strings.HasPrefix(ev.ID, "evt-") {
		t.Fatalf("expected evt- prefix, got %q", ev.ID)
	}
}

func TestEventBuilder_Overrides(t *testing.T) {
	t.Parallel()
	clk := NewFixedClock(time.Date(2026, time.May, 21, 12, 0, 0, 0, time.UTC))
	ev := NewEvent("ORDER_PAID").
		WithClock(clk).
		WithEntity("order-1", "Order").
		WithActor(AgentActor).
		WithTrace("trace-1", "corr-1").
		WithPayload(map[string]any{"amount": 49.0}).
		Build()

	if ev.EntityID != "order-1" || ev.EntityType != "Order" {
		t.Fatalf("entity overrides not applied: %+v", ev)
	}
	if ev.ActorID != AgentActor.ID {
		t.Fatalf("actor override not applied: got %q", ev.ActorID)
	}
	if ev.Metadata.TraceID != "trace-1" || ev.Metadata.CorrelationID != "corr-1" {
		t.Fatalf("trace overrides not applied: %+v", ev.Metadata)
	}
	if !ev.OccurredAt.Equal(clk.Now()) {
		t.Fatalf("expected occurred_at from fixed clock, got %v", ev.OccurredAt)
	}
}

func TestFixedClock(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.May, 21, 12, 0, 0, 0, time.UTC)
	clk := NewFixedClock(start)
	if !clk.Now().Equal(start) {
		t.Fatalf("expected %v, got %v", start, clk.Now())
	}
	clk.Advance(time.Hour)
	if !clk.Now().Equal(start.Add(time.Hour)) {
		t.Fatalf("Advance failed: got %v", clk.Now())
	}
	later := start.Add(24 * time.Hour)
	clk.Set(later)
	if !clk.Now().Equal(later) {
		t.Fatalf("Set failed: got %v", clk.Now())
	}
}

func TestNewActor(t *testing.T) {
	t.Parallel()
	a := NewActor("alice", "operator", "auditor")
	if a.ID != "alice" || a.Type != actor.TypeHuman {
		t.Fatalf("actor identity wrong: %+v", a)
	}
	if len(a.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %v", a.Roles)
	}
}

func TestNewPermissionPolicy(t *testing.T) {
	t.Parallel()
	policy := NewPermissionPolicy(
		"agent", "orders.refund",
		"operator", "orders.approve",
	)
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}

	agent := actor.Actor{ID: "a1"}
	policy.AssignRole(agent.ID, "agent")
	if !policy.Can(testCtx(t), agent, permission.Permission("orders.refund")) {
		t.Fatal("agent should have orders.refund")
	}
	if policy.Can(testCtx(t), agent, permission.Permission("orders.approve")) {
		t.Fatal("agent should not have orders.approve")
	}
	// A role placed on the actor by the caller carries no authority:
	// membership comes from the policy, not actor.Roles (#19).
	forged := actor.Actor{ID: "a2", Roles: []string{"operator"}}
	if policy.Can(testCtx(t), forged, permission.Permission("orders.approve")) {
		t.Fatal("caller-supplied actor.Roles must not grant authority")
	}

	if NewPermissionPolicy("unbalanced") != nil {
		t.Fatal("expected nil for unbalanced grants")
	}
}

func TestNextID_Unique(t *testing.T) {
	t.Parallel()
	a := NextID("x")
	b := NextID("x")
	if a == b {
		t.Fatalf("expected unique ids, got %q twice", a)
	}
	if !strings.HasPrefix(a, "x-") || !strings.HasPrefix(b, "x-") {
		t.Fatalf("expected x- prefix, got %q %q", a, b)
	}
}

// testCtx returns a context.Context for permission checks. Pulled into a helper
// so the test file does not import context just to pass a TODO.
func testCtx(t *testing.T) testContext {
	t.Helper()
	return testContext{}
}

// testContext is a minimal context.Context implementation for permission tests.
// permission.Policy.Can accepts a context.Context; we do not need cancellation.
type testContext struct{}

func (testContext) Deadline() (deadline time.Time, ok bool) { return }
func (testContext) Done() <-chan struct{}                   { return nil }
func (testContext) Err() error                              { return nil }
func (testContext) Value(key any) any                       { return nil }
