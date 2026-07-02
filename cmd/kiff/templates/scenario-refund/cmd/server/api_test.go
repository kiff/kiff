package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func newServer(t *testing.T) (http.Handler, *ledger) {
	t.Helper()
	rt, err := runtimeSeeded(t)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	l := &ledger{}
	return buildMuxWithLedger(rt, l), l
}

func runtimeSeeded(t *testing.T) (*runtime.Runtime, error) {
	t.Helper()
	rt, closer, err := buildRuntime("")
	if err != nil {
		return nil, err
	}
	if closer != nil {
		t.Cleanup(closer)
	}
	if err := seedOrders(context.Background(), rt); err != nil {
		return nil, err
	}
	return rt, nil
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("content-type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// TestToolCall_AllowedExecutesAndRecordsSideEffect: grant, then a refund
// executes and the ledger records exactly one entry.
func TestToolCall_AllowedExecutesAndRecordsSideEffect(t *testing.T) {
	h, l := newServer(t)

	// High risk: first call is held for approval, no side effect yet.
	code, resp := do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)
	if code != http.StatusConflict || resp["outcome"] != "approval_required" {
		t.Fatalf("expected 409 approval_required, got %d %v", code, resp)
	}
	if len(l.all()) != 0 {
		t.Fatalf("no side effect should have run yet, ledger=%v", l.all())
	}

	// Operator grants.
	if code, _ := do(t, h, http.MethodPost, "/api/approvals/a1/grant", `{}`); code != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d", code)
	}

	// Same call now executes; the side effect runs exactly once.
	code, resp = do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)
	if code != http.StatusOK || resp["outcome"] != "allowed" {
		t.Fatalf("expected 200 allowed, got %d %v", code, resp)
	}
	if resp["state"] != "REFUNDED" {
		t.Fatalf("expected post-state REFUNDED, got %v", resp["state"])
	}
	if got := l.totalForOrder("order-2"); got != 99900 {
		t.Fatalf("expected one refund of 99900 in ledger, got %d", got)
	}
}

// TestToolCall_BlockedDoesNotExecuteSideEffect: a repeat after REFUNDED is
// blocked and writes nothing more.
func TestToolCall_BlockedDoesNotExecuteSideEffect(t *testing.T) {
	h, l := newServer(t)
	do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)
	do(t, h, http.MethodPost, "/api/approvals/a1/grant", `{}`)
	do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)
	before := len(l.all())

	code, resp := do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","parameters":{"amount_cents":99900,"reason":"double refund"}}`)
	if code != http.StatusConflict || resp["outcome"] != "blocked" || resp["reason"] != "state_not_allowed" {
		t.Fatalf("expected 409 blocked/state_not_allowed, got %d %v", code, resp)
	}
	if len(l.all()) != before {
		t.Fatalf("blocked call must not write the side effect")
	}
}

// TestToolCall_ApprovalRequiredReturnsApprovalPath: the envelope carries an
// approval id and next step.
func TestToolCall_ApprovalRequiredReturnsApprovalPath(t *testing.T) {
	h, l := newServer(t)
	code, resp := do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-1","parameters":{"amount_cents":4200,"reason":"eligible"}}`)
	if code != http.StatusConflict || resp["outcome"] != "approval_required" {
		t.Fatalf("expected 409 approval_required, got %d %v", code, resp)
	}
	if resp["next_step"] != "request_approval" || resp["approval_id"] == nil {
		t.Fatalf("expected approval path fields, got %v", resp)
	}
	if len(l.all()) != 0 {
		t.Fatalf("no side effect on an approval-required call")
	}
}

func TestToolCall_UnknownTool(t *testing.T) {
	h, _ := newServer(t)
	code, resp := do(t, h, http.MethodPost, "/api/tools/delete_everything", `{"entity_id":"order-1"}`)
	if code != http.StatusNotFound || resp["reason"] != "unknown_action" {
		t.Fatalf("expected 404 unknown_action, got %d %v", code, resp)
	}
}

// TestToolCall_AcceptsStringParamsFromOpenAPISchema: the generated OpenAPI
// types parameters as strings; a client that follows it and sends
// "amount_cents":"99900" must still work (the executor coerces it).
func TestToolCall_AcceptsStringParamsFromOpenAPISchema(t *testing.T) {
	h, l := newServer(t)
	do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":"99900","reason":"eligible"}}`)
	do(t, h, http.MethodPost, "/api/approvals/a1/grant", `{}`)
	code, resp := do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":"99900","reason":"eligible"}}`)
	if code != http.StatusOK || resp["outcome"] != "allowed" {
		t.Fatalf("expected 200 allowed with string amount, got %d %v", code, resp)
	}
	if got := l.totalForOrder("order-2"); got != 99900 {
		t.Fatalf("expected 99900 recorded from string param, got %d", got)
	}
}

// TestTimelineReflectsAPICall: after a governed refund, the entity timeline
// shows the executed action.
func TestTimelineReflectsAPICall(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)
	do(t, h, http.MethodPost, "/api/approvals/a1/grant", `{}`)
	do(t, h, http.MethodPost, "/api/tools/refund_order",
		`{"entity_id":"order-2","approval_id":"a1","parameters":{"amount_cents":99900,"reason":"eligible"}}`)

	code, resp := do(t, h, http.MethodGet, "/api/entities/order-2/timeline", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: expected 200, got %d", code)
	}
	tl, ok := resp["timeline"].([]any)
	if !ok || len(tl) == 0 {
		t.Fatalf("expected a non-empty timeline, got %v", resp["timeline"])
	}
}

// TestManifestAndOpenAPIDerivedFromCatalog: the tool surface lists the domain's
// actions, generated from the catalog.
func TestManifestAndOpenAPIDerivedFromCatalog(t *testing.T) {
	h, _ := newServer(t)

	code, resp := do(t, h, http.MethodGet, "/api/tools/manifest.json", "")
	if code != http.StatusOK {
		t.Fatalf("manifest: %d", code)
	}
	tools, _ := resp["tools"].([]any)
	if !containsTool(tools, "refund_order") || !containsTool(tools, "mark_paid") {
		t.Fatalf("manifest missing expected tools: %v", tools)
	}

	code, resp = do(t, h, http.MethodGet, "/api/openapi.json", "")
	if code != http.StatusOK {
		t.Fatalf("openapi: %d", code)
	}
	paths, _ := resp["paths"].(map[string]any)
	if _, ok := paths["/api/tools/refund_order"]; !ok {
		t.Fatalf("openapi missing /api/tools/refund_order: %v", paths)
	}
}

func containsTool(tools []any, name string) bool {
	for _, tv := range tools {
		if m, ok := tv.(map[string]any); ok && m["tool"] == name {
			return true
		}
	}
	return false
}
