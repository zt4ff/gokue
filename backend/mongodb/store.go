package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/zt4ff/gokue/backend"
	"github.com/zt4ff/gokue/job"
)

// jobDocument is the on-disk representation of a backend.JobRecord. It is kept
// private so that bson tags do not leak into the backend package.
type jobDocument struct {
	ID         job.ID            `bson:"_id"`
	Name       job.Name          `bson:"name"`
	Payload    []byte            `bson:"payload"`
	Attempts   uint              `bson:"attempts"`
	Status     backend.JobStatus `bson:"status"`
	Version    int64             `bson:"version"`
	CreatedAt  time.Time         `bson:"created_at"`
	StartedAt  *time.Time        `bson:"started_at,omitempty"`
	FinishedAt *time.Time        `bson:"finished_at,omitempty"`
}

func toDocument(rec backend.JobRecord) jobDocument {
	return jobDocument{
		ID:         rec.ID,
		Name:       rec.Name,
		Payload:    rec.Payload,
		Attempts:   rec.Attempts,
		Status:     rec.Status,
		Version:    rec.Version,
		CreatedAt:  rec.CreatedAt,
		StartedAt:  rec.StartedAt,
		FinishedAt: rec.FinishedAt,
	}
}

func fromDocument(doc jobDocument) backend.JobRecord {
	return backend.JobRecord{
		ID:         doc.ID,
		Name:       doc.Name,
		Payload:    doc.Payload,
		Attempts:   doc.Attempts,
		Status:     doc.Status,
		Version:    doc.Version,
		CreatedAt:  doc.CreatedAt,
		StartedAt:  doc.StartedAt,
		FinishedAt: doc.FinishedAt,
	}
}

// MongoJobStore is a MongoDB-backed backend.JobStore.
//
// Note that Save stores the serialized payload plus bookkeeping in a single
// document; the serialized job must therefore fit within MongoDB's 16 MB BSON
// document limit.
type MongoJobStore struct {
	conn       *MongoDBConnection
	database   string
	collection string
}

var _ backend.JobStore = (*MongoJobStore)(nil)

// NewMongoJobStore returns a store bound to database.collection. The
// connection does not need to be dialed yet; call Setup before use.
func NewMongoJobStore(conn *MongoDBConnection, database, collection string) (*MongoJobStore, error) {
	if conn == nil {
		return nil, errors.New("mongodb connection cannot be nil")
	}
	if database == "" {
		return nil, errors.New("database name cannot be empty")
	}
	if collection == "" {
		return nil, errors.New("collection name cannot be empty")
	}

	return &MongoJobStore{conn: conn, database: database, collection: collection}, nil
}

// Setup dials the connection and ensures the required indexes exist.
func (s *MongoJobStore) Setup(ctx context.Context) error {
	if err := s.conn.SetupConnection(ctx); err != nil {
		return err
	}
	return s.EnsureIndexes(ctx)
}

// EnsureIndexes creates the index used to order pending jobs.
func (s *MongoJobStore) EnsureIndexes(ctx context.Context) error {
	coll, err := s.collectionHandle()
	if err != nil {
		return err
	}

	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: 1}},
		Options: options.Index().SetName("status_created_at"),
	})
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	return nil
}

// Save implements backend.JobStore.
func (s *MongoJobStore) Save(ctx context.Context, rec backend.JobRecord) error {
	coll, err := s.collectionHandle()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.Status == "" {
		rec.Status = backend.StatusPending
	}
	if rec.Version == 0 {
		rec.Version = 1
	}

	if _, err := coll.InsertOne(ctx, toDocument(rec)); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return backend.ErrAlreadyExists
		}
		return fmt.Errorf("insert job record: %w", err)
	}
	return nil
}

// Get implements backend.JobStore.
func (s *MongoJobStore) Get(ctx context.Context, id job.ID) (backend.JobRecord, error) {
	coll, err := s.collectionHandle()
	if err != nil {
		return backend.JobRecord{}, err
	}

	var doc jobDocument
	if err := coll.FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return backend.JobRecord{}, backend.ErrNotFound
		}
		return backend.JobRecord{}, fmt.Errorf("find job record: %w", err)
	}
	return fromDocument(doc), nil
}

