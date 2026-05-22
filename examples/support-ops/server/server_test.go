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

	supportops "github.com/kiffhq/kiff/examples/support-ops"
)

// TestServer_AutoRefundExecutes covers a small refund flowing straight
// through. Should land on AUTO_REFUND, no approval.
func TestServer_AutoRefundExecutes(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"ticket_id": "ticket-1",
		"tool":      "issue_refund",
		"parameters": map[string]any{
			"amount_cents": 1500,
			"reason":       "small refund",
		},
		"reasoning":  "ticket says small price discrepancy",
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
	if body.Action != supportops.ActionAutoRefund {
		t.Fatalf("expected %s, got %s", supportops.ActionAutoRefund, body.Action)
	}
}

// TestServer_LargeRefund_RequiresApprovalThenExecutes covers the
// approval round-trip on a single large refund.
func TestServer_LargeRefund_RequiresApprovalThenExecutes(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	first := agentDecideResponse{}
	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"ticket_id": "ticket-2",
		"tool":      "issue_refund",
		"parameters": map[string]any{
			"amount_cents": supportops.SingleRefundCeilingCents + 5000,
			"reason":       "high-value refund",
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
			"id":   supportops.OperatorActor.ID,
			"type": supportops.OperatorActor.Type,
		},
		"reason": "approved",
	})
	defer grant.Body.Close()
	if grant.StatusCode != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d body=%s", grant.StatusCode, readBody(t, grant))
	}

	retry := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"ticket_id": "ticket-2",
		"tool":      "issue_refund",
		"parameters": map[string]any{
			"amount_cents": supportops.SingleRefundCeilingCents + 5000,
			"reason":       "high-value refund",
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

// TestServer_OutreachWithoutConsent_BlockedBeforeApproval verifies the
// custom validator semantic: no approval is opened when consent is
// missing. The outcome is `blocked_consent_missing`.
func TestServer_OutreachWithoutConsent_BlockedBeforeApproval(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"ticket_id": "ticket-3",
		"tool":      "send_outreach",
		"parameters": map[string]any{
			"channel":          "email",
			"message":          "follow up",
			"consent_verified": false,
		},
		"reasoning": "ambiguous consent state",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentDecideResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "blocked_consent_missing" {
		t.Fatalf("expected blocked_consent_missing, got %q", body.Outcome)
	}
	if body.ApprovalID != "" {
		t.Fatalf("expected NO approval id (consent gate runs before approval)")
	}

	// And no approvals were opened on the ticket.
	apps, err := http.Get(srv.URL + "/entities/ticket-3/approvals")
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

// TestServer_Escalate covers the always-allowed action: ESCALATE_TO_HUMAN
// runs without approval and parks the ticket in AWAITING_HUMAN.
func TestServer_Escalate(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"ticket_id": "ticket-4",
		"tool":      "escalate_to_human",
		"parameters": map[string]any{
			"reason": "abusive content",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentDecideResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "executed" || body.Action != supportops.ActionEscalate {
		t.Fatalf("unexpected: %+v", body)
	}
	if body.State != supportops.StateAwaitingHuman {
		t.Fatalf("expected %s, got %s", supportops.StateAwaitingHuman, body.State)
	}
}

// TestServer_ListTickets sanity-checks that the seeds landed in TRIAGED.
func TestServer_ListTickets(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/demo/tickets")
	if err != nil {
		t.Fatalf("GET tickets: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Tickets []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"tickets"`
	}
	mustDecode(t, resp, &body)
	if len(body.Tickets) != 5 {
		t.Fatalf("expected 5 tickets, got %d", len(body.Tickets))
	}
	for _, t0 := range body.Tickets {
		expected := supportops.StateTriaged
		if t0.ID == "ticket-5" {
			expected = supportops.StateResolved
		}
		if t0.State != expected {
			t.Fatalf("ticket %s expected %s, got %s", t0.ID, expected, t0.State)
		}
	}
}

// TestServer_Rebuild verifies the rebuild route reconciles materialized
// state with the replayed state for a ticket that has gone through
// multiple actions.
func TestServer_Rebuild(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	// Trigger a small auto refund to write events.
	post := postJSON(t, srv.URL+"/demo/agent/decide", map[string]any{
		"ticket_id":  "ticket-5",
		"tool":       "issue_refund",
		"parameters": map[string]any{"amount_cents": 1000, "reason": "tiny"},
	})
	post.Body.Close()

	resp, err := http.Get(srv.URL + "/demo/rebuild?entity=ticket-5")
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
	d := supportops.New()
	rt, err := d.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := seedTickets(context.Background(), rt); err != nil {
		t.Fatalf("seedTickets: %v", err)
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
