package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Shamas-Ur-Rehman/go-sfda/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoSink is an optional MongoDB adapter that batches conditional upserts.
// The core go-sfda module does not depend on MongoDB.
type MongoSink struct {
	collection *mongo.Collection
	batchSize  int
	mu         sync.Mutex
	pending    []mongo.WriteModel
	pendingIDs map[string]int
}

// NewMongoSink creates a MongoDB sink and ensures that registration numbers
// have a unique index.
func NewMongoSink(ctx context.Context, collection *mongo.Collection, batchSize int) (*MongoSink, error) {
	if collection == nil {
		return nil, errors.New("MongoDB collection cannot be nil")
	}
	if batchSize <= 0 {
		return nil, errors.New("MongoDB batch size must be positive")
	}
	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "registration_number", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, fmt.Errorf("create unique registration-number index: %w", err)
	}
	return &MongoSink{
		collection: collection,
		batchSize:  batchSize,
		pendingIDs: make(map[string]int),
	}, nil
}

// Store queues a conditional upsert and flushes once the configured batch size
// is reached. Duplicate registration numbers within a batch replace the
// previous queued operation.
func (s *MongoSink) Store(ctx context.Context, drug model.Drug) error {
	if drug.RegistrationNumber == "" {
		return errors.New("registration number is required")
	}
	encoded, err := json.Marshal(drug)
	if err != nil {
		return fmt.Errorf("encode drug for MongoDB: %w", err)
	}
	hash := sha256.Sum256(encoded)
	hashString := hex.EncodeToString(hash[:])
	newDocument := bson.D{
		{Key: "registration_number", Value: drug.RegistrationNumber},
		{Key: "content_hash", Value: hashString},
		{Key: "drug", Value: drug},
	}
	updatePipeline := mongo.Pipeline{
		bson.D{{
			Key: "$replaceWith",
			Value: bson.D{{
				Key: "$cond",
				Value: bson.A{
					bson.D{{Key: "$ne", Value: bson.A{"$content_hash", hashString}}},
					newDocument,
					"$$ROOT",
				},
			}},
		}},
	}
	update := mongo.NewUpdateOneModel().
		SetFilter(bson.M{"registration_number": drug.RegistrationNumber}).
		SetUpdate(updatePipeline).
		SetUpsert(true)

	s.mu.Lock()
	defer s.mu.Unlock()
	if index, exists := s.pendingIDs[drug.RegistrationNumber]; exists {
		s.pending[index] = update
	} else {
		s.pendingIDs[drug.RegistrationNumber] = len(s.pending)
		s.pending = append(s.pending, update)
	}
	if len(s.pending) >= s.batchSize {
		return s.flushLocked(ctx)
	}
	return nil
}

// Flush writes queued updates as an unordered bulk operation. If MongoDB
// returns an error, the batch is retained so the caller can retry it.
func (s *MongoSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked(ctx)
}

func (s *MongoSink) flushLocked(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	_, err := s.collection.BulkWrite(
		ctx,
		s.pending,
		options.BulkWrite().SetOrdered(false),
	)
	if err != nil {
		return fmt.Errorf("bulk upsert drugs to MongoDB: %w", err)
	}
	s.pending = nil
	s.pendingIDs = make(map[string]int)
	return nil
}
