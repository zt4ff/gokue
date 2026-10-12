package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zt4ff/gokue/job"
)

func newRecord(id string, created time.Time) JobRecord {
	return JobRecord{
		ID:        job.ID(id),
		Name:      job.Name("testJob"),
		Payload:   []byte("payload-" + id),
		CreatedAt: created,
	}
}

func TestMemoryStoreSaveGet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	rec := newRecord("1", time.Now().UTC())
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}

	if err := store.Save(ctx, rec); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Save error = %v, want ErrAlreadyExists", err)
	}
	if _, err := store.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStorePayloadIsolation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	rec := newRecord("1", time.Now().UTC())
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.Payload[0] = 'X'

	again, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if string(again.Payload) != "payload-1" {
		t.Fatalf("payload mutated: %q", again.Payload)
	}
}

func TestMemoryStoreClaimOrdering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	base := time.Now().UTC()
	if err := store.Save(ctx, newRecord("b", base.Add(2*time.Second))); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	if err := store.Save(ctx, newRecord("a", base.Add(1*time.Second))); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := store.Save(ctx, newRecord("c", base.Add(3*time.Second))); err != nil {
		t.Fatalf("Save c: %v", err)
	}

	want := []job.ID{"a", "b", "c"}
	for _, id := range want {
		claimed, err := store.Claim(ctx)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if claimed.ID != id {
			t.Fatalf("claim order: got %q, want %q", claimed.ID, id)
		}
		if claimed.Status != StatusRunning {
			t.Fatalf("claimed status = %q, want running", claimed.Status)
		}
		if claimed.StartedAt == nil {
			t.Fatal("claimed StartedAt is nil")
		}
		if claimed.Attempts != 1 {
			t.Fatalf("claimed attempts = %d, want 1", claimed.Attempts)
		}
		if claimed.Version != 2 {
			t.Fatalf("claimed version = %d, want 2", claimed.Version)
		}
	}

	if _, err := store.Claim(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Claim empty error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreClaimConcurrent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	const total = 50
	for i := 0; i < total; i++ {
		rec := newRecord(string(rune('A'+i%26))+string(rune('a'+i/26)), time.Now().UTC().Add(time.Duration(i)*time.Millisecond))
		if err := store.Save(ctx, rec); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	var (
		mu   sync.Mutex
		seen = make(map[job.ID]bool)
		wg   sync.WaitGroup
	)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := store.Claim(ctx)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[claimed.ID] {
				t.Errorf("duplicate claim %q", claimed.ID)
			}
			seen[claimed.ID] = true
		}()
	}
	wg.Wait()

	if len(seen) != total {
		t.Fatalf("unique claims = %d, want %d", len(seen), total)
	}
}

func TestMemoryStoreUpdateVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	rec := newRecord("1", time.Now().UTC())
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.Status = StatusSucceeded

	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get updated: %v", err)
	}
	if updated.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", updated.Status)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}

	if err := store.Update(ctx, got); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update error = %v, want ErrConflict", err)
	}

	missing := got
	missing.ID = "missing"
	if err := store.Update(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	rec := newRecord("1", time.Now().UTC())
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, rec.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, rec.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	base := time.Now().UTC()
	for i, id := range []string{"c", "a", "b"} {
		rec := newRecord(id, base.Add(time.Duration(i)*time.Second))
		if err := store.Save(ctx, rec); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	all, err := store.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantOrder := []job.ID{"c", "a", "b"}
	for i, id := range wantOrder {
		if all[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q", i, all[i].ID, id)
		}
	}

	limited, err := store.List(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("List limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited len = %d, want 2", len(limited))
	}

	running := StatusRunning
	filtered, err := store.List(ctx, ListFilter{Status: &running})
	if err != nil {
		t.Fatalf("List filter: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("running records = %d, want 0", len(filtered))
	}
}

func TestMemoryStoreRequeueStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	past := time.Now().UTC().Add(-time.Hour)
	fresh := time.Now().UTC()

	stale := newRecord("stale", past)
	stale.Status = StatusRunning
	stale.StartedAt = &past
	if err := store.Save(ctx, stale); err != nil {
		t.Fatalf("Save stale: %v", err)
	}

	active := newRecord("active", past)
	active.Status = StatusRunning
	active.StartedAt = &fresh
	if err := store.Save(ctx, active); err != nil {
		t.Fatalf("Save active: %v", err)
	}

	pending := newRecord("pending", past)
	if err := store.Save(ctx, pending); err != nil {
		t.Fatalf("Save pending: %v", err)
	}

	count, err := store.RequeueStale(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if count != 1 {
		t.Fatalf("requeued = %d, want 1", count)
	}

	rec, err := store.Get(ctx, stale.ID)
	if err != nil {
		t.Fatalf("Get stale: %v", err)
	}
	if rec.Status != StatusPending || rec.StartedAt != nil {
		t.Fatalf("stale record not reset: %+v", rec)
	}
	if rec.Version != 2 {
		t.Fatalf("stale version = %d, want 2", rec.Version)
	}

	if _, err := store.RequeueStale(ctx, -time.Second); err == nil {
		t.Fatal("negative lease: want error, got nil")
	}
}

func TestMemoryStoreRequeueStaleKeepsFresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	rec := newRecord("fresh", time.Now().UTC())
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.Claim(ctx); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	count, err := store.RequeueStale(ctx, time.Hour)
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if count != 0 {
		t.Fatalf("requeued = %d, want 0", count)
	}
}

func TestMemoryStoreContextCancellation(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Save(ctx, newRecord("1", time.Now().UTC())); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v, want context.Canceled", err)
	}
	if _, err := store.Get(ctx, "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
	if _, err := store.Claim(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Claim error = %v, want context.Canceled", err)
	}
	if _, err := store.List(ctx, ListFilter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("List error = %v, want context.Canceled", err)
	}
	if _, err := store.RequeueStale(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("RequeueStale error = %v, want context.Canceled", err)
	}
}

func TestMemoryStoreClose(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	if err := store.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := store.Save(ctx, newRecord("1", time.Now().UTC())); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Save after Close error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.Get(ctx, "1"); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Get after Close error = %v, want ErrStoreClosed", err)
	}
}
