package adapters

import (
	"context"
	"database/sql"
	"testing"

	"go.mongodb.org/mongo-driver/mongo"
)

func TestPostgresSinkRejectsNilDatabaseAndNonPositiveBatch(t *testing.T) {
	if _, err := NewPostgresSink(nil, 10); err == nil {
		t.Fatal("expected a nil database to be rejected")
	}
	if _, err := NewPostgresSink(&sql.DB{}, 0); err == nil {
		t.Fatal("expected a non-positive batch size to be rejected")
	}
}

func TestMongoSinkRejectsNilCollectionAndNonPositiveBatch(t *testing.T) {
	if _, err := NewMongoSink(context.Background(), nil, 10); err == nil {
		t.Fatal("expected a nil collection to be rejected")
	}
	if _, err := NewMongoSink(context.Background(), &mongo.Collection{}, 0); err == nil {
		t.Fatal("expected a non-positive batch size to be rejected")
	}
}
