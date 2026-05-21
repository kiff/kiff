package approval

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidApproval  = errors.New("invalid approval")
	ErrApprovalNotFound = errors.New("approval not found")
)

// Status describes the review state of an approval.
type Status string

const (
	StatusPending Status = "pending"
	StatusGranted Status = "granted"
	StatusDenied  Status = "denied"
)

// Approval records human authority over an action.
type Approval struct {
	ID          string    `json:"id"`
	EntityID    string    `json:"entity_id"`
	EntityType  string    `json:"entity_type"`
	ActionName  string    `json:"action_name"`
	RequestedBy string    `json:"requested_by"`
	ReviewedBy  string    `json:"reviewed_by,omitempty"`
	Status      Status    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ReviewedAt  time.Time `json:"reviewed_at,omitempty"`
}

// Validate checks the minimum fields needed for an auditable approval.
func (a Approval) Validate() error {
	if a.ID == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval id is required"))
	}
	if a.EntityID == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval entity id is required"))
	}
	if a.EntityType == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval entity type is required"))
	}
	if a.ActionName == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval action name is required"))
	}
	if a.RequestedBy == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval requested by is required"))
	}
	if a.Status == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval status is required"))
	}
	if a.Status != StatusPending && a.Status != StatusGranted && a.Status != StatusDenied {
		return errors.Join(ErrInvalidApproval, errors.New("approval status is unknown"))
	}
	if a.CreatedAt.IsZero() {
		return errors.Join(ErrInvalidApproval, errors.New("approval created at is required"))
	}
	if (a.Status == StatusGranted || a.Status == StatusDenied) && a.ReviewedBy == "" {
		return errors.Join(ErrInvalidApproval, errors.New("approval reviewed by is required for reviewed approvals"))
	}
	if (a.Status == StatusGranted || a.Status == StatusDenied) && a.ReviewedAt.IsZero() {
		return errors.Join(ErrInvalidApproval, errors.New("approval reviewed at is required for reviewed approvals"))
	}
	return nil
}

// Store persists approval records.
type Store interface {
	Save(context.Context, Approval) error
	Get(context.Context, string) (Approval, bool, error)
	List(context.Context, string) ([]Approval, error)
	IsGranted(context.Context, string, string, string) (bool, error)
}

// ApprovalStore is kept as an explicit alias for readability.
type ApprovalStore = Store

// InMemoryStore is a small approval store for tests and local demos.
type InMemoryStore struct {
	mu        sync.RWMutex
	approvals map[string]Approval
	order     []string
}

// InMemoryApprovalStore is kept as an explicit alias for readability.
type InMemoryApprovalStore = InMemoryStore

// NewInMemoryStore creates an empty in-memory approval store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{approvals: map[string]Approval{}}
}

// NewInMemoryApprovalStore creates an empty in-memory approval store.
func NewInMemoryApprovalStore() *InMemoryStore {
	return NewInMemoryStore()
}

// Save validates and upserts an approval record.
func (s *InMemoryStore) Save(ctx context.Context, a Approval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.approvals[a.ID]; !ok {
		s.order = append(s.order, a.ID)
	}
	s.approvals[a.ID] = a
	return nil
}

// Get returns an approval by id.
func (s *InMemoryStore) Get(ctx context.Context, id string) (Approval, bool, error) {
	if err := ctx.Err(); err != nil {
		return Approval{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.approvals[id]
	return a, ok, nil
}

// List returns approvals for an entity. An empty entity id returns all approvals.
func (s *InMemoryStore) List(ctx context.Context, entityID string) ([]Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	approvals := make([]Approval, 0, len(s.order))
	for _, id := range s.order {
		a := s.approvals[id]
		if entityID == "" || a.EntityID == entityID {
			approvals = append(approvals, a)
		}
	}
	return approvals, nil
}

// IsGranted returns true when the approval exists, is granted, and matches the entity/action.
func (s *InMemoryStore) IsGranted(ctx context.Context, id, entityID, actionName string) (bool, error) {
	a, ok, err := s.Get(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	return a.Status == StatusGranted && a.EntityID == entityID && a.ActionName == actionName, nil
}
