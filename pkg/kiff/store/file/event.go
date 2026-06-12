package file

import (
	"context"
	"os"
	"sync"

	"github.com/kiff/kiff/pkg/kiff/event"
)

// EventStore is an append-only JSONL implementation of event.Store.
type EventStore struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewEventStore opens or creates a JSONL event log at path.
func NewEventStore(path string) (*EventStore, error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &EventStore{file: f, path: path}, nil
}

// Close flushes and closes the underlying file.
func (s *EventStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// Append validates and persists an event.
func (s *EventStore) Append(ctx context.Context, e event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return err
	}
	return appendRecord(&s.mu, s.file, e)
}

// List returns events for an entity in append order.
// An empty entityID returns all events.
func (s *EventStore) List(ctx context.Context, entityID string) ([]event.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var events []event.Event
	err := readAll(s.file,
		func() any { return &event.Event{} },
		func(record any) error {
			ev := record.(*event.Event)
			if entityID == "" || ev.EntityID == entityID {
				events = append(events, *ev)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return events, nil
}
