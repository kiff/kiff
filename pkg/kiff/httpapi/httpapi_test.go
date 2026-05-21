package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/examples/mission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
)

func TestHandlerIngestsRawEvent(t *testing.T) {
	handler := newMissionHandler(t)
	body := mustJSON(t, adapter.RawInput{
		ID:         "evt-1",
		Adapter:    mission.AdapterMission,
		Type:       mission.EventMissionSubmitted,
		Source:     "http-test",
		EntityID:   "attempt-1",
		EntityType: mission.EntityTypeMissionAttempt,
		ActorID:    mission.HumanActor.ID,
		ReceivedAt: time.Now().UTC(),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/events/raw", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["event"] == nil {
		t.Fatal("expected event response")
	}
}

func TestHandlerReturnsAllowedActions(t *testing.T) {
	handler := newMissionHandler(t)
	ingestMissionSubmitted(t, handler)

	createAttempt := event.Event{
		ID:         "evt-2",
		Type:       mission.EventAttemptCreated,
		EntityID:   "attempt-1",
		EntityType: mission.EntityTypeMissionAttempt,
		Source:     "http-test",
		ActorID:    mission.AgentActor.ID,
		OccurredAt: time.Now().UTC(),
	}
	if err := handler.Runtime.IngestEvent(context.Background(), createAttempt); err != nil {
		t.Fatalf("ingest attempt created: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/entities/attempt-1/allowed-actions", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(mission.ActionProposeMove)) {
		t.Fatalf("expected allowed action %q in body %s", mission.ActionProposeMove, recorder.Body.String())
	}
}

func TestHandlerReturnsTimeline(t *testing.T) {
	handler := newMissionHandler(t)
	ingestMissionSubmitted(t, handler)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/entities/attempt-1/timeline", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("event_ingested")) {
		t.Fatalf("expected event_ingested in body %s", recorder.Body.String())
	}
}

func TestHandlerValidatesAction(t *testing.T) {
	handler := newMissionHandler(t)
	prepareActiveAttempt(t, handler)
	body := mustJSON(t, actionRequest{
		Actor: mission.AgentActor,
		Parameters: map[string]any{
			"move": "draft the first bounded move",
		},
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/entities/attempt-1/actions/PROPOSE_MOVE/validate", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"valid":true`)) {
		t.Fatalf("expected valid response, got %s", recorder.Body.String())
	}
}

func TestHandlerExecutesAction(t *testing.T) {
	handler := newMissionHandler(t)
	ingestMissionSubmitted(t, handler)
	body := mustJSON(t, actionRequest{Actor: mission.AgentActor})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/entities/attempt-1/actions/CREATE_ATTEMPT/execute", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"succeeded"`)) {
		t.Fatalf("expected succeeded result, got %s", recorder.Body.String())
	}
}

func TestHandlerReturnsConflictWhenApprovalRequired(t *testing.T) {
	handler := newMissionHandler(t)
	prepareWaitingApprovalAttempt(t, handler)
	body := mustJSON(t, actionRequest{
		Actor: mission.AgentActor,
		Parameters: map[string]any{
			"move": "draft the first bounded move",
		},
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/entities/attempt-1/actions/EXECUTE_MOVE/execute", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerRequestsApproval(t *testing.T) {
	handler := newMissionHandler(t)
	prepareWaitingApprovalAttempt(t, handler)

	recorder := requestMoveApproval(t, handler, "approval-1")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("expected pending approval, got %s", recorder.Body.String())
	}
}

func TestHandlerListsApprovals(t *testing.T) {
	handler := newMissionHandler(t)
	prepareWaitingApprovalAttempt(t, handler)
	recorder := requestMoveApproval(t, handler, "approval-1")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("request approval failed with status %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/entities/attempt-1/approvals", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("approval-1")) {
		t.Fatalf("expected approval-1 in response, got %s", recorder.Body.String())
	}
}

func TestHandlerGrantsApprovalAndExecutesAction(t *testing.T) {
	handler := newMissionHandler(t)
	prepareWaitingApprovalAttempt(t, handler)
	recorder := requestMoveApproval(t, handler, "approval-1")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("request approval failed with status %d: %s", recorder.Code, recorder.Body.String())
	}

	body := mustJSON(t, approvalReviewRequest{
		Actor:  mission.HumanActor,
		Reason: "approved after human review",
	})
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/approvals/approval-1/grant", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"granted"`)) {
		t.Fatalf("expected granted approval, got %s", recorder.Body.String())
	}

	body = mustJSON(t, actionRequest{
		Actor:      mission.AgentActor,
		ApprovalID: "approval-1",
		Parameters: map[string]any{
			"move": "draft the first bounded move",
		},
	})
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/entities/attempt-1/actions/EXECUTE_MOVE/execute", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"succeeded"`)) {
		t.Fatalf("expected succeeded result, got %s", recorder.Body.String())
	}
}

