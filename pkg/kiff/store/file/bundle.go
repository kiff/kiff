package file

import (
	"errors"
	"path/filepath"

	"github.com/kiff/kiff/pkg/kiff/store"
)

// Bundle owns the four file-backed stores and lets a runtime use them through
// the existing store.Bundle injection point.
type Bundle struct {
	Events    *EventStore
	Decisions *DecisionStore
	Approvals *ApprovalStore
	Audit     *AuditStore
}

// NewBundle creates the four file-backed stores under dir. Files are named
// events.jsonl, decisions.jsonl, approvals.jsonl, and audit.jsonl.
func NewBundle(dir string) (*Bundle, error) {
	if dir == "" {
		return nil, ErrInvalidPath
	}
	events, err := NewEventStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	decisions, err := NewDecisionStore(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		_ = events.Close()
		return nil, err
	}
	approvals, err := NewApprovalStore(filepath.Join(dir, "approvals.jsonl"))
	if err != nil {
		_ = events.Close()
		_ = decisions.Close()
		return nil, err
	}
	auditStore, err := NewAuditStore(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		_ = events.Close()
		_ = decisions.Close()
		_ = approvals.Close()
		return nil, err
	}
	return &Bundle{
		Events:    events,
		Decisions: decisions,
		Approvals: approvals,
		Audit:     auditStore,
	}, nil
}

// AsStoreBundle adapts the file bundle to the package-level store.Bundle
// expected by runtime.Config.Stores.
func (b *Bundle) AsStoreBundle() store.Bundle {
	return store.Bundle{
		Events:    b.Events,
		Decisions: b.Decisions,
		Approvals: b.Approvals,
		Audit:     b.Audit,
	}
}

// Close closes every underlying file. It returns the first error encountered.
func (b *Bundle) Close() error {
	var firstErr error
	for _, c := range []func() error{
		b.Events.Close, b.Decisions.Close, b.Approvals.Close, b.Audit.Close,
	} {
		if err := c(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Errors that callers may want to match. Currently just the local one.
var _ = errors.New
