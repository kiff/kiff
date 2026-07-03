package idempotency

import (
	"context"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
)

func TestInMemoryStore_ReserveThenComplete(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	key := Key{Value: "k1", EntityID: "e1", ActionName: "PAY"}

	// First Begin reserves.
	b, err := s.Begin(ctx, key)
	if err != nil || b.Status != Reserved {
		t.Fatalf("first begin: status=%v err=%v", b.Status, err)
	}
	// A concurrent Begin while reserved reports in-progress.
	if b2, _ := s.Begin(ctx, key); b2.Status != InProgress {
		t.Fatalf("expected InProgress, got %v", b2.Status)
	}
	// Complete stores the result.
	want := action.ActionResult{ActionName: "PAY", EntityID: "e1", Status: action.ExecutionSucceeded}
	if err := s.Complete(ctx, key, want); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// Begin now returns the completed result.
	b3, _ := s.Begin(ctx, key)
	if b3.Status != Completed || b3.Result.ActionName != "PAY" {
		t.Fatalf("expected Completed with result, got %+v", b3)
	}
	// Lookup returns the stored result.
	got, ok, err := s.Lookup(ctx, key)
	if err != nil || !ok || got.EntityID != "e1" {
		t.Fatalf("lookup: got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestInMemoryStore_LookupMissAndReservedNotVisible(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	key := Key{Value: "k1", EntityID: "e1", ActionName: "PAY"}

	if _, ok, _ := s.Lookup(ctx, key); ok {
		t.Fatal("lookup should miss on empty store")
	}
	// A reservation is not a completed result.
	_, _ = s.Begin(ctx, key)
	if _, ok, _ := s.Lookup(ctx, key); ok {
		t.Fatal("lookup should miss while only reserved (not completed)")
	}
}

func TestInMemoryStore_ReleaseAllowsRetry(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	key := Key{Value: "k1", EntityID: "e1", ActionName: "PAY"}

	_, _ = s.Begin(ctx, key)
	if err := s.Release(ctx, key); err != nil {
		t.Fatalf("release: %v", err)
	}
	// After release the key is free to reserve again.
	if b, _ := s.Begin(ctx, key); b.Status != Reserved {
		t.Fatalf("expected Reserved after release, got %v", b.Status)
	}
}

func TestInMemoryStore_ReleaseIsNoopAfterComplete(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	key := Key{Value: "k1", EntityID: "e1", ActionName: "PAY"}

	_, _ = s.Begin(ctx, key)
	_ = s.Complete(ctx, key, action.ActionResult{Status: action.ExecutionSucceeded})
	if err := s.Release(ctx, key); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok, _ := s.Lookup(ctx, key); !ok {
		t.Fatal("release must not drop a completed record")
	}
}

func TestInMemoryStore_KeyScopedByEntityAndAction(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	base := Key{Value: "same", EntityID: "e1", ActionName: "PAY"}
	_ = s.Complete(ctx, base, action.ActionResult{Status: action.ExecutionSucceeded})

	// Same value, different entity or action → distinct key, no hit.
	if _, ok, _ := s.Lookup(ctx, Key{Value: "same", EntityID: "e2", ActionName: "PAY"}); ok {
		t.Fatal("different entity must be a distinct key")
	}
	if _, ok, _ := s.Lookup(ctx, Key{Value: "same", EntityID: "e1", ActionName: "REFUND"}); ok {
		t.Fatal("different action must be a distinct key")
	}
}
