// Package dispatcher runs gokue's worker pool. It is internal; use the gokue package.
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
	"sync/atomic"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/config"
	"github.com/zt4ff/gokue/v2/internal/logger"
	"github.com/zt4ff/gokue/v2/job"
	"github.com/zt4ff/gokue/v2/stats"
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
	// ErrNotPersistent is returned by DeadJobs and Replay on a dispatcher
	// without a store.
	ErrNotPersistent = errors.New("queue has no store")
	// ErrNotFailed is returned by Replay for a record that is not failed.
	ErrNotFailed = errors.New("job is not failed")
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
	// RunAt, when in the future, is the earliest time the task may start.
	RunAt time.Time
	// Delay, when non-nil, sets RunAt to SubmittedAt plus Delay.
	Delay *time.Duration

	// attempt is the 0-indexed attempt the task runs next, lastErr the error
	// of the previous attempt and firstStart the start of attempt 0.
	attempt    int
	lastErr    error
	firstStart time.Time
	// holdsSlot reports that the task occupies a queue slot until a worker
	// takes it.
	holdsSlot bool

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
	// running is the number of tasks currently executing (live gauge).
	running atomic.Int64
	// liveWorkers is the number of worker goroutines still running (live gauge).
	liveWorkers atomic.Int64
	// logger provides structured logging for observability.
	logger logger.Logger
	// tasks carries tasks that are ready to run to workers.
	tasks chan Task
	// slots limits accepted tasks waiting to start, ready or delayed, to
	// QueueSize. Retries do not take a slot.
	slots chan struct{}
	// schedule passes delayed tasks and retries to the scheduler, which sends
	// them on due once their RunAt has passed.
	schedule chan Task
	due      chan Task
	// scheduled is the number of tasks held by the scheduler (live gauge).
	scheduled atomic.Int64
	// work counts accepted in-memory tasks that have not finished.
	work sync.WaitGroup
	// quit is closed when the dispatcher shuts down, interrupting in-progress retry sleeps.
	quit chan struct{}
	// quitOnce ensures quit is closed exactly once across multiple Close calls and modes.
	quitOnce sync.Once
	// closing is closed as soon as Close is called so that submitters blocked on a
	// full queue are released before Close acquires submitMu.
	closing chan struct{}
	// closingOnce ensures closing is closed exactly once.
	closingOnce sync.Once

	// store and codec are set in persistent mode: jobs are saved to store and
	// workers claim them from it instead of reading the tasks channel.
	store backend.JobStore
	codec *backend.Codec
	// wake nudges idle persistent workers when a job is saved.
	wake chan struct{}
	// local holds per-submit overrides of jobs saved by this process, by job ID.
	local sync.Map

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
		quit:      make(chan struct{}),
		closing:   make(chan struct{}),
	}

	if cfg.Store != nil {
		d.store = cfg.Store
		d.codec = cfg.Codec
		d.tasks = make(chan Task)
		d.wake = make(chan struct{}, cfg.WorkerCount)
		go d.reaper()
	} else {
		d.tasks = make(chan Task, cfg.QueueSize)
		d.slots = make(chan struct{}, cfg.QueueSize)
		d.schedule = make(chan Task)
		d.due = make(chan Task)
		d.wg.Add(1)
		go d.scheduler()
	}

	for worker := 0; worker < cfg.WorkerCount; worker++ {
		d.wg.Add(1)
		d.liveWorkers.Add(1)
		if d.store != nil {
			go d.storeWorker()
		} else {
			go d.worker()
		}
	}

	return d
}

// Submit adds a task to the dispatcher's queue, blocking until the task is enqueued or
// the context is cancelled. It validates that the context and job are not nil and returns
// ErrClosed if the dispatcher is closed. A task with a future RunAt is held
// until then and takes a queue slot meanwhile.
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
	if d.store != nil {
		return d.save(ctx, task)
	}

	select {
	case d.slots <- struct{}{}:
	case <-ctx.Done():
		d.collector.IncDropped()
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonContextCancelled)
		return ctx.Err()
	case <-d.closing:
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonDispatcherClosed)
		return ErrClosed
	}
	d.enqueue(task)
	return nil
}

