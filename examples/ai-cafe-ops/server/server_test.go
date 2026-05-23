package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	aicafeops "github.com/kiffhq/kiff/examples/ai-cafe-ops"
)

// TestServer_AutoOrderExecutes covers a small in-catalog order flowing
// straight through. Should land on AUTO_ORDER_INVENTORY, no approval.
func TestServer_AutoOrderExecutes(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id": "shift-1",
		"tool":     "order_inventory",
		"parameters": map[string]any{
			"item_id":      "napkins",
			"quantity":     200,
			"amount_cents": 1500,
		},
		"reasoning":  "low stock during morning rush",
		"confidence": 0.78,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentDecideResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "executed" {
		t.Fatalf("expected executed, got %q (err=%s)", body.Outcome, body.ErrorMessage)
	}
	if body.Action != aicafeops.ActionAutoOrderInventory {
		t.Fatalf("expected %s, got %s", aicafeops.ActionAutoOrderInventory, body.Action)
	}
}

// TestServer_LargeOrder_RequiresApprovalThenExecutes covers the
// approval round-trip on a single large inventory order.
func TestServer_LargeOrder_RequiresApprovalThenExecutes(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	first := agentDecideResponse{}
	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id": "shift-2",
		"tool":     "order_inventory",
		"parameters": map[string]any{
			"item_id":      "napkins",
			"quantity":     6000,
			"amount_cents": aicafeops.SingleOrderCeilingCents + 5000,
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	mustDecode(t, resp, &first)
	if first.Outcome != "approval_required" {
		t.Fatalf("expected approval_required, got %q", first.Outcome)
	}
	if first.ApprovalID == "" {
		t.Fatalf("expected approval id")
	}

	grant := postJSON(t, srv.URL+"/approvals/"+first.ApprovalID+"/grant", map[string]any{
		"actor": map[string]any{
			"id":   aicafeops.OperatorActor.ID,
			"type": aicafeops.OperatorActor.Type,
		},
		"reason": "approved",
	})
	defer grant.Body.Close()
	if grant.StatusCode != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d body=%s", grant.StatusCode, readBody(t, grant))
	}

	retry := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id": "shift-2",
		"tool":     "order_inventory",
		"parameters": map[string]any{
			"item_id":      "napkins",
			"quantity":     6000,
			"amount_cents": aicafeops.SingleOrderCeilingCents + 5000,
		},
		"approval_id": first.ApprovalID,
	})
	defer retry.Body.Close()
	if retry.StatusCode != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d body=%s", retry.StatusCode, readBody(t, retry))
	}
	var second agentDecideResponse
	mustDecode(t, retry, &second)
	if second.Outcome != "executed" {
		t.Fatalf("expected executed, got %q", second.Outcome)
	}
}

// TestServer_SpecialtyNotInCatalog_BlockedBeforeApproval verifies the
// catalog pre-check semantic: no approval is opened when the item is
// not on the allow-list. The outcome is `blocked_not_in_catalog`.
func TestServer_SpecialtyNotInCatalog_BlockedBeforeApproval(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id": "shift-3",
		"tool":     "request_specialty",
		"parameters": map[string]any{
			"item_id":   "yuzu_concentrate",
			"rationale": "trying a limited summer special",
		},
		"reasoning": "creative inventory experiment",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentDecideResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "blocked_not_in_catalog" {
		t.Fatalf("expected blocked_not_in_catalog, got %q", body.Outcome)
	}
	if body.ApprovalID != "" {
		t.Fatalf("expected NO approval id (catalog gate runs before approval)")
	}

	// And no approvals were opened on the shift.
	apps, err := http.Get(srv.URL + "/entities/shift-3/approvals")
	if err != nil {
		t.Fatalf("GET approvals: %v", err)
	}
	defer apps.Body.Close()
	var listed struct {
		Approvals []any `json:"approvals"`
	}
	mustDecode(t, apps, &listed)
	if len(listed.Approvals) != 0 {
		t.Fatalf("expected 0 approvals, got %d", len(listed.Approvals))
	}
}

