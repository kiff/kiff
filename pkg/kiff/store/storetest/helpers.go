package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/decision"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/evidence"
)

// baseTime is the anchor every test uses. Using a fixed instant keeps test
// output deterministic across runs and across implementations.
var baseTime = time.Date(2026, time.May, 21, 12, 0, 0, 0, time.UTC)

// idAt returns a stable id for the i-th synthetic record in a test.
func idAt(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return "evt-" + string(digits[i])
	}
	return "evt-" + string(digits[i/10]) + string(digits[i%10])
}

// ids returns the IDs of a slice of events for diagnostic output.
func ids(evs []event.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}

// auditIDs returns the IDs of a slice of audit records for diagnostic output.
func auditIDs(rs []audit.Record) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// newEvent constructs a minimal valid event.Event. Tests override fields as
// needed.
func newEvent(id, eventType, entityID string) event.Event {
	return event.Event{
		ID:         id,
		Type:       eventType,
		EntityID:   entityID,
		EntityType: "Entity",
		Source:     "storetest",
		ActorID:    "actor-1",
		OccurredAt: baseTime,
	}
}

// newDecision constructs a minimal valid decision.Decision.
func newDecision(id, entityID string) decision.Decision {
	return decision.Decision{
		ID:         id,
		EntityID:   entityID,
		EntityType: "Entity",
		Kind:       decision.KindActionProposal,
		ActorID:    "actor-1",
		CreatedAt:  baseTime,
	}
}

// newApproval constructs a minimal valid approval.Approval.
func newApproval(id, entityID string, status approval.Status) approval.Approval {
	a := approval.Approval{
		ID:          id,
		EntityID:    entityID,
		EntityType:  "Entity",
		ActionName:  "ACTION",
		RequestedBy: "actor-1",
		Status:      status,
		CreatedAt:   baseTime,
	}
	if status == approval.StatusGranted || status == approval.StatusDenied {
		a.ReviewedBy = "human-1"
		a.ReviewedAt = baseTime.Add(time.Hour)
	}
	return a
}

// newAudit constructs a minimal valid audit.Record.
func newAudit(id string, kind audit.Kind, entityID, actorID, traceID string) audit.Record {
	return audit.Record{
		ID:         id,
		Kind:       kind,
		EntityID:   entityID,
		EntityType: "Entity",
		ActorID:    actorID,
		Message:    "test record",
		TraceID:    traceID,
		CreatedAt:  baseTime,
	}
}

// mustAppendEvent appends an event and fails the test if Append returns an
// error. Used to keep the case bodies focused on the assertion.
func mustAppendEvent(t *testing.T, store event.Store, ev event.Event) {
	t.Helper()
	if err := store.Append(context.Background(), ev); err != nil {
		t.Fatalf("Append %s: %v", ev.ID, err)
	}
}

func mustAppendDecision(t *testing.T, store decision.Store, d decision.Decision) {
	t.Helper()
	if err := store.Append(context.Background(), d); err != nil {
		t.Fatalf("Append %s: %v", d.ID, err)
	}
}

func mustSaveApproval(t *testing.T, store approval.Store, a approval.Approval) {
	t.Helper()
	if err := store.Save(context.Background(), a); err != nil {
		t.Fatalf("Save %s: %v", a.ID, err)
	}
}

func mustAppendAudit(t *testing.T, store audit.Store, r audit.Record) {
	t.Helper()
	if err := store.Append(context.Background(), r); err != nil {
		t.Fatalf("Append %s: %v", r.ID, err)
	}
}

// decisionEvidence is a small helper for constructing evidence references in
// the conformance suite.
type decisionEvidence struct {
	ID      string
	Kind    string
	Source  string
	Summary string
}

type decisionEvidenceList []decisionEvidence

func (l decisionEvidenceList) toEvidenceRefs() []evidence.Ref {
	out := make([]evidence.Ref, len(l))
	for i, e := range l {
		out[i] = evidence.Ref{
			ID:        e.ID,
			Kind:      evidence.Kind(e.Kind),
			Source:    e.Source,
			Summary:   e.Summary,
			CreatedAt: baseTime,
		}
	}
	return out
}
