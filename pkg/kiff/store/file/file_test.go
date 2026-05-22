package file

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/audit"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/event"
)

func TestFileEventStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	first, err := NewEventStore(path)
	if err != nil {
		t.Fatalf("new event store: %v", err)
	}
	ev := event.Event{
		ID: "evt-1", Type: "X", EntityID: "e1", EntityType: "T",
		Source: "test", ActorID: "a", OccurredAt: time.Now().UTC(),
	}
	if err := first.Append(context.Background(), ev); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen and read
	second, err := NewEventStore(path)
	if err != nil {
		t.Fatalf("reopen event store: %v", err)
	}
	defer second.Close()
	events, err := second.List(context.Background(), "e1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 || events[0].ID != "evt-1" {
		t.Fatalf("expected evt-1 to survive reopen, got %v", events)
	}
}

func TestFileApprovalStoreReturnsLatestSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.jsonl")
	s, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	pending := approval.Approval{
		ID: "appr-1", EntityID: "e1", EntityType: "T", ActionName: "A",
		RequestedBy: "agent", Status: approval.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Save(ctx, pending); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	granted := pending
	granted.Status = approval.StatusGranted
	granted.ReviewedBy = "human"
	granted.ReviewedAt = time.Now().UTC()
	if err := s.Save(ctx, granted); err != nil {
		t.Fatalf("save granted: %v", err)
	}

	got, ok, err := s.Get(ctx, "appr-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Status != approval.StatusGranted {
		t.Fatalf("expected granted snapshot, got %q", got.Status)
	}
	isGranted, _ := s.IsGranted(ctx, "appr-1", "e1", "A")
	if !isGranted {
		t.Fatal("expected IsGranted=true")
	}
}

func TestFileAuditStoreFiltersAndOrders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	s, err := NewAuditStore(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	records := []audit.Record{
		{ID: "a-2", Kind: audit.KindActionExecuted, EntityID: "e1", EntityType: "T", ActorID: "agent", CreatedAt: now.Add(2 * time.Second), TraceID: "tr-1"},
		{ID: "a-1", Kind: audit.KindEventIngested, EntityID: "e1", EntityType: "T", ActorID: "human", CreatedAt: now, TraceID: "tr-1"},
		{ID: "a-3", Kind: audit.KindEventIngested, EntityID: "e2", EntityType: "T", ActorID: "human", CreatedAt: now.Add(time.Second), TraceID: "tr-2"},
	}
	for _, r := range records {
		if err := s.Append(ctx, r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	byEntity, _ := s.Query(ctx, audit.Filter{EntityID: "e1"})
	if len(byEntity) != 2 || byEntity[0].ID != "a-1" || byEntity[1].ID != "a-2" {
		t.Fatalf("expected chronological e1 records, got %#v", byEntity)
	}
	byTrace, _ := s.Query(ctx, audit.Filter{TraceID: "tr-2"})
	if len(byTrace) != 1 || byTrace[0].ID != "a-3" {
		t.Fatalf("expected only a-3 for trace tr-2, got %#v", byTrace)
	}
}

func TestFileDecisionStorePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	first, err := NewDecisionStore(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	d := decision.Decision{
		ID: "d-1", EntityID: "e1", EntityType: "T",
		Kind: decision.KindActionProposal, ActorID: "agent",
		CreatedAt: time.Now().UTC(),
	}
	if err := first.Append(context.Background(), d); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = first.Close()

	second, err := NewDecisionStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	got, _ := second.List(context.Background(), "e1")
	if len(got) != 1 || got[0].ID != "d-1" {
		t.Fatalf("expected d-1 to survive reopen, got %#v", got)
	}
}
