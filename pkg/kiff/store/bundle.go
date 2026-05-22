package store

import (
	"errors"

	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/audit"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/event"
)

// Bundle groups the core stores needed by a KIFF runtime.
type Bundle struct {
	Events    event.Store
	Decisions decision.Store
	Approvals approval.Store
	Audit     audit.Store
}

// NewInMemoryBundle creates a complete in-memory store bundle.
func NewInMemoryBundle() Bundle {
	return Bundle{
		Events:    event.NewInMemoryStore(),
		Decisions: decision.NewInMemoryStore(),
		Approvals: approval.NewInMemoryStore(),
		Audit:     audit.NewInMemoryStore(),
	}
}

// Validate checks that every core store has been configured.
func (b Bundle) Validate() error {
	if b.Events == nil {
		return errors.Join(ErrMisconfigured, errors.New("event store is required"))
	}
	if b.Decisions == nil {
		return errors.Join(ErrMisconfigured, errors.New("decision store is required"))
	}
	if b.Approvals == nil {
		return errors.Join(ErrMisconfigured, errors.New("approval store is required"))
	}
	if b.Audit == nil {
		return errors.Join(ErrMisconfigured, errors.New("audit store is required"))
	}
	return nil
}
