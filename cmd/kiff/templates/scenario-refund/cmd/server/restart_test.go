package main

import (
	"context"
	"net/http"
	"testing"
)

// TestPersistenceSurvivesRestart proves the default persistent store keeps both
// surfaces across a restart: the KIFF evidence (so the order stays REFUNDED and
// a repeat is refused) and the app ledger (so the recorded refund is still
// there). It simulates a restart by opening a second runtime over the same
// data directory.
func TestPersistenceSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// ---- First run: refund order-2 to completion. ----
	rt1, l1, close1, err := build(ctx, "file", dir, "")
	if err != nil {
		t.Fatalf("build run1: %v", err)
	}
	if err := seedOrders(ctx, rt1); err != nil {
		t.Fatalf("seed run1: %v", err)
	}
	h1 := buildMuxWithLedger(rt1, l1)

	do(t, h1, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)
	do(t, h1, http.MethodPost, "/api/approvals/a1/grant", `{}`)
	if code, resp := do(t, h1, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`); code != http.StatusOK || resp["outcome"] != "allowed" {
		t.Fatalf("run1 refund: expected 200 allowed, got %d %v", code, resp)
	}
	if l1.totalForOrder("order-2") != 99900 {
		t.Fatalf("run1 ledger should record the refund")
	}
	if close1 != nil {
		close1()
	}

	// ---- Second run: same data dir. State and ledger must survive. ----
	rt2, l2, close2, err := build(ctx, "file", dir, "")
	if err != nil {
		t.Fatalf("build run2: %v", err)
	}
	if close2 != nil {
		defer close2()
	}
	if err := seedOrders(ctx, rt2); err != nil {
		t.Fatalf("seed run2 (should rehydrate, not re-seed): %v", err)
	}
	h2 := buildMuxWithLedger(rt2, l2)

	// KIFF evidence survived: the order is still REFUNDED.
	if code, resp := do(t, h2, http.MethodGet, "/api/entities/order-2", ""); code != http.StatusOK || resp["state"] != "REFUNDED" {
		t.Fatalf("run2 state: expected REFUNDED, got %d %v", code, resp)
	}
	// So a repeat refund is still refused after the restart.
	if code, resp := do(t, h2, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","parameters":{"amount_cents":99900,"reason":"double"}}`); code != http.StatusConflict || resp["reason"] != "state_not_allowed" {
		t.Fatalf("run2 repeat: expected 409 state_not_allowed, got %d %v", code, resp)
	}
	// App ledger survived too.
	if l2.totalForOrder("order-2") != 99900 {
		t.Fatalf("run2 ledger should have loaded the prior refund, got %d", l2.totalForOrder("order-2"))
	}
}