// enqueue hands an accepted task, which holds a slot, to the workers or the
// scheduler. The caller holds submitMu with closed false, so neither has quit
// and neither send blocks for long: tasks has room for every slot and the
// scheduler always accepts.
func (d *Dispatcher) enqueue(task Task) {
	task.holdsSlot = true
	d.work.Add(1)
	d.collector.IncEnqueued()
	logger.LogSubmitEnqueued(d.logger, task.Name)
	if task.RunAt.After(time.Now()) {
		// Counted before the hand-off so Stats sees it once Submit returns.
		d.scheduled.Add(1)
		d.schedule <- task
		return
	}
	d.tasks <- task
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
	if task.Delay != nil && *task.Delay < 0 {
		return task, fmt.Errorf("%w: delay cannot be negative", ErrInvalidTask)
	}
	if task.Name == "" {
		task.Name = job.NameOf(task.Job)
	}
	if task.SubmittedAt.IsZero() {
		task.SubmittedAt = time.Now().UTC()
	}
	if task.Delay != nil {
		task.RunAt = task.SubmittedAt.Add(*task.Delay)
		task.Delay = nil
	}
	task.attempt, task.lastErr, task.firstStart, task.holdsSlot = 0, nil, time.Time{}, false
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
	if d.store != nil {
		return d.save(ctx, task)
	}

	select {
	case d.slots <- struct{}{}:
		d.enqueue(task)
		return nil
	default:
		d.collector.IncDropped()
		logger.LogSubmitRejected(d.logger, task.Name, logger.ReasonQueueFull)
		return ErrQueueFull
	}
}

// Close gracefully shuts down the dispatcher in drain mode. It blocks until
// every accepted job has finished, including delayed jobs and retries waiting
// for their backoff, or until ctx is done.
//
// Behavior (Drain Mode):
//   - Immediately rejects new submissions with ErrClosed.
//   - Returns nil once nothing is queued, running or scheduled.
//   - Returns ctx.Err() if ctx is done first. The quit channel is then closed:
//     scheduled jobs and jobs still waiting in the queue are dropped, pending
//     retries are abandoned, and workers exit after their current attempt,
//     which may still be running when Close returns.
//
// Persistent dispatchers do not wait for scheduled or queued records: they
// stay in the store for the next process. Close only waits for running jobs.
//
// Callers must not tear down shared resources (e.g., the stats collector)
// immediately after a cancelled Close.
//
// For emergency shutdown with immediate return, use CloseWithMode(ctx, ImmediateMode).
func (d *Dispatcher) Close(ctx context.Context) error {
	return d.CloseWithMode(ctx, DrainMode)
}

// CloseWithMode gracefully or immediately shuts down the dispatcher based on the mode.
//
// Modes:
//   - DrainMode: as described on Close.
//   - ImmediateMode: returns immediately. Workers finish their current attempt
//     and stop; queued and scheduled jobs are dropped and pending retries are
//     abandoned.
//
// Both modes reject new submissions with ErrClosed.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := d.CloseWithMode(ctx, DrainMode); err != nil {
//	    log.Printf("close timeout: %v", err)
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
	d.closed = true
	d.submitMu.Unlock()

	if mode == ImmediateMode {
		d.signalQuit()
		duration := time.Since(startTime)
		logger.LogCloseComplete(d.logger, string(mode), duration, nil)
		return nil
	}

	// No task is accepted from here on, so work only goes down.
	done := make(chan struct{})
	go func() {
		d.work.Wait()
		d.signalQuit()
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		duration := time.Since(startTime)
		logger.LogCloseComplete(d.logger, string(mode), duration, nil)
		return nil
	case <-ctx.Done():
		d.signalQuit()
		duration := time.Since(startTime)
		logger.LogCloseComplete(d.logger, string(mode), duration, ctx.Err())
		return ctx.Err()
	}
}

// Stats returns a snapshot of the current execution statistics.
// The live gauges QueueDepth, Scheduled, Running and IdleWorkers reflect the
// moment of the call.
func (d *Dispatcher) Stats() stats.Snapshot {
	s := d.collector.Snapshot()
	running := d.running.Load()
	s.QueueDepth = len(d.tasks)
	s.Scheduled = int(d.scheduled.Load())
	s.Running = int(running)
	if idle := d.liveWorkers.Load() - running; idle > 0 {
		s.IdleWorkers = int(idle)
	}
	return s
}

// Backlog is the number of accepted in-memory jobs that have not started,
// queued or delayed, which together are limited to QueueSize. It is 0 for a
// persistent dispatcher.
func (d *Dispatcher) Backlog() int {
	return len(d.slots)
}

// worker runs ready tasks and tasks the scheduler hands over until quit.
// Once quit is signalled, tasks still waiting are dropped instead of executed.
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	defer d.liveWorkers.Add(-1)

	for {
		var task Task
		select {
		case task = <-d.tasks:
		case task = <-d.due:
		case <-d.quit:
			d.dropQueued()
			return
		}
		select {
		case <-d.quit:
			d.abandon(task)
			continue
		default:
		}
		d.releaseSlot(&task)
		d.running.Add(1)
		d.run(task)
		d.running.Add(-1)
	}
}

