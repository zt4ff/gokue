// Package gokue provides a job queue library for managing and executing tasks with configurable workers and retries.
package gokue

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/config"
	"github.com/zt4ff/gokue/v2/internal/dispatcher"
	"github.com/zt4ff/gokue/v2/internal/logger"
	jobpkg "github.com/zt4ff/gokue/v2/job"
	"github.com/zt4ff/gokue/v2/stats"
)

// Job is a task that can be executed by the queue.
type Job = jobpkg.Job

// PanicError is the error recorded when a job panics. Use errors.As to get the
// panic value and stack trace.
type PanicError = dispatcher.PanicError

// Logger is the structured logging interface accepted by NewQueueWithLogger.
// Implement it to route queue events to your own logging system.
type Logger = logger.Logger

// Level is the log level passed to Logger.Log.
type Level = logger.Level

// Log levels passed to Logger.Log.
const (
	LevelDebug = logger.LevelDebug
	LevelInfo  = logger.LevelInfo
	LevelWarn  = logger.LevelWarn
	LevelError = logger.LevelError
)

// Errors returned by Queue methods. Use errors.Is to match them.
var (
	// ErrNilQueue is returned when a method is called on a nil *Queue.
	ErrNilQueue = errors.New("queue is nil")
	// ErrClosed is returned when submitting to a closed queue.
	ErrClosed = dispatcher.ErrClosed
	// ErrQueueFull is returned by TrySubmit when the queue is at capacity.
	ErrQueueFull = dispatcher.ErrQueueFull
	// ErrNilJob is returned when a nil job is submitted.
	ErrNilJob = dispatcher.ErrNilJob
	// ErrNilCtx is returned when a nil context is provided.
	ErrNilCtx = dispatcher.ErrNilCtx
	// ErrInvalidTask is returned when a per-submission option is out of range.
	ErrInvalidTask = dispatcher.ErrInvalidTask
	// ErrInvalidConfig is returned (wrapped) by NewQueue for invalid configuration.
	ErrInvalidConfig = config.ErrInvalidConfig
	// ErrInvalidShutdownMode is returned by CloseWithMode for an unknown mode.
	ErrInvalidShutdownMode = dispatcher.ErrInvalidShutdownMode
	// ErrNotPersistent is returned by DeadJobs and Replay on a queue created
	// without WithStore.
	ErrNotPersistent = dispatcher.ErrNotPersistent
	// ErrNotFailed is returned by Replay when the job is not in the failed
	// state.
	ErrNotFailed = dispatcher.ErrNotFailed
)

// ShutdownMode selects how CloseWithMode treats queued and running jobs.
type ShutdownMode = dispatcher.ShutdownMode

const (
	// DrainMode processes queued jobs and in-flight retries before returning.
	// It is the mode used by Close.
	DrainMode = dispatcher.DrainMode
	// ImmediateMode stops accepting work, drops queued jobs, abandons pending
	// retries and returns without waiting for running jobs to finish.
	ImmediateMode = dispatcher.ImmediateMode
)

// Option is a functional option for configuring a Queue.
type Option func(*config.Config)

// SubmitOption is a functional option for configuring a single job submission.
type SubmitOption func(*dispatcher.Task)

// MaxRetries returns a SubmitOption that overrides the global MaxRetries for this job.
func MaxRetries(maxRetries int) SubmitOption {
	return func(t *dispatcher.Task) {
		t.MaxRetries = &maxRetries
	}
}

// RetryDelay returns a SubmitOption that overrides the global RetryDelay for this job.
func RetryDelay(delay time.Duration) SubmitOption {
	return func(t *dispatcher.Task) {
		t.RetryDelay = &delay
	}
}

// RetryIf returns a SubmitOption that overrides the queue-level retry predicate
// for this job. The job is retried only when predicate returns true.
func RetryIf(predicate func(error) bool) SubmitOption {
	return func(t *dispatcher.Task) {
		t.RetryPredicates = predicate
	}
}

// Queue manages job submission, execution, and statistics using a pool of workers.
type Queue struct {
	// config holds the queue configuration.
	config config.Config
	// dispatcher handles job execution with worker pool.
	dispatcher *dispatcher.Dispatcher
	// collector tracks execution statistics.
	collector *stats.Collector
	// closed is set once Close or CloseWithMode is called.
	closed atomic.Bool
}

