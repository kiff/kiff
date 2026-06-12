// Package storetest provides a shared conformance test suite for KIFF store
// implementations.
//
// Every backend (in-memory, file, postgres, sqlite, dynamodb, ...) should pass
// the same suite. The point is to catch differences between implementations
// before they show up in production: ordering, validation, idempotency,
// filtering, and consistency.
//
// Usage from a backend's test package:
//
//	func TestEventStore(t *testing.T) {
//	    storetest.RunEventStore(t, func(t *testing.T) (event.Store, func()) {
//	        s := newPostgresEventStoreForTest(t)
//	        return s, func() { s.Close() }
//	    })
//	}
//
// Each backend supplies a factory that returns a fresh, empty store and a
// cleanup function. The suite drives the rest.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
	"github.com/kiff/kiff/pkg/kiff/event"
)

// Factory returns a fresh empty store of type T plus a cleanup func.
type Factory[T any] func(t *testing.T) (T, func())

// RunEventStore runs the conformance suite for an event.Store implementation.
func RunEventStore(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	t.Run("AppendList", func(t *testing.T) { eventAppendList(t, factory) })
	t.Run("ListByEntity", func(t *testing.T) { eventListByEntity(t, factory) })
	t.Run("ListAllWhenEntityEmpty", func(t *testing.T) { eventListAll(t, factory) })
	t.Run("OrderingPreserved", func(t *testing.T) { eventOrdering(t, factory) })
	t.Run("ValidationRejectsInvalid", func(t *testing.T) { eventValidation(t, factory) })
	t.Run("PayloadAndMetadataRoundTrip", func(t *testing.T) { eventRoundTrip(t, factory) })
	t.Run("ContextCancellation", func(t *testing.T) { eventContextCancel(t, factory) })
}

// RunDecisionStore runs the conformance suite for a decision.Store.
func RunDecisionStore(t *testing.T, factory Factory[decision.Store]) {
	t.Helper()
	t.Run("AppendList", func(t *testing.T) { decisionAppendList(t, factory) })
	t.Run("ListByEntity", func(t *testing.T) { decisionListByEntity(t, factory) })
	t.Run("ValidationRejectsInvalid", func(t *testing.T) { decisionValidation(t, factory) })
	t.Run("EvidenceRoundTrip", func(t *testing.T) { decisionEvidenceRoundTrip(t, factory) })
}

// RunApprovalStore runs the conformance suite for an approval.Store.
func RunApprovalStore(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	t.Run("SaveGet", func(t *testing.T) { approvalSaveGet(t, factory) })
	t.Run("UpsertPreservesOrder", func(t *testing.T) { approvalUpsert(t, factory) })
	t.Run("ListByEntity", func(t *testing.T) { approvalListByEntity(t, factory) })
	t.Run("IsGranted", func(t *testing.T) { approvalIsGranted(t, factory) })
	t.Run("ValidationRejectsInvalid", func(t *testing.T) { approvalValidation(t, factory) })
	t.Run("GetMissing", func(t *testing.T) { approvalGetMissing(t, factory) })
}

// RunAuditStore runs the conformance suite for an audit.Store.
func RunAuditStore(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	t.Run("AppendList", func(t *testing.T) { auditAppendList(t, factory) })
	t.Run("FilterByEntity", func(t *testing.T) { auditFilterEntity(t, factory) })
	t.Run("FilterByKind", func(t *testing.T) { auditFilterKind(t, factory) })
	t.Run("FilterByActor", func(t *testing.T) { auditFilterActor(t, factory) })
	t.Run("FilterByTrace", func(t *testing.T) { auditFilterTrace(t, factory) })
	t.Run("ChronologicalOrdering", func(t *testing.T) { auditOrdering(t, factory) })
	t.Run("DataRoundTrip", func(t *testing.T) { auditDataRoundTrip(t, factory) })
}

// ── event.Store cases ──────────────────────────────────────────────────────