// Claim implements backend.JobStore.
func (s *MongoJobStore) Claim(ctx context.Context) (backend.JobRecord, error) {
	coll, err := s.collectionHandle()
	if err != nil {
		return backend.JobRecord{}, err
	}

	now := time.Now().UTC()
	update := bson.M{
		"$set": bson.M{"status": backend.StatusRunning, "started_at": now},
		"$inc": bson.M{"version": 1, "attempts": 1},
	}
	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "created_at", Value: 1}}).
		SetReturnDocument(options.After)

	var doc jobDocument
	err = coll.FindOneAndUpdate(ctx, bson.M{"status": backend.StatusPending}, update, opts).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return backend.JobRecord{}, backend.ErrNotFound
		}
		return backend.JobRecord{}, fmt.Errorf("claim job record: %w", err)
	}
	return fromDocument(doc), nil
}

// Update implements backend.JobStore.
func (s *MongoJobStore) Update(ctx context.Context, rec backend.JobRecord) error {
	coll, err := s.collectionHandle()
	if err != nil {
		return err
	}

	update := bson.M{
		"$set": bson.M{
			"name":        rec.Name,
			"payload":     rec.Payload,
			"attempts":    rec.Attempts,
			"status":      rec.Status,
			"started_at":  rec.StartedAt,
			"finished_at": rec.FinishedAt,
		},
		"$inc": bson.M{"version": 1},
	}

	res, err := coll.UpdateOne(ctx, bson.M{"_id": rec.ID, "version": rec.Version}, update)
	if err != nil {
		return fmt.Errorf("update job record: %w", err)
	}
	if res.MatchedCount > 0 {
		return nil
	}

	exists, err := s.exists(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !exists {
		return backend.ErrNotFound
	}
	return backend.ErrConflict
}

// Delete implements backend.JobStore.
func (s *MongoJobStore) Delete(ctx context.Context, id job.ID) error {
	coll, err := s.collectionHandle()
	if err != nil {
		return err
	}

	res, err := coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return fmt.Errorf("delete job record: %w", err)
	}
	if res.DeletedCount == 0 {
		return backend.ErrNotFound
	}
	return nil
}

// List implements backend.JobStore.
func (s *MongoJobStore) List(ctx context.Context, f backend.ListFilter) ([]backend.JobRecord, error) {
	coll, err := s.collectionHandle()
	if err != nil {
		return nil, err
	}

	filter := bson.M{}
	if f.Status != nil {
		filter["status"] = *f.Status
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}})
	if f.Limit > 0 {
		opts.SetLimit(f.Limit)
	}

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("list job records: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []jobDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode job records: %w", err)
	}

	records := make([]backend.JobRecord, len(docs))
	for i, doc := range docs {
		records[i] = fromDocument(doc)
	}
	return records, nil
}

// RequeueStale implements backend.JobStore.
func (s *MongoJobStore) RequeueStale(ctx context.Context, lease time.Duration) (int, error) {
	if lease < 0 {
		return 0, errors.New("lease cannot be negative")
	}

	coll, err := s.collectionHandle()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().UTC().Add(-lease)
	update := bson.M{
		"$set": bson.M{"status": backend.StatusPending, "started_at": nil},
		"$inc": bson.M{"version": 1},
	}

	res, err := coll.UpdateMany(ctx,
		bson.M{"status": backend.StatusRunning, "started_at": bson.M{"$lt": cutoff}},
		update,
	)
	if err != nil {
		return 0, fmt.Errorf("requeue stale job records: %w", err)
	}
	return int(res.ModifiedCount), nil
}

// Close implements backend.JobStore. It is idempotent.
func (s *MongoJobStore) Close(ctx context.Context) error {
	return s.conn.CloseConnection(ctx)
}

func (s *MongoJobStore) collectionHandle() (*mongo.Collection, error) {
	if s == nil || s.conn == nil {
		return nil, backend.ErrNilStore
	}
	if s.conn.Client == nil {
		return nil, backend.ErrNotConnected
	}
	return s.conn.Client.Database(s.database).Collection(s.collection), nil
}

func (s *MongoJobStore) exists(ctx context.Context, id job.ID) (bool, error) {
	coll, err := s.collectionHandle()
	if err != nil {
		return false, err
	}
	count, err := coll.CountDocuments(ctx, bson.M{"_id": id}, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("count job record: %w", err)
	}
	return count > 0, nil
}
