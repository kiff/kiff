// Package observability provides default-on logging and metrics for KIFF.
//
// The package follows the same compose-by-wrapping discipline as the rest of
// the framework: nothing here changes the runtime API. You wire observability
// by wrapping the audit store you would have wired anyway:
//
//	auditStore := observability.WrapAuditStore(audit.NewInMemoryStore(),
//	    observability.WithLogger(slog.Default()),
//	)
//	bundle := store.Bundle{Audit: auditStore}
//	rt, _ := domain.NewRuntimeWithStores(&bundle)
//
// Every audit record then produces a structured log line and increments a
// counter, in addition to being persisted by the wrapped store. Counters can
// be served at /metrics with NewMetricsHandler.
//
// The package depends only on the Go standard library.
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
)

// AuditWrapper wraps an audit.Store with structured logging and counters. It
// implements audit.Store so it can be dropped into a store.Bundle without
// touching the runtime.
type AuditWrapper struct {
	inner   audit.Store
	logger  *slog.Logger
	metrics *Metrics
}

// Option configures an AuditWrapper.
type Option func(*AuditWrapper)

// WithLogger sets the slog.Logger used to emit structured log lines. Pass
// nil to disable logging. The default is slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(w *AuditWrapper) { w.logger = l }
}

// WithMetrics replaces the metrics registry. Pass your own Metrics value when
// you want to share counters with another wrapper or expose them yourself.
// The default is a fresh Metrics value.
func WithMetrics(m *Metrics) Option {
	return func(w *AuditWrapper) { w.metrics = m }
}

// WrapAuditStore returns an AuditWrapper around inner. By default it logs to
// slog.Default() and tracks counters in a fresh Metrics value.
func WrapAuditStore(inner audit.Store, opts ...Option) *AuditWrapper {
	w := &AuditWrapper{
		inner:   inner,
		logger:  slog.Default(),
		metrics: NewMetrics(),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Metrics returns the metrics registry used by this wrapper. Useful when
// wiring NewMetricsHandler against the same wrapper.
func (w *AuditWrapper) MetricsRegistry() *Metrics { return w.metrics }

// Append delegates to the wrapped store, then records the fact for logging
// and metrics. If the wrapped store returns an error, observability still
// records the attempt under "audit_append_failed".
func (w *AuditWrapper) Append(ctx context.Context, r audit.Record) error {
	err := w.inner.Append(ctx, r)
	if err != nil {
		w.metrics.Inc("audit_append_failed")
		if w.logger != nil {
			w.logger.LogAttrs(ctx, slog.LevelError, "kiff.audit.append_failed",
				slog.String("kind", string(r.Kind)),
				slog.String("entity_id", r.EntityID),
				slog.String("entity_type", r.EntityType),
				slog.String("error", err.Error()),
			)
		}
		return err
	}

	w.metrics.Inc(string(r.Kind))
	if w.logger != nil {
		level := levelForKind(r.Kind)
		w.logger.LogAttrs(ctx, level, "kiff.audit",
			slog.String("kind", string(r.Kind)),
			slog.String("entity_id", r.EntityID),
			slog.String("entity_type", r.EntityType),
			slog.String("actor_id", r.ActorID),
			slog.String("message", r.Message),
			slog.String("trace_id", r.TraceID),
			slog.String("correlation_id", r.CorrelationID),
			slog.String("causation_id", r.CausationID),
		)
	}
	return nil
}

// List delegates to the wrapped store. Reads are not counted because they
// do not change the operational record.
func (w *AuditWrapper) List(ctx context.Context, entityID string) ([]audit.Record, error) {
	return w.inner.List(ctx, entityID)
}

// Query delegates to the wrapped store.
func (w *AuditWrapper) Query(ctx context.Context, f audit.Filter) ([]audit.Record, error) {
	return w.inner.Query(ctx, f)
}

// levelForKind decides whether an audit fact warrants a warning. Failures
// and denials are noisier than ordinary state transitions.
func levelForKind(k audit.Kind) slog.Level {
	switch k {
	case audit.KindActionFailed, audit.KindApprovalDenied:
		return slog.LevelWarn
	case audit.KindApprovalRequired:
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

// Metrics is a small in-process counter registry.
//
// It is intentionally minimal. KIFF does not vendor a metrics library; if you
// need labels, histograms, or Prometheus, plug your own registry in by
// wrapping AuditWrapper or calling Metrics.Snapshot from your /metrics
// handler.
type Metrics struct {
	mu       sync.RWMutex
	counters map[string]*atomic.Uint64
}

// NewMetrics returns an empty registry.
func NewMetrics() *Metrics {
	return &Metrics{counters: make(map[string]*atomic.Uint64)}
}

// Inc increments the counter for the given name, creating it if absent.
func (m *Metrics) Inc(name string) {
	m.mu.RLock()
	c, ok := m.counters[name]
	m.mu.RUnlock()
	if !ok {
		m.mu.Lock()
		c, ok = m.counters[name]
		if !ok {
			c = new(atomic.Uint64)
			m.counters[name] = c
		}
		m.mu.Unlock()
	}
	c.Add(1)
}

// Get returns the current value of a counter (zero if it does not exist).
func (m *Metrics) Get(name string) uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.counters[name]
	if !ok {
		return 0
	}
	return c.Load()
}

// Snapshot returns a sorted copy of all counters. Sorting keeps the /metrics
// output deterministic across requests.
func (m *Metrics) Snapshot() []Counter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Counter, 0, len(m.counters))
	for name, c := range m.counters {
		out = append(out, Counter{Name: name, Value: c.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Counter is one entry in a metrics snapshot.
type Counter struct {
	Name  string
	Value uint64
}

// NewMetricsHandler returns an http.Handler that serves a plain-text counter
// dump at /metrics. The format is one "kiff_<name> <value>" per line, which
// is intentionally close to Prometheus exposition without claiming
// compatibility.
func NewMetricsHandler(m *Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		for _, c := range m.Snapshot() {
			fmt.Fprintf(w, "kiff_%s %d\n", c.Name, c.Value)
		}
	})
}
