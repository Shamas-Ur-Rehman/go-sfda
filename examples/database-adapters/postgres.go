package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
)

const postgresUpsert = `
INSERT INTO sfda_drugs (registration_number, record)
VALUES ($1, $2::jsonb)
ON CONFLICT (registration_number) DO UPDATE
SET record = EXCLUDED.record
WHERE sfda_drugs.record IS DISTINCT FROM EXCLUDED.record`

// PostgresSink buffers records and persists each batch in one transaction.
// The database/sql driver is supplied by the consuming application.
type PostgresSink struct {
	db        *sql.DB
	batchSize int
	mu        sync.Mutex
	pending   []model.Drug
}

// NewPostgresSink creates a PostgreSQL sink. The table must have a unique
// constraint or primary key on registration_number.
func NewPostgresSink(db *sql.DB, batchSize int) (*PostgresSink, error) {
	if db == nil {
		return nil, errors.New("PostgreSQL database cannot be nil")
	}
	if batchSize <= 0 {
		return nil, errors.New("PostgreSQL batch size must be positive")
	}
	return &PostgresSink{db: db, batchSize: batchSize}, nil
}

// Store queues a record and flushes when the configured batch size is reached.
func (s *PostgresSink) Store(ctx context.Context, drug model.Drug) error {
	if drug.RegistrationNumber == "" {
		return errors.New("registration number is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, drug)
	if len(s.pending) >= s.batchSize {
		return s.flushLocked(ctx)
	}
	return nil
}

// Flush upserts queued records in one transaction. The batch is retained when
// the transaction fails so the caller can retry it.
func (s *PostgresSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked(ctx)
}

func (s *PostgresSink) flushLocked(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL batch transaction: %w", err)
	}
	defer tx.Rollback()

	statement, err := tx.PrepareContext(ctx, postgresUpsert)
	if err != nil {
		return fmt.Errorf("prepare PostgreSQL upsert: %w", err)
	}
	defer statement.Close()
	for _, drug := range s.pending {
		encoded, err := json.Marshal(drug)
		if err != nil {
			return fmt.Errorf("encode drug %q for PostgreSQL: %w", drug.RegistrationNumber, err)
		}
		if _, err := statement.ExecContext(ctx, drug.RegistrationNumber, string(encoded)); err != nil {
			return fmt.Errorf("upsert drug %q to PostgreSQL: %w", drug.RegistrationNumber, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL batch: %w", err)
	}
	s.pending = nil
	return nil
}