func eventAppendList(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()

	ev := newEvent("evt-1", "ORDER_PLACED", "order-1")
	if err := store.Append(ctx, ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := store.List(ctx, "order-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "evt-1" {
		t.Fatalf("expected one event with id evt-1, got %+v", got)
	}
}

func eventListByEntity(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()

	mustAppendEvent(t, store, newEvent("evt-1", "T", "order-1"))
	mustAppendEvent(t, store, newEvent("evt-2", "T", "order-2"))
	mustAppendEvent(t, store, newEvent("evt-3", "T", "order-1"))

	got, err := store.List(ctx, "order-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events for order-1, got %d", len(got))
	}
	for _, e := range got {
		if e.EntityID != "order-1" {
			t.Fatalf("expected entity_id order-1, got %q", e.EntityID)
		}
	}
}

func eventListAll(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()

	mustAppendEvent(t, store, newEvent("evt-1", "T", "order-1"))
	mustAppendEvent(t, store, newEvent("evt-2", "T", "order-2"))

	got, err := store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events when entity_id empty, got %d", len(got))
	}
}

func eventOrdering(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		ev := newEvent(idAt(i), "T", "order-1")
		ev.OccurredAt = baseTime.Add(time.Duration(i) * time.Second)
		mustAppendEvent(t, store, ev)
	}
	got, err := store.List(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 events, got %d", len(got))
	}
	for i := 0; i < 5; i++ {
		if got[i].ID != idAt(i) {
			t.Fatalf("expected ordering preserved at i=%d, got %q (full: %v)", i, got[i].ID, ids(got))
		}
	}
}

