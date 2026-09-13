package limit

import (
	"context"
	"sync"
	"time"
)

// MemoryLedger is an in-process Ledger.
//
// Correct for a single process, and only that. Two processes each hold
// their own ledger, so each enforces the full ceiling independently and
// the real total is the sum — an agent running in three replicas can
// spend three times the limit. That is not a bug to be fixed here: a
// bound on a sequence has to live where the whole sequence is visible,
// and for more than one process that means shared storage.
//
// Implement Ledger against your own database when you run more than one
// replica. The read-modify-write in Used/Record must be serialized
// there the way the mutex serializes it here, or two concurrent
// authorizations both read the same remaining balance and both proceed.
type MemoryLedger struct {
	mu sync.Mutex
	// draws is keyed by ledger line, then by request id, so a retry
	// overwrites its own earlier draw instead of adding to it.
	draws map[Key]map[string]drawRecord
}

type drawRecord struct {
	amount int64
	at     time.Time
}

// NewMemoryLedger returns an empty in-process ledger.
func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{draws: map[Key]map[string]drawRecord{}}
}

// Used implements Ledger.
func (m *MemoryLedger) Used(_ context.Context, key Key, since time.Time, excludeRequestID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var total int64
	for requestID, d := range m.draws[key] {
		if requestID == excludeRequestID {
			continue
		}
		if d.at.Before(since) {
			continue
		}
		total += d.amount
	}
	return total, nil
}

// Record implements Ledger. Recording the same request twice replaces
// the earlier draw rather than adding to it.
func (m *MemoryLedger) Record(_ context.Context, requestID string, draws []Draw) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	for _, d := range draws {
		if m.draws[d.Key] == nil {
			m.draws[d.Key] = map[string]drawRecord{}
		}
		m.draws[d.Key][requestID] = drawRecord{amount: d.Amount, at: now}
	}
	return nil
}

// Statement is what has been drawn against one limit in the current
// window: the corporate-card statement for a machine.
type Statement struct {
	Quantity  string
	Max       int64
	Used      int64
	Remaining int64
	Window    Window
	Since     time.Time
}

// StatementFor reports usage for each of a limit's aggregates.
//
// A ledger error is returned rather than reported as zero usage: an
// unused limit and an unreadable one are very different facts, and
// showing the second as the first is the failure this package exists to
// avoid.
func StatementFor(ctx context.Context, l Limit, ledger Ledger, now time.Time) ([]Statement, error) {
	out := make([]Statement, 0, len(l.Aggregates))
	for _, agg := range l.Aggregates {
		since, err := agg.Window.Start(now, l.Location)
		if err != nil {
			return nil, err
		}
		key := Key{LimitID: l.ID, Quantity: agg.Quantity.String()}
		used, err := ledger.Used(ctx, key, since, "")
		if err != nil {
			return nil, err
		}
		remaining := agg.Max - used
		if remaining < 0 {
			remaining = 0
		}
		out = append(out, Statement{
			Quantity:  agg.Quantity.String(),
			Max:       agg.Max,
			Used:      used,
			Remaining: remaining,
			Window:    agg.Window,
			Since:     since,
		})
	}
	return out, nil
}