// TestServer_StaffMessageAfterHours_BlockedBeforeApproval verifies the
// working-hours pre-check. No approval opened, outcome is
// `blocked_after_hours`.
func TestServer_StaffMessageAfterHours_BlockedBeforeApproval(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id": "shift-4",
		"tool":     "send_staff_message",
		"parameters": map[string]any{
			"recipient":     "barista-team",
			"message":       "extra rush expected first thing",
			"sent_at_local": "02:14",
		},
		"reasoning": "shift handoff note",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentDecideResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "blocked_after_hours" {
		t.Fatalf("expected blocked_after_hours, got %q", body.Outcome)
	}
	if body.ApprovalID != "" {
		t.Fatalf("expected NO approval id (working-hours gate runs before approval)")
	}
}

// TestServer_EscalateSupplier covers the always-allowed action:
// ESCALATE_SUPPLIER runs without approval and parks the shift in
// AWAITING_HUMAN.
func TestServer_EscalateSupplier(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id": "shift-5",
		"tool":     "escalate_supplier",
		"parameters": map[string]any{
			"supplier_id": "mercato-supplies",
			"reason":      "missed delivery window",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentDecideResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "executed" || body.Action != aicafeops.ActionEscalateSupplier {
		t.Fatalf("unexpected: %+v", body)
	}
	if body.State != aicafeops.StateAwaitingHuman {
		t.Fatalf("expected %s, got %s", aicafeops.StateAwaitingHuman, body.State)
	}
}

// TestServer_ListShifts sanity-checks that the seeds landed in OPEN.
func TestServer_ListShifts(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/demo/shifts")
	if err != nil {
		t.Fatalf("GET shifts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Shifts []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"shifts"`
	}
	mustDecode(t, resp, &body)
	if len(body.Shifts) != 5 {
		t.Fatalf("expected 5 shifts, got %d", len(body.Shifts))
	}
	for _, s := range body.Shifts {
		if s.State != aicafeops.StateOpen {
			t.Fatalf("shift %s expected %s, got %s", s.ID, aicafeops.StateOpen, s.State)
		}
	}
}

// TestServer_Catalog returns the seed catalog and the working-hours
// window so the agent's caller can render context.
func TestServer_Catalog(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/demo/catalog")
	if err != nil {
		t.Fatalf("GET catalog: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Catalog            []string `json:"catalog"`
		WorkingHoursStart  int      `json:"working_hours_start"`
		WorkingHoursEnd    int      `json:"working_hours_end"`
	}
	mustDecode(t, resp, &body)
	if len(body.Catalog) == 0 {
		t.Fatalf("expected catalog entries")
	}
	if body.WorkingHoursStart == 0 && body.WorkingHoursEnd == 0 {
		t.Fatalf("expected working-hours window in response")
	}
}

// TestServer_Rebuild verifies the rebuild route reconciles materialized
// state with the replayed state for a shift that has gone through
// multiple actions.
func TestServer_Rebuild(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	// Trigger a small auto order to write events.
	post := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"shift_id":   "shift-5",
		"tool":       "order_inventory",
		"parameters": map[string]any{"item_id": "napkins", "quantity": 100, "amount_cents": 1000},
	})
	post.Body.Close()

	resp, err := http.Get(srv.URL + "/demo/rebuild?entity=shift-5")
	if err != nil {
		t.Fatalf("GET rebuild: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Materialized string `json:"materialized"`
		Replayed     string `json:"replayed"`
		Matches      bool   `json:"matches"`
	}
	mustDecode(t, resp, &body)
	if !body.Matches {
		t.Fatalf("rebuild mismatch: %+v", body)
	}
}

// helpers

func newTestServer(t *testing.T) (*httptest.Server, struct{}) {
	t.Helper()
	d := aicafeops.New()
	rt, err := d.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := seedShifts(context.Background(), rt); err != nil {
		t.Fatalf("seedShifts: %v", err)
	}
	srv := httptest.NewServer(buildMux(d, rt))
	return srv, struct{}{}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func mustDecode(t *testing.T, resp *http.Response, into any) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode: %v body=%s", err, string(body))
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("<read err: %v>", err)
	}
	return string(body)
}
