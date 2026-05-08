// Package dispatcher provides a task dispatcher for executing jobs with worker pool pattern.
// It manages job submission, processing, retries, and collection of execution statistics.
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/job"
	"github.com/zt4ff/gokue/stats"
)

var (
	// ErrClosed is returned when attempting to submit to a closed dispatcher.
	ErrClosed = errors.New("dispatcher is closed")
	// ErrQueueFull is returned when the task queue is at capacity in TrySubmit.
	ErrQueueFull = errors.New("dispatcher queue is full")
	// ErrNilJob is returned when a nil job is submitted.
	ErrNilJob = errors.New("job cannot be nil")
	// ErrNilCtx is returned when a nil context is provided.
	ErrNilCtx = errors.New("context cannot be nil")
)

// Task represents a unit of work to be processed by the dispatcher.
type Task struct {
	// Name is the identifier for this task, used in error reporting.
	Name string
	// Job is the job implementation to be executed.
	Job job.Job
	// SubmittedAt is the timestamp when the task was submitted.
	SubmittedAt time.Time
}

// Dispatcher manages a pool of workers to execute tasks with configurable retry logic
// and statistics collection.
type Dispatcher struct {
	// cfg holds the dispatcher configuration.
	cfg config.Config
	// collector tracks execution statistics.
	collector *stats.Collector
	// tasks is the channel for submitting tasks to workers.
	tasks chan Task
	// quit is closed when the dispatcher shuts down, interrupting in-progress retry sleeps.
	quit chan struct{}

	// submitMu protects the closed flag.
	submitMu sync.RWMutex
	// closed indicates whether the dispatcher has been closed.
	closed bool
	// wg tracks all active worker goroutines.
	wg sync.WaitGroup
}

// New creates a new Dispatcher with the given configuration and optional statistics collector.
// If collector is nil, a new Collector is created. It starts the configured number of worker goroutines.
func New(cfg config.Config, collector *stats.Collector) *Dispatcher {
	if collector == nil {
		collector = stats.NewCollector()
	}

	d := &Dispatcher{
		cfg:       cfg,
		collector: collector,
		tasks:     make(chan Task, cfg.QueueSize),
		quit:      make(chan struct{}),
	}

	for worker := 0; worker < cfg.WorkerCount; worker++ {
		d.wg.Add(1)
		go d.worker()
	}

	return d
}

// Submit adds a task to the dispatcher's queue, blocking until the task is enqueued or
// the context is cancelled. It validates that the context and job are not nil and returns
// ErrClosed if the dispatcher is closed.
func (d *Dispatcher) Submit(ctx context.Context, task Task) error {
	if ctx == nil {
		return ErrNilCtx
	}
	if task.Job == nil {
		return ErrNilJob
	}
	if task.SubmittedAt.IsZero() {
		task.SubmittedAt = time.Now().UTC()
	}

	d.submitMu.RLock()
	defer d.submitMu.RUnlock()

	if d.closed {
		return ErrClosed
	}

	select {
	case d.tasks <- task:
		d.collector.IncEnqueued()
		return nil
	case <-ctx.Done():
		d.collector.IncDropped()
		return ctx.Err()
	}
}

// TrySubmit attempts to add a task to the dispatcher's queue without blocking.
// It returns ErrQueueFull if the queue is at capacity, ErrClosed if the dispatcher is closed,
// or ErrNilJob/ErrNilCtx if the job or context is nil.
func (d *Dispatcher) TrySubmit(ctx context.Context, task Task) error {
	if ctx == nil {
		return ErrNilCtx
	}
	if task.Job == nil {
		return ErrNilJob
	}
	if task.SubmittedAt.IsZero() {
		task.SubmittedAt = time.Now().UTC()
	}

	d.submitMu.RLock()
	defer d.submitMu.RUnlock()

	if d.closed {
		return ErrClosed
	}

	select {
	case d.tasks <- task:
		d.collector.IncEnqueued()
		return nil
	default:
		d.collector.IncDropped()
		return ErrQueueFull
	}
}