// WithMongoDB sets Config.MemoryType to config.MongoDB.
//
// Deprecated: use WithStore with a mongodb.MongoJobStore. NewQueue
// returns ErrInvalidConfig when this option is used.
func WithMongoDB(cfg config.Config) Option {
	return func(target *config.Config) {
		target.MemoryType = config.MongoDB
	}
}

// WithWorkerCount returns an Option that sets the number of concurrent workers.
func WithWorkerCount(workerCount int) Option {
	return func(target *config.Config) {
		target.WorkerCount = workerCount
	}
}

// WithQueueSize returns an Option that sets the maximum queue size.
func WithQueueSize(queueSize int) Option {
	return func(target *config.Config) {
		target.QueueSize = queueSize
	}
}

// WithMaxRetries returns an Option that sets the maximum number of retries.
func WithMaxRetries(maxRetries int) Option {
	return func(target *config.Config) {
		target.MaxRetries = maxRetries
	}
}

// WithJobTimeout returns an Option that sets the job execution timeout.
func WithJobTimeout(timeout time.Duration) Option {
	return func(target *config.Config) {
		target.JobTimeout = timeout
	}
}

// WithRetryDelay returns an Option that sets the base delay between retries.
func WithRetryDelay(delay time.Duration) Option {
	return func(target *config.Config) {
		target.RetryDelay = delay
	}
}

// WithMaxRetryDelay returns an Option that sets the maximum delay between retries.
// Exponential backoff will not exceed this value. A zero or negative value means no cap.
func WithMaxRetryDelay(delay time.Duration) Option {
	return func(target *config.Config) {
		target.MaxRetryDelay = delay
	}
}

// JobEvent describes a job outcome passed to lifecycle hooks.
type JobEvent = config.JobEvent

// WithOnSuccess returns an Option that calls hook once when a job succeeds.
// Hooks run synchronously on the worker goroutine, so keep them fast; a
// panicking hook is recovered.
func WithOnSuccess(hook func(JobEvent)) Option {
	return func(target *config.Config) {
		target.OnSuccess = hook
	}
}

// WithOnFailure returns an Option that calls hook once when a job fails for
// good: retries exhausted, retry predicate returned false, or retries
// abandoned during shutdown.
func WithOnFailure(hook func(JobEvent)) Option {
	return func(target *config.Config) {
		target.OnFailure = hook
	}
}

// WithOnRetry returns an Option that calls hook before each retry of a failed
// attempt.
func WithOnRetry(hook func(JobEvent)) Option {
	return func(target *config.Config) {
		target.OnRetry = hook
	}
}

// WithStore returns an Option that makes the queue persistent. Submit saves
// each job to store (encoded with codec) and workers claim jobs from it, so
// jobs left unfinished by a crash or restart run again: delivery is at least
// once, and jobs should be idempotent.
//
// Every submitted job type must be registered in codec, otherwise Submit
// returns backend.ErrJobNotRegistered. QueueSize does not apply and TrySubmit
// never returns ErrQueueFull. Close finishes the jobs this process claimed and
// leaves the rest in the store. Per-submit options are not saved: a job
// recovered after a restart uses the queue-level retry settings. The caller
// closes the store after closing the queue.
func WithStore(store backend.JobStore, codec *backend.Codec) Option {
	return func(target *config.Config) {
		target.MemoryType = config.Persistent
		target.Store = store
		target.Codec = codec
	}
}

// WithLease returns an Option that sets how long a claimed job may go without
// a heartbeat before it is handed to another worker (default 5m). Running jobs
// renew their lease automatically. Only used with WithStore.
func WithLease(lease time.Duration) Option {
	return func(target *config.Config) {
		target.Lease = lease
	}
}

// WithPollInterval returns an Option that sets how often idle workers check
// the store for jobs saved by other processes (default 1s). Only used with
// WithStore.
func WithPollInterval(interval time.Duration) Option {
	return func(target *config.Config) {
		target.PollInterval = interval
	}
}

