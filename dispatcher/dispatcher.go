// Package dispatcher provides a task dispatcher for executing jobs with worker pool pattern.
// It manages job submission, processing, retries, and collection of execution statistics.
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime/debug"
	"sync"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/internal/logger"
	"github.com/zt4ff/gokue/job"
	"github.com/zt4ff/gokue/stats"
)

// ShutdownMode selects how CloseWithMode shuts the dispatcher down.
type ShutdownMode string

const (
	// DrainMode waits for all in-flight and queued jobs to complete before returning.
	// New submissions are rejected immediately upon Close call.
	// This is the recommended mode for graceful shutdown scenarios.
	DrainMode ShutdownMode = "drain"

	// ImmediateMode stops accepting new jobs and returns without waiting for
	// in-flight jobs to complete. Workers finish their current attempt in the
	// background; jobs still waiting in the queue are dropped and counted in
	// the Dropped statistic. Use only for emergency shutdown.
	ImmediateMode ShutdownMode = "immediate"
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
	// ErrInvalidShutdownMode is returned when an unknown shutdown mode is provided.
	ErrInvalidShutdownMode = errors.New("invalid shutdown mode")
	// ErrInvalidTask is returned when a task carries an invalid per-task override.
	ErrInvalidTask = errors.New("invalid task")
)

// Task represents a unit of work to be processed by the dispatcher.
type Task struct {
	// Name is the identifier for this task, used in error reporting.
	Name string
	// Job is the job implementation to be executed.
	Job job.Job
	// SubmittedAt is the timestamp when the task was submitted.
	SubmittedAt time.Time
	// MaxRetries overrides the global MaxRetries for this task when non-nil.
	MaxRetries *int
	// RetryDelay overrides the global RetryDelay for this task when non-nil.
	RetryDelay *time.Duration
	// RetryPredicates overrides the global RetryPredicates for this task when non-nil.
	RetryPredicates func(error) bool

	// ctx carries the submit context's values (not its cancellation) to the job.
	ctx context.Context
}

// PanicError is the error recorded when a job's Process method panics.
type PanicError struct {
	// Job is the name of the job that panicked.
	Job string
	// Value is the value passed to panic.
	Value interface{}
	// Stack is the goroutine stack trace captured when the panic was recovered.
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("job %q panic: %v", e.Job, e.Value)
}

// Dispatcher manages a pool of workers to execute tasks with configurable retry logic
// and statistics collection.
//
// Concurrency Model:
//   - Workers: WorkerCount goroutines continuously read from the tasks channel.
//   - Submit: Synchronized with submitMu to prevent submissions after Close.
//   - Close: Blocks until all workers have finished processing (drain mode).
//   - Quit Channel: Signals workers to abandon retry delays and proceed gracefully.
//
// Shutdown Behavior:
//   - Drain Mode (default): Close blocks until all in-flight and queued jobs finish.
//     Returns ctx.Err() if the wait times out. Workers continue silently if context
//     is cancelled before completion; callers must not tear down shared resources.
//   - Immediate Mode: Close returns quickly without waiting for workers to complete.
//     Useful for emergency shutdowns but leaves goroutines running.
type Dispatcher struct {
	// cfg holds the dispatcher configuration.
	cfg config.Config
	// collector tracks execution statistics.
	collector *stats.Collector
	// logger provides structured logging for observability.
	logger logger.Logger
	// tasks is the channel for submitting tasks to workers.
	tasks chan Task
	// quit is closed when the dispatcher shuts down, interrupting in-progress retry sleeps.
	quit chan struct{}
	// quitOnce ensures quit is closed exactly once across multiple Close calls and modes.
	quitOnce sync.Once
	// closing is closed as soon as Close is called so that submitters blocked on a
	// full queue are released before Close acquires submitMu.
	closing chan struct{}
	// closingOnce ensures closing is closed exactly once.
	closingOnce sync.Once

	// submitMu protects the closed flag.
	submitMu sync.RWMutex
	// closed indicates whether the dispatcher has been closed.
	closed bool
	// wg tracks all active worker goroutines.
	wg sync.WaitGroup
}

// signalQuit closes the quit channel exactly once, interrupting any in-progress
// retry sleeps so workers can abandon remaining retries and exit promptly.
func (d *Dispatcher) signalQuit() {
	d.quitOnce.Do(func() {
		close(d.quit)
	})
}

