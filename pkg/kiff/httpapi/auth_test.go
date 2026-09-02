package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiff/kiff/examples/mission"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

func newTestRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	rt, err := mission.NewRuntime()
	if err != nil {
		t.Fatalf("new mission runtime: %v", err)
	}
	return rt
}

func authedHandler(t *testing.T, rt *runtime.Runtime) *Handler {
	t.Helper()
	return NewHandler(rt, NewStaticTokenAuthenticator(map[string]Principal{
		"agent-token":    {ActorID: "support-agent", Roles: []string{"support_agent"}},
		"operator-token": {ActorID: "ops-human", Roles: []string{"ops_operator"}},
	}))
}

func post(t *testing.T, h http.Handler, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A handler with neither an Authenticator nor an explicit opt-out is a
// misconfiguration. Refusing is the only safe reading of "not decided yet" —
// serving would silently expose every route.
func TestHandlerWithoutAuthenticatorRefusesToServe(t *testing.T) {
	h := &Handler{Runtime: newTestRuntime(t)}
	w := post(t, h, "", "/events/raw", `{}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 for an undecided handler, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no Authenticator") {
		t.Errorf("the error should name the misconfiguration, got %q", w.Body.String())
	}
}

func TestUnauthenticatedRequestsAreRefused(t *testing.T) {
	h := authedHandler(t, newTestRuntime(t))
	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"wrong token", "not-a-real-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := post(t, h, tc.token, "/events/raw", `{"id":"e1","adapter":"a","type":"T","source":"s","received_at":"2026-01-01T00:00:00Z"}`)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d (%s)", w.Code, w.Body.String())
			}
			if got := w.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("want a WWW-Authenticate challenge, got %q", got)
			}
		})
	}
}

// The attack this interface exists to stop. Before authentication, an agent
// could name itself the human operator in the request body, request an
// approval as them, and grant it — with the audit trail recording
// requested_by == reviewed_by == ops-human and nothing objecting.
//
// Now the token decides who the caller is, and the body cannot argue.
func TestBodyActorCannotOverrideTheAuthenticatedPrincipal(t *testing.T) {
	rt := newTestRuntime(t)
	h := authedHandler(t, rt)

	// The agent presents its own token but claims to be the operator.
	body := `{"actor":{"id":"ops-human","roles":["ops_operator"]},"reason":"self-granted"}`
	if err := rt.RecordApproval(context.Background(), approval.Approval{
		ID: "appr-1", EntityID: "order-1", EntityType: "Order",
		ActionName: "REFUND_ORDER", RequestedBy: "support-agent",
		Status: approval.StatusPending, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed approval: %v", err)
	}

	w := post(t, h, "agent-token", "/approvals/appr-1/grant", body)
	if w.Code != http.StatusOK {
		t.Fatalf("grant failed: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Approval approval.Approval `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Approval.ReviewedBy == "ops-human" {
		t.Fatal("IMPERSONATION: the body's actor overrode the authenticated principal")
	}
	if resp.Approval.ReviewedBy != "support-agent" {
		t.Errorf("reviewed_by = %q, want the token's principal support-agent", resp.Approval.ReviewedBy)
	}
}

// Ingestion is identity-bearing: events drive state, and state is what actions
// are judged against.
func TestIngestActorComesFromThePrincipal(t *testing.T) {
	rt := newTestRuntime(t)
	h := authedHandler(t, rt)

	w := post(t, h, "agent-token", "/events/raw",
		`{"id":"e1","adapter":"mission","type":"MISSION_SUBMITTED","source":"s","received_at":"2026-01-01T00:00:00Z","entity_id":"attempt-9","entity_type":"MissionAttempt","actor_id":"ops-human"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("ingest failed: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Event struct {
			ActorID string `json:"actor_id"`
		} `json:"event"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Event.ActorID == "ops-human" {
		t.Fatal("ATTRIBUTION FORGERY: a caller-supplied actor_id survived ingestion")
	}
	if resp.Event.ActorID != "support-agent" {
		t.Errorf("event actor_id = %q, want support-agent", resp.Event.ActorID)
	}
}

func TestStaticTokenAuthenticatorRejectsMalformedHeaders(t *testing.T) {
	a := NewStaticTokenAuthenticator(map[string]Principal{"t": {ActorID: "a"}})
	for _, header := range []string{"", "Bearer", "Bearer ", "Basic t", "t"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if _, err := a.Authenticate(r); err == nil {
			t.Errorf("header %q should not authenticate", header)
		}
	}
}
