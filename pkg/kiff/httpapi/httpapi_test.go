package httpapi

import (
	"bytes"
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
	if err := handler.Runtime.IngestEvent(createAttempt); err != nil {
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
	_, err := handler.Runtime.IngestRaw(adapter.RawInput{
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

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
