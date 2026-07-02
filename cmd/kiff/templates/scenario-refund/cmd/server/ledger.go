package main

import "sync"

// refundRecord is one entry in the mock business ledger — the side effect a
// refund action produces. In a real app this would be a row in your database
// or a call to a payments provider.
type refundRecord struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
	Guarded     bool   `json:"guarded"`
}

// ledger is an in-memory stand-in for the app's business state. The whole
// point of the demo is the contrast: the unguarded path writes here directly
// (a duplicate refund gets recorded), while the KIFF-guarded path only writes
// after the runtime allows the action (a duplicate is refused before it lands).
type ledger struct {
	mu      sync.Mutex
	records []refundRecord
}

func (l *ledger) record(r refundRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r)
}

func (l *ledger) all() []refundRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]refundRecord, len(l.records))
	copy(out, l.records)
	return out
}

func (l *ledger) totalForOrder(orderID string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var total int64
	for _, r := range l.records {
		if r.OrderID == orderID {
			total += r.AmountCents
		}
	}
	return total
}
