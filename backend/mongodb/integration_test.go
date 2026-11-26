package mongodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2"
	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/job"
)

// Integration tests run against a real server when GOKUE_MONGODB_URI is set,
// for example:
//
//	docker run -d -p 27017:27017 mongo:7
//	GOKUE_MONGODB_URI=mongodb://localhost:27017 go test -race ./...
//
// Each test uses its own collection, dropped on cleanup.
func newIntegrationStore(t *testing.T) *MongoJobStore {
	t.Helper()
	uri := os.Getenv("GOKUE_MONGODB_URI")
	if uri == "" {
		t.Skip("GOKUE_MONGODB_URI not set; skipping MongoDB integration test")
	}
	conn, err := NewMongoDBConnection(uri)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMongoJobStore(conn, "gokue_test", fmt.Sprintf("jobs_%s_%d", t.Name(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if coll, err := store.collectionHandle(); err == nil {
			_ = coll.Drop(ctx)
		}
		_ = store.Close(ctx)
	})
	return store
}

func pendingRecord(id string, created time.Time) backend.JobRecord {
	return backend.JobRecord{
		ID:        job.ID(id),
		Name:      "test",
		Payload:   []byte(`{"n":1}`),
		Status:    backend.StatusPending,
		CreatedAt: created.UTC().Truncate(time.Millisecond),
	}
}

func TestIntegrationSaveGetDelete(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	rec := pendingRecord("a", time.Now())

	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(ctx, rec); !errors.Is(err, backend.ErrAlreadyExists) {
		t.Fatalf("second Save = %v, want ErrAlreadyExists", err)
	}

	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 1 || got.Status != backend.StatusPending || string(got.Payload) != string(rec.Payload) ||
		!got.CreatedAt.Equal(rec.CreatedAt) || got.Name != rec.Name {
		t.Fatalf("Get = %+v, want saved record with version 1", got)
	}

	if err := store.Delete(ctx, rec.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, rec.ID); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, rec.ID); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestIntegrationClaimOrderAndEmpty(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Minute)
	// Saved out of order; Claim must return oldest CreatedAt first.
	for _, r := range []backend.JobRecord{
		pendingRecord("second", base.Add(2*time.Second)),
		pendingRecord("first", base.Add(time.Second)),
		pendingRecord("third", base.Add(3*time.Second)),
	} {
		if err := store.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	for _, want := range []string{"first", "second", "third"} {
		got, err := store.Claim(ctx)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if string(got.ID) != want {
			t.Fatalf("Claim = %s, want %s", got.ID, want)
		}
		if got.Status != backend.StatusRunning || got.Attempts != 1 || got.Version != 2 || got.StartedAt == nil {
			t.Fatalf("claimed record = %+v, want running, attempts 1, version 2, StartedAt set", got)
		}
	}
	if _, err := store.Claim(ctx); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Claim on empty = %v, want ErrNotFound", err)
	}
}

// Many concurrent claimers must never receive the same record.
func TestIntegrationConcurrentClaimIsExclusive(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	const jobs, claimers = 50, 8
	for i := 0; i < jobs; i++ {
		if err := store.Save(ctx, pendingRecord(fmt.Sprintf("j%02d", i), time.Now())); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := map[job.ID]int{}
	var wg sync.WaitGroup
	for w := 0; w < claimers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				rec, err := store.Claim(ctx)
				if errors.Is(err, backend.ErrNotFound) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				seen[rec.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %s claimed %d times", id, n)
		}
	}
}

func TestIntegrationUpdateOptimisticConcurrency(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, pendingRecord("a", time.Now())); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}

	done := claimed
	done.Status = backend.StatusSucceeded
	finished := time.Now().UTC().Truncate(time.Millisecond)
	done.FinishedAt = &finished
	if err := store.Update(ctx, done); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// The same version again is stale.
	if err := store.Update(ctx, done); !errors.Is(err, backend.ErrConflict) {
		t.Fatalf("stale Update = %v, want ErrConflict", err)
	}

	got, err := store.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != backend.StatusSucceeded || got.Version != claimed.Version+1 ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("after Update = %+v", got)
	}

	missing := pendingRecord("missing", time.Now())
	missing.Version = 1
	if err := store.Update(ctx, missing); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Update missing = %v, want ErrNotFound", err)
	}
}

func TestIntegrationListFilterAndLimit(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Minute)
	for i := 0; i < 4; i++ {
		if err := store.Save(ctx, pendingRecord(fmt.Sprintf("j%d", i), base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Claim(ctx); err != nil { // j0 becomes running
		t.Fatal(err)
	}

	all, err := store.List(ctx, backend.ListFilter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("List all = %d records, %v; want 4", len(all), err)
	}
	pending := backend.StatusPending
	recs, err := store.List(ctx, backend.ListFilter{Status: &pending, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].ID != "j1" || recs[1].ID != "j2" {
		t.Fatalf("List pending limit 2 = %v, want [j1 j2]", ids(recs))
	}
}

func TestIntegrationRequeueStale(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	for _, id := range []string{"old", "fresh"} {
		if err := store.Save(ctx, pendingRecord(id, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	old, err := store.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the first claim so only it is past the lease.
	past := time.Now().UTC().Add(-time.Hour)
	old.StartedAt = &past
	if err := store.Update(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx); err != nil {
		t.Fatal(err)
	}

	n, err := store.RequeueStale(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RequeueStale = %d, want 1", n)
	}
	got, err := store.Get(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != backend.StatusPending || got.StartedAt != nil {
		t.Fatalf("requeued record = %+v, want pending with no StartedAt", got)
	}
	if _, err := store.RequeueStale(ctx, -time.Second); err == nil {
		t.Fatal("RequeueStale with negative lease succeeded")
	}
}

var integrationRuns atomic.Int64

type integrationJob struct{ N int }

func (integrationJob) Process(context.Context) error {
	integrationRuns.Add(1)
	return nil
}

// A gokue queue on MongoDB runs jobs left pending by an earlier queue.
func TestIntegrationQueueRecoversJobs(t *testing.T) {
	store := newIntegrationStore(t)
	integrationRuns.Store(0)
	codec := backend.NewCodec()
	if err := codec.Register("", integrationJob{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const jobs = 10
	for i := 0; i < jobs; i++ {
		name, payload, err := codec.Encode(integrationJob{N: i})
		if err != nil {
			t.Fatal(err)
		}
		rec := pendingRecord(fmt.Sprintf("left-%d", i), time.Now())
		rec.Name, rec.Payload = name, payload
		if err := store.Save(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	q, err := gokue.NewQueue(
		gokue.WithStore(store, codec),
		gokue.WithWorkerCount(3),
		gokue.WithPollInterval(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(ctx, integrationJob{N: jobs}); err != nil {
		t.Fatal(err)
	}

	succeeded := backend.StatusSucceeded
	deadline := time.Now().Add(10 * time.Second)
	for {
		recs, err := store.List(ctx, backend.ListFilter{Status: &succeeded})
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == jobs+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d jobs succeeded before timeout", len(recs), jobs+1)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := q.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := integrationRuns.Load(); got != jobs+1 {
		t.Fatalf("ran %d jobs, want %d", got, jobs+1)
	}
}

func ids(recs []backend.JobRecord) []job.ID {
	out := make([]job.ID, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}
