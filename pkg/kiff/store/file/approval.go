package file

import (
	"context"
	"os"
	"sync"

	"github.com/kiffhq/kiff/pkg/kiff/approval"
)

// ApprovalStore is a JSONL-backed implementation of approval.Store. Approvals
// are mutable (status moves from pending to granted or denied), so the file is
// an append log of approval snapshots. The latest snapshot wins for any id.
type ApprovalStore struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewApprovalStore opens or creates a JSONL approval log at path.
func NewApprovalStore(path string) (*ApprovalStore, error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &ApprovalStore{file: f, path: path}, nil
}

// Close closes the underlying file.
func (s *ApprovalStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// Save validates and persists an approval record. Reviewing an approval simply
// appends the new state; reads always return the latest snapshot per id.
func (s *ApprovalStore) Save(ctx context.Context, a approval.Approval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	return appendRecord(&s.mu, s.file, a)
}

// Get returns the latest snapshot of an approval by id.
func (s *ApprovalStore) Get(ctx context.Context, id string) (approval.Approval, bool, error) {
	if err := ctx.Err(); err != nil {
		return approval.Approval{}, false, err
	}
	snapshots, err := s.snapshots(ctx)
	if err != nil {
		return approval.Approval{}, false, err
	}
	a, ok := snapshots[id]
	return a, ok, nil
}

// List returns the latest snapshot of every approval for an entity, in
// insertion order. An empty entityID returns all approvals.
func (s *ApprovalStore) List(ctx context.Context, entityID string) ([]approval.Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshots := map[string]approval.Approval{}
	var order []string
	err := readAll(s.file,
		func() any { return &approval.Approval{} },
		func(record any) error {
			a := record.(*approval.Approval)
			if _, exists := snapshots[a.ID]; !exists {
				order = append(order, a.ID)
			}
			snapshots[a.ID] = *a
			return nil
		})
	if err != nil {
		return nil, err
	}
	approvals := make([]approval.Approval, 0, len(order))
	for _, id := range order {
		a := snapshots[id]
		if entityID == "" || a.EntityID == entityID {
			approvals = append(approvals, a)
		}
	}
	return approvals, nil
}

// IsGranted returns true when the latest snapshot of an approval is granted
// and matches the given entity and action.
func (s *ApprovalStore) IsGranted(ctx context.Context, id, entityID, actionName string) (bool, error) {
	a, ok, err := s.Get(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	return a.Status == approval.StatusGranted &&
		a.EntityID == entityID &&
		a.ActionName == actionName, nil
}

// snapshots returns the latest snapshot of every approval keyed by id.
// Caller must not hold the mutex.
func (s *ApprovalStore) snapshots(ctx context.Context) (map[string]approval.Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshots := map[string]approval.Approval{}
	err := readAll(s.file,
		func() any { return &approval.Approval{} },
		func(record any) error {
			a := record.(*approval.Approval)
			snapshots[a.ID] = *a
			return nil
		})
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}
