package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	refundagno "github.com/kiff/kiff/examples/refund-agno"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

// TestServer_AgentRefund_AutoExecutes covers the small-amount happy path:
// the agent's tool call lands on AUTO_REFUND and executes without
// approval. This is the "low-risk action stays low-risk" half of the demo.
func TestServer_AgentRefund_AutoExecutes(t *testing.T) {
	srv, rt := newTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/demo/agent/refund", map[string]any{
		"order_id":     "order-1",
		"amount_cents": 4200,
		"reason":       "small refund, customer mailed",
		"reasoning":    "ticket says product damaged",
		"confidence":   0.7,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body agentResponse
	mustDecode(t, resp, &body)
	if body.Outcome != "executed" {
		t.Fatalf("expected executed, got %q (err=%s)", body.Outcome, body.ErrorMessage)
	}
	if body.Action != refundagno.ActionAutoRefund {
		t.Fatalf("expected %s, got %s", refundagno.ActionAutoRefund, body.Action)
	}
	if body.State != refundagno.StateRefunded {
		t.Fatalf("expected state %s, got %s", refundagno.StateRefunded, body.State)
	}

	// The audit trail must contain a recorded proposal (with reasoning).
	records, err := rt.Audit.Query(context.Background(), audit.Filter{EntityID: "order-1"})
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if !containsKind(records, audit.KindDecisionProposed) {
		t.Fatalf("expected decision_proposed in audit, got %v", kinds(records))
	}
}

// TestServer_AgentRefund_ApprovalRequired_GrantedFlow covers the demo's
// headline path:
//
//   1. agent posts a $999 refund
//   2. server returns approval_required + opens a pending approval
//   3. operator grants the approval through the kiff API
//   4. agent retries; KIFF lets it through
//
// Asserting on outcome strings keeps the test aligned with what the
// Python agent observes.
func TestServer_AgentRefund_ApprovalRequired_GrantedFlow(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	// 1. First tool call: damage *would* happen on an unguarded server.
	first := agentResponse{}
	resp := postJSON(t, srv.URL+"/demo/agent/refund", map[string]any{
		"order_id":     "order-2",
		"amount_cents": 99900,
		"reason":       "agent thinks customer is unhappy",
		"reasoning":    "ticket: 'i want my money back'",
		"confidence":   0.81,
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
		t.Fatalf("expected approval id, got empty")
	}
	if first.State != refundagno.StatePaid {
		t.Fatalf("expected state %s after blocked refund, got %s", refundagno.StatePaid, first.State)
	}

	// 2. Operator grants the approval through the standard kiff route.
	grant := postJSON(t, srv.URL+"/approvals/"+first.ApprovalID+"/grant", map[string]any{
		"actor": map[string]any{
			"id":   refundagno.OperatorActor.ID,
			"type": refundagno.OperatorActor.Type,
		},
		"reason": "checked, refund is reasonable",
	})
	defer grant.Body.Close()
	if grant.StatusCode != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d body=%s", grant.StatusCode, readBody(t, grant))
	}

	// 3. Agent retries with the same approval id. Now KIFF lets it through.
	retry := postJSON(t, srv.URL+"/demo/agent/refund", map[string]any{
		"order_id":     "order-2",
		"amount_cents": 99900,
		"reason":       "agent thinks customer is unhappy",
		"reasoning":    "ticket: 'i want my money back'",
		"confidence":   0.81,
		"approval_id":  first.ApprovalID,
	})
	defer retry.Body.Close()
	if retry.StatusCode != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d body=%s", retry.StatusCode, readBody(t, retry))
	}
	var second agentResponse
	mustDecode(t, retry, &second)
	if second.Outcome != "executed" {
		t.Fatalf("expected executed after grant, got %q (err=%s)", second.Outcome, second.ErrorMessage)
	}
	if second.State != refundagno.StateRefunded {
		t.Fatalf("expected refunded, got %s", second.State)
	}
}

// TestServer_AgentRefund_DeniedStaysBlocked covers the denied path. After
// a denial, retrying with the same approval id must keep returning
// approval_required and the order must remain in PAID.
func TestServer_AgentRefund_DeniedStaysBlocked(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	first := agentResponse{}
	resp := postJSON(t, srv.URL+"/demo/agent/refund", map[string]any{
		"order_id":     "order-3",
		"amount_cents": 25000,
		"reason":       "blanket refund please",
		"reasoning":    "ambiguous ticket; agent guessing",
		"confidence":   0.42,
	})
	defer resp.Body.Close()
	mustDecode(t, resp, &first)
	if first.Outcome != "approval_required" || first.ApprovalID == "" {
		t.Fatalf("expected approval_required + id, got %+v", first)
	}

	deny := postJSON(t, srv.URL+"/approvals/"+first.ApprovalID+"/deny", map[string]any{
		"actor": map[string]any{
			"id":   refundagno.OperatorActor.ID,
			"type": refundagno.OperatorActor.Type,
		},
		"reason": "evidence missing",
	})
	defer deny.Body.Close()
	if deny.StatusCode != http.StatusOK {
		t.Fatalf("deny: expected 200, got %d body=%s", deny.StatusCode, readBody(t, deny))
	}

	retry := postJSON(t, srv.URL+"/demo/agent/refund", map[string]any{
		"order_id":     "order-3",
		"amount_cents": 25000,
		"reason":       "blanket refund please",
		"reasoning":    "still no evidence",
		"confidence":   0.42,
		"approval_id":  first.ApprovalID,
	})
	defer retry.Body.Close()
	if retry.StatusCode == http.StatusOK {
		t.Fatalf("retry must not succeed after denial, body=%s", readBody(t, retry))
	}
	var second agentResponse
	mustDecode(t, retry, &second)
	if second.Outcome != "approval_required" {
		t.Fatalf("expected approval_required after denial, got %q", second.Outcome)
	}
	if second.State != refundagno.StatePaid {
		t.Fatalf("expected state to remain %s, got %s", refundagno.StatePaid, second.State)
	}
}

// TestServer_ListOrders sanity-checks the seeded state.
func TestServer_ListOrders(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/demo/orders")
	if err != nil {
		t.Fatalf("GET orders: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Orders []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"orders"`
	}
	mustDecode(t, resp, &body)
	if len(body.Orders) != 3 {
		t.Fatalf("expected 3 orders, got %d", len(body.Orders))
	}
	for _, o := range body.Orders {
		if o.State != refundagno.StatePaid {
			t.Fatalf("expected %s, got %s for %s", refundagno.StatePaid, o.State, o.ID)
		}
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *runtime.Runtime) {
	t.Helper()
	rt, err := refundagno.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := seedOrders(context.Background(), rt); err != nil {
		t.Fatalf("seedOrders: %v", err)
	}
	srv := httptest.NewServer(buildMux(rt))
	return srv, rt
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

func containsKind(records []audit.Record, kind audit.Kind) bool {
	for _, r := range records {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func kinds(records []audit.Record) []audit.Kind {
	out := make([]audit.Kind, 0, len(records))
	for _, r := range records {
		out = append(out, r.Kind)
	}
	return out
}

// silenceUnused keeps the linter from complaining about errors helpers
// that the test scaffolding may not always need to invoke.
var _ = errors.Is