// New creates a new Dispatcher with the given configuration and optional statistics collector.
// If collector is nil, a new Collector is created. It starts the configured number of worker goroutines.
// If logger is nil, logging is disabled.
func NewDispatcher(cfg config.Config, collector *stats.Collector) *Dispatcher {
	if collector == nil {
		collector = stats.NewCollector() // WIP users should provide their collector function and handle it themselve
	}

	d := &Dispatcher{
		cfg:       cfg,
		collector: collector,
		logger:    cfg.Logger,
		tasks:     make(chan Task, cfg.QueueSize),
		quit:      make(chan struct{}),
		closing:   make(chan struct{}),
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
	task, err := prepareTask(ctx, task)
	if err != nil {
		return err
	}

	d.submitMu.RLock()
	defer d.submitMu.RUnlock()

	if d.closed {
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonDispatcherClosed)
		return ErrClosed
	}

	select {
	case d.tasks <- task:
		d.collector.IncEnqueued()
		logger.LogSubmitEnqueued(d.logger, task.Name)
		return nil
	case <-ctx.Done():
		d.collector.IncDropped()
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonContextCancelled)
		return ctx.Err()
	case <-d.closing:
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonDispatcherClosed)
		return ErrClosed
	}
}

// prepareTask validates task and fills in its derived defaults. The job's
// context inherits ctx's values but not its cancellation or deadline, since the
// job runs after Submit returns.
func prepareTask(ctx context.Context, task Task) (Task, error) {
	if task.Job == nil {
		return task, ErrNilJob
	}
	if task.MaxRetries != nil && *task.MaxRetries < 0 {
		return task, fmt.Errorf("%w: max retries cannot be negative", ErrInvalidTask)
	}
	if task.RetryDelay != nil && *task.RetryDelay < 0 {
		return task, fmt.Errorf("%w: retry delay cannot be negative", ErrInvalidTask)
	}
	if task.Name == "" {
		task.Name = job.NameOf(task.Job)
	}
	if task.SubmittedAt.IsZero() {
		task.SubmittedAt = time.Now().UTC()
	}
	task.ctx = context.WithoutCancel(ctx)
	return task, nil
}

// TrySubmit attempts to add a task to the dispatcher's queue without blocking.
// It returns ErrQueueFull if the queue is at capacity, ErrClosed if the dispatcher is closed,
// or ErrNilJob/ErrNilCtx if the job or context is nil.
func (d *Dispatcher) TrySubmit(ctx context.Context, task Task) error {
	if ctx == nil {
		return ErrNilCtx
	}
	task, err := prepareTask(ctx, task)
	if err != nil {
		return err
	}

	d.submitMu.RLock()
	defer d.submitMu.RUnlock()

	if d.closed {
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonDispatcherClosed)
		return ErrClosed
	}

	select {
	case d.tasks <- task:
		d.collector.IncEnqueued()
		logger.LogSubmitEnqueued(d.logger, task.Name)
		return nil
	default:
		d.collector.IncDropped()
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonQueueFull)
		return ErrQueueFull
	}
}

// Close gracefully shuts down the dispatcher in drain mode, waiting for all workers
// to finish processing their current tasks. It blocks until all workers have completed
// or the context is cancelled.
//
// Behavior (Drain Mode):
//   - Immediately rejects new submissions with ErrClosed.
//   - Closes the tasks channel to indicate no more work will arrive.
//   - Blocks waiting for all workers to finish draining the task queue,
//     including any in-flight retries and their backoff delays.
//   - Returns nil if all workers complete before context cancellation.
//   - Returns ctx.Err() if context is cancelled during the wait. On cancellation
//     the quit channel is closed so workers abandon remaining retry sleeps, drop
//     jobs still waiting in the queue, and can exit promptly; workers may still
//     be finishing their current attempt when Close returns.
//
// Important: If ctx is cancelled before workers finish, Close returns ctx.Err()
// but workers continue running in the background until they complete. Callers
// must not tear down shared resources (e.g., the stats collector) immediately
// after a cancelled Close. Use the returned error to distinguish between a
// successful close and a timeout.
//
// For emergency shutdown with immediate return, use CloseWithMode(ctx, ImmediateMode).
func (d *Dispatcher) Close(ctx context.Context) error {
	return d.CloseWithMode(ctx, DrainMode)
}

