package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/limit"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func refundContract() action.ActionContract {
	return action.ActionContract{
		Name:               "AUTO_REFUND",
		AllowedStates:      []string{"PAID"},
		RequiredParameters: []string{"amount"},
		Executor: func(context.Context, action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{Status: action.ExecutionSucceeded, Executed: true}, nil
		},
	}
}

func refundCtx(entityID string, amount int64) action.ActionContext {
	return action.ActionContext{
		ActionName:   "AUTO_REFUND",
		EntityID:     entityID,
		EntityType:   "Order",
		CurrentState: "PAID",
		Actor:        actor.Actor{ID: "refund-agent", Type: actor.TypeAgent},
		Parameters:   map[string]any{"amount": amount},
	}
}

func limitedRuntime(t *testing.T, max int64) *runtime.Runtime {
	t.Helper()
	rt, err := runtime.New(runtime.Config{
		Limits: runtime.StaticLimits{{
			ID: "refund-agent-daily", Subject: "refund-agent",
			Actions: []string{"AUTO_REFUND"},
			Aggregates: []limit.Aggregate{{
				Quantity: limit.Quantity{Parameter: "amount"},
				Max:      max, Window: limit.WindowCalendarDay,
			}},
		}},
		LimitLedger: limit.NewMemoryLedger(),
		Now:         func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	return rt
}

// The beat the demo needs: three refunds, each satisfying the contract,
// and the third refused while smaller than the two before it.
func TestTheThirdRefundIsRefusedOnTheTotal(t *testing.T) {
	t.Parallel()
	rt := limitedRuntime(t, 110000)
	ctx := context.Background()
	c := refundContract()

	for _, tc := range []struct {
		entity string
		amount int64
	}{{"order-1", 4200}, {"order-2", 99000}} {
		d := rt.EvaluateAction(ctx, refundCtx(tc.entity, tc.amount), c)
		if d.Outcome != outcome.Allowed {
			t.Fatalf("%s (%d) = %s: %s", tc.entity, tc.amount, d.Outcome, d.Message)
		}
	}

	d := rt.EvaluateAction(ctx, refundCtx("order-3", 8800), c)
	if d.Outcome == outcome.Allowed {
		t.Fatal("the third refund was allowed; the day's ceiling is spent")
	}
	if d.Reason != outcome.ReasonLimitReached {
		t.Errorf("reason = %q, want limit_reached — a working control must not read as an error", d.Reason)
	}
	if d.Message == "" {
		t.Error("the refusal carries no message saying which limit bound it")
	}
}

// A runtime with no limits wired behaves exactly as it did before, so
// this is something a domain opts into rather than something that
// changes under existing users.
func TestARuntimeWithoutLimitsIsUnchanged(t *testing.T) {
	t.Parallel()
	rt, err := runtime.New(runtime.Config{})
	if err != nil {
		t.Fatal(err)
	}
	d := rt.EvaluateAction(context.Background(), refundCtx("order-1", 999999999), refundContract())
	if d.Outcome != outcome.Allowed {
		t.Fatalf("an unlimited runtime refused: %s %s", d.Outcome, d.Message)
	}
}

// Half-wiring is a configuration error, not a half-enabled check: a
// limits source with no ledger cannot tell what has been drawn and
// would allow everything while appearing to bound it.
func TestHalfWiredLimitsAreRefusedAtConstruction(t *testing.T) {
	t.Parallel()
	if _, err := runtime.New(runtime.Config{Limits: runtime.StaticLimits{}}); err == nil {
		t.Error("a limits source with no ledger was accepted")
	}
	if _, err := runtime.New(runtime.Config{LimitLedger: limit.NewMemoryLedger()}); err == nil {
		t.Error("a ledger with no limits source was accepted")
	}
}

// The aggregate runs last: an action the contract already refuses must
// not consume authority, or a malformed request spends the day's
// budget.
func TestARefusedActionDoesNotSpendAuthority(t *testing.T) {
	t.Parallel()
	rt := limitedRuntime(t, 110000)
	ctx := context.Background()
	c := refundContract()

	// Wrong state: refused by the contract, before the aggregate.
	bad := refundCtx("order-1", 100000)
	bad.CurrentState = "REFUNDED"
	if d := rt.EvaluateAction(ctx, bad, c); d.Reason != outcome.ReasonStateNotAllowed {
		t.Fatalf("reason = %q, want state_not_allowed", d.Reason)
	}

	// The full ceiling is still available.
	if d := rt.EvaluateAction(ctx, refundCtx("order-2", 110000), c); d.Outcome != outcome.Allowed {
		t.Errorf("the refused action spent authority: %s %s", d.Outcome, d.Message)
	}
}

// An unreadable ledger refuses rather than allowing on an unknown
// balance, and says so with its own reason.
func TestAnUnreadableLedgerRefusesTheAction(t *testing.T) {
	t.Parallel()
	rt, err := runtime.New(runtime.Config{
		Limits:      brokenLimits{},
		LimitLedger: limit.NewMemoryLedger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	d := rt.EvaluateAction(context.Background(), refundCtx("order-1", 1), refundContract())
	if d.Outcome == outcome.Allowed {
		t.Fatal("an action was allowed while the limits could not be read")
	}
}

type brokenLimits struct{}

func (brokenLimits) ForSubject(context.Context, string) ([]limit.Limit, error) {
	return nil, errors.New("limits unavailable")
}

// StaticLimits must return revoked limits too. A source that hides them
// turns revocation into a widening of authority — the inversion the
// Limits interface is worded to prevent.
func TestStaticLimitsReturnsRevokedOnes(t *testing.T) {
	t.Parallel()
	revoked := limit.Limit{
		ID: "l1", Subject: "refund-agent", RevokedAt: time.Now().Add(-time.Hour),
		Aggregates: []limit.Aggregate{{Max: 1, Window: limit.WindowCalendarDay}},
	}
	got, err := runtime.StaticLimits{revoked}.ForSubject(context.Background(), "refund-agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("revoked limit was filtered out; the subject would read as unbounded")
	}
}