func TestHandlerDeniesApproval(t *testing.T) {
	handler := newMissionHandler(t)
	prepareWaitingApprovalAttempt(t, handler)
	recorder := requestMoveApproval(t, handler, "approval-1")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("request approval failed with status %d: %s", recorder.Code, recorder.Body.String())
	}

	body := mustJSON(t, approvalReviewRequest{
		Actor:  mission.HumanActor,
		Reason: "not enough evidence",
	})
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/approvals/approval-1/deny", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"denied"`)) {
		t.Fatalf("expected denied approval, got %s", recorder.Body.String())
	}
}

func TestHandlerReturnsBadRequestForMissingAdapter(t *testing.T) {
	handler := newMissionHandler(t)
	body := mustJSON(t, adapter.RawInput{
		ID:         "evt-1",
		Adapter:    "missing",
		Type:       mission.EventMissionSubmitted,
		Source:     "http-test",
		ReceivedAt: time.Now().UTC(),
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/events/raw", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func newMissionHandler(t *testing.T) *Handler {
	t.Helper()
	rt, err := mission.NewRuntime()
	if err != nil {
		t.Fatalf("new mission runtime: %v", err)
	}
	return NewHandler(rt)
}

func ingestMissionSubmitted(t *testing.T, handler *Handler) {
	t.Helper()
	_, err := handler.Runtime.IngestRaw(context.Background(), adapter.RawInput{
		ID:         "evt-1",
		Adapter:    mission.AdapterMission,
		Type:       mission.EventMissionSubmitted,
		Source:     "http-test",
		EntityID:   "attempt-1",
		EntityType: mission.EntityTypeMissionAttempt,
		ActorID:    mission.HumanActor.ID,
		ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ingest mission submitted: %v", err)
	}
}

func prepareActiveAttempt(t *testing.T, handler *Handler) {
	t.Helper()
	ingestMissionSubmitted(t, handler)
	createAttempt := event.Event{
		ID:         "evt-2",
		Type:       mission.EventAttemptCreated,
		EntityID:   "attempt-1",
		EntityType: mission.EntityTypeMissionAttempt,
		Source:     "http-test",
		ActorID:    mission.AgentActor.ID,
		OccurredAt: time.Now().UTC(),
	}
	if err := handler.Runtime.IngestEvent(context.Background(), createAttempt); err != nil {
		t.Fatalf("ingest attempt created: %v", err)
	}
}

func prepareWaitingApprovalAttempt(t *testing.T, handler *Handler) {
	t.Helper()
	prepareActiveAttempt(t, handler)
	moveProposed := event.Event{
		ID:         "evt-3",
		Type:       mission.EventMoveProposed,
		EntityID:   "attempt-1",
		EntityType: mission.EntityTypeMissionAttempt,
		Source:     "http-test",
		ActorID:    mission.AgentActor.ID,
		OccurredAt: time.Now().UTC(),
		Payload:    map[string]any{"move": "draft the first bounded move"},
	}
	if err := handler.Runtime.IngestEvent(context.Background(), moveProposed); err != nil {
		t.Fatalf("ingest move proposed: %v", err)
	}
}

func requestMoveApproval(t *testing.T, handler *Handler, approvalID string) *httptest.ResponseRecorder {
	t.Helper()
	body := mustJSON(t, actionRequest{
		Actor:      mission.AgentActor,
		ApprovalID: approvalID,
		Reason:     "high-risk move execution requires human authority",
		Parameters: map[string]any{
			"move": "draft the first bounded move",
		},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/entities/attempt-1/actions/EXECUTE_MOVE/approvals", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)
	return recorder
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
