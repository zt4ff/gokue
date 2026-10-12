package backend

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zt4ff/gokue/job"
)

// MemoryStore is an in-memory JobStore. It is safe for concurrent use and is
// intended as a reference implementation and test double. All data is lost
// when the process exits.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[job.ID]JobRecord
	closed  bool
}

var _ JobStore = (*MemoryStore)(nil)

// NewMemoryStore returns an empty, open MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[job.ID]JobRecord)}
}

// Save implements JobStore.
func (s *MemoryStore) Save(ctx context.Context, rec JobRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if _, ok := s.records[rec.ID]; ok {
		return ErrAlreadyExists
	}
	if rec.Status == "" {
		rec.Status = StatusPending
	}
	if rec.Version == 0 {
		rec.Version = 1
	}
	s.records[rec.ID] = cloneRecord(rec)
	return nil
}

// Get implements JobStore.
func (s *MemoryStore) Get(ctx context.Context, id job.ID) (JobRecord, error) {
	if err := ctx.Err(); err != nil {
		return JobRecord{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return JobRecord{}, ErrStoreClosed
	}
	rec, ok := s.records[id]
	if !ok {
		return JobRecord{}, ErrNotFound
	}
	return cloneRecord(rec), nil
}

// Claim implements JobStore.
func (s *MemoryStore) Claim(ctx context.Context) (JobRecord, error) {
	if err := ctx.Err(); err != nil {
		return JobRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return JobRecord{}, ErrStoreClosed
	}

	var chosen job.ID
	var chosenRec JobRecord
	found := false
	for id, rec := range s.records {
		if rec.Status != StatusPending {
			continue
		}
		if !found || rec.CreatedAt.Before(chosenRec.CreatedAt) ||
			(rec.CreatedAt.Equal(chosenRec.CreatedAt) && id < chosen) {
			chosen, chosenRec, found = id, rec, true
		}
	}
	if !found {
		return JobRecord{}, ErrNotFound
	}

	now := time.Now().UTC()
	chosenRec.Status = StatusRunning
	chosenRec.StartedAt = &now
	chosenRec.Attempts++
	chosenRec.Version++
	s.records[chosen] = cloneRecord(chosenRec)
	return cloneRecord(chosenRec), nil
}

// Update implements JobStore.
func (s *MemoryStore) Update(ctx context.Context, rec JobRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	stored, ok := s.records[rec.ID]
	if !ok {
		return ErrNotFound
	}
	if stored.Version != rec.Version {
		return ErrConflict
	}
	rec.Version = stored.Version + 1
	s.records[rec.ID] = cloneRecord(rec)
	return nil
}

// Delete implements JobStore.
func (s *MemoryStore) Delete(ctx context.Context, id job.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if _, ok := s.records[id]; !ok {
		return ErrNotFound
	}
	delete(s.records, id)
	return nil
}

// List implements JobStore.
func (s *MemoryStore) List(ctx context.Context, f ListFilter) ([]JobRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrStoreClosed
	}

	out := make([]JobRecord, 0, len(s.records))
	for _, rec := range s.records {
		if f.Status != nil && rec.Status != *f.Status {
			continue
		}
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if f.Limit > 0 && int64(len(out)) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// RequeueStale implements JobStore.
func (s *MemoryStore) RequeueStale(ctx context.Context, lease time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if lease < 0 {
		return 0, fmt.Errorf("lease cannot be negative")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrStoreClosed
	}

	cutoff := time.Now().UTC().Add(-lease)
	count := 0
	for id, rec := range s.records {
		if rec.Status != StatusRunning || rec.StartedAt == nil {
			continue
		}
		if rec.StartedAt.After(cutoff) {
			continue
		}
		rec.Status = StatusPending
		rec.StartedAt = nil
		rec.Version++
		s.records[id] = rec
		count++
	}
	return count, nil
}

// Close implements JobStore. It is idempotent.
func (s *MemoryStore) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func cloneRecord(rec JobRecord) JobRecord {
	out := rec
	if rec.Payload != nil {
		out.Payload = append([]byte(nil), rec.Payload...)
	}
	if rec.StartedAt != nil {
		started := *rec.StartedAt
		out.StartedAt = &started
	}
	if rec.FinishedAt != nil {
		finished := *rec.FinishedAt
		out.FinishedAt = &finished
	}
	return out
}
