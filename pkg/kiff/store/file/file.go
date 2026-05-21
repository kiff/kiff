// Package file provides append-only JSONL implementations of the KIFF store
// interfaces. It is intended for local testing, demos, and small single-process
// deployments. It is not a database. Real production deployments should
// implement the store interfaces against a transactional system.
package file

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// open creates or opens a JSONL file in append mode and returns the handle.
func openAppend(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create dir for %s: %w", path, err)
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
}

// readAll reads every record from a JSONL file, decoding each line into the
// value produced by newRecord and passing it to consume.
func readAll(file *os.File, newRecord func() any, consume func(any) error) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		record := newRecord()
		if err := json.Unmarshal(line, record); err != nil {
			return fmt.Errorf("decode jsonl line: %w", err)
		}
		if err := consume(record); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// appendRecord serializes record as a single JSON line and writes it to file.
// The file must have been opened with O_APPEND.
func appendRecord(mu *sync.Mutex, file *os.File, record any) error {
	mu.Lock()
	defer mu.Unlock()
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode jsonl line: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write jsonl line: %w", err)
	}
	return file.Sync()
}

// ErrInvalidPath is returned when a store is configured with an empty path.
var ErrInvalidPath = errors.New("invalid file store path")
