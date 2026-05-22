package file

import (
	"context"
	"os"
	"sync"

	"github.com/kiffhq/kiff/pkg/kiff/decision"
)

// DecisionStore is an append-only JSONL implementation of decision.Store.
type DecisionStore struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewDecisionStore opens or creates a JSONL decision log at path.
func NewDecisionStore(path string) (*DecisionStore, error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &DecisionStore{file: f, path: path}, nil
}

// Close closes the underlying file.
func (s *DecisionStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// Append validates and persists a decision.
func (s *DecisionStore) Append(ctx context.Context, d decision.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	return appendRecord(&s.mu, s.file, d)
}

// List returns decisions for an entity in append order.
// An empty entityID returns all decisions.
func (s *DecisionStore) List(ctx context.Context, entityID string) ([]decision.Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var decisions []decision.Decision
	err := readAll(s.file,
		func() any { return &decision.Decision{} },
		func(record any) error {
			d := record.(*decision.Decision)
			if entityID == "" || d.EntityID == entityID {
				decisions = append(decisions, *d)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return decisions, nil
}
