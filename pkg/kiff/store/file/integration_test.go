package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store/file"
)

// TestFileBundleSurvivesProcessRestart drives a runtime backed by file stores,
// "restarts" by closing the bundle and reopening, and rebuilds state from the
// persisted event log.
func TestFileBundleSurvivesProcessRestart(t *testing.T) {
	dir := t.TempDir()

	// First run: ingest two events through a runtime backed by file stores.
	first, err := file.NewBundle(dir)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}
	bundle := first.AsStoreBundle()
	rt1, err := runtime.New(runtime.Config{
		Stores: &bundle,
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "X", From: "", To: "A"},
			state.Transition{EventType: "Y", From: "A", To: "B"},
		),
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	now := time.Now().UTC()
	for _, ev := range []event.Event{
		{ID: "evt-1", Type: "X", EntityID: "e1", EntityType: "T",
			Source: "t", ActorID: "h", OccurredAt: now,
			Metadata: event.Metadata{TraceID: "trace-1"}},
		{ID: "evt-2", Type: "Y", EntityID: "e1", EntityType: "T",
			Source: "t", ActorID: "h", OccurredAt: now.Add(time.Second),
			Metadata: event.Metadata{TraceID: "trace-1"}},
	} {
		if err := rt1.IngestEvent(context.Background(), ev); err != nil {
			t.Fatalf("ingest %s: %v", ev.ID, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close bundle: %v", err)
	}

	// Verify files exist
	for _, name := range []string{"events.jsonl", "audit.jsonl"} {
		if _, err := readFile(t, filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s on disk: %v", name, err)
		}
	}

	// Second run: open the same dir, rebuild state from events.
	second, err := file.NewBundle(dir)
	if err != nil {
		t.Fatalf("reopen bundle: %v", err)
	}
	defer second.Close()
	bundle2 := second.AsStoreBundle()
	rt2, err := runtime.New(runtime.Config{
		Stores: &bundle2,
		StateMachine: state.NewTransitionMachine(
			state.Transition{EventType: "X", From: "", To: "A"},
			state.Transition{EventType: "Y", From: "A", To: "B"},
		),
	})
	if err != nil {
		t.Fatalf("new runtime after restart: %v", err)
	}

	result, err := rt2.RebuildState(context.Background(), "e1")
	if err != nil {
		t.Fatalf("rebuild state: %v", err)
	}
	if result.State.Value != "B" {
		t.Fatalf("expected rebuilt state B, got %q", result.State.Value)
	}

	// Audit trail from the first run is still queryable, and new audit
	// records (from RebuildState) are appended to the same file.
	records, _ := rt2.Audit.Query(context.Background(), audit.Filter{EntityID: "e1"})
	if len(records) < 4 {
		t.Fatalf("expected persisted audit to include first-run records, got %d", len(records))
	}
	traceRecords, _ := rt2.Audit.Query(context.Background(), audit.Filter{TraceID: "trace-1"})
	if len(traceRecords) == 0 {
		t.Fatal("expected trace metadata to survive disk round-trip")
	}
}

func readFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(path)
}