func eventValidation(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()

	bad := newEvent("", "T", "order-1") // missing id
	err := store.Append(context.Background(), bad)
	if !errors.Is(err, event.ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func eventRoundTrip(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()

	original := newEvent("evt-rt", "ORDER_REFUNDED", "order-9")
	original.Metadata = event.Metadata{
		TraceID:       "trace-1",
		CorrelationID: "corr-1",
		CausationID:   "cause-1",
		Tags:          map[string]string{"region": "us-east-1", "tier": "gold"},
	}
	original.Payload = map[string]any{
		"amount":  float64(99.5),
		"reason":  "customer request",
		"refunds": float64(2),
	}

	if err := store.Append(ctx, original); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := store.List(ctx, "order-9")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	roundTrip := got[0]
	if roundTrip.Metadata.TraceID != "trace-1" || roundTrip.Metadata.CorrelationID != "corr-1" || roundTrip.Metadata.CausationID != "cause-1" {
		t.Fatalf("metadata fields not preserved: %+v", roundTrip.Metadata)
	}
	if roundTrip.Metadata.Tags["region"] != "us-east-1" {
		t.Fatalf("tag round-trip lost region: %+v", roundTrip.Metadata.Tags)
	}
	if amount, ok := roundTrip.Payload["amount"].(float64); !ok || amount != 99.5 {
		t.Fatalf("payload round-trip lost amount: %+v", roundTrip.Payload)
	}
}

func eventContextCancel(t *testing.T, factory Factory[event.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := store.Append(ctx, newEvent("evt-cx", "T", "order-1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ── decision.Store cases ────────────────────────────────────────────────────

func decisionAppendList(t *testing.T, factory Factory[decision.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendDecision(t, store, newDecision("dec-1", "order-1"))
	got, err := store.List(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "dec-1" {
		t.Fatalf("expected dec-1, got %+v", got)
	}
}

func decisionListByEntity(t *testing.T, factory Factory[decision.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendDecision(t, store, newDecision("d1", "e1"))
	mustAppendDecision(t, store, newDecision("d2", "e2"))
	mustAppendDecision(t, store, newDecision("d3", "e1"))
	got, err := store.List(context.Background(), "e1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 decisions for e1, got %d", len(got))
	}
}

func decisionValidation(t *testing.T, factory Factory[decision.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	bad := newDecision("", "e1")
	err := store.Append(context.Background(), bad)
	if !errors.Is(err, decision.ErrInvalidDecision) {
		t.Fatalf("expected ErrInvalidDecision, got %v", err)
	}
}

func decisionEvidenceRoundTrip(t *testing.T, factory Factory[decision.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()

	d := newDecision("d-ev", "e1")
	d.Evidence = decisionEvidenceList{
		{ID: "evref-1", Kind: "event", Source: "events", Summary: "order paid"},
		{ID: "evref-2", Kind: "document", Source: "docs", Summary: "receipt"},
	}.toEvidenceRefs()
	d.Confidence = 0.71
	d.ReasoningSummary = "the agent thinks the customer is unhappy"

	mustAppendDecision(t, store, d)
	got, err := store.List(context.Background(), "e1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	rt := got[0]
	if len(rt.Evidence) != 2 || rt.Evidence[0].ID != "evref-1" {
		t.Fatalf("evidence not round-tripped: %+v", rt.Evidence)
	}
	if rt.Confidence != 0.71 {
		t.Fatalf("confidence not preserved: %v", rt.Confidence)
	}
	if rt.ReasoningSummary != "the agent thinks the customer is unhappy" {
		t.Fatalf("reasoning summary not preserved: %q", rt.ReasoningSummary)
	}
}

// ── approval.Store cases ────────────────────────────────────────────────────

func approvalSaveGet(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()

	a := newApproval("ap-1", "order-1", approval.StatusPending)
	if err := store.Save(ctx, a); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Get(ctx, "ap-1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.ID != "ap-1" || got.Status != approval.StatusPending {
		t.Fatalf("expected pending ap-1, got %+v", got)
	}
}

func approvalUpsert(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()

	a := newApproval("ap-1", "order-1", approval.StatusPending)
	if err := store.Save(ctx, a); err != nil {
		t.Fatalf("Save initial: %v", err)
	}
	a.Status = approval.StatusGranted
	a.ReviewedBy = "human-1"
	a.ReviewedAt = baseTime.Add(time.Hour)
	if err := store.Save(ctx, a); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, ok, _ := store.Get(ctx, "ap-1")
	if !ok || got.Status != approval.StatusGranted || got.ReviewedBy != "human-1" {
		t.Fatalf("upsert did not persist new fields: %+v", got)
	}

	// ListByEntity should still contain only one record.
	all, err := store.List(ctx, "order-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 approval after upsert, got %d", len(all))
	}
}

func approvalListByEntity(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()
	mustSaveApproval(t, store, newApproval("ap-1", "e1", approval.StatusPending))
	mustSaveApproval(t, store, newApproval("ap-2", "e2", approval.StatusPending))
	mustSaveApproval(t, store, newApproval("ap-3", "e1", approval.StatusPending))
	got, err := store.List(ctx, "e1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 for e1, got %d", len(got))
	}
	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 total, got %d", len(all))
	}
}

func approvalIsGranted(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	ctx := context.Background()

	a := newApproval("ap-1", "order-1", approval.StatusGranted)
	a.ActionName = "REFUND_ORDER"
	a.ReviewedBy = "human-1"
	a.ReviewedAt = baseTime.Add(time.Hour)
	mustSaveApproval(t, store, a)

	ok, err := store.IsGranted(ctx, "ap-1", "order-1", "REFUND_ORDER")
	if err != nil || !ok {
		t.Fatalf("expected granted, ok=%v err=%v", ok, err)
	}
	ok, _ = store.IsGranted(ctx, "ap-1", "order-1", "OTHER")
	if ok {
		t.Fatal("expected non-granted for mismatched action")
	}
	ok, _ = store.IsGranted(ctx, "ap-1", "order-99", "REFUND_ORDER")
	if ok {
		t.Fatal("expected non-granted for mismatched entity")
	}
	ok, _ = store.IsGranted(ctx, "missing", "order-1", "REFUND_ORDER")
	if ok {
		t.Fatal("expected non-granted for missing approval")
	}
}

func approvalValidation(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	bad := newApproval("", "e1", approval.StatusPending)
	err := store.Save(context.Background(), bad)
	if !errors.Is(err, approval.ErrInvalidApproval) {
		t.Fatalf("expected ErrInvalidApproval, got %v", err)
	}
}

func approvalGetMissing(t *testing.T, factory Factory[approval.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	_, ok, err := store.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Get missing returned error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for missing approval")
	}
}

// ── audit.Store cases ───────────────────────────────────────────────────────

func auditAppendList(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendAudit(t, store, newAudit("rec-1", audit.KindEventIngested, "e1", "actor-1", "trace-1"))
	got, err := store.List(context.Background(), "e1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "rec-1" {
		t.Fatalf("expected rec-1, got %+v", got)
	}
}

func auditFilterEntity(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendAudit(t, store, newAudit("r1", audit.KindEventIngested, "e1", "a1", "t1"))
	mustAppendAudit(t, store, newAudit("r2", audit.KindEventIngested, "e2", "a1", "t1"))
	got, err := store.Query(context.Background(), audit.Filter{EntityID: "e1"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].EntityID != "e1" {
		t.Fatalf("expected only e1, got %+v", got)
	}
}

func auditFilterKind(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendAudit(t, store, newAudit("r1", audit.KindEventIngested, "e1", "a1", "t1"))
	mustAppendAudit(t, store, newAudit("r2", audit.KindActionExecuted, "e1", "a1", "t1"))
	mustAppendAudit(t, store, newAudit("r3", audit.KindActionExecuted, "e1", "a2", "t1"))
	got, err := store.Query(context.Background(), audit.Filter{Kind: audit.KindActionExecuted})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 action_executed records, got %d", len(got))
	}
}

func auditFilterActor(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendAudit(t, store, newAudit("r1", audit.KindEventIngested, "e1", "agent", "t1"))
	mustAppendAudit(t, store, newAudit("r2", audit.KindEventIngested, "e1", "human", "t1"))
	got, err := store.Query(context.Background(), audit.Filter{ActorID: "agent"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].ActorID != "agent" {
		t.Fatalf("expected one agent record, got %+v", got)
	}
}

func auditFilterTrace(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	mustAppendAudit(t, store, newAudit("r1", audit.KindEventIngested, "e1", "a1", "trace-A"))
	mustAppendAudit(t, store, newAudit("r2", audit.KindActionExecuted, "e2", "a1", "trace-A"))
	mustAppendAudit(t, store, newAudit("r3", audit.KindActionExecuted, "e3", "a1", "trace-B"))

	got, err := store.Query(context.Background(), audit.Filter{TraceID: "trace-A"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records for trace-A, got %d", len(got))
	}
}

func auditOrdering(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()

	// Insert out of timestamp order. Query must still return chronological.
	r3 := newAudit("r3", audit.KindActionExecuted, "e1", "a1", "t1")
	r3.CreatedAt = baseTime.Add(3 * time.Second)
	r1 := newAudit("r1", audit.KindEventIngested, "e1", "a1", "t1")
	r1.CreatedAt = baseTime.Add(1 * time.Second)
	r2 := newAudit("r2", audit.KindStateChanged, "e1", "a1", "t1")
	r2.CreatedAt = baseTime.Add(2 * time.Second)

	mustAppendAudit(t, store, r3)
	mustAppendAudit(t, store, r1)
	mustAppendAudit(t, store, r2)

	got, err := store.List(context.Background(), "e1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if got[0].ID != "r1" || got[1].ID != "r2" || got[2].ID != "r3" {
		t.Fatalf("expected chronological order, got %v", auditIDs(got))
	}
}

func auditDataRoundTrip(t *testing.T, factory Factory[audit.Store]) {
	t.Helper()
	store, cleanup := factory(t)
	defer cleanup()
	r := newAudit("r-data", audit.KindActionExecuted, "e1", "a1", "trace-1")
	r.Data = map[string]any{
		"action": "REFUND_ORDER",
		"amount": float64(49.5),
		"nested": map[string]any{"reason": "customer request"},
	}
	mustAppendAudit(t, store, r)
	got, err := store.List(context.Background(), "e1")
	if err != nil || len(got) != 1 {
		t.Fatalf("expected single record, got %d err=%v", len(got), err)
	}
	if got[0].Data["action"] != "REFUND_ORDER" {
		t.Fatalf("expected action key, got %+v", got[0].Data)
	}
	if amount, ok := got[0].Data["amount"].(float64); !ok || amount != 49.5 {
		t.Fatalf("expected amount round-trip, got %+v", got[0].Data["amount"])
	}
}
