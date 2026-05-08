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
	// Name is the identifier for this task.
	Name string
	// Job is the job implementation to be executed.
	Job job.Job
	// SubmittedAt is the timestamp when the task was submitted.
	SubmittedAt time.Time
}

// Dispatcher manages a pool of workers to execute tasks with configurable retry logic and statistics collection.
type Dispatcher struct {
	// cfg holds the dispatcher configuration.
	cfg config.Config
	// collector tracks execution statistics.
	collector *stats.Collector
	// tasks is the channel for submitting tasks to workers.
	tasks chan Task

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
	}

	for worker := 0; worker < cfg.WorkerCount; worker++ {
		d.wg.Add(1)
		go d.worker()
	}

	return d
}

// Submit adds a task to the dispatcher's queue, blocking until the task is enqueued or the context is cancelled.
// It validates that the context and job are not nil and returns ErrClosed if the dispatcher is closed.
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

// Close gracefully shuts down the dispatcher, waiting for all workers to finish processing their current tasks.
// It blocks until all workers have completed or the context is cancelled.
func (d *Dispatcher) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilCtx
	}

	d.submitMu.Lock()
	if !d.closed {
		d.closed = true
		close(d.tasks)
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
// It continuously calls execute on each received task.
func (d *Dispatcher) worker() {
	defer d.wg.Done()

	for task := range d.tasks {
		d.execute(task)
	}
}

// execute runs a task with retry logic based on the dispatcher's configuration.
// It attempts to execute the job up to MaxRetries times with exponential backoff delays.
// Statistics are updated after each execution attempt.
func (d *Dispatcher) execute(task Task) {
	var err error
	for attempt := 0; attempt <= d.cfg.MaxRetries; attempt++ {
		err = runJob(task.Job, d.cfg.JobTimeout)
		if err == nil {
			d.collector.IncProcessed()
			return
		}

		if attempt < d.cfg.MaxRetries {
			d.collector.IncRetried()
			if delay := retryDelay(d.cfg.RetryDelay, attempt, d.cfg.BackoffStrategy); delay > 0 {
				time.Sleep(delay)
			}
		}
	}

	d.collector.IncFailed()
}

// runJob executes a job with the specified timeout and recovers from any panics.
// It creates a context with the given timeout and calls the job's Process method.
func runJob(task job.Job, timeout time.Duration) (err error) {
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job panic: %v", recovered)
		}
	}()

	return task.Process(ctx)
}

// retryDelay calculates the exponential backoff delay for the given attempt number.
// Returns 0 if base delay is <= 0, otherwise returns base * (attempt + 1).
func retryDelay(base time.Duration, attempt int, strategy string) time.Duration {
	if base <= 0 {
		return 0
	}

	switch strategy {
	case config.Constant:
		return base

	case config.Linear:
		return base * time.Duration(attempt+1)

	case config.Exponential:
		multiplier := math.Pow(2, float64(attempt))
		return time.Duration(float64(base) * multiplier)

	case config.ExponentialJitter:
		multiplier := math.Pow(2, float64(attempt))
		exp := time.Duration(float64(base) * multiplier)
		jitter := time.Duration(rand.Int63n(int64(base)))
		return exp + jitter

	default:
		return 0
	}
}