// run executes one attempt of task. A retry goes to the scheduler, so the
// worker is free during the backoff; once shutdown has started it is
// abandoned instead.
func (d *Dispatcher) run(task Task) {
	if task.firstStart.IsZero() {
		task.firstStart = time.Now()
	}
	result, delay, err := d.safeExecute(task)
	if result != outcomeRetry {
		d.work.Done()
		return
	}
	task.attempt++
	task.lastErr = err
	task.RunAt = time.Now().Add(delay)
	d.scheduled.Add(1)
	select {
	case <-d.quit:
	default:
		select {
		case d.schedule <- task:
			return
		case <-d.quit:
		}
	}
	d.scheduled.Add(-1)
	d.abandon(task)
}

// safeExecute runs execute and recovers from panics in the dispatcher's own
// logic (not the job, which runJob already guards) so that the worker keeps
// serving the queue instead of silently shrinking the pool.
func (d *Dispatcher) safeExecute(task Task) (result outcome, delay time.Duration, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogWorkerPanic(d.logger, recovered)
			result, delay, err = outcomeFailed, 0, fmt.Errorf("gokue: dispatcher panic: %v", recovered)
		}
	}()
	return d.execute(task)
}

// outcome is how one attempt of a task finished.
type outcome int

const (
	outcomeSucceeded outcome = iota
	outcomeFailed
	// outcomeRetry means the attempt failed and the task should run again
	// after the returned delay.
	outcomeRetry
)

// execute runs attempt task.attempt of task and records its statistics,
// logs and hooks. On a retryable failure it returns outcomeRetry and the
// backoff before the next attempt; the caller schedules it. Per-task
// MaxRetries, RetryDelay and RetryPredicates overrides are honored.
//
// IncRetried tracks individual retry attempts, not retried tasks — a task retried
// 3 times increments the counter 3 times.
func (d *Dispatcher) execute(task Task) (outcome, time.Duration, error) {
	startTime := task.firstStart
	if startTime.IsZero() {
		startTime = time.Now()
	}
	attempt := task.attempt

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

	logger.LogJobProcessing(d.logger, task.Name, attempt+1)

	err := runJob(task.ctx, task.Name, task.Job, d.cfg.JobTimeout)
	var panicErr *PanicError
	if errors.As(err, &panicErr) {
		logger.LogJobPanic(d.logger, task.Name, attempt+1, panicErr.Value, panicErr.Stack)
	}
	if err == nil {
		duration := time.Since(startTime)
		d.collector.IncProcessed()
		logger.LogJobSuccess(d.logger, task.Name, duration)
		d.callHook(d.cfg.OnSuccess, config.JobEvent{Name: task.Name, Attempt: attempt + 1, Duration: duration})
		return outcomeSucceeded, 0, nil
	}

	// The predicate only decides whether to retry, so skip it when no
	// retry remains.
	if attempt < maxRetries && retryPredicates != nil && !retryPredicates(err) {
		duration := time.Since(startTime)
		d.collector.IncRetryPredicatesFailed()
		d.collector.IncFailed()
		logger.LogJobFailure(d.logger, task.Name, attempt+1, err, duration)
		d.callHook(d.cfg.OnFailure, config.JobEvent{Name: task.Name, Attempt: attempt + 1, Err: err, Duration: duration})
		return outcomeFailed, 0, err
	}

	if attempt < maxRetries {
		d.collector.IncRetried()
		delay := retryDelay(retryBaseDelay, d.cfg.MaxRetryDelay, attempt, d.cfg.BackoffStrategy)
		logger.LogJobRetry(d.logger, task.Name, attempt+1, err, delay)
		d.callHook(d.cfg.OnRetry, config.JobEvent{Name: task.Name, Attempt: attempt + 1, Err: err, Duration: time.Since(startTime), RetryDelay: delay})
		return outcomeRetry, delay, err
	}

	duration := time.Since(startTime)
	d.collector.IncFailed()
	logger.LogJobFailure(d.logger, task.Name, attempt+1, err, duration)
	d.callHook(d.cfg.OnFailure, config.JobEvent{Name: task.Name, Attempt: attempt + 1, Err: err, Duration: duration})
	return outcomeFailed, 0, err
}

// callHook invokes a lifecycle hook, recovering from panics so a faulty hook
// cannot change the job's outcome or kill the worker.
func (d *Dispatcher) callHook(hook func(config.JobEvent), event config.JobEvent) {
	if hook == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogWorkerPanic(d.logger, recovered)
		}
	}()
	hook(event)
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
