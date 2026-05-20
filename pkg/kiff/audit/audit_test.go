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