// Close gracefully shuts down the dispatcher, waiting for all workers to finish processing
// their current tasks. It blocks until all workers have completed or the context is cancelled.
// Note: if ctx is cancelled before workers finish, Close returns ctx.Err() but workers
// continue running in the background until they complete — callers should not tear down
// shared resources (e.g. the stats collector) immediately after a cancelled Close.
func (d *Dispatcher) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilCtx
	}

	d.submitMu.Lock()
	if !d.closed {
		d.closed = true
		close(d.quit)  // signal workers to stop sleeping between retries
		close(d.tasks) // stop accepting new tasks; workers drain the rest
	}
	d.submitMu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns a snapshot of the current execution statistics.
func (d *Dispatcher) Stats() stats.Snapshot {
	return d.collector.Snapshot()
}

// worker processes tasks from the dispatcher's queue until it is closed.
// It continuously calls execute on each received task and recovers from any panics
// in the dispatcher's own logic to avoid crashing the entire program.
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			// A panic in the dispatcher itself (not the job) is a programming error.
			// Log it and let this worker die gracefully rather than crashing the program.
			_ = fmt.Errorf("dispatcher worker panic: %v", recovered)
		}
	}()

	for task := range d.tasks {
		d.execute(task)
	}
}

// execute runs a task with retry logic based on the dispatcher's configuration.
// It attempts to execute the job up to MaxRetries times with configurable backoff delays.
// The retry sleep is interruptible via the dispatcher's quit channel so that Close
// is responsive. Statistics are updated after each execution attempt.
//
// IncRetried tracks individual retry attempts, not retried tasks — a task retried
// 3 times increments the counter 3 times.
func (d *Dispatcher) execute(task Task) {
	var err error
	for attempt := 0; attempt <= d.cfg.MaxRetries; attempt++ {
		err = runJob(task.Name, task.Job, d.cfg.JobTimeout)
		if err == nil {
			d.collector.IncProcessed()
			return
		}

		if attempt < d.cfg.MaxRetries {
			d.collector.IncRetried()
			delay := retryDelay(d.cfg.RetryDelay, attempt, d.cfg.BackoffStrategy)
			if delay > 0 {
				select {
				case <-time.After(delay):
					// delay elapsed, continue to next attempt
				case <-d.quit:
					// dispatcher is shutting down; abandon remaining retries
					d.collector.IncFailed()
					return
				}
			}
		}
	}

	d.collector.IncFailed()
}

// runJob executes a job with the specified timeout and recovers from any panics.
// It creates a cancellable context, optionally bounded by timeout, and calls the
// job's Process method. The task name is included in panic error messages.
func runJob(name string, j job.Job, timeout time.Duration) (err error) {
	// Always create a cancellable context so cancel is always called,
	// regardless of whether a timeout is set.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job %q panic: %v", name, recovered)
		}
	}()

	return j.Process(ctx)
}

// retryDelay calculates the delay before the next retry attempt based on the chosen
// backoff strategy:
//
//   - Constant:          base (same delay every attempt)
//   - Linear:            base * (attempt + 1)  →  1x, 2x, 3x, ...
//   - Exponential:       base * 2^attempt      →  1x, 2x, 4x, 8x, ...
//   - ExponentialJitter: Exponential delay + random jitter in [0, exp) to spread
//     load across retrying clients (full jitter strategy)
//
// Returns 0 if base <= 0.
//
// Note: math/rand global functions are safe for concurrent use in Go 1.20+.
// For older Go versions, supply a locked per-worker rand.Rand source instead.
func retryDelay(base time.Duration, attempt int, strategy string) time.Duration {
	if base <= 0 {
		return 0
	}

	switch strategy {
	case config.Constant:
		return base

	case config.Linear:
		// attempt is 0-indexed; use attempt+1 so the first retry is never 0.
		return base * time.Duration(attempt+1)

	case config.Exponential:
		// base * 2^attempt: 1x, 2x, 4x, 8x, ...
		multiplier := math.Pow(2, float64(attempt))
		return time.Duration(float64(base) * multiplier)

	case config.ExponentialJitter:
		// Full jitter: jitter is drawn from [0, exp) so it scales with the
		// exponential component, effectively spreading retries under load.
		exp := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
		jitter := time.Duration(rand.Int63n(int64(exp)))
		return exp + jitter

	default:
		return 0
	}
}