// CloseWithMode gracefully or immediately shuts down the dispatcher based on the mode.
//
// Modes:
//   - DrainMode: Waits for all in-flight and queued jobs to complete, including
//     retries and their backoff delays. Returns ctx.Err() if the wait times out.
//     If the context is cancelled, retry sleeps are interrupted so workers can
//     abandon remaining retries and exit.
//   - ImmediateMode: Returns immediately without waiting for workers to complete.
//     Retry sleeps are interrupted so workers finish their current attempt and
//     stop instead of continuing to retry. Queued jobs are dropped.
//
// Behavior (both modes):
//   - Rejects new submissions with ErrClosed.
//   - Closes the tasks channel.
//
// Drain Mode specifics:
//   - Blocks until all workers finish or context is cancelled.
//   - Returns nil on successful completion.
//   - Returns ctx.Err() if context is cancelled.
//
// Immediate Mode specifics:
//   - Returns immediately with nil.
//   - Background workers continue until they finish current tasks.
//   - Caller must not tear down shared resources immediately.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := d.CloseWithMode(ctx, DrainMode); err != nil {
//	    log.Printf("close timeout: %v", err)
//	    // Attempt immediate shutdown
//	    d.CloseWithMode(context.Background(), ImmediateMode)
//	}
func (d *Dispatcher) CloseWithMode(ctx context.Context, mode ShutdownMode) error {
	if ctx == nil {
		return ErrNilCtx
	}

	// Validate shutdown mode
	if mode != DrainMode && mode != ImmediateMode {
		return ErrInvalidShutdownMode
	}

	startTime := time.Now()
	logger.LogCloseStart(d.logger, string(mode))

	// Release submitters blocked on a full queue before taking the write lock;
	// otherwise Close would wait on them regardless of ctx.
	d.closingOnce.Do(func() {
		close(d.closing)
	})

	d.submitMu.Lock()
	if !d.closed {
		d.closed = true
		close(d.tasks) // stop accepting new tasks; workers drain the rest
	}
	d.submitMu.Unlock()

	// For immediate mode, signal workers to abandon retries and return without waiting.
	if mode == ImmediateMode {
		d.signalQuit()
		duration := time.Since(startTime)
		logger.LogCloseComplete(d.logger, string(mode), duration, nil)
		return nil
	}

	// For drain mode, wait for all workers to finish. Retries and their backoff
	// delays are allowed to complete so the queue truly drains.
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		duration := time.Since(startTime)
		logger.LogCloseComplete(d.logger, string(mode), duration, nil)
		return nil
	case <-ctx.Done():
		// Interrupt in-flight retry sleeps so workers can abandon remaining
		// retries and exit instead of running out the full backoff schedule.
		d.signalQuit()
		duration := time.Since(startTime)
		logger.LogCloseComplete(d.logger, string(mode), duration, ctx.Err())
		return ctx.Err()
	}
}

// Stats returns a snapshot of the current execution statistics.
func (d *Dispatcher) Stats() stats.Snapshot {
	return d.collector.Snapshot()
}

// worker processes tasks from the dispatcher's queue until it is closed.
// Once quit is signalled, tasks still waiting in the queue are dropped instead
// of executed.
func (d *Dispatcher) worker() {
	defer d.wg.Done()

	for task := range d.tasks {
		select {
		case <-d.quit:
			d.collector.IncDropped()
			logger.LogJobAbandoned(d.logger, task.Name, 0, logger.ReasonDispatcherShutdown, 0)
			continue
		default:
		}
		d.safeExecute(task)
	}
}

// safeExecute runs execute and recovers from panics in the dispatcher's own
// logic (not the job, which runJob already guards) so that the worker keeps
// serving the queue instead of silently shrinking the pool.
func (d *Dispatcher) safeExecute(task Task) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogWorkerPanic(d.logger, recovered)
		}
	}()
	d.execute(task)
}

