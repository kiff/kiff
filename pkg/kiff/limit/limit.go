// Package limit bounds what an actor may do in total.
//
// Every other check in KIFF answers a question about one action: is the
// entity in a state that allows it, does this actor hold the
// permission, are the parameters valid, does a human need to sign. Each
// is right every time it runs, and none of them can answer the question
// this package exists for — what has this actor already done today, and
// is that enough.
//
// That gap is structural rather than an oversight. A per-call check
// sees one call; ten correct refunds is ten correct decisions. The
// bound on the sequence has to be held somewhere the sequence is
// visible, which is why this is a ledger and not a predicate.
//
// # What a limit is not
//
// It is not a rate limit. A rate limit protects infrastructure from
// volume; a limit here bounds consequence, in whatever unit the action
// is measured in.
//
// It is not a second opinion on one action. It is checked last, after
// everything else has already said yes, and it refuses actions that are
// correct in every other way.
//
// # Two rules that look like details and are not
//
// A limit constrains; it never confers. An actor with no limit is
// unaffected, so a first limit cannot break a running system, and
// limits can be adopted one actor at a time.
//
// An expired or revoked limit refuses. It does not vanish and leave the
// actor unbounded. The obvious implementation filters to the limits
// currently in force and then treats an empty set as "nothing applies,
// so allow" — which makes revoking a limit the act that removes the
// bound. Grant.Covers and Evaluate are shaped to make that unsayable.
package limit

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrUsageUnavailable means the ledger could not be read. Callers
	// must refuse rather than allow: the whole value of a limit is
	// that it is known, and acting on an unknown balance is the thing
	// it exists to prevent.
	ErrUsageUnavailable = errors.New("limit: current usage unavailable; refuse")

	// ErrInvalidLimit reports a limit that cannot be enforced as
	// written, refused when it is declared rather than silently never
	// binding at runtime.
	ErrInvalidLimit = errors.New("limit: invalid")

	// ErrUnknownWindow reports a window this package cannot resolve.
	ErrUnknownWindow = errors.New("limit: unknown window")
)

// Window is the period a limit resets over.
type Window string

const (
	// WindowCalendarDay resets at midnight in the limit's location.
	// The default, because it is what a person means by "a day" when
	// they say what an agent may spend in one, and a limit nobody can
	// explain is a limit nobody trusts.
	WindowCalendarDay Window = "calendar_day"

	// WindowRolling24h is the trailing 24 hours. Harder to game at a
	// boundary than the calendar day, and harder to explain.
	WindowRolling24h Window = "rolling_24h"

	// WindowRolling1h is the trailing hour.
	WindowRolling1h Window = "rolling_1h"
)

// Start returns the instant the current window opened.
func (w Window) Start(now time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	switch w {
	case WindowCalendarDay:
		local := now.In(loc)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc), nil
	case WindowRolling24h:
		return now.Add(-24 * time.Hour), nil
	case WindowRolling1h:
		return now.Add(-time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("%w: %q", ErrUnknownWindow, w)
	}
}

// Quantity is what a limit counts.
//
// Either the number of actions, or the sum of one numeric parameter
// across them. Deliberately only these two: a quantity that needs
// arithmetic to describe is one nobody can predict the behaviour of,
// and a limit that cannot be predicted is not a control.
type Quantity struct {
	// Parameter is the action parameter to add up. Empty counts
	// actions instead.
	Parameter string
}

// Counting reports whether this quantity counts actions rather than
// summing a parameter.
func (q Quantity) Counting() bool { return q.Parameter == "" }

func (q Quantity) String() string {
	if q.Counting() {
		return "count"
	}
	return "sum(" + q.Parameter + ")"
}

// ParseQuantity reads "count" or "sum(<parameter>)".
func ParseQuantity(s string) (Quantity, error) {
	s = strings.TrimSpace(s)
	if s == "count" {
		return Quantity{}, nil
	}
	if strings.HasPrefix(s, "sum(") && strings.HasSuffix(s, ")") {
		name := strings.TrimSpace(s[len("sum(") : len(s)-1])
		if name == "" {
			return Quantity{}, fmt.Errorf("%w: sum() names no parameter", ErrInvalidLimit)
		}
		return Quantity{Parameter: name}, nil
	}
	return Quantity{}, fmt.Errorf("%w: quantity %q is not count or sum(parameter)", ErrInvalidLimit, s)
}

// Key identifies one limit's ledger line: the limit and the thing it
// counts. A limit with two quantities draws against two keys.
type Key struct {
	LimitID  string
	Quantity string
}

// Aggregate is one ceiling.
type Aggregate struct {
	Quantity Quantity
	Max      int64
	Window   Window
}

// Limit is a bounded grant of authority to one subject.
//
// Actions is the set it covers. An empty Actions covers every action,
// which is the useful default for "this agent may do at most N things
// today" and is stated explicitly rather than left to be discovered.
type Limit struct {
	ID      string
	Subject string
	Actions []string

	Aggregates []Aggregate

	// ValidFrom and ValidUntil bound when the limit is in force. Zero
	// means unbounded in that direction.
	ValidFrom  time.Time
	ValidUntil time.Time

	// RevokedAt ends the limit early. A revoked limit refuses; it does
	// not disappear and leave the subject unbounded.
	RevokedAt time.Time

	// Location resolves the calendar day. Nil means UTC.
	Location *time.Location
}

// Covers reports whether this limit governs an action.
func (l Limit) Covers(action string) bool {
	if len(l.Actions) == 0 {
		return true
	}
	for _, a := range l.Actions {
		if strings.EqualFold(a, action) {
			return true
		}
	}
	return false
}

// InForce reports whether the limit is currently active.
//
// Never use this to filter a set before evaluating it. A limit that is
// out of force still refuses the actions it covers — see Evaluate, and
// the package comment for why this is the failure worth naming.
func (l Limit) InForce(now time.Time) bool {
	if !l.RevokedAt.IsZero() && !now.Before(l.RevokedAt) {
		return false
	}
	if !l.ValidFrom.IsZero() && now.Before(l.ValidFrom) {
		return false
	}
	if !l.ValidUntil.IsZero() && !now.Before(l.ValidUntil) {
		return false
	}
	return true
}

// Validate reports a limit that cannot be enforced as written.
func (l Limit) Validate() error {
	if strings.TrimSpace(l.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidLimit)
	}
	if strings.TrimSpace(l.Subject) == "" {
		return fmt.Errorf("%w: subject is required", ErrInvalidLimit)
	}
	if len(l.Aggregates) == 0 {
		return fmt.Errorf("%w: %s bounds nothing", ErrInvalidLimit, l.ID)
	}
	for _, a := range l.Aggregates {
		if a.Max <= 0 {
			return fmt.Errorf("%w: %s has a ceiling of %d; a ceiling of zero or less is a revocation, which has its own field",
				ErrInvalidLimit, l.ID, a.Max)
		}
		if _, err := a.Window.Start(time.Now(), l.Location); err != nil {
			return fmt.Errorf("%w: %s: %s", ErrInvalidLimit, l.ID, err.Error())
		}
	}
	if !l.ValidUntil.IsZero() && !l.ValidFrom.IsZero() && !l.ValidUntil.After(l.ValidFrom) {
		return fmt.Errorf("%w: %s is never in force", ErrInvalidLimit, l.ID)
	}
	return nil
}
