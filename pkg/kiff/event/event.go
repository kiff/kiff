package event

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrInvalidEvent = errors.New("invalid event")

// Metadata carries operational correlation information for an event.
type Metadata struct {
	TraceID       string            `json:"trace_id,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	CausationID   string            `json:"causation_id,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
}

// Event is a normalized record of something that happened.
type Event struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	EntityID   string         `json:"entity_id"`
	EntityType string         `json:"entity_type"`
	Source     string         `json:"source"`
	ActorID    string         `json:"actor_id"`
	OccurredAt time.Time      `json:"occurred_at"`
	Metadata   Metadata       `json:"metadata,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// Validate checks the minimum fields KIFF needs to preserve an event as an
// auditable operational fact.
func (e Event) Validate() error {
	if e.ID == "" {
		return errors.Join(ErrInvalidEvent, errors.New("event id is required"))
	}
	if e.Type == "" {
		return errors.Join(ErrInvalidEvent, errors.New("event type is required"))
	}
	if e.EntityID == "" {
		return errors.Join(ErrInvalidEvent, errors.New("event entity id is required"))
	}
	if e.EntityType == "" {
		return errors.Join(ErrInvalidEvent, errors.New("event entity type is required"))
	}
	if e.Source == "" {
		return errors.Join(ErrInvalidEvent, errors.New("event source is required"))
	}
	if e.ActorID == "" {
		return errors.Join(ErrInvalidEvent, errors.New("event actor id is required"))
	}
	if e.OccurredAt.IsZero() {
		return errors.Join(ErrInvalidEvent, errors.New("event occurred at is required"))
	}
	return nil
}

// Store persists normalized events.
type Store interface {
	Append(context.Context, Event) error
	List(context.Context, string) ([]Event, error)
}

// EventStore is kept as an explicit alias for readability in framework code.
type EventStore = Store

// InMemoryStore is a small append-only event store for tests and local demos.
type InMemoryStore struct {
	mu     sync.RWMutex
	events []Event
}

// InMemoryEventStore is kept as an explicit alias for readability.
type InMemoryEventStore = InMemoryStore

// NewInMemoryStore creates an empty in-memory event store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{}
}

// NewInMemoryEventStore creates an empty in-memory event store.
func NewInMemoryEventStore() *InMemoryStore {
	return NewInMemoryStore()
}

// Append validates and stores an event.
func (s *InMemoryStore) Append(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// List returns events for an entity. An empty entity id returns all events.
func (s *InMemoryStore) List(ctx context.Context, entityID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := make([]Event, 0, len(s.events))
	for _, e := range s.events {
		if entityID == "" || e.EntityID == entityID {
			events = append(events, e)
		}
	}
	return events, nil
}
