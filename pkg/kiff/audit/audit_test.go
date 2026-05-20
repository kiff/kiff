package audit

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryAuditStoreAppendsAndListsRecords(t *testing.T) {
	store := NewInMemoryStore()
	record := Record{
		ID:         "audit-1",
		Kind:       KindEventIngested,
		EntityID:   "attempt-1",
		EntityType: "MissionAttempt",
		CreatedAt:  time.Now().UTC(),
	}

	if err := store.Append(context.Background(), record); err != nil {
		t.Fatalf("append audit record: %v", err)
	}
	records, err := store.List(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("list audit records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
}

func TestInMemoryAuditStoreQueriesByKindActorAndChronology(t *testing.T) {
	store := NewInMemoryStore()
	now := time.Now().UTC()
	records := []Record{
		{
			ID:         "audit-2",
			Kind:       KindActionExecuted,
			EntityID:   "attempt-1",
			EntityType: "MissionAttempt",
			ActorID:    "agent",
			CreatedAt:  now.Add(2 * time.Second),
		},
		{
			ID:         "audit-1",
			Kind:       KindEventIngested,
			EntityID:   "attempt-1",
			EntityType: "MissionAttempt",
			ActorID:    "human",
			CreatedAt:  now,
		},
		{
			ID:         "audit-3",
			Kind:       KindActionExecuted,
			EntityID:   "attempt-2",
			EntityType: "MissionAttempt",
			ActorID:    "agent",
			CreatedAt:  now.Add(time.Second),
		},
	}
	for _, record := range records {
		if err := store.Append(context.Background(), record); err != nil {
			t.Fatalf("append audit record: %v", err)
		}
	}

	byEntity, err := store.Query(context.Background(), Filter{EntityID: "attempt-1"})
	if err != nil {
		t.Fatalf("query by entity: %v", err)
	}
	if len(byEntity) != 2 {
		t.Fatalf("expected 2 entity records, got %d", len(byEntity))
	}
	if byEntity[0].ID != "audit-1" || byEntity[1].ID != "audit-2" {
		t.Fatalf("expected chronological entity records, got %#v", byEntity)
	}

	byKindAndActor, err := store.Query(context.Background(), Filter{Kind: KindActionExecuted, ActorID: "agent"})
	if err != nil {
		t.Fatalf("query by kind and actor: %v", err)
	}
	if len(byKindAndActor) != 2 {
		t.Fatalf("expected 2 action execution records, got %d", len(byKindAndActor))
	}
	if byKindAndActor[0].ID != "audit-3" || byKindAndActor[1].ID != "audit-2" {
		t.Fatalf("expected chronological filtered records, got %#v", byKindAndActor)
	}
}