// WithRetryPredicate returns an Option that sets the queue-level retry predicate.
// A failed job is retried only when predicate returns true; otherwise it is
// marked failed immediately. Use RetryIf to override it per submission.
func WithRetryPredicate(predicate func(error) bool) Option {
	return func(target *config.Config) {
		target.RetryPredicates = predicate
	}
}

// WithShutdownTimeout returns an Option that sets the graceful shutdown timeout.
// Close applies it when the context passed to Close has no deadline.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(target *config.Config) {
		target.ShutdownTimeout = timeout
	}
}

// WithLogger enables structured logging to w and returns the Option to pass to
// NewQueue plus a closer that flushes the logger and closes the write stream
// (os.Stdout and os.Stderr are left open).
// Call the closer (typically via defer) when the queue is no longer needed.
// Logging is disabled by default.
func WithLogger(w io.Writer) (Option, func() error) {
	logger, closer := logger.NewLogger(w)
	return func(target *config.Config) {
		target.Logger = logger
	}, closer
}

// WithBackoffStrategy defines the backoff strategy to employ when failure happens. The strategies includes:
//
// Constant is default. It is the backoff strategy that retries after a fixed delay.
//
// Formula:
//
//	delay = B
//
// Example:
//
//	B = 2s
//	retries = 2s, 2s, 2s, 2s...
//
// B = base retry delay.
// Linear is the backoff strategy that increases the retry delay
// by a fixed amount on every retry.
//
// Formula:
//
//	delay = B * N
//
// Example:
//
//	B = 2s
//	retries = 2s, 4s, 6s, 8s...
//
// B = base retry delay.
// N = retry attempt number starting from 1.
//
// Exponential is the backoff strategy that doubles the retry delay
// on every retry attempt.
//
// Formula:
//
//	delay = B * 2^(N-1)
//
// Example:
//
//	B = 2s
//	retries = 2s, 4s, 8s, 16s...
//
// B = base retry delay.
// N = retry attempt number starting from 1.
//
// ExponentialJitter is the exponential backoff strategy with
// randomness added to reduce synchronized retries and retry storms.
//
// Formula:
//
//	delay = random(0, B * 2^(N-1))
//
// Example:
//
//	B = 2s
//	retries ≈ 1.2s, 3.8s, 5.1s, 14.7s...
//
// B = base retry delay.
// N = retry attempt number starting from 1.
func WithBackoffStrategy(stragegy config.BackoffStrategy) Option {
	return func(target *config.Config) {
		target.BackoffStrategy = stragegy
	}
}

// NewQueue creates and returns a new Queue with the provided configuration options.
// It returns an error if the configuration is invalid.
// Logging is disabled by default; pass WithLogger to enable it.
func NewQueue(options ...Option) (*Queue, error) {
	return NewQueueWithLogger(nil, options...)
}

// NewQueueWithLogger creates and returns a new Queue with the provided configuration options and logger.
// It returns an error if the configuration is invalid.
// If logger is nil, logging is disabled.
func NewQueueWithLogger(logger Logger, options ...Option) (*Queue, error) {
	queueConfig := config.Default()
	for _, option := range options {
		if option != nil {
			option(&queueConfig)
		}
	}
	if logger != nil {
		queueConfig.Logger = logger
	}

	if err := queueConfig.Validate(); err != nil {
		return nil, err
	}

	collector := stats.NewCollector()
	queue := &Queue{
		config:    queueConfig,
		collector: collector,
	}
	queue.dispatcher = dispatcher.NewDispatcher(queueConfig, collector)

	return queue, nil
}

// Submit adds a job to the queue, blocking until the job is enqueued or the context is cancelled.
// The job's name is derived from its type, or from its Name method if it implements job.NamedJob.
// SubmitOption arguments allow per-job overrides for MaxRetries and RetryDelay.
func (q *Queue) Submit(ctx context.Context, task Job, opts ...SubmitOption) error {
	if q == nil {
		return ErrNilQueue
	}
	if task == nil {
		return ErrNilJob
	}
	if ctx == nil {
		return ErrNilCtx
	}

	t := dispatcher.Task{Name: jobpkg.NameOf(task), Job: task}
	for _, opt := range opts {
		if opt != nil {
			opt(&t)
		}
	}
	return q.dispatcher.Submit(ctx, t)
}

