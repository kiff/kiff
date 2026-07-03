// Package idempotency provides first-class duplicate protection for action
// execution. Consequential workflows — payments, refunds, external API writes,
// production operations — must not create duplicate side effects when the same
// proposed action is retried (a client timeout and resend, an at-least-once
// queue, a double click).
//
// Idempotency is the complement to state-based refusal, not a replacement. A
// double refund is normally refused because the entity is already REFUNDED
// (state_not_allowed). Idempotency covers the other case: a network retry of
// the *same request* returns the prior successful result instead of executing
// again, even after state has advanced.
//
// The store caches only terminal successful executions. Actions that are held
// for approval, blocked, invalid, or that fail are never cached, so they can
// still proceed on a later attempt. On replay the runtime returns the prior
// result without re-emitting the action's follow-up events, so entity state is
// never double-applied.
package idempotency

import (
	"context"
	"errors"
	"sync"

	"github.com/kiff/kiff/pkg/kiff/action"
)

// ErrInProgress reports that an identical action is already executing under the
// same key. Callers should retry once the in-flight attempt has settled.
var ErrInProgress = errors.New("idempotent action already in progress")

// Key identifies a logical action attempt for deduplication. The same Value
// under a different entity or action is a distinct key, so a reused client key
// cannot collapse unrelated operations.
type Key struct {
	Value      string
	EntityID   string
	ActionName string
}

// Status is the outcome of Begin.
type Status string

const (
	// Reserved means this caller created the reservation and now owns
	// execution of the action.
	Reserved Status = "reserved"
	// Completed means a prior successful result is stored for the key and is
	// returned in BeginResult.Result.
	Completed Status = "completed"
	// InProgress means another caller holds an unfinished reservation for the
	// key.
	InProgress Status = "in_progress"
)

// BeginResult reports whether the caller may execute, or an existing record.
type BeginResult struct {
	Status Status
	Result action.ActionResult // populated only when Status == Completed
}

// Store provides atomic reserve-or-return semantics around action execution.
// Begin must be atomic against concurrent callers: exactly one caller may
// receive Reserved for a key until that reservation is Completed or Released.
type Store interface {
	// Lookup returns a prior completed result for the key, if one exists. It
	// is a read used before validation so a retry of an already-succeeded
	// request returns the stored result without re-validating state.
	Lookup(ctx context.Context, key Key) (action.ActionResult, bool, error)
	// Begin atomically reserves the key for the caller (Reserved), or reports
	// an existing Completed result, or an unfinished InProgress reservation.
	Begin(ctx context.Context, key Key) (BeginResult, error)
	// Complete stores the terminal successful result for a reserved key.
	Complete(ctx context.Context, key Key, result action.ActionResult) error
	// Release drops a reservation so the action can be retried, used when the
	// executor failed or produced a non-terminal result. It is a no-op once a
	// key has been Completed.
	Release(ctx context.Context, key Key) error
}

type record struct {
	done   bool
	result action.ActionResult
}

// InMemoryStore is a concurrency-safe in-memory idempotency store for tests,
// examples, and single-process apps.
type InMemoryStore struct {
	mu      sync.Mutex
	records map[Key]record
}

// NewInMemoryStore creates an empty in-memory idempotency store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{records: map[Key]record{}}
}

// Lookup returns the stored successful result for the key, if completed.
func (s *InMemoryStore) Lookup(ctx context.Context, key Key) (action.ActionResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return action.ActionResult{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.records[key]; ok && rec.done {
		return rec.result, true, nil
	}
	return action.ActionResult{}, false, nil
}

// Begin atomically reserves the key or returns the existing record's status.
func (s *InMemoryStore) Begin(ctx context.Context, key Key) (BeginResult, error) {
	if err := ctx.Err(); err != nil {
		return BeginResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.records[key]; ok {
		if rec.done {
			return BeginResult{Status: Completed, Result: rec.result}, nil
		}
		return BeginResult{Status: InProgress}, nil
	}
	s.records[key] = record{}
	return BeginResult{Status: Reserved}, nil
}

// Complete stores the terminal successful result for the key.
func (s *InMemoryStore) Complete(ctx context.Context, key Key, result action.ActionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key] = record{done: true, result: result}
	return nil
}

// Release drops an unfinished reservation. A no-op once completed.
func (s *InMemoryStore) Release(ctx context.Context, key Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.records[key]; ok && !rec.done {
		delete(s.records, key)
	}
	return nil
}
