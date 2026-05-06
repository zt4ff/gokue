// Package stats provides statistics collection for the gokue job queue.
package stats

import "sync/atomic"

// Collector tracks queue activity without taking locks on the hot path using atomic operations.
type Collector struct {
	// enqueued counts the number of jobs enqueued.
	enqueued atomic.Uint64
	// processed counts the number of jobs successfully processed.
	processed atomic.Uint64
	// failed counts the number of jobs that failed after all retries.
	failed atomic.Uint64
	// retried counts the number of job retry attempts.
	retried atomic.Uint64
	// dropped counts the number of jobs dropped (queue full or cancelled).
	dropped atomic.Uint64
}

// Snapshot represents a point-in-time capture of queue statistics.
type Snapshot struct {
	// Enqueued is the total number of jobs enqueued.
	Enqueued uint64
	// Processed is the total number of jobs successfully processed.
	Processed uint64
	// Failed is the total number of jobs that failed after all retries.
	Failed uint64
	// Retried is the total number of job retry attempts.
	Retried uint64
	// Dropped is the total number of jobs dropped.
	Dropped uint64
}

// NewCollector creates and returns a new Collector initialized with zero values.
func NewCollector() *Collector {
	return &Collector{}
}

// IncEnqueued increments the enqueued counter.
func (c *Collector) IncEnqueued() {
	c.enqueued.Add(1)
}

// IncProcessed increments the processed counter.
func (c *Collector) IncProcessed() {
	c.processed.Add(1)
}

// IncFailed increments the failed counter.
func (c *Collector) IncFailed() {
	c.failed.Add(1)
}

// IncRetried increments the retried counter.
func (c *Collector) IncRetried() {
	c.retried.Add(1)
}

// IncDropped increments the dropped counter.
func (c *Collector) IncDropped() {
	c.dropped.Add(1)
}

// Snapshot returns a snapshot of the current statistics.
func (c *Collector) Snapshot() Snapshot {
	return Snapshot{
		Enqueued:  c.enqueued.Load(),
		Processed: c.processed.Load(),
		Failed:    c.failed.Load(),
		Retried:   c.retried.Load(),
		Dropped:   c.dropped.Load(),
	}
}
