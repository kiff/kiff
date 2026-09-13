package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/limit"
)

// Limits supplies the limits in force for a subject.
//
// ForSubject must return the subject's complete set, including limits
// that are revoked or outside their validity window. Filtering here is
// the mistake the interface is worded to prevent: an empty set reads as
// "nothing bounds this subject", which means allowed, so a source that
// hides revoked limits turns revocation into a widening of authority.
type Limits interface {
	ForSubject(ctx context.Context, subject string) ([]limit.Limit, error)
}

// StaticLimits is a fixed set, the useful shape for a domain whose
// limits are declared in code alongside its actions.
type StaticLimits []limit.Limit

// ForSubject implements Limits. It returns every limit for the subject,
// in force or not.
func (s StaticLimits) ForSubject(_ context.Context, subject string) ([]limit.Limit, error) {
	out := make([]limit.Limit, 0, len(s))
	for _, l := range s {
		if l.Subject == subject {
			out = append(out, l)
		}
	}
	return out, nil
}

// checkLimits runs the aggregate check for an action the contract has
// already accepted.
//
// It runs last, after state, parameters, permissions and approval, for
// two reasons. It is the only check whose answer depends on other
// requests, so it needs the others to have settled. And a proposal the
// contract already refuses must not consume authority — otherwise a
// malformed action spends part of the day's budget.
//
// Returns the refusing error, or nil when the action may proceed.
//
// An error rather than a decision, so the sentinel survives: both
// callers need it intact — one to classify it into an outcome, the
// other to return it — and a refusal re-wrapped as a plain error
// classifies as a generic failure, which on a dashboard is
// indistinguishable from a bug. A working control must never read like
// one.
//
// A runtime with no limits wired returns nil immediately, so every
// existing caller is unaffected.
func (r *Runtime) checkLimits(ctx context.Context, actionCtx action.ActionContext, contract action.ActionContract) error {
	if r.Limits == nil || r.LimitLedger == nil {
		return nil
	}
	subject := actionCtx.Actor.ID
	if subject == "" {
		// A limit names a subject. An action with no actor cannot be
		// bound to one, and the contract has already decided whether it
		// may act.
		return nil
	}

	name := contract.Name
	if name == "" {
		name = actionCtx.ActionName
	}

	limits, err := r.Limits.ForSubject(ctx, subject)
	if err != nil {
		// Fail closed. Allowing here means acting with authority nobody
		// could confirm, which is the thing a limit exists to prevent.
		return fmt.Errorf("%w: %s", limit.ErrUsageUnavailable, err.Error())
	}

	req := limit.Request{
		ID:         r.limitRequestID(actionCtx, contract),
		Subject:    subject,
		Action:     name,
		Parameters: numericParameters(actionCtx.Parameters),
	}

	d, err := limit.Evaluate(ctx, limits, req, r.LimitLedger, r.now())
	if err != nil {
		return err
	}
	if !d.Allowed() {
		return d.Err()
	}

	// The draw is recorded at authorization rather than at execution,
	// because the boundary is told whether an action may run and never
	// told whether it did. An authorization the caller chose not to
	// execute still counts, and the statement says so.
	if len(d.Draws) > 0 {
		if err := r.LimitLedger.Record(ctx, req.ID, d.Draws); err != nil {
			// The draw could not be written, so the next action would
			// see a stale balance. Refuse rather than authorize against
			// a ledger we know is wrong.
			return fmt.Errorf("%w: recording the draw failed: %s", limit.ErrUsageUnavailable, err.Error())
		}
	}
	return nil
}

// limitRequestID identifies this attempt for the ledger, so a retry
// does not draw twice. It reuses the idempotency key when the contract
// declares one and falls back to the entity and action.
func (r *Runtime) limitRequestID(actionCtx action.ActionContext, contract action.ActionContract) string {
	if key, ok := r.idempotencyKeyFor(actionCtx, contract); ok {
		return key.Value + ":" + key.EntityID + ":" + key.ActionName
	}
	name := contract.Name
	if name == "" {
		name = actionCtx.ActionName
	}
	return actionCtx.EntityID + ":" + name
}

func (r *Runtime) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

// numericParameters projects an action's parameters onto the int64s a
// limit can sum. Non-numeric values are simply absent.
func numericParameters(params map[string]any) map[string]int64 {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]int64, len(params))
	for k, v := range params {
		switch n := v.(type) {
		case int:
			out[k] = int64(n)
		case int32:
			out[k] = int64(n)
		case int64:
			out[k] = n
		case float64:
			// JSON numbers decode as float64. Truncation is correct for
			// the integer quantities a limit sums; a fractional currency
			// amount is a modelling error the domain should refuse, not
			// something to round here.
			out[k] = int64(n)
		}
	}
	return out
}
