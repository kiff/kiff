package audit

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrInvalidAuditRecord = errors.New("invalid audit record")

// Kind describes the operational fact captured by an audit record.
type Kind string

const (
	KindEventIngested    Kind = "event_ingested"
	KindStateChanged     Kind = "state_changed"
	KindDecisionProposed Kind = "decision_proposed"
	KindActionValidated  Kind = "action_validated"
	KindApprovalRequired Kind = "approval_required"
	KindApprovalRecorded Kind = "approval_recorded"
	KindApprovalGranted  Kind = "approval_granted"
	KindApprovalDenied   Kind = "approval_denied"
	KindActionExecuted   Kind = "action_executed"
	KindActionFailed     Kind = "action_failed"
)

// AuditKind is kept as an explicit alias for readability.
type AuditKind = Kind

// Record is an append-only operational trace.
type Record struct {
	ID         string
	Kind       Kind
	EntityID   string
	EntityType string
	ActorID    string
	Message    string
	Data       map[string]any
	CreatedAt  time.Time
}

// AuditRecord is kept as an explicit alias for readability.
type AuditRecord = Record

// Validate checks the minimum fields needed for reconstruction.
func (r Record) Validate() error {
	if r.ID == "" {
		return errors.Join(ErrInvalidAuditRecord, errors.New("audit record id is required"))
	}
	if r.Kind == "" {
		return errors.Join(ErrInvalidAuditRecord, errors.New("audit record kind is required"))
	}
	if r.EntityID == "" {
		return errors.Join(ErrInvalidAuditRecord, errors.New("audit record entity id is required"))
	}
	if r.EntityType == "" {
		return errors.Join(ErrInvalidAuditRecord, errors.New("audit record entity type is required"))
	}
	if r.CreatedAt.IsZero() {
		return errors.Join(ErrInvalidAuditRecord, errors.New("audit record created at is required"))
	}
	return nil
}

// Store persists audit records.
type Store interface {
	Append(context.Context, Record) error
	List(context.Context, string) ([]Record, error)
}

// AuditStore is kept as an explicit alias for readability.
type AuditStore = Store

// InMemoryStore is a small append-only audit store.
type InMemoryStore struct {
	mu      sync.RWMutex
	records []Record
}

// InMemoryAuditStore is kept as an explicit alias for readability.
type InMemoryAuditStore = InMemoryStore

// NewInMemoryStore creates an empty in-memory audit store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{}
}

// NewInMemoryAuditStore creates an empty in-memory audit store.
func NewInMemoryAuditStore() *InMemoryStore {
	return NewInMemoryStore()
}

// Append validates and stores an audit record.
func (s *InMemoryStore) Append(ctx context.Context, r Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	return nil
}

// List returns audit records for an entity. An empty entity id returns all records.
func (s *InMemoryStore) List(ctx context.Context, entityID string) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		if entityID == "" || r.EntityID == entityID {
			records = append(records, r)
		}
	}
	return records, nil
}
