package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

// refundRecord is one entry in the mock business ledger — the side effect a
// refund action produces. In a real app this would be a row in your database
// or a call to a payments provider.
type refundRecord struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
	Guarded     bool   `json:"guarded"`
}

// ledger is the app's business state. The contrast the demo exists to show:
// the unguarded path writes here directly (a duplicate refund lands), while
// the KIFF-guarded path only writes after the runtime allows the action.
//
// When path is set, the ledger is append-only JSONL on disk and is loaded on
// startup, so the app-state surface survives a restart alongside the KIFF
// evidence. An empty path keeps it in-memory (the -store=memory mode).
type ledger struct {
	mu      sync.Mutex
	records []refundRecord
	path    string
}

func newLedger(path string) *ledger {
	l := &ledger{path: path}
	if path != "" {
		l.load()
	}
	return l
}

func (l *ledger) load() {
	f, err := os.Open(l.path)
	if err != nil {
		return // no prior ledger yet
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r refundRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			l.records = append(l.records, r)
		}
	}
}

func (l *ledger) record(r refundRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r)
	if l.path == "" {
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if b, err := json.Marshal(r); err == nil {
		_, _ = f.Write(append(b, '\n'))
	}
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
