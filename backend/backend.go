// Package backend defines the storage abstraction used to persist queued jobs.
//
// A JobStore persists serialized jobs (see Codec) as records together with the
// bookkeeping needed by a queue: lifecycle status, attempt counts, optimistic
// concurrency and timestamps. Implementations live alongside this package
// (for example backend/mongodb); the in-package MemoryStore is the reference
// implementation and a convenient test double.
package backend

import (
	"context"
	"errors"
	"time"

	"github.com/zt4ff/gokue/job"
)

// Status describes the lifecycle stage of a persisted job.
type JobStatus string

const (
	// StatusPending marks a job that is ready to be claimed.
	StatusPending JobStatus = "pending"
	// StatusRunning marks a job that has been claimed and is executing.
	StatusRunning JobStatus = "running"
	// StatusSucceeded marks a job that completed successfully.
	StatusSucceeded JobStatus = "succeeded"
	// StatusFailed marks a job that terminated with an error.
	StatusFailed JobStatus = "failed"
)

// JobRecord is the storage representation of a Job.
//
// Payload holds the serialized job produced by a Codec. Version is an
// optimistic concurrency token: Save assigns version 1 and every successful
// Update (or Claim / RequeueStale) increments it. A caller that wants to mutate
// a record must pass back the version it read; a mismatch is reported as
// ErrConflict so concurrent writers cannot silently clobber each other.
type JobRecord struct {
	ID         job.ID
	Name       job.Name
	Payload    []byte
	Attempts   uint
	Status     JobStatus
	Version    int64
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// ListFilter narrows the results returned by JobStore.List.
type ListFilter struct {
	// Status, when set, restricts results to records with that status.
	Status *JobStatus
	// Limit, when greater than zero, caps the number of returned records.
	Limit int64
}

// JobStore persists JobRecord values.
//
// Implementations must be safe for concurrent use and must translate their
// native "not found" and "already exists" conditions into ErrNotFound and
// ErrAlreadyExists respectively.
type JobStore interface {
	// Save inserts rec. It returns ErrAlreadyExists if a record with the same
	// ID already exists. An empty Status defaults to StatusPending and a zero
	// Version is stored as 1.
	Save(ctx context.Context, rec JobRecord) error

	// Get returns the record with the given ID, or ErrNotFound.
	Get(ctx context.Context, id job.ID) (JobRecord, error)

	// Claim atomically selects the oldest pending record, marks it running,
	// sets StartedAt, increments Attempts and increments Version. It returns
	// ErrNotFound when no pending record is available.
	Claim(ctx context.Context) (JobRecord, error)

	// Update writes rec if the stored Version matches rec.Version. On success
	// the stored Version is incremented. A version mismatch yields
	// ErrConflict; a missing record yields ErrNotFound.
	Update(ctx context.Context, rec JobRecord) error

	// Delete removes the record with the given ID, or returns ErrNotFound.
	Delete(ctx context.Context, id job.ID) error

	// List returns records matching f, ordered by CreatedAt then ID.
	List(ctx context.Context, f ListFilter) ([]JobRecord, error)

	// RequeueStale reverts running records whose StartedAt is older than
	// now-lease back to pending and returns the number of affected records.
	RequeueStale(ctx context.Context, lease time.Duration) (int, error)

	// Close releases the resources held by the store. It must be idempotent.
	Close(ctx context.Context) error
}

var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("job record not found")
	// ErrAlreadyExists is returned when inserting a duplicate record.
	ErrAlreadyExists = errors.New("job record already exists")
	// ErrConflict is returned when an update is based on a stale version.
	ErrConflict = errors.New("job record version conflict")
	// ErrStoreClosed is returned when an operation is attempted on a closed store.
	ErrStoreClosed = errors.New("job store is closed")
	// ErrNilStore is returned when an operation is attempted on a nil store.
	ErrNilStore = errors.New("job store is nil")
	// ErrNotConnected is returned when a store is used before it is connected.
	ErrNotConnected = errors.New("job store is not connected")
)