// TrySubmit attempts to add a job to the queue without blocking.
// The job's name is derived from its type, or from its Name method if it implements job.NamedJob.
// SubmitOption arguments allow per-job overrides for MaxRetries and RetryDelay.
func (q *Queue) TrySubmit(ctx context.Context, task Job, opts ...SubmitOption) error {
	if q == nil {
		return ErrNilQueue
	}
	if task == nil {
		return ErrNilJob
	}
	if ctx == nil {
		return ErrNilCtx
	}

	t := dispatcher.Task{Name: jobpkg.NameOf(task), Job: task}
	for _, opt := range opts {
		if opt != nil {
			opt(&t)
		}
	}
	return q.dispatcher.TrySubmit(ctx, t)
}

// Run submits a job to the queue using a background context and returns the
// submission error, if any. Like Submit, it blocks while the queue is full; it
// returns ErrClosed if the queue is closed while waiting. Use Submit with a
// context to bound the wait, or TrySubmit to never block.
// The job's name is derived from its type, or from its Name method if it implements job.NamedJob.
// SubmitOption arguments allow per-job overrides for MaxRetries and RetryDelay.
func (q *Queue) Run(task Job, opts ...SubmitOption) error {
	if q == nil {
		return ErrNilQueue
	}
	return q.Submit(context.Background(), task, opts...)
}

// Close gracefully shuts down the queue and waits for all workers to finish processing.
// If ctx has no deadline and Config.ShutdownTimeout is positive, the wait is
// bounded by ShutdownTimeout and Close returns context.DeadlineExceeded when it
// elapses.
func (q *Queue) Close(ctx context.Context) error {
	return q.CloseWithMode(ctx, DrainMode)
}

// CloseWithMode shuts down the queue using mode. DrainMode behaves like Close;
// ImmediateMode drops queued jobs and returns without waiting for running jobs.
// ShutdownTimeout applies as in Close.
func (q *Queue) CloseWithMode(ctx context.Context, mode ShutdownMode) error {
	if q == nil {
		return ErrNilQueue
	}
	if ctx == nil {
		return ErrNilCtx
	}
	if _, ok := ctx.Deadline(); !ok && q.config.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, q.config.ShutdownTimeout)
		defer cancel()
	}
	if mode == DrainMode || mode == ImmediateMode {
		q.closed.Store(true)
	}
	return q.dispatcher.CloseWithMode(ctx, mode)
}

// Stats returns a snapshot of the current execution statistics.
func (q *Queue) Stats() stats.Snapshot {
	if q == nil {
		return stats.Snapshot{}
	}
	if q.dispatcher != nil {
		return q.dispatcher.Stats()
	}
	if q.collector == nil {
		return stats.Snapshot{}
	}
	return q.collector.Snapshot()
}

// DeadJobs returns up to limit jobs that failed for good (retries exhausted, a
// retry predicate refused another attempt, or the payload could not be
// decoded), oldest first. limit <= 0 returns all of them. Each record's
// LastError holds the final error. It requires a queue created with WithStore
// and returns ErrNotPersistent otherwise.
func (q *Queue) DeadJobs(ctx context.Context, limit int64) ([]backend.JobRecord, error) {
	if q == nil {
		return nil, ErrNilQueue
	}
	if ctx == nil {
		return nil, ErrNilCtx
	}
	return q.dispatcher.DeadJobs(ctx, limit)
}

// Replay puts the failed job id back in the queue with its attempt count reset,
// so it runs again with a full set of retries (per-submit retry overrides are
// not restored). It returns ErrNotFailed if the job is not failed,
// backend.ErrNotFound if it does not exist, backend.ErrConflict if another
// process changed it concurrently, and ErrNotPersistent on a queue created
// without WithStore.
func (q *Queue) Replay(ctx context.Context, id jobpkg.ID) error {
	if q == nil {
		return ErrNilQueue
	}
	if ctx == nil {
		return ErrNilCtx
	}
	return q.dispatcher.Replay(ctx, id)
}
