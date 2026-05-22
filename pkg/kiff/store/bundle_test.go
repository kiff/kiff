package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/event"
)

func TestBundleValidationRequiresAllStores(t *testing.T) {
	if err := (Bundle{}).Validate(); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("expected ErrMisconfigured, got %v", err)
	}

	bundle := NewInMemoryBundle()
	if err := bundle.Validate(); err != nil {
		t.Fatalf("validate in-memory bundle: %v", err)
	}
}

func TestInMemoryBundleProvidesWorkingStores(t *testing.T) {
	bundle := NewInMemoryBundle()
	err := bundle.Events.Append(context.Background(), event.Event{
		ID:         "evt-1",
		Type:       "MISSION_SUBMITTED",
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		Source:     "test",
		ActorID:    "human",
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append event through bundle: %v", err)
	}

	events, err := bundle.Events.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list events through bundle: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}