// execute runs a task with retry logic based on the dispatcher's configuration.
// It attempts to execute the job up to MaxRetries times with configurable backoff delays.
// Per-task MaxRetries and RetryDelay overrides are honored when set on the Task.
// The retry sleep is interruptible via the dispatcher's quit channel so that Close
// is responsive. Statistics are updated after each execution attempt.
//
// IncRetried tracks individual retry attempts, not retried tasks — a task retried
// 3 times increments the counter 3 times.
func (d *Dispatcher) execute(task Task) {
	startTime := time.Now()
	var err error

	maxRetries := d.cfg.MaxRetries
	if task.MaxRetries != nil {
		maxRetries = *task.MaxRetries
	}

	retryBaseDelay := d.cfg.RetryDelay
	if task.RetryDelay != nil {
		retryBaseDelay = *task.RetryDelay
	}

	retryPredicates := d.cfg.RetryPredicates
	if task.RetryPredicates != nil {
		retryPredicates = task.RetryPredicates
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		logger.LogJobProcessing(d.logger, task.Name, attempt+1)

		err = runJob(task.ctx, task.Name, task.Job, d.cfg.JobTimeout)
		var panicErr *PanicError
		if errors.As(err, &panicErr) {
			logger.LogJobPanic(d.logger, task.Name, attempt+1, panicErr.Value, panicErr.Stack)
		}
		if err == nil {
			duration := time.Since(startTime)
			d.collector.IncProcessed()
			logger.LogJobSuccess(d.logger, task.Name, duration)
			return
		}

		// The predicate only decides whether to retry, so skip it when no
		// retry remains.
		if attempt < maxRetries && retryPredicates != nil && !retryPredicates(err) {
			// retry predicate blocked retries; mark final failure immediately
			duration := time.Since(startTime)
			d.collector.IncRetryPredicatesFailed()
			d.collector.IncFailed()
			logger.LogJobFailure(d.logger, task.Name, attempt+1, err, duration)
			return
		}

		if attempt < maxRetries {
			d.collector.IncRetried()
			delay := retryDelay(retryBaseDelay, d.cfg.MaxRetryDelay, attempt, d.cfg.BackoffStrategy)
			logger.LogJobRetry(d.logger, task.Name, attempt+1, err, delay)

			if delay > 0 {
				select {
				case <-time.After(delay):
					// delay elapsed, continue to next attempt
				case <-d.quit:
					// dispatcher is shutting down; abandon remaining retries
					d.collector.IncFailed()
					duration := time.Since(startTime)
					logger.LogJobAbandoned(d.logger, task.Name, attempt+1, logger.ReasonDispatcherShutdown, duration)
					return
				}
			}
		}
	}

	// All retries exhausted
	duration := time.Since(startTime)
	d.collector.IncFailed()
	logger.LogJobFailure(d.logger, task.Name, maxRetries+1, err, duration)
}

// runJob executes a job with the specified timeout and recovers from any panics.
// It derives a cancellable context from parent (or context.Background if nil),
// optionally bounded by timeout, and calls the job's Process method. A panic is
// returned as a *PanicError carrying the stack trace.
func runJob(parent context.Context, name string, j job.Job, timeout time.Duration) (err error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Job: name, Value: recovered, Stack: debug.Stack()}
		}
	}()

	return j.Process(ctx)
}

// retryDelay calculates the delay before the next retry attempt based on the chosen
// backoff strategy. Every strategy is capped at maxDelay (no cap if <= 0).
//
//   - Constant:          min(maxDelay, base)
//   - Linear:            min(maxDelay, base * (attempt + 1))  →  1x, 2x, 3x, capped, ...
//   - Exponential:       min(maxDelay, base * 2^attempt)  →  1x, 2x, 4x, capped, ...
//   - ExponentialJitter: random(0, min(maxDelay, base * 2^attempt))  (full jitter)
//
// Returns 0 if base <= 0.
//
// Note: math/rand global functions are safe for concurrent use in Go 1.20+.
// For older Go versions, supply a locked per-worker rand.Rand source instead.
func retryDelay(base time.Duration, maxDelay time.Duration, attempt int, strategy config.BackoffStrategy) time.Duration {
	if base <= 0 {
		return 0
	}

	switch strategy {
	case config.Constant:
		return capDelay(base, maxDelay)

	case config.Linear:
		// attempt is 0-indexed; use attempt+1 so the first retry is never 0.
		return capDelay(saturatingDuration(float64(base)*float64(attempt+1)), maxDelay)

	case config.Exponential:
		// base * 2^attempt: 1x, 2x, 4x, 8x, ..., capped at maxDelay.
		return cappedExponential(base, maxDelay, attempt)

	case config.ExponentialJitter:
		// Full jitter: random delay uniformly drawn from [0, capped_exp) so it
		// scales with the exponential component and spreads retries under load.
		exp := cappedExponential(base, maxDelay, attempt)
		if exp <= 0 {
			return 0
		}
		return time.Duration(rand.Int63n(int64(exp)))

	default:
		return 0
	}
}

// capDelay returns d limited to maxDelay when maxDelay > 0.
func capDelay(d, maxDelay time.Duration) time.Duration {
	if maxDelay > 0 && d > maxDelay {
		return maxDelay
	}
	return d
}

// cappedExponential returns base * 2^attempt, capped at maxDelay when
// maxDelay > 0 and saturated at the largest representable time.Duration.
func cappedExponential(base, maxDelay time.Duration, attempt int) time.Duration {
	exp := float64(base) * math.Pow(2, float64(attempt))
	if maxDelay > 0 && exp > float64(maxDelay) {
		return maxDelay
	}
	return saturatingDuration(exp)
}

// saturatingDuration converts d to a time.Duration, clamping values that would
// overflow int64 instead of wrapping to a negative duration.
func saturatingDuration(d float64) time.Duration {
	if d >= math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(d)
}
