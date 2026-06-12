package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/event"
)

func TestRawInputValidationFailsWhenRequiredFieldsMissing(t *testing.T) {
	err := RawInput{}.Validate()
	if !errors.Is(err, ErrInvalidRawInput) {
		t.Fatalf("expected ErrInvalidRawInput, got %v", err)
	}
}

func TestPassthroughAdapterNormalizesRawInputToEvent(t *testing.T) {
	adapter, err := NewPassthroughAdapter("mission")
	if err != nil {
		t.Fatalf("new passthrough adapter: %v", err)
	}

	input := RawInput{
		ID:         "raw-1",
		Adapter:    "mission",
		Type:       "MISSION_SUBMITTED",
		Source:     "mission-cli",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		ActorID:    "human",
		ReceivedAt: time.Now().UTC(),
		Payload:    map[string]any{"mission": "cross the line"},
	}

	ev, err := adapter.Normalize(context.Background(), input)
	if err != nil {
		t.Fatalf("normalize raw input: %v", err)
	}
	if ev.Type != input.Type {
		t.Fatalf("expected event type %q, got %q", input.Type, ev.Type)
	}
	if ev.EntityID != input.EntityID {
		t.Fatalf("expected entity id %q, got %q", input.EntityID, ev.EntityID)
	}
}

func TestPassthroughAdapterRejectsMismatchedRawInputAdapter(t *testing.T) {
	adapter, err := NewPassthroughAdapter("mission")
	if err != nil {
		t.Fatalf("new passthrough adapter: %v", err)
	}

	_, err = adapter.Normalize(context.Background(), RawInput{
		ID:         "raw-1",
		Adapter:    "other",
		Type:       "MISSION_SUBMITTED",
		Source:     "mission-cli",
		ReceivedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrInvalidRawInput) {
		t.Fatalf("expected ErrInvalidRawInput, got %v", err)
	}
}

func TestMappingAdapterValidatesMappedEvent(t *testing.T) {
	adapter, err := NewMappingAdapter("bad", func(context.Context, RawInput) (event.Event, error) {
		return event.Event{}, nil
	})
	if err != nil {
		t.Fatalf("new mapping adapter: %v", err)
	}

	_, err = adapter.Normalize(context.Background(), RawInput{
		ID:         "raw-1",
		Adapter:    "bad",
		Type:       "MISSION_SUBMITTED",
		Source:     "test",
		ReceivedAt: time.Now().UTC(),
	})
	if !errors.Is(err, event.ErrInvalidEvent) {
		t.Fatalf("expected event.ErrInvalidEvent, got %v", err)
	}
}
