// Package storage defines the minimal persistence contract for scraper results.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
)

// Sink receives each scraped drug. Implement this interface to store records in
// MongoDB, PostgreSQL, another SQL database, or a custom destination.
type Sink interface {
	Store(context.Context, model.Drug) error
}

// Flusher is an optional sink extension for adapters that buffer writes.
// Scraper calls Flush before it marks a page complete in a checkpoint.
type Flusher interface {
	Flush(context.Context) error
}

// SinkFunc adapts a function to the Sink interface.
type SinkFunc func(context.Context, model.Drug) error

// Store calls f.
func (f SinkFunc) Store(ctx context.Context, drug model.Drug) error {
	return f(ctx, drug)
}

// Checkpoint stores progress for sequential, resumable scrapes.
type Checkpoint struct {
	Version           int       `json:"version"`
	LastCompletedPage int       `json:"last_completed_page"`
	QueryFingerprint  string    `json:"query_fingerprint,omitempty"`
	InProgressPage    *int      `json:"in_progress_page,omitempty"`
	FailedPages       []int     `json:"failed_pages,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// NewCheckpoint returns an empty versioned checkpoint.
func NewCheckpoint() Checkpoint {
	return Checkpoint{Version: 1, LastCompletedPage: -1}
}

// CheckpointStore persists scrape progress. Implementations must make Save
// safe against interrupted writes.
type CheckpointStore interface {
	Load() (Checkpoint, error)
	Save(Checkpoint) error
}

// JSONCheckpointStore persists checkpoints as atomically replaced JSON files.
type JSONCheckpointStore struct {
	path string
	mu   sync.Mutex
}

// NewJSONCheckpointStore creates a checkpoint store for path.
func NewJSONCheckpointStore(path string) (*JSONCheckpointStore, error) {
	if path == "" {
		return nil, errors.New("checkpoint path cannot be empty")
	}
	return &JSONCheckpointStore{path: path}, nil
}

// Load reads a checkpoint. A missing file is treated as a new scrape; corrupt
// or unsupported checkpoint data is returned as an explicit error.
func (s *JSONCheckpointStore) Load() (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return NewCheckpoint(), nil
	}
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read checkpoint %q: %w", s.path, err)
	}

	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode checkpoint %q: %w", s.path, err)
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("validate checkpoint %q: %w", s.path, err)
	}
	return checkpoint, nil
}

// Save writes a complete checkpoint to a temporary file in the destination
// directory before atomically replacing the current checkpoint.
func (s *JSONCheckpointStore) Save(checkpoint Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if checkpoint.Version == 0 {
		checkpoint.Version = 1
	}
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = time.Now().UTC()
	}
	sort.Ints(checkpoint.FailedPages)
	if err := validateCheckpoint(checkpoint); err != nil {
		return fmt.Errorf("validate checkpoint before save: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".sfda-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set checkpoint file permissions: %w", err)
	}
	encodeErr := json.NewEncoder(temporary).Encode(checkpoint)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(encodeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write temporary checkpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace checkpoint %q: %w", s.path, err)
	}
	return nil
}

func validateCheckpoint(checkpoint Checkpoint) error {
	if checkpoint.Version != 1 {
		return fmt.Errorf("unsupported version %d", checkpoint.Version)
	}
	if checkpoint.LastCompletedPage < -1 {
		return fmt.Errorf("last completed page cannot be less than -1")
	}
	if checkpoint.InProgressPage != nil && *checkpoint.InProgressPage < 0 {
		return fmt.Errorf("in-progress page cannot be negative")
	}
	seen := make(map[int]struct{}, len(checkpoint.FailedPages))
	for _, page := range checkpoint.FailedPages {
		if page < 0 {
			return fmt.Errorf("failed page cannot be negative")
		}
		if _, exists := seen[page]; exists {
			return fmt.Errorf("duplicate failed page %d", page)
		}
		seen[page] = struct{}{}
	}
	return nil
}
