package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
)

func TestAuditWrapper_LogsAndCounts(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	inner := audit.NewInMemoryStore()
	w := WrapAuditStore(inner, WithLogger(logger))

	r := audit.Record{
		ID:         "rec-1",
		Kind:       audit.KindActionExecuted,
		EntityID:   "order-1",
		EntityType: "Order",
		ActorID:    "agent",
		Message:    "executed",
		TraceID:    "trace-1",
		CreatedAt:  time.Now().UTC(),
	}
	if err := w.Append(context.Background(), r); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if got := w.MetricsRegistry().Get(string(audit.KindActionExecuted)); got != 1 {
		t.Fatalf("expected counter 1 for action_executed, got %d", got)
	}
	out := buf.String()
	if !strings.Contains(out, "kiff.audit") || !strings.Contains(out, "trace_id=trace-1") {
		t.Fatalf("expected log line with trace, got %q", out)
	}

	stored, err := inner.List(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected record persisted, got %d", len(stored))
	}
}

func TestAuditWrapper_FailureCountsSeparately(t *testing.T) {
	t.Parallel()
	w := WrapAuditStore(brokenStore{}, WithLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))))
	err := w.Append(context.Background(), audit.Record{
		ID:         "rec-x",
		Kind:       audit.KindActionExecuted,
		EntityID:   "order-1",
		EntityType: "Order",
		CreatedAt:  time.Now().UTC(),
	})
	if !errors.Is(err, errBroken) {
		t.Fatalf("expected errBroken, got %v", err)
	}
	if got := w.MetricsRegistry().Get("audit_append_failed"); got != 1 {
		t.Fatalf("expected audit_append_failed counter, got %d", got)
	}
	if got := w.MetricsRegistry().Get(string(audit.KindActionExecuted)); got != 0 {
		t.Fatalf("expected zero kind counter on failure, got %d", got)
	}
}

func TestMetricsHandler(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.Inc("event_ingested")
	m.Inc("event_ingested")
	m.Inc("approval_denied")

	h := NewMetricsHandler(m)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	wantLines := []string{
		"kiff_approval_denied 1",
		"kiff_event_ingested 2",
	}
	for _, line := range wantLines {
		if !strings.Contains(body, line) {
			t.Fatalf("expected %q in body:\n%s", line, body)
		}
	}
	// Sorted: approval_denied < event_ingested.
	if strings.Index(body, "kiff_approval_denied") > strings.Index(body, "kiff_event_ingested") {
		t.Fatalf("expected sorted output, got:\n%s", body)
	}
}

func TestMetrics_ConcurrentInc(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Inc("k")
		}()
	}
	wg.Wait()
	if got := m.Get("k"); got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
}

var errBroken = errors.New("broken")

type brokenStore struct{}

func (brokenStore) Append(context.Context, audit.Record) error            { return errBroken }
func (brokenStore) List(context.Context, string) ([]audit.Record, error)  { return nil, errBroken }
func (brokenStore) Query(context.Context, audit.Filter) ([]audit.Record, error) {
	return nil, errBroken
}
