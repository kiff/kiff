package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rowReader is a minimal subset of pgx.Rows used by the store implementations.
// Defining it as a local interface keeps the per-store code easier to scan
// without coupling it to pgx specifics.
type rowReader interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

// queryAll runs a query and returns the resulting rows wrapped in a
// rowReader. The wrapper is just an adapter around pgx.Rows; it exists so
// tests for individual stores can replace it later if they want.
func queryAll(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (rowReader, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return rowsAdapter{rows: rows}, nil
}

// rowsAdapter adapts pgx.Rows to the local rowReader interface.
type rowsAdapter struct {
	rows pgx.Rows
}

func (a rowsAdapter) Next() bool         { return a.rows.Next() }
func (a rowsAdapter) Scan(v ...any) error { return a.rows.Scan(v...) }
func (a rowsAdapter) Err() error         { return a.rows.Err() }
func (a rowsAdapter) Close()             { a.rows.Close() }

// marshalJSONOrEmpty marshals a value to JSON, returning an empty JSON object
// when the value is nil. Postgres JSONB columns reject NULL on NOT NULL
// columns, so we always send valid JSON bytes.
func marshalJSONOrEmpty(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	if m, ok := v.(map[string]any); ok && m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}"), nil
	}
	return b, nil
}

// marshalJSONArrayOrEmpty marshals a slice to JSON, returning [] when the
// slice is nil or empty.
func marshalJSONArrayOrEmpty(v any) ([]byte, error) {
	if v == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || string(b) == "null" {
		return []byte("[]"), nil
	}
	return b, nil
}

// unmarshalToMap parses JSON bytes into a map[string]any, treating the JSON
// "null" or empty input as a nil map. The conformance suite expects nil
// payloads to round-trip as nil rather than empty maps.
func unmarshalToMap(data []byte, target *map[string]any) error {
	if len(data) == 0 || string(data) == "null" || string(data) == "{}" {
		*target = nil
		return nil
	}
	return json.Unmarshal(data, target)
}
