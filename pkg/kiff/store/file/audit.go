package file

import (
	"context"
	"os"
	"sort"
	"sync"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
)

// AuditStore is an append-only JSONL implementation of audit.Store.
type AuditStore struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewAuditStore opens or creates a JSONL audit log at path.
func NewAuditStore(path string) (*AuditStore, error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &AuditStore{file: f, path: path}, nil
}

// Close closes the underlying file.
func (s *AuditStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// Append validates and persists an audit record.
func (s *AuditStore) Append(ctx context.Context, r audit.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	return appendRecord(&s.mu, s.file, r)
}

// List returns audit records for an entity in chronological order.
// An empty entityID returns all records.
func (s *AuditStore) List(ctx context.Context, entityID string) ([]audit.Record, error) {
	return s.Query(ctx, audit.Filter{EntityID: entityID})
}

// Query returns audit records matching filter in chronological order.
func (s *AuditStore) Query(ctx context.Context, filter audit.Filter) ([]audit.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []audit.Record
	err := readAll(s.file,
		func() any { return &audit.Record{} },
		func(record any) error {
			r := record.(*audit.Record)
			if matchesFilter(*r, filter) {
				records = append(records, *r)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	return records, nil
}

func matchesFilter(r audit.Record, f audit.Filter) bool {
	if f.EntityID != "" && r.EntityID != f.EntityID {
		return false
	}
	if f.Kind != "" && r.Kind != f.Kind {
		return false
	}
	if f.ActorID != "" && r.ActorID != f.ActorID {
		return false
	}
	if f.TraceID != "" && r.TraceID != f.TraceID {
		return false
	}
	if f.CorrelationID != "" && r.CorrelationID != f.CorrelationID {
		return false
	}
	return true
}
