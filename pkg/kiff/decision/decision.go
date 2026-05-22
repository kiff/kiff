package decision

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kiffhq/kiff/pkg/kiff/evidence"
)

var ErrInvalidDecision = errors.New("invalid decision")

// Kind describes the type of decision being recorded.
type Kind string

const (
	KindClassification Kind = "classification"
	KindRecommendation Kind = "recommendation"
	KindActionProposal Kind = "action_proposal"
	KindApproval       Kind = "approval"
)

// DecisionKind is kept as an explicit alias for readability.
type DecisionKind = Kind

// Decision records why an action, classification, recommendation, or next step was selected.
type Decision struct {
	ID               string         `json:"id"`
	EntityID         string         `json:"entity_id"`
	EntityType       string         `json:"entity_type"`
	Kind             Kind           `json:"kind"`
	ProposedAction   string         `json:"proposed_action,omitempty"`
	Evidence         []evidence.Ref `json:"evidence,omitempty"`
	ReasoningSummary string         `json:"reasoning_summary,omitempty"`
	Confidence       float64        `json:"confidence,omitempty"`
	ActorID          string         `json:"actor_id"`
	CreatedAt        time.Time      `json:"created_at"`
}

// Validate checks the minimum fields needed for an auditable decision.
func (d Decision) Validate() error {
	if d.ID == "" {
		return errors.Join(ErrInvalidDecision, errors.New("decision id is required"))
	}
	if d.EntityID == "" {
		return errors.Join(ErrInvalidDecision, errors.New("decision entity id is required"))
	}
	if d.EntityType == "" {
		return errors.Join(ErrInvalidDecision, errors.New("decision entity type is required"))
	}
	if d.Kind == "" {
		return errors.Join(ErrInvalidDecision, errors.New("decision kind is required"))
	}
	if d.ActorID == "" {
		return errors.Join(ErrInvalidDecision, errors.New("decision actor id is required"))
	}
	if d.CreatedAt.IsZero() {
		return errors.Join(ErrInvalidDecision, errors.New("decision created at is required"))
	}
	return nil
}

// Store persists decisions.
type Store interface {
	Append(context.Context, Decision) error
	List(context.Context, string) ([]Decision, error)
}

// DecisionStore is kept as an explicit alias for readability.
type DecisionStore = Store

// InMemoryStore is a small append-only decision store.
type InMemoryStore struct {
	mu        sync.RWMutex
	decisions []Decision
}

// InMemoryDecisionStore is kept as an explicit alias for readability.
type InMemoryDecisionStore = InMemoryStore

// NewInMemoryStore creates an empty in-memory decision store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{}
}

// NewInMemoryDecisionStore creates an empty in-memory decision store.
func NewInMemoryDecisionStore() *InMemoryStore {
	return NewInMemoryStore()
}

// Append validates and stores a decision.
func (s *InMemoryStore) Append(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, d)
	return nil
}

// List returns decisions for an entity. An empty entity id returns all decisions.
func (s *InMemoryStore) List(ctx context.Context, entityID string) ([]Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	decisions := make([]Decision, 0, len(s.decisions))
	for _, d := range s.decisions {
		if entityID == "" || d.EntityID == entityID {
			decisions = append(decisions, d)
		}
	}
	return decisions, nil
}
