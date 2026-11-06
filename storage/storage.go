// Package storage defines the minimal persistence contract for scraper results.
package storage

import (
	"context"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
)

// Sink receives each scraped drug. Implement this interface to store records in
// MongoDB, PostgreSQL, another SQL database, or a custom destination.
type Sink interface {
	Store(context.Context, model.Drug) error
}

// SinkFunc adapts a function to the Sink interface.
type SinkFunc func(context.Context, model.Drug) error

// Store calls f.
func (f SinkFunc) Store(ctx context.Context, drug model.Drug) error {
	return f(ctx, drug)
}
