package mongodb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zt4ff/gokue/backend"
)

func TestNewMongoDBConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{name: "valid", uri: "mongodb://localhost:27017"},
		{name: "valid with credentials", uri: "mongodb://user:pass@localhost:27017/db?authSource=admin"},
		{name: "empty", uri: "", wantErr: true},
		{name: "malformed", uri: "not-a-uri", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn, err := NewMongoDBConnection(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewMongoDBConnection(%q): want error, got nil", tt.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMongoDBConnection(%q): %v", tt.uri, err)
			}
			if conn.ConnectionURI != tt.uri {
				t.Fatalf("URI = %q, want %q", conn.ConnectionURI, tt.uri)
			}
		})
	}
}

func TestCloseConnectionNilSafe(t *testing.T) {
	t.Parallel()

	var nilConn *MongoDBConnection
	if err := nilConn.CloseConnection(context.Background()); err != nil {
		t.Fatalf("nil receiver CloseConnection: %v", err)
	}

	conn := &MongoDBConnection{}
	if err := conn.CloseConnection(context.Background()); err != nil {
		t.Fatalf("unconnected CloseConnection: %v", err)
	}
}

func TestNewMongoJobStoreValidation(t *testing.T) {
	t.Parallel()

	valid := &MongoDBConnection{}

	tests := []struct {
		name       string
		conn       *MongoDBConnection
		database   string
		collection string
		wantErr    bool
	}{
		{name: "valid", conn: valid, database: "db", collection: "jobs"},
		{name: "nil connection", conn: nil, database: "db", collection: "jobs", wantErr: true},
		{name: "empty database", conn: valid, database: "", collection: "jobs", wantErr: true},
		{name: "empty collection", conn: valid, database: "db", collection: "", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, err := NewMongoJobStore(tt.conn, tt.database, tt.collection)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				if store != nil {
					t.Fatal("want nil store on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMongoJobStore: %v", err)
			}
		})
	}
}

func TestMongoJobStoreRequiresConnection(t *testing.T) {
	t.Parallel()

	conn := &MongoDBConnection{}
	store, err := NewMongoJobStore(conn, "db", "jobs")
	if err != nil {
		t.Fatalf("NewMongoJobStore: %v", err)
	}

	ctx := context.Background()
	rec := backend.JobRecord{ID: "1", Name: "job", CreatedAt: time.Now().UTC()}

	checks := []struct {
		name string
		call func() error
	}{
		{"Save", func() error { return store.Save(ctx, rec) }},
		{"Get", func() error { _, err := store.Get(ctx, rec.ID); return err }},
		{"Claim", func() error { _, err := store.Claim(ctx); return err }},
		{"Update", func() error { return store.Update(ctx, rec) }},
		{"Delete", func() error { return store.Delete(ctx, rec.ID) }},
		{"List", func() error { _, err := store.List(ctx, backend.ListFilter{}); return err }},
		{"RequeueStale", func() error { _, err := store.RequeueStale(ctx, time.Minute); return err }},
		{"EnsureIndexes", func() error { return store.EnsureIndexes(ctx) }},
	}

	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			if err := check.call(); !errors.Is(err, backend.ErrNotConnected) {
				t.Fatalf("error = %v, want ErrNotConnected", err)
			}
		})
	}
}
