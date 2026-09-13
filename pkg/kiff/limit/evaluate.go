package limit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Outcome is why a request was allowed or refused.
type Outcome string

const (
	// OutcomeAllowed means every limit covering the action had room.
	// Also the outcome when no limit covers it: a limit constrains, it
	// does not confer.
	OutcomeAllowed Outcome = "allowed"

	// OutcomeLimitReached means a covering limit had no room left.
	OutcomeLimitReached Outcome = "limit_reached"

	// OutcomeExpired means a covering limit is revoked or outside its
	// validity window.
	//
	// This is a refusal, not an absence. Reporting it as "no limit
	// applies" would make revoking a limit the act that removes the
	// bound, which is the inversion this package is shaped to prevent.
	OutcomeExpired Outcome = "expired"
)

// Request is one action being authorized.
type Request struct {
	// ID identifies this attempt. A retry carrying the same ID must
	// not draw twice, so it is excluded from its own usage — see
	// Ledger.Used.
	ID      string
	Subject string
	Action  string

	// Parameters are the numeric values a sum(...) quantity can add
	// up. Non-numeric parameters are simply absent: the action
	// contract, not the limit, decides whether a parameter is
	// required.
	Parameters map[string]int64
}

// Decision is the result of evaluating every limit covering a request.
type Decision struct {
	Outcome Outcome
	LimitID string
	Reason  string

	// Draws are the ledger lines to record when the action is
	// authorized. Empty when the decision refuses.
	Draws []Draw
}

// Allowed reports whether the action may proceed.
func (d Decision) Allowed() bool { return d.Outcome == OutcomeAllowed }

// ErrLimitReached and ErrExpired let a refusal travel as an error
// through code that speaks errors, and let outcome.Classify give it a
// reason of its own rather than the generic failure bucket. A limit
// refusal that reads as "error" on a dashboard is indistinguishable
// from a bug, which is the wrong thing to learn from a working control.
var (
	ErrLimitReached = errors.New("limit: no authority left")
	ErrExpired      = errors.New("limit: revoked or outside its validity window")
)

// Err renders a refusing decision as an error. Returns nil when the
// decision allows.
func (d Decision) Err() error {
	switch d.Outcome {
	case OutcomeLimitReached:
		return fmt.Errorf("%w: %s: %s", ErrLimitReached, d.LimitID, d.Reason)
	case OutcomeExpired:
		return fmt.Errorf("%w: %s", ErrExpired, d.LimitID)
	default:
		return nil
	}
}

// Draw is one consumption of authority.
type Draw struct {
	Key    Key
	Amount int64
}

// Ledger records what has been drawn and reports what remains.
//
// Used must exclude the request being decided, so a retry of the same
// request does not count against itself. Excluding rather than
// returning early also repairs a draw that was written before a crash:
// the retry recomputes from the same base and overwrites its own line.
type Ledger interface {
	// Used returns the amount drawn against key since the instant,
	// excluding any draw recorded for excludeRequestID.
	//
	// It must return an error rather than zero when the amount cannot
	// be determined. Zero is indistinguishable from an unused limit,
	// which is the most dangerous value this interface can produce.
	Used(ctx context.Context, key Key, since time.Time, excludeRequestID string) (int64, error)

	// Record writes the draws for a request. Recording the same
	// request twice must be idempotent.
	Record(ctx context.Context, requestID string, draws []Draw) error
}

// Evaluate decides a request against every limit for its subject.
//
// limits must be the subject's complete set, in force or not. Filtering
// it to the limits currently active before calling is the mistake this
// signature exists to prevent: an empty set reads as "nothing covers
// this action", which by the constrains-not-confers rule means allowed,
// so pre-filtering turns revocation into a widening of authority.
//
// Conjunctive: every covering limit must have room, and the first
// refusal decides. There is no precedence to reason about and no way to
// gain authority by adding a limit.
func Evaluate(ctx context.Context, limits []Limit, req Request, ledger Ledger, now time.Time) (Decision, error) {
	var draws []Draw

	for _, l := range limits {
		if l.Subject != req.Subject || !l.Covers(req.Action) {
			continue
		}
		if !l.InForce(now) {
			return Decision{
				Outcome: OutcomeExpired,
				LimitID: l.ID,
				Reason:  "the limit authorizing this subject is revoked or outside its validity window",
			}, nil
		}
		for _, agg := range l.Aggregates {
			amount, ok := amountFor(agg.Quantity, req)
			if !ok {
				// The action did not carry the parameter this limit
				// sums. It draws nothing rather than refusing: whether
				// the parameter is required belongs to the action
				// contract, which has already run.
				continue
			}
			since, err := agg.Window.Start(now, l.Location)
			if err != nil {
				return Decision{}, err
			}
			key := Key{LimitID: l.ID, Quantity: agg.Quantity.String()}
			used, err := ledger.Used(ctx, key, since, req.ID)
			if err != nil {
				return Decision{}, fmt.Errorf("%w: %s: %s", ErrUsageUnavailable, l.ID, err.Error())
			}
			if used+amount > agg.Max {
				return Decision{
					Outcome: OutcomeLimitReached,
					LimitID: l.ID,
					Reason: fmt.Sprintf("%s limit %d per %s, %d already drawn, this action needs %d",
						agg.Quantity, agg.Max, agg.Window, used, amount),
				}, nil
			}
			draws = append(draws, Draw{Key: key, Amount: amount})
		}
	}

	return Decision{Outcome: OutcomeAllowed, Draws: draws}, nil
}

// amountFor is what this request draws against a quantity.
func amountFor(q Quantity, req Request) (int64, bool) {
	if q.Counting() {
		return 1, true
	}
	v, ok := req.Parameters[q.Parameter]
	if !ok {
		return 0, false
	}
	return v, true
}
