package lifecycle

import (
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
)

func rec(kind audit.Kind, at time.Time, data map[string]any) audit.Record {
	return audit.Record{
		ID: string(kind), Kind: kind, EntityID: "inv-1", EntityType: "Invoice",
		Data: data, CreatedAt: at,
	}
}

func TestAssemble_ProjectsSpineAndDerivesState(t *testing.T) {
	base := time.Now().UTC()
	records := []audit.Record{
		rec(audit.KindEventIngested, base, map[string]any{"event_type": "RECEIVED"}),
		rec(audit.KindStateChanged, base.Add(time.Second), map[string]any{"to": "RECEIVED"}),
		rec(audit.KindDecisionProposed, base.Add(2*time.Second), map[string]any{"proposed_action": "RELEASE"}),
		rec(audit.KindActionExecuted, base.Add(3*time.Second), map[string]any{"action": "RELEASE"}),
		rec(audit.KindStateChanged, base.Add(4*time.Second), map[string]any{"to": "PAID"}),
	}
	decisions := []decision.Decision{{ID: "dec-1", EntityID: "inv-1", ProposedAction: "RELEASE"}}
	approvals := []approval.Approval{}

	lc := Assemble("inv-1", records, decisions, approvals)

	if lc.EntityID != "inv-1" || lc.EntityType != "Invoice" {
		t.Fatalf("identity: %+v", lc)
	}
	if lc.CurrentState != "PAID" {
		t.Fatalf("expected derived current state PAID, got %q", lc.CurrentState)
	}
	if len(lc.Stages) != len(records) {
		t.Fatalf("expected %d stages, got %d", len(records), len(lc.Stages))
	}
	if len(lc.Decisions) != 1 {
		t.Fatalf("expected decisions attached")
	}
	if !lc.Executed() {
		t.Fatal("Executed should be true")
	}
	// The executed stage carries the action name from Data.
	last, _ := lc.LastStage()
	if last.Kind != audit.KindStateChanged {
		t.Fatalf("last stage should be the final state change, got %q", last.Kind)
	}
}

func TestAssemble_AwaitingApproval(t *testing.T) {
	base := time.Now().UTC()
	lc := Assemble("inv-1", []audit.Record{
		rec(audit.KindDecisionProposed, base, map[string]any{"proposed_action": "RELEASE"}),
		rec(audit.KindApprovalRequired, base.Add(time.Second), map[string]any{"action": "RELEASE"}),
	}, nil, nil)

	if !lc.AwaitingApproval() {
		t.Fatal("expected AwaitingApproval when a hold is unresolved")
	}
	if lc.Executed() {
		t.Fatal("should not be executed while held")
	}
}

func TestAssemble_ApprovalResolvedIsNotAwaiting(t *testing.T) {
	base := time.Now().UTC()
	lc := Assemble("inv-1", []audit.Record{
		rec(audit.KindApprovalRequired, base, map[string]any{"action": "RELEASE"}),
		rec(audit.KindApprovalGranted, base.Add(time.Second), map[string]any{"action": "RELEASE"}),
		rec(audit.KindActionExecuted, base.Add(2*time.Second), map[string]any{"action": "RELEASE"}),
	}, nil, nil)

	if lc.AwaitingApproval() {
		t.Fatal("a granted+executed hold is no longer awaiting")
	}
	if !lc.Has(audit.KindApprovalGranted) || !lc.Executed() {
		t.Fatal("expected granted + executed stages present")
	}
}
