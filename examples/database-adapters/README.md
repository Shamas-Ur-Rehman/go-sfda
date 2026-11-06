# Database adapter patterns

These are application-owned examples. The core `go-sfda` module does not
depend on either database driver. The compiling adapter implementations are in
[`mongo.go`](mongo.go) and [`postgres.go`](postgres.go); this module has its
own `go.mod`, separate from the core package.

Run the compile checks without connecting to either database:

```shell
go test ./...
go build ./...
```

## MongoDB: unique key, conditional bulk upserts

Create a unique index once during application setup:

```go
_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys:    bson.D{{Key: "registration_number", Value: 1}},
	Options: options.Index().SetUnique(true),
})
```

The `registration_number` field is the stable unique key. This batch sink uses
MongoDB's update pipeline to leave an unchanged document untouched. The hash is
over the JSON representation of the `Drug`; the actual record is stored in the
`drug` field.

```go
type mongoSink struct {
	collection *mongo.Collection
	pending    []mongo.WriteModel
	batchSize  int
}

func (s *mongoSink) Store(ctx context.Context, drug model.Drug) error {
	if drug.RegistrationNumber == "" {
		return errors.New("registration number is required")
	}
	encoded, err := json.Marshal(drug)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(encoded)
	hashString := hex.EncodeToString(hash[:])
	newDocument := bson.D{
		{Key: "registration_number", Value: drug.RegistrationNumber},
		{Key: "content_hash", Value: hashString},
		{Key: "drug", Value: drug},
	}
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$replaceWith", Value: bson.D{{
			Key: "$cond",
			Value: bson.A{
				bson.D{{Key: "$ne", Value: bson.A{"$content_hash", hashString}}},
				newDocument,
				"$$ROOT",
			},
		}}}},
	}
	update := mongo.NewUpdateOneModel().
		SetFilter(bson.M{"registration_number": drug.RegistrationNumber}).
		SetUpdate(pipeline).
		SetUpsert(true)
	s.pending = append(s.pending, update)
	if len(s.pending) >= s.batchSize {
		return s.Flush(ctx)
	}
	return nil
}

func (s *mongoSink) Flush(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	_, err := s.collection.BulkWrite(
		ctx,
		s.pending,
		options.BulkWrite().SetOrdered(false),
	)
	if err != nil {
		// Keep the batch so the caller can retry. Upserts are idempotent by key.
		return err
	}
	s.pending = nil
	return nil
}
```

Required imports include `context`, `crypto/sha256`, `encoding/hex`,
`encoding/json`, `errors`, `github.com/Shamas-Ur-Rehman/go-sfda/model`, and the
MongoDB driver's `bson`, `mongo`, and `options` packages (driver v1 API).
Construct the sink with a positive batch size and pass it to `Scrape`:

```go
sink := &mongoSink{collection: collection, batchSize: 200}
if err := client.Scrape(ctx, sink); err != nil {
	return err
}
```

`Scrape` invokes `Flush` at each page boundary. `BulkWrite` can partially apply
an unordered batch before returning an error; retrying the same keyed,
conditional upserts is safe. Confirm this behavior against the MongoDB server
version and driver version used by your application. The update-pipeline form
requires MongoDB 4.2 or newer.

## PostgreSQL: JSONB upsert in a transaction

The primary key prevents duplicate registrations. The `WHERE` condition skips
the update when the stored JSONB record is unchanged:

```sql
CREATE TABLE sfda_drugs (
    registration_number text PRIMARY KEY,
    record jsonb NOT NULL
);
```

```go
const upsert = `
INSERT INTO sfda_drugs (registration_number, record)
VALUES ($1, $2::jsonb)
ON CONFLICT (registration_number) DO UPDATE
SET record = EXCLUDED.record
WHERE sfda_drugs.record IS DISTINCT FROM EXCLUDED.record`

func saveBatch(ctx context.Context, db *sql.DB, drugs []model.Drug) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, upsert)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, drug := range drugs {
		if drug.RegistrationNumber == "" {
			return errors.New("registration number is required")
		}
		record, err := json.Marshal(drug)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, drug.RegistrationNumber, string(record)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

This transaction batches commits while retaining parameterized statements and
per-record upserts. For much larger batches, use PostgreSQL `COPY` into a
staging table followed by one `INSERT ... ON CONFLICT` statement. When wiring
batch persistence into `Scrape`, implement `storage.Sink` and `storage.Flusher`;
the scraper will only checkpoint the page after the flush succeeds.
